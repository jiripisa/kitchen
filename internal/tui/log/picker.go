package log

import (
	"time"

	"github.com/charmbracelet/bubbles/list"
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

// newPickerDelegate returns a list delegate themed with the kitchen palette.
func newPickerDelegate() list.DefaultDelegate {
	d := list.NewDefaultDelegate()

	d.Styles.NormalTitle = lipgloss.NewStyle().
		Foreground(styles.ColorText).
		Padding(0, 0, 0, 2)
	d.Styles.NormalDesc = lipgloss.NewStyle().
		Foreground(styles.ColorDim).
		Padding(0, 0, 0, 2)

	d.Styles.SelectedTitle = lipgloss.NewStyle().
		Foreground(styles.ColorAccent).
		Bold(true).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(styles.ColorAccent).
		Padding(0, 0, 0, 1)
	d.Styles.SelectedDesc = lipgloss.NewStyle().
		Foreground(styles.ColorAccent2).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(styles.ColorAccent).
		Padding(0, 0, 0, 1)

	d.Styles.DimmedTitle = lipgloss.NewStyle().Foreground(styles.ColorDim).Padding(0, 0, 0, 2)
	d.Styles.DimmedDesc = lipgloss.NewStyle().Foreground(styles.ColorDim).Padding(0, 0, 0, 2)

	return d
}
