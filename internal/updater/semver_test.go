package updater

import "testing"

func TestIsNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"1.2.3", "1.2.2", true},
		{"1.2.3", "1.2.3", false},
		{"1.2.3", "1.3.0", false},
		{"v2.0.0", "1.99.99", true},
		{"1.0.0", "1.0.0-rc.1", true},
		{"1.0.0", "1.0.0+meta", false},
		{"0.1.0", "dev", true},
	}
	for _, tc := range cases {
		t.Run(tc.latest+"_vs_"+tc.current, func(t *testing.T) {
			if got := isNewer(tc.latest, tc.current); got != tc.want {
				t.Fatalf("isNewer(%q, %q) = %v, want %v", tc.latest, tc.current, got, tc.want)
			}
		})
	}
}
