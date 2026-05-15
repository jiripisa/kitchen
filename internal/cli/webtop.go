package cli

import (
	"context"
	"fmt"
	"io"
	neturl "net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/jiripisa/kitchen/internal/github"
	"github.com/jiripisa/kitchen/internal/k8s"
	"github.com/jiripisa/kitchen/internal/tui/styles"
	"github.com/spf13/cobra"
)

// Image and env-var conventions are documented in docs/upstream-pipelines.md.
const (
	webtopImageRepo     = "ghcr.io/finforce/mafin-coreo-app"
	webtopBackendEnvVar = "MAFIN_URL"

	webtopRepoOwner = "finforce"
	webtopRepoName  = "mafin-coreo-app"
	coreoRepoOwner  = "finforce"
	coreoRepoName   = "mafin-coreo"

	webtopDeployNamePrefix = "mafin-coreo-app-"
	coreoIngressHostPrefix = "coreo-"
	mafinHostSuffix        = ".mafin.finforce.dev"
)

func newWebtopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "webtop",
		Short: "Show webtop deployments and the coreo backend each talks to.",
		Long: "Renders a framed two-column table — WEBTOP on the left (the URL " +
			"the webtop is served at), COREO on the right (the URL of the coreo " +
			"backend it talks to, from MAFIN_URL on the pod). Rows are grouped " +
			"by coreo with separators between groups, and each coreo URL appears " +
			"only once. When a deployment came from a pull request (slug matches " +
			"an open PR in the upstream repo), a clickable PR link is appended.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := k8s.NewClient()
			if err != nil {
				return fmt.Errorf("connect to cluster: %w", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			data, err := fetchWebtopData(ctx, cmd.ErrOrStderr(), client)
			if err != nil {
				return err
			}
			if len(data.entries) == 0 {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"no deployments running image %q found in context %q\n",
					webtopImageRepo, client.Context())
				return nil
			}

			fmt.Fprintln(cmd.OutOrStdout(), renderWebtopTable(data.groups()))
			return nil
		},
	}
}

// webtopData bundles everything fetched for one `kitchen webtop` invocation.
type webtopData struct {
	entries []webtopEntry
}

func fetchWebtopData(ctx context.Context, stderr io.Writer, client *k8s.Client) (*webtopData, error) {
	var (
		deps       []k8s.Deployment
		ingresses  []k8s.IngressEndpoint
		coreoPRs   github.Index
		webtopPRs  github.Index
		depsErr    error
		ingressErr error
		wg         sync.WaitGroup
	)

	wg.Add(4)
	go func() {
		defer wg.Done()
		deps, depsErr = client.ListAllDeployments(ctx)
	}()
	go func() {
		defer wg.Done()
		ingresses, ingressErr = client.ListAllIngresses(ctx)
	}()
	go func() {
		defer wg.Done()
		idx, err := github.FetchIndex(ctx, coreoRepoOwner, coreoRepoName)
		if err != nil {
			fmt.Fprintf(stderr, "warning: coreo PR lookup: %v\n", err)
		}
		coreoPRs = idx
	}()
	go func() {
		defer wg.Done()
		idx, err := github.FetchIndex(ctx, webtopRepoOwner, webtopRepoName)
		if err != nil {
			fmt.Fprintf(stderr, "warning: webtop PR lookup: %v\n", err)
		}
		webtopPRs = idx
	}()
	wg.Wait()

	if depsErr != nil {
		return nil, depsErr
	}
	if ingressErr != nil {
		fmt.Fprintf(stderr,
			"warning: could not list ingresses (%v); webtop URL column will be empty\n", ingressErr)
		ingresses = nil
	}

	urls := buildIngressURLIndex(ingresses)

	entries := make([]webtopEntry, 0, len(deps))
	for _, d := range deps {
		if !isWebtopDeployment(d) {
			continue
		}
		e := webtopEntry{
			Namespace: d.Namespace,
			Name:      d.Name,
			Backend:   webtopBackend(d),
			URL:       urls[d.Namespace+"/"+d.Name],
		}
		if pr, ok := webtopPRs[webtopSlugFromName(d.Name)]; ok {
			e.WebtopPR = &pr
		}
		if pr, ok := coreoPRs[coreoSlugFromURL(e.Backend)]; ok {
			e.CoreoPR = &pr
		}
		entries = append(entries, e)
	}

	return &webtopData{entries: entries}, nil
}

// buildIngressURLIndex returns a map from "<namespace>/<service-name>" to
// "https://<host>". Multiple ingresses pointing at the same service: first one wins.
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

// webtopEntry is one webtop instance with everything we display about it.
type webtopEntry struct {
	Namespace string
	Name      string
	Backend   string     // coreo URL the webtop talks to (MAFIN_URL)
	URL       string     // webtop URL (from ingress)
	WebtopPR  *github.PR // PR that spawned this webtop deployment, if any
	CoreoPR   *github.PR // PR that spawned the coreo backend, if any
}

// noCoreoLabel is shown when MAFIN_URL isn't set on the webtop pod.
const noCoreoLabel = "(no coreo)"

// webtopGroup is one row in the rendered table — all webtops sharing the
// same coreo backend. PR refs are kept separately from the URL strings so
// the renderer can put them in a side column outside the framed table.
type webtopGroup struct {
	Coreo     string
	CoreoPR   *github.PR
	Webtops   []string // webtop URLs, one per entry
	WebtopPRs []*github.PR
}

// groups buckets entries by Backend URL. Coreo URLs are sorted alphabetically
// with no-coreo last; webtops within each group are sorted alphabetically by
// URL.
func (d *webtopData) groups() []webtopGroup {
	buckets := map[string][]webtopEntry{}
	for _, e := range d.entries {
		buckets[e.Backend] = append(buckets[e.Backend], e)
	}

	keys := make([]string, 0, len(buckets))
	for k := range buckets {
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
		items := buckets[k]
		sort.Slice(items, func(a, b int) bool { return items[a].URL < items[b].URL })

		coreoLabel := k
		if coreoLabel == "" {
			coreoLabel = noCoreoLabel
		}

		webtops := make([]string, 0, len(items))
		webtopPRs := make([]*github.PR, 0, len(items))
		for _, e := range items {
			cell := e.URL
			if cell == "" {
				cell = "-"
			}
			webtops = append(webtops, cell)
			webtopPRs = append(webtopPRs, e.WebtopPR)
		}

		out = append(out, webtopGroup{
			Coreo:     coreoLabel,
			CoreoPR:   items[0].CoreoPR,
			Webtops:   webtops,
			WebtopPRs: webtopPRs,
		})
	}
	return out
}

// renderWebtopTable renders groups as a framed lipgloss table for the
// WEBTOP/COREO columns and then appends a borderless PR column to the right
// of every data line. The PR column has no header and no frame; PR labels
// for adjacent rows sit in the same column position so they read as their
// own visual stack, in a distinct color so they don't blend with the URLs.
func renderWebtopTable(groups []webtopGroup) string {
	if len(groups) == 0 {
		return ""
	}

	headerStyle := lipgloss.NewStyle().
		Foreground(styles.ColorAccent).
		Bold(true).
		Padding(0, 1)
	cellStyle := lipgloss.NewStyle().Padding(0, 1)
	borderStyle := lipgloss.NewStyle().Foreground(styles.ColorDim)

	rows := make([][]string, 0, len(groups))
	for _, g := range groups {
		rows = append(rows, []string{strings.Join(g.Webtops, "\n"), g.Coreo})
	}

	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(borderStyle).
		BorderRow(true).
		Headers("WEBTOP", "COREO").
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == table.HeaderRow {
				return headerStyle
			}
			return cellStyle
		}).
		Rows(rows...)

	return appendPRColumn(t.Render(), groups)
}

// appendPRColumn walks the rendered table line-by-line and appends PR text
// to body lines. Header, separators and outer borders are left untouched.
//
// Body-line layout with lipgloss.NormalBorder() and BorderRow(true) is
// deterministic:
//
//	row 0  ┌─┬─┐               top border
//	row 1  │ WEBTOP │ COREO │  header
//	row 2  ├─┼─┤               header / body separator
//	... for each group:
//	         one line per webtop URL (cell rows)
//	         then ├─┼─┤ separator (unless last group)
//	last   └─┴─┘               bottom border
func appendPRColumn(tableStr string, groups []webtopGroup) string {
	lines := strings.Split(tableStr, "\n")
	out := make([]string, 0, len(lines))

	li := 0
	// Top border, header, header-body separator.
	for i := 0; i < 3 && li < len(lines); i, li = i+1, li+1 {
		out = append(out, lines[li])
	}

	for gi, g := range groups {
		for wi := range g.Webtops {
			if li >= len(lines) {
				break
			}
			out = append(out, lines[li]+prSuffix(g, wi))
			li++
		}
		// Group separator (skipped after the last group).
		if gi < len(groups)-1 && li < len(lines) {
			out = append(out, lines[li])
			li++
		}
	}
	// Bottom border (and any trailing blank line the renderer emitted).
	for li < len(lines) {
		out = append(out, lines[li])
		li++
	}
	return strings.Join(out, "\n")
}

// prSuffix renders the PR labels (webtop, then coreo) for a given body line.
// Empty string when no PR applies — the line is left as-is so PR labels on
// adjacent lines stay column-aligned, but lines without PRs don't carry any
// trailing space.
func prSuffix(g webtopGroup, wi int) string {
	var parts []string
	if wi < len(g.WebtopPRs) && g.WebtopPRs[wi] != nil {
		parts = append(parts, prLink(*g.WebtopPRs[wi]))
	}
	if wi == 0 && g.CoreoPR != nil {
		parts = append(parts, prLink(*g.CoreoPR))
	}
	if len(parts) == 0 {
		return ""
	}
	return "  " + strings.Join(parts, "  ")
}

// prLinkStyle gives the PR labels a distinct color so they don't blend with
// the table content (which uses the terminal's default foreground).
var prLinkStyle = lipgloss.NewStyle().Foreground(styles.ColorAccent2)

// prLink builds an OSC 8 hyperlink wrapped in a color escape — modern
// terminals render the styled "PR #123" as clickable text taking the user
// to the PR. Terminals that don't understand OSC 8 just print the label.
func prLink(pr github.PR) string {
	label := prLinkStyle.Render(fmt.Sprintf("PR #%d", pr.Number))
	return fmt.Sprintf("\x1b]8;;%s\x1b\\%s\x1b]8;;\x1b\\", pr.URL, label)
}

// isWebtopDeployment reports whether a Deployment has at least one container
// running the webtop image. See docs/upstream-pipelines.md for the rationale
// behind image-based identity.
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

// webtopBackend returns the literal MAFIN_URL set on the webtop container.
func webtopBackend(d k8s.Deployment) string {
	for _, c := range d.Containers {
		if !isWebtopImage(c.Image) {
			continue
		}
		return c.Env[webtopBackendEnvVar]
	}
	return ""
}

// webtopSlugFromName extracts the SUFFIX from a webtop Deployment name. The
// staging deployment has no suffix and returns "".
func webtopSlugFromName(name string) string {
	if name == strings.TrimSuffix(webtopDeployNamePrefix, "-") {
		return ""
	}
	return strings.TrimPrefix(name, webtopDeployNamePrefix)
}

// coreoSlugFromURL extracts the SUFFIX from a coreo URL like
// "https://coreo-<slug>.mafin.finforce.dev". Empty for the canonical
// staging/production URL (no suffix), or when the URL doesn't match the
// expected pattern.
func coreoSlugFromURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	u, err := neturl.Parse(rawURL)
	if err != nil {
		return ""
	}
	host := u.Host
	if !strings.HasSuffix(host, mafinHostSuffix) {
		return ""
	}
	base := strings.TrimSuffix(host, mafinHostSuffix)
	if !strings.HasPrefix(base, coreoIngressHostPrefix) {
		return ""
	}
	return strings.TrimPrefix(base, coreoIngressHostPrefix)
}
