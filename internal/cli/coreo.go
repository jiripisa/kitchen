package cli

import (
	"github.com/jiripisa/kitchen/internal/tui/coreo"
	"github.com/spf13/cobra"
)

// newCoreoCmd builds the `kitchen coreo` command tree. Only `list` exists
// for now; the bare `kitchen coreo` opens the same screen so muscle memory
// from `kitchen webtop` carries over.
func newCoreoCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "coreo",
		Short: "Browse the coreo deployments running in this cluster.",
		Long: "Group of TUIs for the mafin coreo backend. Currently `list` " +
			"is the only subcommand and `kitchen coreo` opens it directly.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return coreo.Run(cmd.Context())
		},
	}

	root.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List coreo deployments with PR + tag links and bound-webtop counts.",
		Long: "Opens the live picker: every coreo deployment in the current " +
			"kubeconfig context with its URL, PR + image-tag links, time " +
			"since the last log line, and the number of webtops currently " +
			"pointing at it via MAFIN_URL. Press enter to view the manifest.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return coreo.Run(cmd.Context())
		},
	})

	return root
}
