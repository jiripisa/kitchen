package webtop

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jiripisa/kitchen/internal/k8s"
)

// Run starts the top-level webtop menu (`kitchen webtop`).
func Run(ctx context.Context) error { return runAt(ctx, screenMenu) }

// RunList opens directly on the deployments list (`kitchen webtop list`).
func RunList(ctx context.Context) error { return runAt(ctx, screenList) }

// RunDeploy opens directly on the deploy wizard (`kitchen webtop deploy`).
func RunDeploy(ctx context.Context) error { return runAt(ctx, screenDeploy) }

// RunUndeploy opens directly on the undeploy picker (`kitchen webtop undeploy`).
func RunUndeploy(ctx context.Context) error { return runAt(ctx, screenUndeploy) }

func runAt(ctx context.Context, start screen) error {
	client, err := k8s.NewClient()
	if err != nil {
		return fmt.Errorf("connect to cluster: %w", err)
	}
	// No `WithMouseCellMotion` — see the note in the previous version: we
	// want the terminal to handle OSC 8 hyperlink clicks and text selection.
	p := tea.NewProgram(newRootModel(ctx, client, start), tea.WithAltScreen())
	_, err = p.Run()
	return err
}
