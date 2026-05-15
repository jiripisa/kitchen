package webtop

import (
	"reflect"
	"testing"
	"time"

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

func TestHumanDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{-time.Hour, "0s"}, // clock skew shouldn't produce a negative duration
		{45 * time.Second, "45s"},
		{75 * time.Second, "1m"},
		{2*time.Hour + 30*time.Minute, "2h"},
		{36 * time.Hour, "1d"},
		{8 * 24 * time.Hour, "8d"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			if got := humanDuration(tc.d); got != tc.want {
				t.Fatalf("humanDuration(%v) = %q, want %q", tc.d, got, tc.want)
			}
		})
	}
}

func TestImageTag(t *testing.T) {
	cases := []struct {
		image, want string
	}{
		{"ghcr.io/finforce/mafin-coreo-app:feat-foo", "feat-foo"},
		{"ghcr.io/finforce/mafin-coreo-app:1.2.3", "1.2.3"},
		{"ghcr.io/finforce/mafin-coreo-app@sha256:deadbeef", "sha256:deadbeef"},
		{"ghcr.io/finforce/mafin-coreo-app", ""},     // bare repo
		{"ghcr.io:443/finforce/mafin-coreo-app", ""}, // ":443" before slash is a port, not a tag
	}
	for _, tc := range cases {
		t.Run(tc.image, func(t *testing.T) {
			if got := imageTag(tc.image); got != tc.want {
				t.Fatalf("imageTag(%q) = %q, want %q", tc.image, got, tc.want)
			}
		})
	}
}

func TestGithubRefURL(t *testing.T) {
	if got := githubRefURL("o", "r", "feat-foo"); got != "https://github.com/o/r/tree/feat-foo" {
		t.Fatalf("got %q", got)
	}
	if got := githubRefURL("o", "r", "sha256:abc"); got != "" {
		t.Fatalf("digest should not produce a URL, got %q", got)
	}
	if got := githubRefURL("o", "r", ""); got != "" {
		t.Fatalf("empty ref should not produce a URL, got %q", got)
	}
}

func TestCoreoDeploymentKeyForURL(t *testing.T) {
	cases := []struct {
		url, want string
	}{
		{"https://coreo-feat-101.mafin.finforce.dev", "mafin/mafin-coreo-feat-101"},
		{"https://coreo.mafin.finforce.dev", "mafin/mafin-coreo"},
		{"https://elsewhere.example.com", "mafin/mafin-coreo"}, // unknown host falls through
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			if got := coreoDeploymentKeyForURL(tc.url); got != tc.want {
				t.Fatalf("coreoDeploymentKeyForURL(%q) = %q, want %q", tc.url, got, tc.want)
			}
		})
	}
}

func TestBuildCoreoTagIndex(t *testing.T) {
	deps := []k8s.Deployment{
		{Namespace: "mafin", Name: "mafin-coreo", Containers: []k8s.Container{
			{Name: "mafin-coreo", Image: "ghcr.io/finforce/mafin-coreo:v1.2.3"},
		}},
		{Namespace: "mafin", Name: "mafin-coreo-feat-x", Containers: []k8s.Container{
			{Name: "mafin-coreo", Image: "ghcr.io/finforce/mafin-coreo:feat-x"},
		}},
		// Not coreo — should be ignored.
		{Namespace: "mafin", Name: "mafin-coreo-app-main", Containers: []k8s.Container{
			{Name: "mafin-coreo-app", Image: "ghcr.io/finforce/mafin-coreo-app:main"},
		}},
	}
	got := buildCoreoTagIndex(deps)
	want := map[string]string{
		"mafin/mafin-coreo":        "v1.2.3",
		"mafin/mafin-coreo-feat-x": "feat-x",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildCoreoTagIndex mismatch:\ngot:  %v\nwant: %v", got, want)
	}
}
