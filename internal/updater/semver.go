package updater

import (
	"strconv"
	"strings"
)

// isNewer reports whether latest is strictly newer than current.
//
// Inputs are plain semver-ish strings ("1.2.3", "1.2.3-rc.1", "v1.2.3"). We
// treat a pre-release (anything after `-`) as older than its release per
// semver §11. Build metadata (`+...`) is ignored.
func isNewer(latest, current string) bool {
	return cmpSemver(latest, current) > 0
}

func cmpSemver(a, b string) int {
	ma, pa := parseSemver(a)
	mb, pb := parseSemver(b)
	for i := 0; i < 3; i++ {
		if ma[i] != mb[i] {
			if ma[i] > mb[i] {
				return 1
			}
			return -1
		}
	}
	// Major.Minor.Patch equal — compare pre-release tags.
	switch {
	case pa == "" && pb == "":
		return 0
	case pa == "" && pb != "":
		return 1 // release > pre-release
	case pa != "" && pb == "":
		return -1
	default:
		return strings.Compare(pa, pb)
	}
}

// parseSemver returns the (major, minor, patch) tuple and the pre-release tag.
func parseSemver(v string) ([3]int, string) {
	v = strings.TrimPrefix(v, "v")
	// Drop build metadata.
	if i := strings.Index(v, "+"); i >= 0 {
		v = v[:i]
	}
	var pre string
	if i := strings.Index(v, "-"); i >= 0 {
		pre = v[i+1:]
		v = v[:i]
	}
	parts := strings.SplitN(v, ".", 3)
	var out [3]int
	for i := 0; i < len(parts) && i < 3; i++ {
		n, _ := strconv.Atoi(parts[i])
		out[i] = n
	}
	return out, pre
}
