// Package cli wires the cobra command tree for the kitchen binary.
package cli

import (
	"github.com/spf13/cobra"
)

// NewRootCmd builds the root cobra command with all subcommands attached.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "kitchen",
		Short:         "Developer toolbox with a TUI for Kubernetes (and more).",
		Long:          "kitchen is a developer tool. The first feature streams live logs from Kubernetes deployments inside a Bubble Tea TUI.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(newCoreoCmd())
	root.AddCommand(newLogCmd())
	root.AddCommand(newUpgradeCmd())
	root.AddCommand(newVersionCmd())
	root.AddCommand(newWebtopCmd())

	return root
}
