// Package webtop implements the `kitchen webtop` Bubble Tea program: a
// fullscreen TUI that lists every webtop deployment in the current
// kubeconfig context with its coreo backend, PR + tag links, and
// time-since-last-log, then opens the deployment's YAML on selection.
package webtop

import (
	"context"
	"fmt"
	"io"
	neturl "net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jiripisa/kitchen/internal/github"
	"github.com/jiripisa/kitchen/internal/k8s"
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

	// noCoreoLabel is shown when MAFIN_URL isn't set on the webtop pod.
	noCoreoLabel = "(no coreo)"
)

// entry is one webtop instance with everything we display about it.
type entry struct {
	Namespace string
	Name      string
	Backend   string     // coreo URL the webtop talks to (MAFIN_URL)
	URL       string     // webtop URL (from ingress)
	WebtopTag string     // image tag the webtop is running
	CoreoTag  string     // image tag the matched coreo deployment is running
	WebtopPR  *github.PR // PR that spawned this webtop deployment, if any
	CoreoPR   *github.PR // PR that spawned the coreo backend, if any

	// Timestamps of the most recent log line in the first pod of each
	// deployment. Zero ⇒ unknown / not yet logged / fetch failed.
	WebtopLastLog time.Time
	CoreoLastLog  time.Time
}

// fetchEntries gathers everything we need to render one `kitchen webtop`
// table: deployments, ingresses, PRs from both upstream repos, and the
// most recent log timestamp for each deployment. All four base fetches
// run in parallel.
func fetchEntries(ctx context.Context, stderr io.Writer, client *k8s.Client) ([]entry, error) {
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
	lastLogs := fetchLastLogTimes(ctx, client, deps)

	entries := make([]entry, 0, len(deps))
	for _, d := range deps {
		if !isWebtopDeployment(d) {
			continue
		}
		e := entry{
			Namespace:     d.Namespace,
			Name:          d.Name,
			Backend:       webtopBackend(d),
			URL:           urls[d.Namespace+"/"+d.Name],
			WebtopTag:     webtopImageTag(d),
			WebtopLastLog: lastLogs[d.Namespace+"/"+d.Name],
		}
		if pr, ok := webtopPRs[webtopSlugFromName(d.Name)]; ok {
			e.WebtopPR = &pr
		}
		if pr, ok := coreoPRs[coreoSlugFromURL(e.Backend)]; ok {
			e.CoreoPR = &pr
		}
		coreoKey := coreoDeploymentKeyForURL(e.Backend)
		e.CoreoTag = coreoTags[coreoKey]
		e.CoreoLastLog = lastLogs[coreoKey]
		entries = append(entries, e)
	}

	// Sort entries: by coreo backend (ascending, no-coreo last), then by
	// webtop URL. Adjacent entries thus share a coreo, which is what the
	// list-with-grouping rendering relies on.
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i].Backend, entries[j].Backend
		switch {
		case a == "" && b != "":
			return false
		case a != "" && b == "":
			return true
		case a != b:
			return a < b
		default:
			return entries[i].URL < entries[j].URL
		}
	})

	return entries, nil
}

// fetchLastLogTimes asks every webtop and coreo deployment in `deps` for the
// timestamp of its most recent log line, in parallel.
func fetchLastLogTimes(ctx context.Context, client *k8s.Client, deps []k8s.Deployment) map[string]time.Time {
	type result struct {
		key string
		t   time.Time
	}

	relevant := make([]k8s.Deployment, 0, len(deps))
	for _, d := range deps {
		if isWebtopDeployment(d) || isCoreoDeployment(d) {
			relevant = append(relevant, d)
		}
	}

	results := make(chan result, len(relevant))
	var wg sync.WaitGroup
	for _, d := range relevant {
		wg.Add(1)
		go func(ns, name string) {
			defer wg.Done()
			rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			t, _ := client.LastLogTimeForDeployment(rctx, ns, name)
			results <- result{key: ns + "/" + name, t: t}
		}(d.Namespace, d.Name)
	}
	go func() { wg.Wait(); close(results) }()

	out := map[string]time.Time{}
	for r := range results {
		if !r.t.IsZero() {
			out[r.key] = r.t
		}
	}
	return out
}

// --- identification helpers ----------------------------------------------------

func isWebtopDeployment(d k8s.Deployment) bool {
	for _, c := range d.Containers {
		if isWebtopImage(c.Image) {
			return true
		}
	}
	return false
}

func isCoreoDeployment(d k8s.Deployment) bool {
	for _, c := range d.Containers {
		if isCoreoImage(c.Image) {
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

func isCoreoImage(image string) bool {
	if image == coreoImageRepo {
		return true
	}
	return strings.HasPrefix(image, coreoImageRepo+":") ||
		strings.HasPrefix(image, coreoImageRepo+"@")
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

func webtopImageTag(d k8s.Deployment) string {
	for _, c := range d.Containers {
		if isWebtopImage(c.Image) {
			return imageTag(c.Image)
		}
	}
	return ""
}

func imageTag(image string) string {
	if i := strings.LastIndex(image, "@"); i > 0 {
		return image[i+1:]
	}
	if i := strings.LastIndex(image, ":"); i > 0 {
		if slash := strings.LastIndex(image, "/"); slash > i {
			return ""
		}
		return image[i+1:]
	}
	return ""
}

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

// coreoDeploymentKeyForURL maps a coreo URL to the deployment key
// ("<namespace>/<deployment-name>") that serves it.
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

func webtopSlugFromName(name string) string {
	if name == strings.TrimSuffix(webtopDeployNamePrefix, "-") {
		return ""
	}
	return strings.TrimPrefix(name, webtopDeployNamePrefix)
}

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

func githubRefURL(owner, repo, ref string) string {
	if ref == "" || strings.HasPrefix(ref, "sha256:") {
		return ""
	}
	return fmt.Sprintf("https://github.com/%s/%s/tree/%s", owner, repo, ref)
}

// prLabelWidth returns the width of the longest "PR #NNN" label across
// every PR referenced from the entries.
func prLabelWidth(entries []entry) int {
	maxW := 0
	for _, e := range entries {
		for _, pr := range [...]*github.PR{e.WebtopPR, e.CoreoPR} {
			if pr == nil {
				continue
			}
			if w := len(fmt.Sprintf("PR #%d", pr.Number)); w > maxW {
				maxW = w
			}
		}
	}
	return maxW
}

// humanDuration formats a duration as "5s" / "3m" / "2h" / "4d".
func humanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
