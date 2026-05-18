// Package coreo implements the `kitchen coreo list` Bubble Tea program: a
// fullscreen picker over every coreo deployment in the current kubeconfig
// context, decorated with its PR link, image tag, last-log age, and the
// number of webtops currently pointing at it (via MAFIN_URL).
package coreo

import (
	"context"
	"fmt"
	neturl "net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jiripisa/kitchen/internal/github"
	"github.com/jiripisa/kitchen/internal/k8s"
)

// Image and env-var conventions are documented in docs/upstream-pipelines.md.
// Duplicated from internal/tui/webtop/data.go on purpose: the two TUIs are
// siblings, not subsets of each other, and the constants are tiny enough that
// extracting a shared package would buy more imports than it saves.
const (
	webtopImageRepo     = "ghcr.io/finforce/mafin-coreo-app"
	coreoImageRepo      = "ghcr.io/finforce/mafin-coreo"
	webtopBackendEnvVar = "MAFIN_URL"

	coreoRepoOwner = "finforce"
	coreoRepoName  = "mafin-coreo"

	coreoDeployNamePrefix  = "mafin-coreo-"
	coreoIngressHostPrefix = "coreo-"
	mafinHostSuffix        = ".mafin.finforce.dev"

	mafinNamespace = "mafin"
)

// entry is one coreo instance with everything we display about it.
type entry struct {
	Namespace   string
	Name        string
	URL         string     // ingress host as https://…
	Tag         string     // image tag actually deployed
	PR          *github.PR // PR that spawned this coreo, if any
	LastLog     time.Time  // most recent log line of the first pod (zero ⇒ unknown)
	WebtopCount int        // how many webtops have MAFIN_URL == URL
	IsMain      bool       // canonical no-suffix staging coreo
}

// fetchEntries gathers everything we need to render one `kitchen coreo list`
// table: deployments, ingresses, coreo PRs, last log times. All fetches run
// in parallel.
func fetchEntries(ctx context.Context, client *k8s.Client) ([]entry, error) {
	var (
		deps      []k8s.Deployment
		ingresses []k8s.IngressEndpoint
		prs       github.Index
		depsErr   error
		wg        sync.WaitGroup
	)

	wg.Add(3)
	go func() {
		defer wg.Done()
		deps, depsErr = client.ListAllDeployments(ctx)
	}()
	go func() {
		defer wg.Done()
		ingresses, _ = client.ListAllIngresses(ctx)
	}()
	go func() {
		defer wg.Done()
		prs, _ = github.FetchIndex(ctx, coreoRepoOwner, coreoRepoName)
	}()
	wg.Wait()

	if depsErr != nil {
		return nil, depsErr
	}
	return entriesFromInputs(deps, buildIngressURLIndex(ingresses), prs, fetchLastLogTimes(ctx, client, deps)), nil
}

// entriesFromInputs builds the displayable entries from the independently
// fetched data sources. Any input map may be nil; the resulting entries
// simply carry the corresponding fields empty.
func entriesFromInputs(
	deps []k8s.Deployment,
	urls map[string]string,
	prs github.Index,
	logTimes map[string]time.Time,
) []entry {
	// Build the webtop → backend map once so the entry loop can read it.
	webtopCountByURL := map[string]int{}
	for _, d := range deps {
		if !isWebtopDeployment(d) {
			continue
		}
		if be := webtopBackend(d); be != "" {
			webtopCountByURL[be]++
		}
	}

	out := make([]entry, 0, len(deps))
	for _, d := range deps {
		if !isCoreoDeployment(d) {
			continue
		}
		key := d.Namespace + "/" + d.Name
		e := entry{
			Namespace: d.Namespace,
			Name:      d.Name,
			Tag:       coreoImageTagOf(d),
			IsMain:    d.Name == "mafin-coreo",
		}
		if urls != nil {
			e.URL = urls[key]
		}
		if prs != nil {
			if pr, ok := prs[coreoSlugFromDeploymentName(d.Name)]; ok {
				e.PR = &pr
			}
		}
		if logTimes != nil {
			e.LastLog = logTimes[key]
		}
		if e.URL != "" {
			e.WebtopCount = webtopCountByURL[e.URL]
		}
		out = append(out, e)
	}

	// Sort: staging first (URL `https://coreo.mafin…` < `https://coreo-…`),
	// then alphabetically by URL. Stable so unloaded URLs don't reshuffle.
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case a.URL == "" && b.URL != "":
			return false
		case a.URL != "" && b.URL == "":
			return true
		case a.URL != b.URL:
			return a.URL < b.URL
		default:
			return a.Name < b.Name
		}
	})
	return out
}

// fetchLastLogTimes asks every coreo deployment for its most recent log
// timestamp, in parallel. Mirrors the helper in the webtop package but with
// a coreo-only filter so we don't pay the API for unrelated deployments.
func fetchLastLogTimes(ctx context.Context, client *k8s.Client, deps []k8s.Deployment) map[string]time.Time {
	type result struct {
		key string
		t   time.Time
	}

	relevant := make([]k8s.Deployment, 0, len(deps))
	for _, d := range deps {
		if isCoreoDeployment(d) {
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

func isCoreoDeployment(d k8s.Deployment) bool {
	for _, c := range d.Containers {
		if isCoreoImage(c.Image) {
			return true
		}
	}
	return false
}

func isWebtopDeployment(d k8s.Deployment) bool {
	for _, c := range d.Containers {
		if isWebtopImage(c.Image) {
			return true
		}
	}
	return false
}

func isCoreoImage(image string) bool {
	if image == coreoImageRepo {
		return true
	}
	return strings.HasPrefix(image, coreoImageRepo+":") ||
		strings.HasPrefix(image, coreoImageRepo+"@")
}

func isWebtopImage(image string) bool {
	if image == webtopImageRepo {
		return true
	}
	return strings.HasPrefix(image, webtopImageRepo+":") ||
		strings.HasPrefix(image, webtopImageRepo+"@")
}

// webtopBackend returns the literal MAFIN_URL set on a webtop container, or
// "" if it isn't a webtop or has no MAFIN_URL value.
func webtopBackend(d k8s.Deployment) string {
	for _, c := range d.Containers {
		if !isWebtopImage(c.Image) {
			continue
		}
		return c.Env[webtopBackendEnvVar]
	}
	return ""
}

func coreoImageTagOf(d k8s.Deployment) string {
	for _, c := range d.Containers {
		if isCoreoImage(c.Image) {
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

// coreoSlugFromDeploymentName returns the suffix part of a coreo deployment
// name, used as the key in the PR index. Returns "" for the canonical
// no-suffix `mafin-coreo` staging deployment.
func coreoSlugFromDeploymentName(name string) string {
	if name == strings.TrimSuffix(coreoDeployNamePrefix, "-") {
		return ""
	}
	return strings.TrimPrefix(name, coreoDeployNamePrefix)
}

// coreoSlugFromURL extracts the slug from `https://coreo-<slug>.mafin…`. Kept
// here for completeness — currently unused by the list but symmetric with
// the webtop package and useful when extending.
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
