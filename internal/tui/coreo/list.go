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

	// Two-panel layout: target right-panel width with a min/max clamp. The
	// left panel gets whatever's left after the divider + spaces. Below
	// minTotalForSplit the right panel is hidden and the list runs full
	// width again.
	rightTargetWidth = 50
	rightMinWidth    = 28
	dividerCols      = 3 // " │ "
	minTotalForSplit = 90
)

// kindPR distinguishes which PR index a prsLoadedMsg carries.
type kindPR int

const (
	prCoreo kindPR = iota
	prWebtop
)

// listModel is the first (and only) primary screen of `kitchen coreo list`.
type listModel struct {
	client *k8s.Client

	width, height int
	leftWidth     int // width of the bubbles/list panel
	rightWidth    int // width of the bound-webtops panel (0 ⇒ hidden)

	list    list.Model
	spinner spinner.Model

	loading bool
	err     error

	// Raw inputs progressively merged in by background loaders.
	deps      []k8s.Deployment
	urls      map[string]string
	coreoPRs  github.Index
	webtopPRs github.Index
	logTimes  map[string]time.Time

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

	if w >= minTotalForSplit {
		right := rightTargetWidth
		if right > w/2 {
			right = w / 2
		}
		if right < rightMinWidth {
			right = rightMinWidth
		}
		m.rightWidth = right
		m.leftWidth = w - right - dividerCols
	} else {
		m.rightWidth = 0
		m.leftWidth = w
	}

	listH := h - 3
	if listH < 3 {
		listH = 3
	}
	m.list.SetSize(m.leftWidth, listH)
}

// --- messages -------------------------------------------------------------

type deploymentsLoadedMsg struct{ deps []k8s.Deployment }
type ingressesLoadedMsg struct{ urls map[string]string }
type prsLoadedMsg struct {
	kind  kindPR
	index github.Index
}
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

func loadPRsCmd(owner, repo string, kind kindPR) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), defaultAPITimeout)
		defer cancel()
		idx, _ := github.FetchIndex(ctx, owner, repo)
		return prsLoadedMsg{kind: kind, index: idx}
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
			loadPRsCmd(coreoRepoOwner, coreoRepoName, prCoreo),
			loadPRsCmd(webtopRepoOwner, webtopRepoName, prWebtop),
			m.loadLogTimesCmd(),
		)

	case ingressesLoadedMsg:
		m.urls = msg.urls
		return m, m.refreshItems()

	case prsLoadedMsg:
		switch msg.kind {
		case prCoreo:
			m.coreoPRs = msg.index
		case prWebtop:
			m.webtopPRs = msg.index
		}
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
	entries := entriesFromInputs(m.deps, m.urls, m.coreoPRs, m.webtopPRs, m.logTimes)
	m.cw = computeColWidths(entries, m.leftWidth)
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

// --- View -----------------------------------------------------------------

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
			b.WriteString(m.renderBody())
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

// renderBody composes the left list + right webtop panel side-by-side. When
// the terminal is too narrow for a sensible split, falls back to the list
// alone.
func (m *listModel) renderBody() string {
	if m.rightWidth <= 0 {
		return m.list.View()
	}
	left := m.list.View()
	bodyH := m.height - 3
	if bodyH < 3 {
		bodyH = 3
	}
	right := m.renderRightPanel(bodyH)
	divider := dividerView(bodyH)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, divider, right)
}

// renderRightPanel renders the bound-webtops view for the currently
// selected coreo entry, sized to `m.rightWidth` × `height`.
func (m *listModel) renderRightPanel(height int) string {
	var sel *entry
	if it, ok := m.list.SelectedItem().(entryItem); ok {
		ev := it.e
		sel = &ev
	}

	var b strings.Builder

	if sel == nil {
		b.WriteString(rightHintStyle.Render("select a coreo to see its webtops"))
		return rightPanelStyle.Width(m.rightWidth).Height(height).Render(b.String())
	}

	heading := fmt.Sprintf("%d webtop", sel.WebtopCount())
	if sel.WebtopCount() != 1 {
		heading += "s"
	}
	heading += " bound"
	b.WriteString(rightHeadingStyle.Render(heading))
	b.WriteByte('\n')
	if sel.URL != "" {
		b.WriteString(rightDimStyle.Render("→ " + truncateText(sel.URL, m.rightWidth-2)))
	}
	b.WriteByte('\n')
	b.WriteByte('\n')

	if sel.WebtopCount() == 0 {
		b.WriteString(rightHintStyle.Render("(nothing pointing here yet)"))
		return rightPanelStyle.Width(m.rightWidth).Height(height).Render(b.String())
	}

	// Each bound webtop occupies 3 lines (URL, meta, blank).
	const linesPerWebtop = 3
	// Reserve 4 lines for heading + URL + blank + blank (footer overflow).
	reservedLines := 4
	maxFit := (height - reservedLines) / linesPerWebtop
	if maxFit < 1 {
		maxFit = 1
	}

	shown := sel.Webtops
	overflow := 0
	if len(shown) > maxFit {
		shown = sel.Webtops[:maxFit]
		overflow = len(sel.Webtops) - maxFit
	}

	for _, bw := range shown {
		b.WriteString(m.renderBoundWebtop(bw))
		b.WriteByte('\n')
		b.WriteByte('\n')
	}
	if overflow > 0 {
		b.WriteString(rightHintStyle.Render(fmt.Sprintf("… %d more not shown", overflow)))
	}

	return rightPanelStyle.Width(m.rightWidth).Height(height).Render(b.String())
}

// renderBoundWebtop is one entry in the right panel: URL line + meta line.
func (m *listModel) renderBoundWebtop(bw boundWebtop) string {
	var b strings.Builder

	// First line: the webtop URL (truncated, hyperlinked if HTTP).
	urlText := bw.URL
	if urlText == "" {
		urlText = "(no ingress)"
	}
	display := truncateText(urlText, m.rightWidth-2)
	styled := rightURLStyle.Render(display)
	if isHTTP(urlText) {
		styled = hyperlink(urlText, styled)
	}
	b.WriteString(styled)
	b.WriteByte('\n')

	// Second line: PR · tag · age.
	parts := []string{}
	if bw.PR != nil {
		parts = append(parts, hyperlink(bw.PR.URL,
			prLinkStyle.Render(fmt.Sprintf("PR #%d", bw.PR.Number))))
	}
	if bw.Tag != "" {
		ref := bw.Tag
		if bw.PR != nil && bw.PR.HeadRef != "" {
			ref = bw.PR.HeadRef
		}
		tagLabel := truncateText(bw.Tag, 20)
		if u := githubRefURL(webtopRepoOwner, webtopRepoName, ref); u != "" {
			parts = append(parts, hyperlink(u, tagLinkStyle.Render(tagLabel)))
		} else {
			parts = append(parts, tagLinkStyle.Render(tagLabel))
		}
	}
	if !bw.LastLog.IsZero() {
		parts = append(parts, lastLogStyle.Render(humanDuration(time.Since(bw.LastLog))))
	}
	if len(parts) > 0 {
		b.WriteString("  " + strings.Join(parts, "  "))
	}

	return b.String()
}

func dividerView(height int) string {
	bar := dividerStyle.Render("│")
	return strings.Repeat(" "+bar+" \n", height-1) + " " + bar + " "
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
		mark(m.coreoPRs != nil, "co-pr"),
		mark(m.webtopPRs != nil, "wt-pr"),
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
	// hasTag is true when the tag column is shown at all. Narrow layouts
	// (two-panel mode with a small left panel) may drop it.
	hasTag bool
}

// computeColWidths derives column widths from the data, then shrinks them to
// fit the available leftWidth. URL gets the lion's share; the tag column is
// the first to go when space is tight.
func computeColWidths(es []entry, leftWidth int) colWidths {
	cw := colWidths{hasTag: true}
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

	if leftWidth <= 0 {
		return cw
	}
	// Fixed overhead inside one rendered row (delegate Render builds this
	// shape: "▸ <URL>  <PR>  <TAG>  <AGE>"). 2 for cursor, then 2 spaces
	// between each column.
	const cursor = 2
	gap := 2
	overhead := cursor + gap + cw.pr + gap + ageWidth + 1 // +1 spare

	available := leftWidth - overhead
	if cw.hasTag {
		available -= gap // for the gap before the tag
	}
	if available <= 0 {
		// Crammed; drop tag and try again.
		cw.hasTag = false
		cw.tag = 0
		available = leftWidth - cursor - gap - cw.pr - gap - ageWidth - 1
		if available < 10 {
			available = 10
		}
		if cw.url > available {
			cw.url = available
		}
		return cw
	}

	// Allocate proportionally between URL and TAG. URL takes priority.
	if cw.url+cw.tag <= available {
		return cw
	}
	// First shrink the tag toward its minimum useful width (10).
	targetTag := cw.tag
	if available-cw.url < cw.tag {
		targetTag = available - cw.url
	}
	if targetTag < 10 {
		// Tag is getting too short; drop it entirely.
		cw.hasTag = false
		cw.tag = 0
		available = leftWidth - cursor - gap - cw.pr - gap - ageWidth - 1
		if cw.url > available {
			cw.url = available
		}
		return cw
	}
	cw.tag = targetTag
	if cw.url+cw.tag > available {
		cw.url = available - cw.tag
	}
	if cw.url < 20 {
		cw.url = 20
	}
	return cw
}

type entryDelegate struct{}

func (d entryDelegate) Height() int                         { return 2 }
func (d entryDelegate) Spacing() int                        { return 1 }
func (d entryDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

var (
	urlStyle          = lipgloss.NewStyle().Foreground(styles.ColorText)
	urlSelectedStyle  = lipgloss.NewStyle().Foreground(styles.ColorAccent).Bold(true)
	prLinkStyle       = lipgloss.NewStyle().Foreground(styles.ColorMutedAccent)
	tagLinkStyle      = lipgloss.NewStyle().Foreground(styles.ColorMutedWarn)
	lastLogStyle      = lipgloss.NewStyle().Foreground(styles.ColorDim)
	placeholderStyle  = lipgloss.NewStyle().Foreground(styles.ColorDim).Italic(true)
	footerStyle       = lipgloss.NewStyle().Foreground(styles.ColorDim)
	zeroWebtopStyle   = lipgloss.NewStyle().Foreground(styles.ColorDim).Italic(true)
	manyWebtopStyle   = lipgloss.NewStyle().Foreground(styles.ColorAccent2)
	dividerStyle      = lipgloss.NewStyle().Foreground(styles.ColorDim)
	rightPanelStyle   = lipgloss.NewStyle().Padding(0, 1)
	rightHeadingStyle = lipgloss.NewStyle().Foreground(styles.ColorAccent).Bold(true)
	rightURLStyle     = lipgloss.NewStyle().Foreground(styles.ColorText)
	rightHintStyle    = lipgloss.NewStyle().Foreground(styles.ColorDim).Italic(true)
	rightDimStyle     = lipgloss.NewStyle().Foreground(styles.ColorDim)
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
	footer += "  " + renderWebtopCount(it.e.WebtopCount())
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
// widths so multiple entries line up. Honours `cw.hasTag` so narrow layouts
// can drop the tag column entirely.
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

	// Tag column (skipped when the layout is too narrow).
	if cw.hasTag && cw.tag > 0 {
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
	if maxW <= 0 {
		return ""
	}
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

// hyperlink wraps body in an OSC 8 envelope. A stable id= derived from the
// URL keeps terminals from merging adjacent links on one row.
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
