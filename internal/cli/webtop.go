package cli

import (
	"github.com/jiripisa/kitchen/internal/tui/webtop"
	"github.com/spf13/cobra"
)

func newWebtopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "webtop",
		Short: "Browse webtop deployments and view their manifests via a TUI.",
		Long: "Opens a fullscreen TUI that lists every webtop deployment in the " +
			"current kubeconfig context together with its coreo backend, PR + " +
			"image-tag links and time since the last log line. Pressing enter on " +
			"an entry shows the deployment's full YAML manifest.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return webtop.Run(cmd.Context())
		},
	}
}
