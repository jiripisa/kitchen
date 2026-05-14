package cli

import (
	"github.com/jiripisa/kitchen/internal/tui/log"
	"github.com/spf13/cobra"
)

func newLogCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "log",
		Short: "Stream live logs from a Kubernetes deployment via a TUI.",
		Long: "Opens a three-screen TUI: pick a namespace, pick a deployment, " +
			"then watch live logs from every pod of that deployment in one view.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return log.Run(cmd.Context())
		},
	}
}
