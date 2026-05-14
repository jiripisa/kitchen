package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
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
const (
	webtopImageRepo     = "ghcr.io/finforce/mafin-coreo-app"
	webtopBackendEnvVar = "MAFIN_URL"
)

func newWebtopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "webtop",
		Short: "List all deployments running the webtop application, grouped by backend.",
		Long: "Lists every Deployment across all namespaces in the current kubeconfig " +
			"context whose pod template runs the webtop container image (" +
			webtopImageRepo + "), grouped by the backend URL each instance is wired " +
			"to (MAFIN_URL env var). Webtops with no backend set are listed last under " +
			"\"(no backend)\". Identification is image-based, not name-based, so it " +
			"survives Deployment renames and matches review-apps, staging, and production " +
			"uniformly.",
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

			entries := make([]webtopEntry, 0, len(deps))
			for _, d := range deps {
				if !isWebtopDeployment(d) {
					continue
				}
				entries = append(entries, webtopEntry{
					Namespace: d.Namespace,
					Name:      d.Name,
					Backend:   webtopBackend(d),
				})
			}

			if len(entries) == 0 {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"no deployments running image %q found in context %q\n",
					webtopImageRepo, client.Context())
				return nil
			}

			renderWebtopGroups(cmd.OutOrStdout(), groupWebtops(entries))
			return nil
		},
	}
}

// webtopEntry is one row in the cross-namespace webtop listing.
type webtopEntry struct {
	Namespace string
	Name      string
	Backend   string
}

// webtopGroup is a backend URL together with every webtop wired to it.
type webtopGroup struct {
	Backend string
	Entries []webtopEntry
}

// groupWebtops buckets entries by backend URL. Buckets are returned in a
// stable order: backend URLs alphabetically first, the empty backend last
// (rendered as "(no backend)"). Entries within each bucket are sorted by
// (namespace, name).
func groupWebtops(entries []webtopEntry) []webtopGroup {
	bucket := map[string][]webtopEntry{}
	for _, e := range entries {
		bucket[e.Backend] = append(bucket[e.Backend], e)
	}

	keys := make([]string, 0, len(bucket))
	for k := range bucket {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		switch {
		case keys[i] == "":
			return false
		case keys[j] == "":
			return true
		default:
			return keys[i] < keys[j]
		}
	})

	out := make([]webtopGroup, 0, len(keys))
	for _, k := range keys {
		items := bucket[k]
		sort.Slice(items, func(a, b int) bool {
			if items[a].Namespace != items[b].Namespace {
				return items[a].Namespace < items[b].Namespace
			}
			return items[a].Name < items[b].Name
		})
		out = append(out, webtopGroup{Backend: k, Entries: items})
	}
	return out
}

// renderWebtopGroups prints groups as plain text: backend URL (count),
// then indented namespace/name children, with a blank line between groups.
// Format is intentionally human-first; for raw machine-readable output use
// kubectl directly.
func renderWebtopGroups(w io.Writer, groups []webtopGroup) {
	for i, g := range groups {
		if i > 0 {
			fmt.Fprintln(w)
		}
		label := g.Backend
		if label == "" {
			label = "(no backend)"
		}
		fmt.Fprintf(w, "%s (%d)\n", label, len(g.Entries))
		for _, e := range g.Entries {
			fmt.Fprintf(w, "  %s/%s\n", e.Namespace, e.Name)
		}
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

// webtopBackend returns the backend URL the webtop instance is wired to,
// taken from the MAFIN_URL env var on the webtop container. Empty string
// when the env var isn't set or isn't a literal value (e.g. sourced from
// a ConfigMap/Secret — we don't resolve those).
func webtopBackend(d k8s.Deployment) string {
	for _, c := range d.Containers {
		if !isWebtopImage(c.Image) {
			continue
		}
		return c.Env[webtopBackendEnvVar]
	}
	return ""
}
