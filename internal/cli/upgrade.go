package cli

import (
	"github.com/jiripisa/kitchen/internal/updater"
	"github.com/jiripisa/kitchen/internal/version"
	"github.com/spf13/cobra"
)

func newUpgradeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "upgrade",
		Short: "Check for a newer kitchen release and replace the running binary.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return updater.Run(cmd.Context(), cmd.OutOrStdout(), updater.Options{
				Owner:          "jiripisa",
				Repo:           "kitchen",
				CurrentVersion: version.Version,
			})
		},
	}
}
