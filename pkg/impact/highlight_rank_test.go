package impact

import (
	"encoding/json"
	"os"
	"testing"
)

// TestHighlightRankFixture pins highlightRank to testdata/highlight-rank.json. The
// dashboard's isoImpact.test.ts reads the same file through verdictOf, so the two
// rankings cannot drift apart without one of the tests failing.
func TestHighlightRankFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/highlight-rank.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name string `json:"name"`
		ProductConsumer
		Rank int `json:"rank"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("empty fixture")
	}
	for _, tc := range cases {
		if got := highlightRank(tc.ProductConsumer); got != tc.Rank {
			t.Errorf("%s: highlightRank = %d, want %d", tc.Name, got, tc.Rank)
		}
	}
}
