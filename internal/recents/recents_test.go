package recents

import (
	"reflect"
	"testing"
)

func TestPushFront(t *testing.T) {
	cases := []struct {
		name string
		list []string
		item string
		want []string
	}{
		{"empty list", nil, "a", []string{"a"}},
		{"new item", []string{"b", "c"}, "a", []string{"a", "b", "c"}},
		{"dedup head", []string{"a", "b"}, "a", []string{"a", "b"}},
		{"dedup middle", []string{"b", "a", "c"}, "a", []string{"a", "b", "c"}},
		{"overflow", []string{"a", "b", "c", "d", "e"}, "f", []string{"f", "a", "b", "c", "d"}},
		{"overflow with dedup", []string{"a", "b", "c", "d", "e"}, "c", []string{"c", "a", "b", "d", "e"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pushFront(tc.list, tc.item, MaxEntries)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("pushFront(%v, %q) = %v, want %v", tc.list, tc.item, got, tc.want)
			}
		})
	}
}

func TestStoreRoundTrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	// Fresh store: no entries.
	s1, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := s1.Namespaces("prod"); got != nil {
		t.Fatalf("expected nil namespaces, got %v", got)
	}

	// Record several entries; order is newest first.
	for _, ns := range []string{"a", "b", "c", "d", "e", "f"} {
		if err := s1.RecordNamespace("prod", ns); err != nil {
			t.Fatalf("RecordNamespace(%q): %v", ns, err)
		}
	}
	got := s1.Namespaces("prod")
	want := []string{"f", "e", "d", "c", "b"} // cap = 5
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	// Deployment per namespace.
	if err := s1.RecordDeployment("prod", "kube-system", "coredns"); err != nil {
		t.Fatal(err)
	}
	if err := s1.RecordDeployment("prod", "kube-system", "metrics"); err != nil {
		t.Fatal(err)
	}
	if got := s1.Deployments("prod", "kube-system"); !reflect.DeepEqual(got, []string{"metrics", "coredns"}) {
		t.Fatalf("deployments: got %v", got)
	}

	// Contexts are isolated.
	if got := s1.Namespaces("dev"); got != nil {
		t.Fatalf("dev context should be empty: %v", got)
	}

	// Reopen: state survives.
	s2, err := Open()
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	if got := s2.Namespaces("prod"); !reflect.DeepEqual(got, want) {
		t.Fatalf("after reopen got %v, want %v", got, want)
	}
}
