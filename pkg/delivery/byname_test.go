package delivery

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// TestRefLessDependencyRangeReadsUnknown: a parsed dependency by name reads
// unknown, never a range that cannot be read.
func TestRefLessDependencyRangeReadsUnknown(t *testing.T) {
	c, err := contract.Parse(strings.NewReader("pactoVersion: \"2.5\"\nservice: {name: reports, version: 1.0.0}\ndependencies:\n  - name: ledger\n"))
	if err != nil {
		t.Fatal(err)
	}
	_, ok, err := acceptsBoth(c, "ledger", "3.0.0", "2.0.0")
	if ok || err == nil || !strings.Contains(err.Error(), "compatibility: unknown") || strings.Contains(err.Error(), "cannot be read") {
		t.Fatalf("ok = %v, err = %v", ok, err)
	}
}
