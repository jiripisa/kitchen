package log

import (
	"testing"
)

func TestSubstringFilter(t *testing.T) {
	targets := []string{
		"mafin-auth",
		"mafin-coreo-main",
		"mafin-coreo-app-main2",
		"mafin-coreo-app-main",
		"frontend",
	}

	cases := []struct {
		term string
		want []string
	}{
		{"main", []string{"mafin-coreo-main", "mafin-coreo-app-main2", "mafin-coreo-app-main"}},
		{"MAIN", []string{"mafin-coreo-main", "mafin-coreo-app-main2", "mafin-coreo-app-main"}}, // case-insensitive
		{"auth", []string{"mafin-auth"}},
		{"xyz", nil},
		{"", nil},
	}

	for _, tc := range cases {
		t.Run(tc.term, func(t *testing.T) {
			ranks := substringFilter(tc.term, targets)
			got := make([]string, len(ranks))
			for i, r := range ranks {
				got[i] = targets[r.Index]
			}
			if len(got) != len(tc.want) {
				t.Fatalf("term %q: got %v, want %v", tc.term, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("term %q: got %v, want %v", tc.term, got, tc.want)
				}
			}
		})
	}
}
