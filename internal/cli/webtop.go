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
		Short: "List webtop deployments as a backend / webtop / url table.",
		Long: "Prints a three-column table mapping each webtop Deployment (across " +
			"all namespaces in the current kubeconfig context) to the backend URL it's " +
			"wired to (MAFIN_URL env var) and the URL it serves to users (taken from " +
			"the Ingress that fronts the deployment's Service). Rows are sorted by " +
			"backend so instances sharing the same backend sit next to each other; " +
			"webtops with no backend set sort under \"(no backend)\" at the bottom. " +
			"Identification is image-based (" + webtopImageRepo + "), not name-based, " +
			"so it survives Deployment renames and matches review-apps, staging, and " +
			"production uniformly.",
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
			ingresses, err := client.ListAllIngresses(ctx)
			if err != nil {
				// Non-fatal: we can still show backend + name without URLs.
				fmt.Fprintf(cmd.ErrOrStderr(),
					"warning: could not list ingresses (%v); URL column will be empty\n", err)
				ingresses = nil
			}
			urls := buildIngressURLIndex(ingresses)

			entries := make([]webtopEntry, 0, len(deps))
			for _, d := range deps {
				if !isWebtopDeployment(d) {
					continue
				}
				entries = append(entries, webtopEntry{
					Namespace: d.Namespace,
					Name:      d.Name,
					Backend:   webtopBackend(d),
					URL:       urls[d.Namespace+"/"+d.Name],
				})
			}

			if len(entries) == 0 {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"no deployments running image %q found in context %q\n",
					webtopImageRepo, client.Context())
				return nil
			}

			renderWebtopTable(cmd.OutOrStdout(), buildWebtopRows(entries))
			return nil
		},
	}
}

// buildIngressURLIndex returns a map from "<namespace>/<service-name>" to
// "https://<host>". We use the webtop convention that Deployment name equals
// Service name equals Ingress backend service name, so a lookup by the
// Deployment's "namespace/name" key resolves to the serving URL.
//
// If multiple ingresses point at the same service, the first one wins —
// deterministic enough for the standard one-service-one-ingress webtop
// layout, and not worth a multi-host comma-joined column for the edge
// case.
func buildIngressURLIndex(endpoints []k8s.IngressEndpoint) map[string]string {
	out := make(map[string]string, len(endpoints))
	for _, e := range endpoints {
		key := e.Namespace + "/" + e.ServiceName
		if _, exists := out[key]; exists {
			continue
		}
		out[key] = "https://" + e.Host
	}
	return out
}

// webtopEntry is one row in the cross-namespace webtop listing.
type webtopEntry struct {
	Namespace string
	Name      string
	Backend   string
	URL       string
}

// webtopRow is the rendered shape: one webtop per row, with the
// backend URL repeated on each row so the table reads top-to-bottom.
type webtopRow struct {
	Backend string
	Webtop  string
	URL     string
}

// noBackendLabel is shown in the BACKEND column for webtops where
// MAFIN_URL isn't a literal value (unset, or sourced from a Secret /
// ConfigMap we don't resolve).
const noBackendLabel = "(no backend)"

// buildWebtopRows turns entries into table rows, sorted by backend (with
// the no-backend bucket pinned to the bottom) and then by namespace/name.
func buildWebtopRows(entries []webtopEntry) []webtopRow {
	rows := make([]webtopRow, 0, len(entries))
	for _, e := range entries {
		backend := e.Backend
		if backend == "" {
			backend = noBackendLabel
		}
		url := e.URL
		if url == "" {
			url = "-"
		}
		rows = append(rows, webtopRow{
			Backend: backend,
			Webtop:  e.Namespace + "/" + e.Name,
			URL:     url,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		ai, bi := rows[i].Backend == noBackendLabel, rows[j].Backend == noBackendLabel
		switch {
		case ai && !bi:
			return false
		case !ai && bi:
			return true
		case rows[i].Backend != rows[j].Backend:
			return rows[i].Backend < rows[j].Backend
		default:
			return rows[i].Webtop < rows[j].Webtop
		}
	})
	return rows
}

// renderWebtopTable prints rows as a three-column table with an aligned
// header. Column widths grow to fit the longest value in each column.
func renderWebtopTable(w io.Writer, rows []webtopRow) {
	if len(rows) == 0 {
		return
	}
	const (
		hBackend = "BACKEND"
		hWebtop  = "WEBTOP"
		hURL     = "URL"
		gap      = "  "
	)

	bWidth, wWidth, uWidth := len(hBackend), len(hWebtop), len(hURL)
	for _, r := range rows {
		if l := len(r.Backend); l > bWidth {
			bWidth = l
		}
		if l := len(r.Webtop); l > wWidth {
			wWidth = l
		}
		if l := len(r.URL); l > uWidth {
			uWidth = l
		}
	}

	fmt.Fprintf(w, "%-*s%s%-*s%s%s\n",
		bWidth, hBackend, gap,
		wWidth, hWebtop, gap,
		hURL,
	)
	fmt.Fprintf(w, "%s%s%s%s%s\n",
		strings.Repeat("-", bWidth), gap,
		strings.Repeat("-", wWidth), gap,
		strings.Repeat("-", uWidth),
	)
	for _, r := range rows {
		fmt.Fprintf(w, "%-*s%s%-*s%s%s\n",
			bWidth, r.Backend, gap,
			wWidth, r.Webtop, gap,
			r.URL,
		)
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
