package cli

import (
	"github.com/jiripisa/kitchen/internal/tui/webtop"
	"github.com/spf13/cobra"
)

// newWebtopCmd builds the `kitchen webtop` command tree:
//
//	kitchen webtop            opens the menu (list / deploy / undeploy)
//	kitchen webtop list       lists running webtop deployments
//	kitchen webtop deploy     three-step wizard to roll out a new webtop
//	kitchen webtop undeploy   picker that removes kitchen-managed webtops
func newWebtopCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "webtop",
		Short: "Browse, deploy and undeploy webtop instances against any coreo.",
		Long: "Group of TUIs for the mafin webtop frontend. Without a subcommand " +
			"opens a small menu; the three subcommands jump straight to the " +
			"matching screen for shell scripts and muscle memory.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return webtop.Run(cmd.Context())
		},
	}

	root.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List webtop deployments with their coreo backend, PR + tag links.",
		Long: "Opens the live picker: every webtop deployment in the current " +
			"kubeconfig context with its coreo backend, PR + image-tag links " +
			"and time since the last log line. Press enter to view the " +
			"manifest of the selected deployment.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return webtop.RunList(cmd.Context())
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "deploy",
		Short: "Deploy a new webtop instance against a chosen coreo backend.",
		Long: "Three-step wizard: pick a build (open PR or `main`), pick a coreo " +
			"backend from the cluster, name the new deployment. Kitchen renders " +
			"the upstream `k8s.yml` template, injects an " +
			"`app.kubernetes.io/managed-by=kitchen` label and provenance " +
			"annotations (branch, coreo URL, you, timestamp), then creates the " +
			"Deployment + Service + Ingress in the `mafin` namespace.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return webtop.RunDeploy(cmd.Context())
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "undeploy",
		Short: "Remove a webtop previously deployed by kitchen.",
		Long: "Lists only deployments carrying kitchen's `managed-by=kitchen` " +
			"label, so this can never accidentally tear down an upstream " +
			"review-app. After a confirmation prompt the Deployment, Service " +
			"and Ingress are deleted in one go.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return webtop.RunUndeploy(cmd.Context())
		},
	})

	return root
}
