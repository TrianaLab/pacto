package dashboard

import (
	"os"
	"regexp"
	"slices"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// TestEvidenceGatesMatchFrontend: the CLI's Checked label and the
// dashboard's verdictBadge read one rule, so the gates the frontend labels
// Checked are exactly delivery.EvidenceGates.
func TestEvidenceGatesMatchFrontend(t *testing.T) {
	src, err := os.ReadFile("frontend/src/lib/delivery.ts")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`const EVIDENCE_GATES = new Set\(\[([^\]]*)\]\)`).FindSubmatch(src)
	if m == nil {
		t.Fatal("EVIDENCE_GATES not found in frontend/src/lib/delivery.ts")
	}
	var ts []string
	for _, g := range regexp.MustCompile(`'([^']+)'`).FindAllSubmatch(m[1], -1) {
		ts = append(ts, string(g[1]))
	}
	want := slices.Clone(delivery.EvidenceGates)
	slices.Sort(ts)
	slices.Sort(want)
	if !slices.Equal(ts, want) {
		t.Fatalf("frontend EVIDENCE_GATES %v, delivery.EvidenceGates %v", ts, want)
	}
}
