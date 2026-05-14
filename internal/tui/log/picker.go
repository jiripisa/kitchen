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
}

func (i simpleItem) Title() string       { return i.title }
func (i simpleItem) Description() string { return i.desc }
func (i simpleItem) FilterValue() string { return i.title }

// compactDelegate renders a list item on a single line so the picker shows
// many entries at once. If the item has a description, it's right-aligned
// on the same row as the title.
type compactDelegate struct{}

func (d compactDelegate) Height() int                         { return 1 }
func (d compactDelegate) Spacing() int                        { return 0 }
func (d compactDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

var (
	compactTitle         = lipgloss.NewStyle().Foreground(styles.ColorText)
	compactDesc          = lipgloss.NewStyle().Foreground(styles.ColorDim)
	compactSelectedTitle = lipgloss.NewStyle().Foreground(styles.ColorAccent).Bold(true)
	compactSelectedDesc  = lipgloss.NewStyle().Foreground(styles.ColorAccent2)
)

func (d compactDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	it, ok := item.(simpleItem)
	if !ok {
		return
	}

	selected := index == m.Index()

	prefix := "  "
	titleStyle := compactTitle
	descStyle := compactDesc
	if selected {
		prefix = "▸ "
		titleStyle = compactSelectedTitle
		descStyle = compactSelectedDesc
	}

	width := m.Width()
	if width <= 0 {
		width = 80
	}

	title := titleStyle.Render(it.title)
	line := prefix + title

	if it.desc != "" {
		desc := descStyle.Render(it.desc)
		gap := width - lipgloss.Width(line) - lipgloss.Width(desc) - 2
		if gap < 1 {
			gap = 1
		}
		line += strings.Repeat(" ", gap) + desc
	}

	fmt.Fprint(w, line)
}

// newPickerDelegate returns the compact one-line-per-item delegate used by
// both pickers.
func newPickerDelegate() list.ItemDelegate { return compactDelegate{} }
