package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jiripisa/kitchen/internal/k8s"
	"github.com/spf13/cobra"
)

// webtopImageRepo is the canonical container image path for the webtop
// application on GitHub Container Registry. A Deployment is identified as
// webtop when at least one of its containers runs an image from this repo,
// regardless of the tag (review-apps use feature-branch slugs, staging uses
// release versions, etc.).
//
// We deliberately match on the image repo rather than the Deployment name:
// review-apps, staging and any future production deployment all share the
// same image path, but the names differ (and might be renamed by future CI
// pipelines without breaking detection).
const webtopImageRepo = "ghcr.io/finforce/mafin-coreo-app"

func newWebtopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "webtop",
		Short: "List all deployments running the webtop application.",
		Long: "Lists every Deployment across all namespaces in the current kubeconfig " +
			"context whose pod template runs the webtop container image (" +
			webtopImageRepo + "). Identification is image-based, not name-based, " +
			"so it survives Deployment renames and matches review-apps, staging, " +
			"and production uniformly. Output is namespace/name, one per line, " +
			"suitable for piping into other tools.",
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
				if !isWebtopDeployment(d) {
					continue
				}
				fmt.Fprintf(out, "%s/%s\n", d.Namespace, d.Name)
				found++
			}
			if found == 0 {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"no deployments running image %q found in context %q\n",
					webtopImageRepo, client.Context())
			}
			return nil
		},
	}
}

// isWebtopDeployment reports whether a Deployment has at least one container
// running the webtop image.
//
// A container image reference can take three shapes:
//
//	<repo>           — implicit :latest tag
//	<repo>:<tag>     — explicit tag
//	<repo>@sha256:…  — digest pinning
//
// We accept all three so digest-pinned production deployments aren't missed.
func isWebtopDeployment(d k8s.Deployment) bool {
	for _, c := range d.Containers {
		if isWebtopImage(c.Image) {
			return true
		}
	}
	return false
}

func isWebtopImage(image string) bool {
	if image == webtopImageRepo {
		return true
	}
	return strings.HasPrefix(image, webtopImageRepo+":") ||
		strings.HasPrefix(image, webtopImageRepo+"@")
}
