package github

import "testing"

func TestEffectiveSlug(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"main", "main"},
		{"feature/coreo-101", "feature-coreo-101"},
		{"refs/heads/feature/coreo-101", "feature-coreo-101"},
		// Real-world example from the cluster — matches the actual deployment
		// suffix (45 chars, truncated mid-word).
		{"chore/COREO-1101/dividendy/bugfix/zapis-na-chybu", "chore-coreo-1101-dividendy-bugfix-zapis-na-ch"},
		{"feat/wt-471-deposits-rework-reject-button-", "feat-wt-471-deposits-rework-reject-button"},
		// Only trailing dashes are stripped (matches upstream `sed 's/-*$//'`);
		// leading and embedded dashes survive.
		{"!!special$$chars##", "--special--chars"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := EffectiveSlug(tc.in); got != tc.want {
				t.Fatalf("EffectiveSlug(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
