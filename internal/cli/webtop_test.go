package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jiripisa/kitchen/internal/github"
	"github.com/jiripisa/kitchen/internal/k8s"
)

func TestIsWebtopImage(t *testing.T) {
	cases := []struct {
		image string
		want  bool
	}{
		{"ghcr.io/finforce/mafin-coreo-app:chore-coreo-1101", true},
		{"ghcr.io/finforce/mafin-coreo-app:1.2.3", true},
		{"ghcr.io/finforce/mafin-coreo-app@sha256:abc123", true},
		{"ghcr.io/finforce/mafin-coreo-app", true},
		{"ghcr.io/finforce/mafin-coreo-app-helper:foo", false},
		{"ghcr.io/other/mafin-coreo-app:1", false},
		{"nginx:1.27", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.image, func(t *testing.T) {
			if got := isWebtopImage(tc.image); got != tc.want {
				t.Fatalf("isWebtopImage(%q) = %v, want %v", tc.image, got, tc.want)
			}
		})
	}
}

func TestWebtopBackend(t *testing.T) {
	cases := []struct {
		name string
		d    k8s.Deployment
		want string
	}{
		{
			name: "backend present on webtop container",
			d: k8s.Deployment{Containers: []k8s.Container{{
				Name:  "mafin-coreo-app",
				Image: "ghcr.io/finforce/mafin-coreo-app:foo",
				Env:   map[string]string{"MAFIN_URL": "https://coreo.mafin.finforce.dev"},
			}}},
			want: "https://coreo.mafin.finforce.dev",
		},
		{
			name: "env on a sibling container is ignored",
			d: k8s.Deployment{Containers: []k8s.Container{
				{Name: "sidecar", Image: "envoyproxy/envoy:v1", Env: map[string]string{"MAFIN_URL": "https://wrong"}},
				{Name: "app", Image: "ghcr.io/finforce/mafin-coreo-app:foo", Env: map[string]string{"MAFIN_URL": "https://right"}},
			}},
			want: "https://right",
		},
		{
			name: "no webtop container",
			d: k8s.Deployment{Containers: []k8s.Container{
				{Name: "nginx", Image: "nginx", Env: map[string]string{"MAFIN_URL": "x"}},
			}},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := webtopBackend(tc.d); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIsWebtopDeployment(t *testing.T) {
	cases := []struct {
		name string
		d    k8s.Deployment
		want bool
	}{
		{
			name: "single webtop container",
			d:    k8s.Deployment{Containers: []k8s.Container{{Name: "mafin-coreo-app", Image: "ghcr.io/finforce/mafin-coreo-app:main"}}},
			want: true,
		},
		{
			name: "no containers",
			d:    k8s.Deployment{},
			want: false,
		},
		{
			name: "name says webtop but image does not — not webtop",
			d: k8s.Deployment{
				Name:       "mafin-coreo-app-something",
				Containers: []k8s.Container{{Name: "app", Image: "nginx:latest"}},
			},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isWebtopDeployment(tc.d); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildIngressURLIndex(t *testing.T) {
	endpoints := []k8s.IngressEndpoint{
		{Namespace: "mafin", ServiceName: "mafin-coreo-app-main", Host: "webtop-main.mafin.finforce.dev"},
		{Namespace: "mafin", ServiceName: "mafin-coreo-app-main", Host: "duplicate.host.dev"}, // ignored
		{Namespace: "other", ServiceName: "mafin-coreo-app-main", Host: "other-main.dev"},
	}
	got := buildIngressURLIndex(endpoints)
	want := map[string]string{
		"mafin/mafin-coreo-app-main": "https://webtop-main.mafin.finforce.dev",
		"other/mafin-coreo-app-main": "https://other-main.dev",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildIngressURLIndex mismatch:\ngot:  %v\nwant: %v", got, want)
	}
}

func TestWebtopSlugFromName(t *testing.T) {
	cases := []struct {
		name, want string
	}{
		{"mafin-coreo-app-feat-coreo-101", "feat-coreo-101"},
		{"mafin-coreo-app-main", "main"},
		{"mafin-coreo-app", ""}, // staging — no suffix
		{"unrelated-name", "unrelated-name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := webtopSlugFromName(tc.name); got != tc.want {
				t.Fatalf("webtopSlugFromName(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestCoreoSlugFromURL(t *testing.T) {
	cases := []struct {
		url, want string
	}{
		{"https://coreo-feature-101.mafin.finforce.dev", "feature-101"},
		{"https://coreo.mafin.finforce.dev", ""}, // canonical (no suffix)
		{"https://coreo-main.mafin.finforce.dev/foo/bar", "main"},
		{"https://webtop-foo.mafin.finforce.dev", ""}, // not a coreo URL
		{"https://elsewhere.example.com", ""},
		{"", ""},
		{"not a url at all", ""},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			if got := coreoSlugFromURL(tc.url); got != tc.want {
				t.Fatalf("coreoSlugFromURL(%q) = %q, want %q", tc.url, got, tc.want)
			}
		})
	}
}

// TestWebtopDataGroups exercises the grouping & PR-enrichment shape. PR
// refs are kept separately from the URL strings so the renderer can place
// them in the borderless side column.
func TestWebtopDataGroups(t *testing.T) {
	webtopPR1 := &github.PR{Number: 1, URL: "https://github.com/x/webtop/pull/1"}
	coreoPR7 := &github.PR{Number: 7, URL: "https://github.com/x/coreo/pull/7"}

	d := &webtopData{
		entries: []webtopEntry{
			{Namespace: "mafin", Name: "mafin-coreo-app-a", Backend: "https://coreo.main", URL: "https://webtop-a.dev",
				WebtopPR: webtopPR1},
			{Namespace: "mafin", Name: "mafin-coreo-app-b", Backend: "https://coreo.main", URL: "https://webtop-b.dev"},
			{Namespace: "mafin", Name: "mafin-coreo-app-feat", Backend: "https://coreo-feat.dev", URL: "https://webtop-feat.dev",
				CoreoPR: coreoPR7},
			{Namespace: "mafin", Name: "mafin-coreo-app-broken", Backend: "", URL: ""},
		},
	}
	groups := d.groups()

	if got, want := len(groups), 3; got != want {
		t.Fatalf("got %d groups, want %d", got, want)
	}
	// Group 0: coreo-feat — carries coreo PR.
	if groups[0].Coreo != "https://coreo-feat.dev" {
		t.Fatalf("groups[0].Coreo = %q", groups[0].Coreo)
	}
	if groups[0].CoreoPR != coreoPR7 {
		t.Fatalf("groups[0].CoreoPR = %+v, want %+v", groups[0].CoreoPR, coreoPR7)
	}
	if got, want := groups[0].Webtops[0], "https://webtop-feat.dev"; got != want {
		t.Fatalf("groups[0].Webtops[0] = %q, want %q", got, want)
	}
	if groups[0].WebtopPRs[0] != nil {
		t.Fatalf("groups[0].WebtopPRs[0] should be nil, got %+v", groups[0].WebtopPRs[0])
	}
	// Group 1: coreo.main — two webtops, first carries a webtop PR.
	if groups[1].Coreo != "https://coreo.main" {
		t.Fatalf("groups[1].Coreo = %q", groups[1].Coreo)
	}
	if groups[1].CoreoPR != nil {
		t.Fatalf("groups[1].CoreoPR should be nil, got %+v", groups[1].CoreoPR)
	}
	if got, want := len(groups[1].Webtops), 2; got != want {
		t.Fatalf("groups[1].Webtops len = %d, want %d", got, want)
	}
	if groups[1].WebtopPRs[0] != webtopPR1 {
		t.Fatalf("groups[1].WebtopPRs[0] = %+v, want %+v", groups[1].WebtopPRs[0], webtopPR1)
	}
	// Group 2: no coreo at the bottom.
	if groups[2].Coreo != noCoreoLabel {
		t.Fatalf("groups[2].Coreo = %q, want %q", groups[2].Coreo, noCoreoLabel)
	}
	if got, want := groups[2].Webtops[0], "-"; got != want {
		t.Fatalf("groups[2].Webtops[0] = %q, want %q", got, want)
	}
}

// TestPRSuffix exercises the per-line PR column rendering — webtop PR first,
// then coreo PR (only on the first webtop in a group). Lines with no PR
// produce an empty suffix so we don't emit trailing whitespace.
func TestPRSuffix(t *testing.T) {
	wpr := &github.PR{Number: 11, URL: "https://github.com/x/w/pull/11"}
	cpr := &github.PR{Number: 22, URL: "https://github.com/x/c/pull/22"}

	cases := []struct {
		name     string
		g        webtopGroup
		wi       int
		contains []string
		empty    bool
	}{
		{
			name:  "no PRs on a line returns empty string",
			g:     webtopGroup{Webtops: []string{"a"}, WebtopPRs: []*github.PR{nil}},
			wi:    0,
			empty: true,
		},
		{
			name:     "webtop-only PR on first row",
			g:        webtopGroup{Webtops: []string{"a"}, WebtopPRs: []*github.PR{wpr}},
			wi:       0,
			contains: []string{"PR #11"},
		},
		{
			name:     "coreo PR appears on first row alongside webtop PR",
			g:        webtopGroup{Coreo: "c", CoreoPR: cpr, Webtops: []string{"a", "b"}, WebtopPRs: []*github.PR{wpr, nil}},
			wi:       0,
			contains: []string{"PR #11", "PR #22"},
		},
		{
			name:  "coreo PR is NOT repeated on a continuation row",
			g:     webtopGroup{Coreo: "c", CoreoPR: cpr, Webtops: []string{"a", "b"}, WebtopPRs: []*github.PR{wpr, nil}},
			wi:    1,
			empty: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := prSuffix(tc.g, tc.wi)
			if tc.empty {
				if got != "" {
					t.Fatalf("expected empty suffix, got %q", got)
				}
				return
			}
			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Fatalf("suffix %q missing %q", got, want)
				}
			}
		})
	}
}
