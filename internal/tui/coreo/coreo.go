package coreo

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jiripisa/kitchen/internal/k8s"
)

// Run is the entry point for `kitchen coreo` (no subcommand) and
// `kitchen coreo list`. There's currently only one screen, so both forms
// behave identically — leave room to grow into a menu later.
func Run(ctx context.Context) error {
	client, err := k8s.NewClient()
	if err != nil {
		return fmt.Errorf("connect to cluster: %w", err)
	}
	// No `WithMouseCellMotion` — the terminal handles OSC 8 hyperlink
	// clicks and text selection natively (see webtop list for the rationale).
	p := tea.NewProgram(newRootModel(ctx, client), tea.WithAltScreen())
	_, err = p.Run()
	return err
}
