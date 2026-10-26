package diff

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func TestDiffChecks(t *testing.T) {
	if got := diffChecks(nil, nil, nil, nil); got != nil {
		t.Fatalf("no checks: %v", got)
	}
	got := diffChecks(
		[]contract.Check{{Name: "a", File: "a.yaml"}, {Name: "b", File: "b.yaml"}},
		[]contract.Check{{Name: "b", File: "b2.yaml"}, {Name: "c", File: "c.yaml"}}, nil, nil)
	want := []string{"checks removed POTENTIAL_BREAKING", "checks.file modified POTENTIAL_BREAKING", "checks added NON_BREAKING"}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i, c := range got {
		if s := c.Path + " " + c.Type.String() + " " + c.Classification.String(); s != want[i] {
			t.Errorf("%d: got %s, want %s", i, s, want[i])
		}
	}
}

// TestDiffChecksSuiteContent: a release that edits a suite in place (drops
// the cases it fails, say) is potentially breaking; the same bytes, or no
// bundles to read, is no change.
func TestDiffChecksSuiteContent(t *testing.T) {
	a := []contract.Check{{Name: "evals", File: "evals/suite.yaml"}}
	full := fstest.MapFS{"evals/suite.yaml": {Data: []byte("cases: [refund, chargeback]\n")}}
	weaker := fstest.MapFS{"evals/suite.yaml": {Data: []byte("cases: [refund]\n")}}
	got := diffChecks(a, a, full, weaker)
	if len(got) != 1 || got[0].Classification != PotentialBreaking || got[0].Type != Modified || got[0].Reason != "the suite evals/suite.yaml of check evals or a file beside it changed" {
		t.Fatalf("suite edited: %+v", got)
	}
	// suite.yaml unchanged, but the cases it loads from beside it weakened.
	withCases := fstest.MapFS{"evals/suite.yaml": full["evals/suite.yaml"], "evals/cases.csv": {Data: []byte("refund\nchargeback\n")}}
	fewerCases := fstest.MapFS{"evals/suite.yaml": full["evals/suite.yaml"], "evals/cases.csv": {Data: []byte("refund\n")}}
	if got := diffChecks(a, a, withCases, fewerCases); len(got) != 1 || got[0].Classification != PotentialBreaking {
		t.Fatalf("a case file beside the suite changed: %+v", got)
	}
	if got := diffChecks(a, a, full, fstest.MapFS{}); len(got) != 1 {
		t.Fatalf("suite gone from the bundle: %+v", got)
	}
	for name, fsys := range map[string][2]fs.FS{"same bytes": {full, full}, "no bundles": {nil, full}, "both unreadable": {fstest.MapFS{}, fstest.MapFS{}}} {
		if got := diffChecks(a, a, fsys[0], fsys[1]); len(got) != 0 {
			t.Errorf("%s: %+v", name, got)
		}
	}
}
