// Package log implements the `kitchen log` Bubble Tea program: pick a
// namespace, pick a deployment, then stream live logs from every pod.
package log

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jiripisa/kitchen/internal/k8s"
)

// Run starts the TUI program and blocks until the user quits.
func Run(ctx context.Context) error {
	client, err := k8s.NewClient()
	if err != nil {
		return fmt.Errorf("connect to cluster: %w", err)
	}

	p := tea.NewProgram(
		newRootModel(ctx, client),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	_, err = p.Run()
	return err
}
