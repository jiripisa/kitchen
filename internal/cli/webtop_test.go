package cli

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/jiripisa/kitchen/internal/k8s"
)

func TestBuildWebtopRows(t *testing.T) {
	in := []webtopEntry{
		// out of order on purpose; entries with the same coreo are sorted
		// alphabetically by webtop URL.
		{Namespace: "mafin", Name: "app-b", Backend: "https://coreo.main", URL: "https://b.dev"},
		{Namespace: "mafin", Name: "no-coreo", Backend: "", URL: ""},
		{Namespace: "mafin", Name: "app-a", Backend: "https://coreo.main", URL: "https://a.dev"},
		{Namespace: "mafin", Name: "feat-app", Backend: "https://coreo-feat-1", URL: "https://feat.dev"},
		{Namespace: "other", Name: "shared", Backend: "https://coreo.main", URL: "https://shared.dev"},
	}
	got := buildWebtopRows(in)

	want := []webtopRow{
		{Coreo: "https://coreo-feat-1", Webtop: "https://feat.dev"},
		{Coreo: "https://coreo.main", Webtop: "https://a.dev"},
		{Coreo: "https://coreo.main", Webtop: "https://b.dev"},
		{Coreo: "https://coreo.main", Webtop: "https://shared.dev"},
		{Coreo: "(no coreo)", Webtop: "-"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildWebtopRows mismatch:\ngot:  %+v\nwant: %+v", got, want)
	}
}

func TestRenderWebtopTable(t *testing.T) {
	rows := []webtopRow{
		{Coreo: "https://coreo.feat", Webtop: "https://feat.dev"},
		{Coreo: "https://coreo.main", Webtop: "https://a.dev"},
		{Coreo: "https://coreo.main", Webtop: "https://b.dev"},
		{Coreo: "(no coreo)", Webtop: "-"},
	}
	var buf bytes.Buffer
	renderWebtopTable(&buf, rows)

	// Column widths:
	//   WEBTOP = max(len("WEBTOP")=6, len("https://feat.dev")=16) = 16
	//   COREO  = max(len("COREO")=5,  len("https://coreo.main")=18) = 18
	//
	// Note: the COREO column is blank on the continuation row (a.dev → b.dev
	// share the same coreo).
	want := "" +
		"WEBTOP            COREO\n" +
		"----------------  ------------------\n" +
		"https://feat.dev  https://coreo.feat\n" +
		"https://a.dev     https://coreo.main\n" +
		"https://b.dev\n" +
		"-                 (no coreo)\n"
	if got := buf.String(); got != want {
		t.Fatalf("render mismatch:\ngot:\n%q\nwant:\n%q", got, want)
	}
}

func TestBuildIngressURLIndex(t *testing.T) {
	endpoints := []k8s.IngressEndpoint{
		{Namespace: "mafin", ServiceName: "mafin-coreo-app-main", Host: "webtop-main.mafin.finforce.dev"},
		{Namespace: "mafin", ServiceName: "mafin-coreo-app-main", Host: "duplicate.host.dev"}, // ignored (first wins)
		{Namespace: "other", ServiceName: "mafin-coreo-app-main", Host: "other-main.dev"},     // different ns
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

func TestRenderWebtopTableEmpty(t *testing.T) {
	var buf bytes.Buffer
	renderWebtopTable(&buf, nil)
	if buf.Len() != 0 {
		t.Fatalf("expected empty output, got %q", buf.String())
	}
}

func TestIsWebtopImage(t *testing.T) {
	cases := []struct {
		image string
		want  bool
	}{
		// Positive: review-app with feature-branch slug.
		{"ghcr.io/finforce/mafin-coreo-app:chore-coreo-1101", true},
		// Positive: staging release tag.
		{"ghcr.io/finforce/mafin-coreo-app:1.2.3", true},
		// Positive: digest-pinned production.
		{"ghcr.io/finforce/mafin-coreo-app@sha256:abc123", true},
		// Positive: bare repo (implicit :latest).
		{"ghcr.io/finforce/mafin-coreo-app", true},

		// Negative: sibling project sharing the org prefix.
		{"ghcr.io/finforce/mafin-coreo-app-helper:foo", false},
		// Negative: same name in a different org.
		{"ghcr.io/other/mafin-coreo-app:1", false},
		// Negative: unrelated image.
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
			name: "no env at all",
			d: k8s.Deployment{Containers: []k8s.Container{{
				Name:  "mafin-coreo-app",
				Image: "ghcr.io/finforce/mafin-coreo-app:foo",
			}}},
			want: "",
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
			name: "unrelated containers",
			d: k8s.Deployment{Containers: []k8s.Container{
				{Name: "nginx", Image: "nginx:1.27"},
				{Name: "redis", Image: "redis:7"},
			}},
			want: false,
		},
		{
			name: "webtop image in second container (e.g. sidecar layout)",
			d: k8s.Deployment{Containers: []k8s.Container{
				{Name: "proxy", Image: "envoyproxy/envoy:v1.30"},
				{Name: "app", Image: "ghcr.io/finforce/mafin-coreo-app:feat-foo"},
			}},
			want: true,
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
