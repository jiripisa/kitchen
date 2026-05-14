package cli

import (
	"fmt"
	"runtime"

	"github.com/jiripisa/kitchen/internal/version"
	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version, commit, and build date.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(),
				"kitchen %s\n  commit: %s\n  built:  %s\n  go:     %s %s/%s\n",
				version.Version, version.Commit, version.Date,
				runtime.Version(), runtime.GOOS, runtime.GOARCH,
			)
			return nil
		},
	}
}
