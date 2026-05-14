package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jiripisa/kitchen/internal/k8s"
	"github.com/spf13/cobra"
)

// webtopDeploymentPrefix is the convention used in this cluster: every
// webtop application instance lives in a Deployment whose name starts with
// this prefix.
const webtopDeploymentPrefix = "mafin-coreo-app"

func newWebtopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "webtop",
		Short: "List all deployments running the webtop application.",
		Long: "Lists every Deployment across all namespaces in the current kubeconfig context " +
			"whose name starts with \"" + webtopDeploymentPrefix + "\" — the convention used " +
			"for webtop application instances. Output is namespace/name, one per line, suitable " +
			"for piping into other tools.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := k8s.NewClient()
			if err != nil {
				return fmt.Errorf("connect to cluster: %w", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			deps, err := client.ListAllDeployments(ctx)
			if err != nil {
				return err
			}

			found := 0
			out := cmd.OutOrStdout()
			for _, d := range deps {
				if !strings.HasPrefix(d.Name, webtopDeploymentPrefix) {
					continue
				}
				fmt.Fprintf(out, "%s/%s\n", d.Namespace, d.Name)
				found++
			}
			if found == 0 {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"no deployments with prefix %q found in context %q\n",
					webtopDeploymentPrefix, client.Context())
			}
			return nil
		},
	}
}
