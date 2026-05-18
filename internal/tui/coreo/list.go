package coreo

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jiripisa/kitchen/internal/github"
	"github.com/jiripisa/kitchen/internal/k8s"
	"github.com/jiripisa/kitchen/internal/tui/components"
	"github.com/jiripisa/kitchen/internal/tui/styles"
)

const (
	defaultAPITimeout = 30 * time.Second
	logRefreshEvery   = 15 * time.Second
	ageTickEvery      = time.Second

	listTitle = "Coreo deployments"

	maxURLWidth = 72
	maxTagWidth = 45
	ageWidth    = 4
)

// listModel is the first (and only) primary screen of `kitchen coreo list`.
type listModel struct {
	client *k8s.Client

	width, height int

	list    list.Model
	spinner spinner.Model

	loading bool
	err     error

	// Raw inputs progressively merged in by background loaders.
	deps     []k8s.Deployment
	urls     map[string]string
	prs      github.Index
	logTimes map[string]time.Time

	cw colWidths
}

func newListModel(client *k8s.Client) *listModel {
	l := list.New(nil, entryDelegate{}, 0, 0)
	l.Title = listTitle
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.Filter = substringFilter
	l.Styles.Title = styles.Title

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return &listModel{
		client:  client,
		list:    l,
		spinner: sp,
		loading: true,
	}
}

func (m *listModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.loadDeploymentsCmd(), ageTickCmd())
}

func (m *listModel) SetSize(w, h int) {
	m.width, m.height = w, h
	listH := h - 3
	if listH < 3 {
		listH = 3
	}
	m.list.SetSize(w, listH)
}

// --- messages -------------------------------------------------------------

type deploymentsLoadedMsg struct{ deps []k8s.Deployment }
type ingressesLoadedMsg struct{ urls map[string]string }
type prsLoadedMsg struct{ index github.Index }
type logTimesLoadedMsg struct{ times map[string]time.Time }
type logTimesRefreshMsg struct{}
type ageTickMsg struct{}
type loadErrMsg struct{ err error }

func (m *listModel) loadDeploymentsCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), defaultAPITimeout)
		defer cancel()
		deps, err := m.client.ListAllDeployments(ctx)
		if err != nil {
			return loadErrMsg{err: err}
		}
		return deploymentsLoadedMsg{deps: deps}
	}
}

func (m *listModel) loadIngressesCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), defaultAPITimeout)
		defer cancel()
		ings, err := m.client.ListAllIngresses(ctx)
		if err != nil {
			return ingressesLoadedMsg{urls: map[string]string{}}
		}
		return ingressesLoadedMsg{urls: buildIngressURLIndex(ings)}
	}
}

func loadPRsCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), defaultAPITimeout)
		defer cancel()
		idx, _ := github.FetchIndex(ctx, coreoRepoOwner, coreoRepoName)
		return prsLoadedMsg{index: idx}
	}
}

func (m *listModel) loadLogTimesCmd() tea.Cmd {
	deps := m.deps
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), defaultAPITimeout)
		defer cancel()
		return logTimesLoadedMsg{times: fetchLastLogTimes(ctx, client, deps)}
	}
}

func scheduleLogRefresh() tea.Cmd {
	return tea.Tick(logRefreshEvery, func(time.Time) tea.Msg { return logTimesRefreshMsg{} })
}

func ageTickCmd() tea.Cmd {
	return tea.Tick(ageTickEvery, func(time.Time) tea.Msg { return ageTickMsg{} })
}

// --- bubbletea wiring -----------------------------------------------------

func (m *listModel) Update(msg tea.Msg) (*listModel, tea.Cmd) {
	switch msg := msg.(type) {
	case deploymentsLoadedMsg:
		m.loading = false
		m.deps = msg.deps
		refresh := m.refreshItems()
		return m, tea.Batch(
			refresh,
			m.loadIngressesCmd(),
			loadPRsCmd(),
			m.loadLogTimesCmd(),
		)

	case ingressesLoadedMsg:
		m.urls = msg.urls
		return m, m.refreshItems()

	case prsLoadedMsg:
		m.prs = msg.index
		return m, m.refreshItems()

	case logTimesLoadedMsg:
		m.logTimes = msg.times
		return m, tea.Batch(m.refreshItems(), scheduleLogRefresh())

	case logTimesRefreshMsg:
		if m.deps == nil {
			return m, nil
		}
		return m, m.loadLogTimesCmd()

	case ageTickMsg:
		return m, tea.Batch(m.refreshItems(), ageTickCmd())

	case loadErrMsg:
		m.loading = false
		m.err = msg.err
		return m, nil

	case spinner.TickMsg:
		if !m.loading {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tea.KeyMsg:
		if shouldStartFiltering(msg, m.list.FilterState()) {
			return m, startFilteringWith(&m.list, msg)
		}
		switch msg.String() {
		case "esc":
			if m.list.FilterState() == list.Filtering || m.list.FilterState() == list.FilterApplied {
				break
			}
			return m, func() tea.Msg { return backMsg{} }
		case "ctrl+c", "q":
			return m, tea.Quit
		case "enter":
			if it, ok := m.list.SelectedItem().(entryItem); ok {
				ns, name := it.e.Namespace, it.e.Name
				return m, func() tea.Msg {
					return entrySelectedMsg{namespace: ns, name: name}
				}
			}
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	syncListTitle(&m.list, listTitle)
	return m, cmd
}

// refreshItems rebuilds the list's items from the currently-known inputs.
// SetItems returns a tea.Cmd that re-runs the active filter — we MUST thread
// it back, otherwise typing in the filter prompt empties the view (the same
// bug we fixed in webtop list).
func (m *listModel) refreshItems() tea.Cmd {
	entries := entriesFromInputs(m.deps, m.urls, m.prs, m.logTimes)
	m.cw = computeColWidths(entries)
	items := make([]list.Item, 0, len(entries))
	for _, e := range entries {
		items = append(items, entryItem{e: e, cw: m.cw})
	}
	cur := m.list.Index()
	cmd := m.list.SetItems(items)
	if cur >= 0 && cur < len(items) {
		m.list.Select(cur)
	}
	return cmd
}

func (m *listModel) View() string {
	var b strings.Builder
	b.WriteString(components.TitleBar(m.width, "coreo picker"))
	b.WriteByte('\n')

	switch {
	case m.err != nil:
		b.WriteString(styles.Error.Render(fmt.Sprintf("error: %v", m.err)))
		b.WriteByte('\n')
	case m.loading:
		fmt.Fprintf(&b, "  %s loading coreo deployments…", m.spinner.View())
		b.WriteByte('\n')
	default:
		if len(m.list.Items()) == 0 {
			b.WriteString(styles.Hint.Render(
				"  no coreo deployments found in this context."))
			b.WriteByte('\n')
		} else {
			b.WriteString(m.list.View())
		}
	}

	b.WriteByte('\n')
	b.WriteString(components.StatusBar(m.width,
		[]components.StatusItem{
			{Key: "context", Value: m.client.Context()},
			{Key: "loaded", Value: m.loadProgress()},
		},
		pickerHint(m.list.FilterState()),
	))
	return b.String()
}

func (m *listModel) loadProgress() string {
	mark := func(done bool, name string) string {
		if done {
			return name
		}
		return styles.Hint.Render(name)
	}
	return strings.Join([]string{
		mark(m.urls != nil, "ing"),
		mark(m.prs != nil, "pr"),
		mark(m.logTimes != nil, "logs"),
	}, " ")
}

// --- delegate / column-aligned rendering ----------------------------------

type entryItem struct {
	e  entry
	cw colWidths
}

func (i entryItem) FilterValue() string {
	return i.e.Name + " " + i.e.URL
}

type colWidths struct {
	url, pr, tag int
}

func computeColWidths(es []entry) colWidths {
	cw := colWidths{}
	for _, e := range es {
		url := e.URL
		if url == "" {
			url = "-"
		}
		if w := lipgloss.Width(url); w > cw.url {
			cw.url = w
		}
		if e.PR != nil {
			if w := len(fmt.Sprintf("PR #%d", e.PR.Number)); w > cw.pr {
				cw.pr = w
			}
		}
		if w := len(e.Tag); w > cw.tag {
			cw.tag = w
		}
	}
	if cw.url > maxURLWidth {
		cw.url = maxURLWidth
	}
	if cw.tag > maxTagWidth {
		cw.tag = maxTagWidth
	}
	return cw
}

type entryDelegate struct{}

func (d entryDelegate) Height() int                         { return 2 }
func (d entryDelegate) Spacing() int                        { return 1 }
func (d entryDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

var (
	urlStyle         = lipgloss.NewStyle().Foreground(styles.ColorText)
	urlSelectedStyle = lipgloss.NewStyle().Foreground(styles.ColorAccent).Bold(true)
	prLinkStyle      = lipgloss.NewStyle().Foreground(styles.ColorMutedAccent)
	tagLinkStyle     = lipgloss.NewStyle().Foreground(styles.ColorMutedWarn)
	lastLogStyle     = lipgloss.NewStyle().Foreground(styles.ColorDim)
	placeholderStyle = lipgloss.NewStyle().Foreground(styles.ColorDim).Italic(true)
	footerStyle      = lipgloss.NewStyle().Foreground(styles.ColorDim)
	zeroWebtopStyle  = lipgloss.NewStyle().Foreground(styles.ColorDim).Italic(true)
	manyWebtopStyle  = lipgloss.NewStyle().Foreground(styles.ColorAccent2)
)

func (d entryDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	it, ok := item.(entryItem)
	if !ok {
		return
	}
	selected := index == m.Index()

	cursor := "  "
	style := urlStyle
	if selected {
		cursor = "▸ "
		style = urlSelectedStyle
	}

	url := it.e.URL
	if url == "" {
		url = "-"
		style = placeholderStyle
	}

	top := cursor + renderRow(url, style, it.e.PR, it.e.Tag, it.e.LastLog, it.cw)

	// Second line: "mafin/<name>" + N webtops bound. Indented to align with
	// the URL on the line above.
	footer := "  " + footerStyle.Render(it.e.Namespace+"/"+it.e.Name)
	footer += "  " + renderWebtopCount(it.e.WebtopCount)
	if it.e.IsMain {
		footer += "  " + footerStyle.Render("· staging")
	}

	fmt.Fprint(w, top+"\n"+footer)
}

func renderWebtopCount(n int) string {
	switch n {
	case 0:
		return zeroWebtopStyle.Render("no webtops bound")
	case 1:
		return manyWebtopStyle.Render("1 webtop bound")
	default:
		return manyWebtopStyle.Render(fmt.Sprintf("%d webtops bound", n))
	}
}

// renderRow lays out one row of `URL  PR #N  tag  age` with shared column
// widths so multiple entries line up.
func renderRow(urlLabel string, style lipgloss.Style, pr *github.PR, tag string, lastLog time.Time, cw colWidths) string {
	var b strings.Builder

	// URL column.
	urlDisplay := truncateText(urlLabel, cw.url)
	if isHTTP(urlLabel) {
		b.WriteString(hyperlink(urlLabel, style.Render(urlDisplay)))
	} else {
		b.WriteString(style.Render(urlDisplay))
	}
	padTo(&b, lipgloss.Width(urlDisplay), cw.url)
	b.WriteString("  ")

	// PR column.
	if cw.pr > 0 {
		if pr != nil {
			label := fmt.Sprintf("PR #%d", pr.Number)
			b.WriteString(hyperlink(pr.URL, prLinkStyle.Render(label)))
			padTo(&b, len(label), cw.pr)
		} else {
			b.WriteString(strings.Repeat(" ", cw.pr))
		}
		b.WriteString("  ")
	}

	// Tag column. The image tag is the EFFECTIVE_SLUG of the head ref, so
	// link to the PR's HeadRef (the real branch) instead — kitchen webtop
	// learned this the hard way; mirror that fix here.
	if cw.tag > 0 {
		if tag != "" {
			tagDisplay := truncateText(tag, cw.tag)
			ref := tag
			if pr != nil && pr.HeadRef != "" {
				ref = pr.HeadRef
			}
			if u := githubRefURL(coreoRepoOwner, coreoRepoName, ref); u != "" {
				b.WriteString(hyperlink(u, tagLinkStyle.Render(tagDisplay)))
			} else {
				b.WriteString(tagLinkStyle.Render(tagDisplay))
			}
			padTo(&b, lipgloss.Width(tagDisplay), cw.tag)
		} else {
			b.WriteString(strings.Repeat(" ", cw.tag))
		}
		b.WriteString("  ")
	}

	// Age column, right-aligned in a small fixed slot.
	if !lastLog.IsZero() {
		age := humanDuration(time.Since(lastLog))
		if pad := ageWidth - lipgloss.Width(age); pad > 0 {
			b.WriteString(strings.Repeat(" ", pad))
		}
		b.WriteString(lastLogStyle.Render(age))
	}

	return strings.TrimRight(b.String(), " ")
}

func padTo(b *strings.Builder, visible, target int) {
	if pad := target - visible; pad > 0 {
		b.WriteString(strings.Repeat(" ", pad))
	}
}

func truncateText(s string, maxW int) string {
	if lipgloss.Width(s) <= maxW {
		return s
	}
	if maxW <= 1 {
		return "…"
	}
	runes := []rune(s)
	for i := len(runes); i >= 0; i-- {
		candidate := string(runes[:i]) + "…"
		if lipgloss.Width(candidate) <= maxW {
			return candidate
		}
	}
	return "…"
}

func isHTTP(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// hyperlink wraps body in an OSC 8 envelope. The same id= trick as the
// webtop list — distinct ids per URL keep terminals like iTerm2 from
// merging adjacent links on one row into a single hyperlink.
func hyperlink(url, body string) string {
	if url == "" {
		return body
	}
	h := sha1.Sum([]byte(url))
	id := hex.EncodeToString(h[:4])
	return "\x1b]8;id=k" + id + ";" + url + "\x07" + body + "\x1b]8;;\x07"
}

// --- filter / typing helpers ---------------------------------------------

func substringFilter(term string, targets []string) []list.Rank {
	if term == "" {
		return nil
	}
	lcTerm := strings.ToLower(term)
	termLen := len([]rune(lcTerm))
	out := make([]list.Rank, 0, len(targets))
	for i, t := range targets {
		lcTarget := strings.ToLower(t)
		idx := strings.Index(lcTarget, lcTerm)
		if idx < 0 {
			continue
		}
		startRune := len([]rune(t[:idx]))
		matched := make([]int, termLen)
		for j := 0; j < termLen; j++ {
			matched[j] = startRune + j
		}
		out = append(out, list.Rank{Index: i, MatchedIndexes: matched})
	}
	return out
}

func shouldStartFiltering(msg tea.KeyMsg, state list.FilterState) bool {
	return state == list.Unfiltered &&
		msg.Type == tea.KeyRunes &&
		len(msg.Runes) > 0
}

func startFilteringWith(l *list.Model, msg tea.KeyMsg) tea.Cmd {
	openFilter := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}}
	*l, _ = l.Update(openFilter)
	var cmd tea.Cmd
	*l, cmd = l.Update(msg)
	return cmd
}

func syncListTitle(l *list.Model, base string) {
	if l.FilterState() == list.FilterApplied {
		l.Title = "Filter: " + l.FilterValue()
	} else if l.FilterState() == list.Unfiltered {
		l.Title = base
	}
}

func pickerHint(state list.FilterState) string {
	if state == list.Unfiltered {
		return "type to filter · ↑/↓ move · enter manifest · esc quit"
	}
	return "↑/↓ move · enter manifest · esc clear filter · ^c quit"
}
