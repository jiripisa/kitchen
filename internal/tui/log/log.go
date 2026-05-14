// Package log implements the `kitchen log` Bubble Tea program: pick a
// namespace, pick a deployment, then stream live logs from every pod.
package log

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jiripisa/kitchen/internal/k8s"
	"github.com/jiripisa/kitchen/internal/recents"
)

// Run starts the TUI program and blocks until the user quits.
func Run(ctx context.Context) error {
	client, err := k8s.NewClient()
	if err != nil {
		return fmt.Errorf("connect to cluster: %w", err)
	}

	store, err := recents.Open()
	if err != nil {
		// Recents are nice-to-have; if disk is unreadable for some reason
		// don't block the rest of the TUI on it.
		store = recents.Empty()
	}

	p := tea.NewProgram(
		newRootModel(ctx, client, store),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	_, err = p.Run()
	return err
}
