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
	coreoImageRepo      = "ghcr.io/finforce/mafin-coreo"
	webtopBackendEnvVar = "MAFIN_URL"

	webtopRepoOwner = "finforce"
	webtopRepoName  = "mafin-coreo-app"
	coreoRepoOwner  = "finforce"
	coreoRepoName   = "mafin-coreo"

	webtopDeployNamePrefix = "mafin-coreo-app-"
	coreoDeployNamePrefix  = "mafin-coreo-"
	coreoIngressHostPrefix = "coreo-"
	mafinHostSuffix        = ".mafin.finforce.dev"

	// mafinNamespace is where both webtop and coreo are deployed in this
	// cluster; both apps' k8s.yml hard-code it in the Service / Ingress.
	mafinNamespace = "mafin"
)

func newWebtopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "webtop",
		Short: "Show webtop deployments and the coreo backend each talks to.",
		Long: "Renders a framed two-column table — WEBTOP on the left (URL " +
			"served by the ingress), COREO on the right (URL of the coreo backend " +
			"the webtop talks to, from MAFIN_URL). Rows are grouped by coreo with " +
			"separators between groups, and each coreo URL appears only once. " +
			"Under each URL kitchen prints, when available, a clickable PR link " +
			"and a clickable GitHub link to the image tag that's actually deployed.",
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
	coreoTags := buildCoreoTagIndex(deps)

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
			WebtopTag: webtopImageTag(d),
		}
		if pr, ok := webtopPRs[webtopSlugFromName(d.Name)]; ok {
			e.WebtopPR = &pr
		}
		if pr, ok := coreoPRs[coreoSlugFromURL(e.Backend)]; ok {
			e.CoreoPR = &pr
		}
		e.CoreoTag = coreoTags[coreoDeploymentKeyForURL(e.Backend)]
		entries = append(entries, e)
	}

	return &webtopData{entries: entries}, nil
}

// buildCoreoTagIndex picks the image tag off every coreo deployment in
// `deps` and returns a map keyed by "<namespace>/<deployment-name>".
func buildCoreoTagIndex(deps []k8s.Deployment) map[string]string {
	out := map[string]string{}
	for _, d := range deps {
		for _, c := range d.Containers {
			if !isCoreoImage(c.Image) {
				continue
			}
			out[d.Namespace+"/"+d.Name] = imageTag(c.Image)
			break
		}
	}
	return out
}

// coreoDeploymentKeyForURL returns the "<namespace>/<deployment-name>" key
// for the coreo deployment that serves a given coreo URL, using the
// project convention that:
//   - the URL is https://coreo-<SLUG>.mafin.finforce.dev
//   - the corresponding Deployment is `mafin-coreo-<SLUG>` in the `mafin`
//     namespace (or plain `mafin-coreo` when there's no slug, i.e. staging)
func coreoDeploymentKeyForURL(coreoURL string) string {
	if coreoURL == "" {
		return ""
	}
	slug := coreoSlugFromURL(coreoURL)
	if slug == "" {
		return mafinNamespace + "/" + strings.TrimSuffix(coreoDeployNamePrefix, "-")
	}
	return mafinNamespace + "/" + coreoDeployNamePrefix + slug
}

// isCoreoImage mirrors isWebtopImage for the backend repo.
func isCoreoImage(image string) bool {
	if image == coreoImageRepo {
		return true
	}
	return strings.HasPrefix(image, coreoImageRepo+":") ||
		strings.HasPrefix(image, coreoImageRepo+"@")
}

// webtopImageTag returns the tag of the first webtop container in d, or "".
func webtopImageTag(d k8s.Deployment) string {
	for _, c := range d.Containers {
		if isWebtopImage(c.Image) {
			return imageTag(c.Image)
		}
	}
	return ""
}

// imageTag extracts the tag from "<repo>:<tag>" or the digest from
// "<repo>@<digest>". Returns "" for a bare repo (implicit :latest).
func imageTag(image string) string {
	if i := strings.LastIndex(image, "@"); i > 0 {
		return image[i+1:] // sha256:...
	}
	if i := strings.LastIndex(image, ":"); i > 0 {
		// Guard against a port like "ghcr.io:443" — the colon must be after the last "/".
		if slash := strings.LastIndex(image, "/"); slash > i {
			return ""
		}
		return image[i+1:]
	}
	return ""
}

// githubRefURL returns a clickable URL for a tag/branch/version ref. Empty
// for unsupported refs (digest pinning) where we can't map back to a git
// commit.
func githubRefURL(owner, repo, ref string) string {
	if ref == "" || strings.HasPrefix(ref, "sha256:") {
		return ""
	}
	return fmt.Sprintf("https://github.com/%s/%s/tree/%s", owner, repo, ref)
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
	WebtopTag string     // image tag the webtop is running
	CoreoTag  string     // image tag the matched coreo deployment is running
	WebtopPR  *github.PR // PR that spawned this webtop deployment, if any
	CoreoPR   *github.PR // PR that spawned the coreo backend, if any
}

// noCoreoLabel is shown when MAFIN_URL isn't set on the webtop pod.
const noCoreoLabel = "(no coreo)"

// webtopGroup is one row in the rendered table — all webtops sharing the
// same coreo backend. Each cell value is a pre-rendered string that may
// span multiple lines (URL on the first line; PR + tag refs underneath).
type webtopGroup struct {
	Coreo   string   // multi-line coreo cell content
	Webtops []string // each entry: multi-line webtop cell content
}

// groups buckets entries by Backend URL and renders each cell. Coreo URLs
// are sorted alphabetically with no-coreo last; webtops within each group
// are sorted alphabetically by URL.
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

	prPad := d.prLabelWidth()

	out := make([]webtopGroup, 0, len(keys))
	for _, k := range keys {
		items := buckets[k]
		sort.Slice(items, func(a, b int) bool { return items[a].URL < items[b].URL })

		coreoLabel := k
		if coreoLabel == "" {
			coreoLabel = noCoreoLabel
		}
		// Coreo cell: URL line + optional metadata line. CoreoPR and
		// CoreoTag are the same for every entry in this group.
		coreoCell := renderCell(coreoLabel, items[0].CoreoPR,
			items[0].CoreoTag, coreoRepoOwner, coreoRepoName, prPad)

		webtops := make([]string, 0, len(items))
		for _, e := range items {
			url := e.URL
			if url == "" {
				url = "-"
			}
			webtops = append(webtops, renderCell(url, e.WebtopPR,
				e.WebtopTag, webtopRepoOwner, webtopRepoName, prPad))
		}

		out = append(out, webtopGroup{
			Coreo:   coreoCell,
			Webtops: webtops,
		})
	}
	return out
}

// prLabelWidth returns the width of the longest "PR #NNN" label across all
// entries, so we can pad every PR slot to the same width and have the tag
// column line up under itself.
func (d *webtopData) prLabelWidth() int {
	max := 0
	consider := func(pr *github.PR) {
		if pr == nil {
			return
		}
		if w := len(fmt.Sprintf("PR #%d", pr.Number)); w > max {
			max = w
		}
	}
	for _, e := range d.entries {
		consider(e.WebtopPR)
		consider(e.CoreoPR)
	}
	return max
}

// renderCell builds the multi-line content of one cell:
//
//	<url-or-label>
//	  <PR padded to prPad>  <tag>     <- only if PR and/or tag is set
//
// The PR slot is padded to `prPad` width across every row so the tag
// column lines up vertically. Rows with no PR (but with a tag, when at
// least one other row has a PR) fill the slot with spaces.
//
// For PR-backed deployments the image tag is the EFFECTIVE_SLUG of the
// branch, not a real git ref — GitHub returns 404 for tree/<slug>. We
// instead link to tree/<PR.HeadRef> (the actual branch name) while keeping
// the slug as the visible label, since the slug is what's literally
// deployed in the cluster.
func renderCell(urlOrLabel string, pr *github.PR, tag, repoOwner, repoName string, prPad int) string {
	if pr == nil && tag == "" {
		return urlOrLabel
	}

	var b strings.Builder
	b.WriteString("  ")

	switch {
	case pr != nil:
		label := fmt.Sprintf("PR #%d", pr.Number)
		b.WriteString(hyperlink(pr.URL, prLinkStyle.Render(label)))
		if tag != "" {
			// Pad to align the tag column.
			if pad := prPad - len(label); pad > 0 {
				b.WriteString(strings.Repeat(" ", pad))
			}
		}
	case tag != "" && prPad > 0:
		// No PR on this row, but the table has a PR column elsewhere — fill
		// the slot with spaces so the tag column still aligns.
		b.WriteString(strings.Repeat(" ", prPad))
	}

	if tag != "" {
		if pr != nil || prPad > 0 {
			b.WriteString("  ")
		}
		ref := tag
		if pr != nil && pr.HeadRef != "" {
			ref = pr.HeadRef
		}
		b.WriteString(tagLink(tag, githubRefURL(repoOwner, repoName, ref)))
	}

	return urlOrLabel + "\n" + b.String()
}

// renderWebtopTable renders groups as a framed lipgloss table. Each cell
// content is already multi-line (URL plus an indented metadata line); the
// table just frames it and draws separators between coreo groups.
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

	return t.Render()
}

var (
	prLinkStyle  = lipgloss.NewStyle().Foreground(styles.ColorMutedAccent)
	tagLinkStyle = lipgloss.NewStyle().Foreground(styles.ColorMutedWarn)
)

// tagLink renders the image tag as a clickable GitHub ref link in warn
// color. Empty URL ⇒ no hyperlink, just the colored label.
func tagLink(label, url string) string {
	styled := tagLinkStyle.Render(label)
	if url == "" {
		return styled
	}
	return hyperlink(url, styled)
}

func hyperlink(url, body string) string {
	return fmt.Sprintf("\x1b]8;;%s\x1b\\%s\x1b]8;;\x1b\\", url, body)
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
