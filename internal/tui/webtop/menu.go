package webtop

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jiripisa/kitchen/internal/k8s"
	"github.com/jiripisa/kitchen/internal/tui/components"
	"github.com/jiripisa/kitchen/internal/tui/styles"
)

// menuChoice identifies what the user picked from the top-level menu.
type menuChoice int

const (
	menuList menuChoice = iota
	menuDeploy
	menuUndeploy
)

// menuItem is one row in the top-level menu.
type menuItem struct {
	choice menuChoice
	title  string
	desc   string
}

var menuItems = []menuItem{
	{menuList, "List", "Browse the webtop deployments running in this cluster."},
	{menuDeploy, "Deploy", "Roll out a new webtop pointing at a chosen coreo backend."},
	{menuUndeploy, "Undeploy", "Remove a webtop previously deployed by kitchen."},
}

// menuModel is the entry screen the user lands on when running `kitchen
// webtop` without a subcommand.
type menuModel struct {
	client *k8s.Client

	width, height int
	cursor        int
}

func newMenuModel(client *k8s.Client) *menuModel {
	return &menuModel{client: client}
}

func (m *menuModel) Init() tea.Cmd { return nil }

func (m *menuModel) SetSize(w, h int) {
	m.width, m.height = w, h
}

// menuSelectedMsg is fired when the user confirms a menu entry.
type menuSelectedMsg struct{ choice menuChoice }

func (m *menuModel) Update(msg tea.Msg) (*menuModel, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		} else {
			m.cursor = len(menuItems) - 1
		}
	case "down", "j":
		if m.cursor < len(menuItems)-1 {
			m.cursor++
		} else {
			m.cursor = 0
		}
	case "1", "2", "3":
		// Direct number jumps for keyboard speedruns.
		i := int(key.String()[0] - '1')
		if i >= 0 && i < len(menuItems) {
			m.cursor = i
			return m, func() tea.Msg { return menuSelectedMsg{choice: menuItems[i].choice} }
		}
	case "enter":
		choice := menuItems[m.cursor].choice
		return m, func() tea.Msg { return menuSelectedMsg{choice: choice} }
	case "esc", "q", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

var (
	menuRowSelected = lipgloss.NewStyle().Foreground(styles.ColorAccent).Bold(true)
	menuRowNormal   = lipgloss.NewStyle().Foreground(styles.ColorText)
	menuDescSel     = lipgloss.NewStyle().Foreground(styles.ColorAccent2)
	menuDescNorm    = lipgloss.NewStyle().Foreground(styles.ColorDim)
)

func (m *menuModel) View() string {
	var b strings.Builder
	b.WriteString(components.TitleBar(m.width, "webtop"))
	b.WriteByte('\n')
	b.WriteByte('\n')

	for i, item := range menuItems {
		cursor := "  "
		titleStyle := menuRowNormal
		descStyle := menuDescNorm
		if i == m.cursor {
			cursor = "▸ "
			titleStyle = menuRowSelected
			descStyle = menuDescSel
		}
		fmt.Fprintf(&b, "%s%d. %s\n", cursor, i+1, titleStyle.Render(item.title))
		fmt.Fprintf(&b, "     %s\n\n", descStyle.Render(item.desc))
	}

	// Pad to fill the screen so the status bar sticks to the bottom.
	consumed := 4 + len(menuItems)*3
	if pad := m.height - consumed - 2; pad > 0 {
		b.WriteString(strings.Repeat("\n", pad))
	}
	b.WriteString(components.StatusBar(m.width,
		[]components.StatusItem{
			{Key: "context", Value: m.client.Context()},
		},
		"↑/↓ move · 1-3 jump · enter select · q quit",
	))
	return b.String()
}
