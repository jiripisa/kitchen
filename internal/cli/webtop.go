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
		Short: "List webtops as a webtop → coreo URL table.",
		Long: "Prints a two-column table — WEBTOP on the left (the URL where the " +
			"webtop is served, from its Ingress), COREO on the right (the URL of the " +
			"coreo backend the webtop talks to, from MAFIN_URL on the pod). Rows are " +
			"sorted by COREO so webtops sharing the same coreo sit next to each " +
			"other; on those continuation rows the COREO cell is left blank — each " +
			"coreo URL is printed only once at the top of its group. Webtops with no " +
			"MAFIN_URL set sort under \"(no coreo)\" at the bottom. Identification " +
			"is image-based (" + webtopImageRepo + "), not name-based, so it " +
			"survives Deployment renames and matches review-apps, staging, and " +
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
				// Non-fatal: we can still show the coreo column without URLs.
				fmt.Fprintf(cmd.ErrOrStderr(),
					"warning: could not list ingresses (%v); webtop URL column will be empty\n", err)
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

// webtopRow is the rendered shape: one webtop per row, with the coreo URL
// repeated on each row so the table reads top-to-bottom.
type webtopRow struct {
	Coreo  string
	Webtop string
}

// noCoreoLabel is shown in the COREO column for webtops where MAFIN_URL
// isn't a literal value (unset, or sourced from a Secret / ConfigMap we
// don't resolve).
const noCoreoLabel = "(no coreo)"

// buildWebtopRows turns entries into table rows, sorted by coreo (with the
// no-coreo bucket pinned to the bottom) and then by webtop URL.
func buildWebtopRows(entries []webtopEntry) []webtopRow {
	rows := make([]webtopRow, 0, len(entries))
	for _, e := range entries {
		coreo := e.Backend
		if coreo == "" {
			coreo = noCoreoLabel
		}
		webtop := e.URL
		if webtop == "" {
			webtop = "-"
		}
		rows = append(rows, webtopRow{
			Coreo:  coreo,
			Webtop: webtop,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		ai, bi := rows[i].Coreo == noCoreoLabel, rows[j].Coreo == noCoreoLabel
		switch {
		case ai && !bi:
			return false
		case !ai && bi:
			return true
		case rows[i].Coreo != rows[j].Coreo:
			return rows[i].Coreo < rows[j].Coreo
		default:
			return rows[i].Webtop < rows[j].Webtop
		}
	})
	return rows
}

// renderWebtopTable prints rows as a two-column WEBTOP/COREO table with an
// aligned header. Rows are expected to be pre-sorted by Coreo (so identical
// values are adjacent); the renderer shows each Coreo URL only on the first
// row of its group and leaves the cell blank on continuation rows — the
// visual grouping comes for free, no separators or blank lines needed.
func renderWebtopTable(w io.Writer, rows []webtopRow) {
	if len(rows) == 0 {
		return
	}
	const (
		hWebtop = "WEBTOP"
		hCoreo  = "COREO"
		gap     = "  "
	)

	wWidth, cWidth := len(hWebtop), len(hCoreo)
	for _, r := range rows {
		if l := len(r.Webtop); l > wWidth {
			wWidth = l
		}
		if l := len(r.Coreo); l > cWidth {
			cWidth = l
		}
	}

	fmt.Fprintf(w, "%-*s%s%s\n", wWidth, hWebtop, gap, hCoreo)
	fmt.Fprintf(w, "%s%s%s\n",
		strings.Repeat("-", wWidth),
		gap,
		strings.Repeat("-", cWidth),
	)

	prevCoreo := ""
	for i, r := range rows {
		if i > 0 && r.Coreo == prevCoreo {
			// Continuation row — no COREO printed, also no need to pad the
			// WEBTOP column to the full width (we'd just emit trailing
			// whitespace before the newline).
			fmt.Fprintf(w, "%s\n", r.Webtop)
			continue
		}
		prevCoreo = r.Coreo
		fmt.Fprintf(w, "%-*s%s%s\n", wWidth, r.Webtop, gap, r.Coreo)
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
