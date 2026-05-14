package log

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jiripisa/kitchen/internal/tui/styles"
)

// defaultAPITimeout caps how long we wait for any single API list call.
const defaultAPITimeout = 15 * time.Second

// simpleItem is the standard list item used by both pickers.
type simpleItem struct {
	title, desc string
	recent      bool
}

func (i simpleItem) Title() string       { return i.title }
func (i simpleItem) Description() string { return i.desc }
func (i simpleItem) FilterValue() string { return i.title }

// separatorItem is a non-selectable visual divider between the recents zone
// and the rest of the list. Its FilterValue is empty so it gets filtered out
// whenever the user types into the filter prompt.
type separatorItem struct {
	label string
}

func (s separatorItem) FilterValue() string { return "" }

// compactDelegate renders a list item on a single line so the picker shows
// many entries at once. Recent items get a ★ prefix; the separator renders
// as a dim horizontal rule.
type compactDelegate struct{}

func (d compactDelegate) Height() int                         { return 1 }
func (d compactDelegate) Spacing() int                        { return 0 }
func (d compactDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

var (
	compactTitle         = lipgloss.NewStyle().Foreground(styles.ColorText)
	compactDesc          = lipgloss.NewStyle().Foreground(styles.ColorDim)
	compactSelectedTitle = lipgloss.NewStyle().Foreground(styles.ColorAccent).Bold(true)
	compactSelectedDesc  = lipgloss.NewStyle().Foreground(styles.ColorAccent2)
	compactStar          = lipgloss.NewStyle().Foreground(styles.ColorAccent2)
	compactSeparator     = lipgloss.NewStyle().Foreground(styles.ColorDim)
)

func (d compactDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	width := m.Width()
	if width <= 0 {
		width = 80
	}

	switch it := item.(type) {
	case separatorItem:
		fmt.Fprint(w, renderSeparator(width, it.label))
		return
	case simpleItem:
		fmt.Fprint(w, renderSimpleItem(width, it, index == m.Index()))
		return
	}
}

func renderSeparator(width int, label string) string {
	mid := " " + label + " "
	if label == "" {
		mid = ""
	}
	line := compactSeparator.Render(strings.Repeat("─", max(width-lipgloss.Width(mid)-2, 4)))
	if mid == "" {
		return "  " + line
	}
	half := compactSeparator.Render("──")
	return "  " + half + compactSeparator.Render(mid) + line
}

func renderSimpleItem(width int, it simpleItem, selected bool) string {
	titleStyle := compactTitle
	descStyle := compactDesc
	if selected {
		titleStyle = compactSelectedTitle
		descStyle = compactSelectedDesc
	}

	var prefix string
	switch {
	case selected && it.recent:
		prefix = compactStar.Render("▸★")
	case selected:
		prefix = "▸ "
	case it.recent:
		prefix = compactStar.Render(" ★")
	default:
		prefix = "  "
	}
	prefix += " "

	title := titleStyle.Render(it.title)
	line := prefix + title

	if it.desc != "" {
		// Render desc immediately after the title with a small fixed gap,
		// rather than right-aligning to the window edge — names vary in
		// length so ragged-right reads better than a far-away column.
		line += "  " + descStyle.Render(it.desc)
	}
	// Clamp to the available width so a stray long line can't break the
	// layout of the list.
	if w := lipgloss.Width(line); w > width {
		line = lipgloss.NewStyle().MaxWidth(width).Render(line)
	}
	return line
}

// newPickerDelegate returns the compact one-line-per-item delegate used by
// both pickers.
func newPickerDelegate() list.ItemDelegate { return compactDelegate{} }

// substringFilter is a case-insensitive contains-substring matcher used in
// place of the list's default fuzzy filter. Fuzzy matched too many false
// positives for k8s names (e.g. "main" matched "mafin-auth"). With contains
// matching, the filter behaves predictably: the typed text must appear as a
// contiguous substring of the item title.
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
		// MatchedIndexes are rune indices, used by the default list
		// delegate to highlight matched chars. Our custom delegate doesn't
		// highlight, but we fill them in correctly so future delegates can.
		startRune := len([]rune(t[:idx]))
		matched := make([]int, termLen)
		for j := 0; j < termLen; j++ {
			matched[j] = startRune + j
		}
		out = append(out, list.Rank{Index: i, MatchedIndexes: matched})
	}
	return out
}

// syncListTitle keeps the list's title visible when a filter is applied so
// the user always knows what's being filtered. In Filtering state (user is
// actively typing) the list renders "Filter: <input-with-cursor>" itself, so
// we leave its Title alone there.
func syncListTitle(l *list.Model, base string) {
	if l.FilterState() == list.FilterApplied {
		l.Title = "Filter: " + l.FilterValue()
	} else if l.FilterState() == list.Unfiltered {
		l.Title = base
	}
}

// buildItemsWithRecents takes the full list of names plus an MRU recents
// slice and returns a list.Item sequence: recents first (only those still
// present in `all`, in MRU order), then a separator (if any recents
// survived), then the remaining names in their original order.
func buildItemsWithRecents(all, recents []string, descFor func(name string) string) []list.Item {
	allSet := make(map[string]bool, len(all))
	for _, n := range all {
		allSet[n] = true
	}
	recentSet := make(map[string]bool, len(recents))
	out := make([]list.Item, 0, len(all)+1)
	for _, r := range recents {
		if !allSet[r] || recentSet[r] {
			continue
		}
		recentSet[r] = true
		out = append(out, simpleItem{title: r, desc: descFor(r), recent: true})
	}
	if len(out) > 0 {
		out = append(out, separatorItem{label: "more"})
	}
	for _, n := range all {
		if recentSet[n] {
			continue
		}
		out = append(out, simpleItem{title: n, desc: descFor(n)})
	}
	return out
}
