package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/ci"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// TestParsePolicy: pacto impact and pacto ci refuse a bad --policy before
// reading anything, naming the form.
func TestParsePolicy(t *testing.T) {
	ciEnvOff(t)
	statsOff(t)
	for _, tc := range []struct{ value, want string }{
		{"coverage=80", "use unknown-range=warn|fail or uncontracted=warn|fail:<window>"},
		{"unknown-range=maybe", "use unknown-range=warn or unknown-range=fail"},
		{"uncontracted=fail", "such as fail:7d"},
	} {
		for _, args := range [][]string{{"impact", "a", "b"}, {"ci", ciRepo(t)}} {
			_, _, err := runRoot(t, defaultDeps(), nil, append(args, "--policy", tc.value)...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s --policy %s: %v", args[0], tc.value, err)
			}
		}
	}
}

func TestImpactPrintsRangesLine(t *testing.T) {
	res := &impact.Result{Ranges: &impact.RangeCoverage{Scope: "consumers of ledger", Edges: 58, Declared: 41, Accepted: 6, UnknownTotal: 1,
		Unknown: []impact.UnknownRange{{From: "reports", To: "ledger", Proposed: "^2.3.0"}}},
		Notes: []string{"POLICY_WARNING: ledger -> reports range unknown, 1 consumer (policy unknown-range=warn)"}}
	var w bytes.Buffer
	printTargetSnapshots(&w, res)
	want := "Ranges: 41 declared and 6 accepted of 58 dependency edges (81%)\n  unknown: reports -> ledger (proposed ^2.3.0)\n" +
		"POLICY_WARNING: ledger -> reports range unknown, 1 consumer (policy unknown-range=warn)\n"
	if w.String() != want {
		t.Fatalf("got %q", w.String())
	}
}

func TestCIPrintsRangesLine(t *testing.T) {
	p := &ci.Plan{Ranges: &impact.RangeCoverage{Edges: 0}}
	var w bytes.Buffer
	ci.Text(&w, p)
	if !strings.Contains(w.String(), "\nRanges: no dependency edges\n") || !strings.Contains(ci.Markdown(p), "- `Ranges: no dependency edges`") {
		t.Fatalf("text:\n%s\nmarkdown:\n%s", w.String(), ci.Markdown(p))
	}
	ciEnvOff(t)
	statsOff(t)
	stdout, _, _ := runRoot(t, defaultDeps(), nil, "ci", ciRepo(t), "--registry", "127.0.0.1:1/acme")
	if !strings.Contains(stdout, "\nRanges: no dependency edges\n") {
		t.Errorf("pacto ci prints no Ranges line:\n%s", stdout)
	}
}
