package webtop

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jiripisa/kitchen/internal/k8s"
)

// Run starts the webtop TUI and blocks until the user quits.
func Run(ctx context.Context) error {
	client, err := k8s.NewClient()
	if err != nil {
		return fmt.Errorf("connect to cluster: %w", err)
	}

	// No `WithMouseCellMotion` here on purpose: the webtop screen doesn't
	// react to mouse events, and grabbing them would block the terminal's
	// own handling of OSC 8 hyperlink clicks and text selection. Navigation
	// is keyboard-only (↑/↓ enter esc q), so we leave mouse to the terminal.
	p := tea.NewProgram(
		newRootModel(ctx, client),
		tea.WithAltScreen(),
	)
	_, err = p.Run()
	return err
}
