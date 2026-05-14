// Package components provides reusable building blocks shared across TUI
// screens (title bar, status bar, ...).
package components

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/jiripisa/kitchen/internal/tui/styles"
)

// TitleBar renders the top bar: app name on the left, screen name on the right.
func TitleBar(width int, screen string) string {
	if width <= 0 {
		width = 80
	}
	left := styles.Title.Render(" kitchen ")
	right := styles.Subtitle.Render(screen)
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

// StatusItem is a single key/value pair shown in the status bar.
type StatusItem struct {
	Key   string
	Value string
}

// StatusBar renders the bottom status bar with a list of key/value items on
// the left and a hint string (typically keybindings) on the right.
func StatusBar(width int, items []StatusItem, hint string) string {
	if width <= 0 {
		width = 80
	}

	var parts []string
	for _, it := range items {
		if it.Value == "" {
			it.Value = "—"
		}
		parts = append(parts,
			styles.StatusKey.Render(it.Key+":")+" "+styles.StatusValue.Render(it.Value),
		)
	}
	left := strings.Join(parts, "  ")
	right := styles.Hint.Render(hint)

	gap := width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		gap = 1
	}
	return styles.StatusBar.Width(width).Render(left + strings.Repeat(" ", gap) + right)
}
