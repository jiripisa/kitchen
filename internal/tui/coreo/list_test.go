package coreo

import "testing"

// TestComputeColWidths_TagStaysVisible captures the invariant the user asked
// for after v0.6.2: the tag column (which carries the clickable branch link)
// should remain visible across the full range of plausible left-panel widths
// in two-panel mode. It only drops at extreme narrowness, where dropping is
// preferable to having a 12-character URL.
func TestComputeColWidths_TagStaysVisible(t *testing.T) {
	entries := []entry{
		{
			URL: "https://coreo-feature-coreo-506-rebalance.mafin.finforce.dev",
			Tag: "feature-coreo-506-rebalance",
		},
		{
			URL: "https://coreo.mafin.finforce.dev",
			Tag: "main",
		},
	}

	cases := []struct {
		leftWidth      int
		wantTagVisible bool
	}{
		// Very wide — both URL and tag at their data-driven sizes.
		{leftWidth: 130, wantTagVisible: true},
		// Comfortable two-panel left (after the bumped leftMinPanelWidth=55).
		{leftWidth: 80, wantTagVisible: true},
		// Tight two-panel left.
		{leftWidth: 60, wantTagVisible: true},
		// Right at the leftMin floor.
		{leftWidth: 55, wantTagVisible: true},
		// Even tighter than the floor — tag should still survive.
		{leftWidth: 50, wantTagVisible: true},
		// Way below sensible: tag finally drops to give URL room.
		{leftWidth: 35, wantTagVisible: false},
	}
	for _, tc := range cases {
		cw := computeColWidths(entries, tc.leftWidth)
		if cw.hasTag != tc.wantTagVisible {
			t.Errorf("leftWidth=%d: hasTag=%v, want %v (cw=%+v)",
				tc.leftWidth, cw.hasTag, tc.wantTagVisible, cw)
		}
		if cw.hasTag && cw.tag < tagMinDisplay {
			t.Errorf("leftWidth=%d: tag width %d below minDisplay %d",
				tc.leftWidth, cw.tag, tagMinDisplay)
		}
		if cw.url < urlMinDisplay {
			t.Errorf("leftWidth=%d: url width %d below minDisplay %d",
				tc.leftWidth, cw.url, urlMinDisplay)
		}
	}
}
