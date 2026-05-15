package webtop

import (
	"context"
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

const defaultAPITimeout = 30 * time.Second

const listTitle = "Webtop deployments"

// listModel is the first screen: a picker of every webtop deployment found in
// the current kubeconfig context.
type listModel struct {
	client *k8s.Client

	width, height int

	list    list.Model
	spinner spinner.Model

	loading bool
	err     error

	// prPad is the width of the longest "PR #N" label across the current
	// entries, used to align the tag column under the PR column.
	prPad int
}

// entryItem wraps one webtop entry for use in bubbles/list.
type entryItem struct {
	e     entry
	prPad int
}

// FilterValue is what the list's filter matches against. Concatenating both
// URLs lets the user filter on either side of the row.
func (i entryItem) FilterValue() string {
	return i.e.URL + " " + i.e.Backend
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
	return tea.Batch(m.spinner.Tick, m.loadCmd())
}

func (m *listModel) SetSize(w, h int) {
	m.width, m.height = w, h
	// Reserve 1 line for title, 1 for status bar, 1 padding.
	listH := h - 3
	if listH < 3 {
		listH = 3
	}
	m.list.SetSize(w, listH)
}

type entriesLoadedMsg struct {
	entries []entry
}

type loadErrMsg struct{ err error }

func (m *listModel) loadCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), defaultAPITimeout)
		defer cancel()
		es, err := fetchEntries(ctx, io.Discard, m.client)
		if err != nil {
			return loadErrMsg{err: err}
		}
		return entriesLoadedMsg{entries: es}
	}
}

func (m *listModel) Update(msg tea.Msg) (*listModel, tea.Cmd) {
	switch msg := msg.(type) {
	case entriesLoadedMsg:
		m.prPad = prLabelWidth(msg.entries)
		items := make([]list.Item, 0, len(msg.entries))
		for _, e := range msg.entries {
			items = append(items, entryItem{e: e, prPad: m.prPad})
		}
		m.list.SetItems(items)
		m.loading = false
		return m, nil

	case loadErrMsg:
		m.loading = false
		m.err = msg.err
		return m, nil

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

	case spinner.TickMsg:
		if !m.loading {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	syncListTitle(&m.list, listTitle)
	return m, cmd
}

func (m *listModel) View() string {
	var b strings.Builder
	b.WriteString(components.TitleBar(m.width, "webtop picker"))
	b.WriteByte('\n')

	switch {
	case m.err != nil:
		b.WriteString(styles.Error.Render(fmt.Sprintf("error: %v", m.err)))
		b.WriteByte('\n')
	case m.loading:
		fmt.Fprintf(&b, "  %s loading webtop deployments…", m.spinner.View())
		b.WriteByte('\n')
	default:
		if len(m.list.Items()) == 0 {
			b.WriteString(styles.Hint.Render(
				"  no webtop deployments found in this context."))
			b.WriteByte('\n')
		} else {
			b.WriteString(m.list.View())
		}
	}

	b.WriteByte('\n')
	b.WriteString(components.StatusBar(m.width,
		[]components.StatusItem{
			{Key: "context", Value: m.client.Context()},
		},
		pickerHint(m.list.FilterState()),
	))
	return b.String()
}

// --- delegate rendering -------------------------------------------------------

// entryDelegate renders each entry on two lines: webtop URL on top, coreo
// backend underneath, both decorated with PR / tag / log-age metadata.
type entryDelegate struct{}

func (d entryDelegate) Height() int                         { return 2 }
func (d entryDelegate) Spacing() int                        { return 1 }
func (d entryDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

var (
	urlStyle         = lipgloss.NewStyle().Foreground(styles.ColorText)
	urlSelectedStyle = lipgloss.NewStyle().Foreground(styles.ColorAccent).Bold(true)
	urlDimStyle      = lipgloss.NewStyle().Foreground(styles.ColorDim)
	prLinkStyle      = lipgloss.NewStyle().Foreground(styles.ColorMutedAccent)
	tagLinkStyle     = lipgloss.NewStyle().Foreground(styles.ColorMutedWarn)
	lastLogStyle     = lipgloss.NewStyle().Foreground(styles.ColorDim)
)

func (d entryDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	it, ok := item.(entryItem)
	if !ok {
		return
	}
	selected := index == m.Index()
	width := m.Width()
	if width <= 0 {
		width = 80
	}

	cursor := "  "
	if selected {
		cursor = "▸ "
	}

	webtopLine := cursor + renderURLLine(
		it.e.URL, it.e.WebtopPR, it.e.WebtopTag, it.e.WebtopLastLog,
		webtopRepoOwner, webtopRepoName, it.prPad, selected)

	coreoLabel := it.e.Backend
	if coreoLabel == "" {
		coreoLabel = noCoreoLabel
	}
	coreoLine := "    " + renderURLLine(
		coreoLabel, it.e.CoreoPR, it.e.CoreoTag, it.e.CoreoLastLog,
		coreoRepoOwner, coreoRepoName, it.prPad, false)

	fmt.Fprint(w, webtopLine+"\n"+coreoLine)
}

// renderURLLine renders one line of "URL    PR #N  tag  age", colouring the
// URL accent when selected and dimmed when this is the coreo "(no coreo)"
// placeholder.
func renderURLLine(urlOrLabel string, pr *github.PR, tag string, lastLog time.Time, repoOwner, repoName string, prPad int, selected bool) string {
	style := urlStyle
	switch {
	case selected:
		style = urlSelectedStyle
	case urlOrLabel == noCoreoLabel || urlOrLabel == "-":
		style = urlDimStyle
	}

	url := urlOrLabel
	link := style.Render(url)
	if isHTTP(urlOrLabel) {
		link = hyperlink(urlOrLabel, link)
	}

	if pr == nil && tag == "" && lastLog.IsZero() {
		return link
	}

	var b strings.Builder
	b.WriteString(link)
	b.WriteString("  ")

	switch {
	case pr != nil:
		label := fmt.Sprintf("PR #%d", pr.Number)
		b.WriteString(hyperlink(pr.URL, prLinkStyle.Render(label)))
		if (tag != "" || !lastLog.IsZero()) && prPad > len(label) {
			b.WriteString(strings.Repeat(" ", prPad-len(label)))
		}
	case prPad > 0 && (tag != "" || !lastLog.IsZero()):
		b.WriteString(strings.Repeat(" ", prPad))
	}

	if tag != "" {
		if pr != nil || prPad > 0 {
			b.WriteString("  ")
		}
		ref := tag
		if pr != nil && pr.HeadRef != "" {
			ref = pr.HeadRef
		}
		b.WriteString(tagLink(tag, githubRefURL(repoOwner, repoName, ref)))
	}

	if !lastLog.IsZero() {
		if pr != nil || tag != "" || prPad > 0 {
			b.WriteString("  ")
		}
		b.WriteString(lastLogStyle.Render(humanDuration(time.Since(lastLog))))
	}

	return b.String()
}

func isHTTP(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

func tagLink(label, url string) string {
	styled := tagLinkStyle.Render(label)
	if url == "" {
		return styled
	}
	return hyperlink(url, styled)
}

func hyperlink(url, body string) string {
	return fmt.Sprintf("\x1b]8;;%s\x1b\\%s\x1b]8;;\x1b\\", url, body)
}

// --- filter / typing helpers --------------------------------------------------

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
