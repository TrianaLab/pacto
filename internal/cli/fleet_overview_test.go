package cli_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func overviewJSON(t *testing.T, args ...string) fleet.Overview {
	t.Helper()
	out, _, err := execFleet(t, append(append([]string{"fleet", "overview"}, args...), "--output-format", "json")...)
	if err != nil {
		t.Fatalf("overview json: %v", err)
	}
	var ov fleet.Overview
	if err := json.Unmarshal([]byte(out), &ov); err != nil {
		t.Fatalf("overview json: %v\n%s", err, out)
	}
	return ov
}

// TestFleetOverviewText: every line of the text answer prints the counts of
// the overview the JSON answer carries.
func TestFleetOverviewText(t *testing.T) {
	root, ev := writeFleetFixture(t)
	args := []string{"--local", root, "--target-state", ev}
	ov := overviewJSON(t, args...)
	out, _, err := execFleet(t, append([]string{"fleet", "overview"}, args...)...)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	s, h, k, o, c := ov.Summary, ov.Summary.OperationalHealth, ov.Summary.Knowledge, ov.Summary.Ownership, ov.Summary.Coverage
	for _, want := range []string{
		"completeness " + string(ov.Meta.Completeness),
		fmt.Sprintf("Targets %d: compliant %d (%d stale) · not compliant %d · unknown %d", s.Targets, s.CompliantTargets, s.StaleCompliantTargets, s.NonCompliantTargets, s.UnknownTargets),
		fmt.Sprintf("Evidence: %d targets with evidence (%d stale) · %d never observed", s.Evidence.WithEvidence, s.Evidence.Stale, s.Evidence.WithoutEvidence),
		fmt.Sprintf("Health: healthy %d · failing %d · unknown %d · not observed %d", h.Available, h.Unavailable, h.Unknown, h.NotObserved),
		fmt.Sprintf("Knowledge: %d services · %d fully described · %d with gaps · %d no summary · %d no domain · ", k.Services, k.Described, k.WithGaps, k.Gaps.NoSummary, k.Gaps.NoDomain),
		fmt.Sprintf("%d run nowhere · %d unverified capability · %d unknown", k.Gaps.NotDeployed, k.Gaps.UnverifiedCapability, k.Unknown),
		fmt.Sprintf("Owners: %d declared · %d disputed · %d none declared · %d unknown", o.Consistent, o.Conflicting, o.Unowned, o.Unknown),
		fmt.Sprintf("Coverage: %d of %d required assertions evaluated on %d targets; %d report none", c.Evaluated, c.Required, c.Reporting, c.NotReporting),
		fleet.NoBusinessDomain + "  ",
		"Next: pacto fleet attention  (Services needing attention: ",
		"Next: pacto fleet attention --category non-compliant  (Operational targets not compliant: 1)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("overview text lacks %q:\n%s", want, out)
		}
	}
	if !regexp.MustCompile(`(?m)^DOMAIN +SERVICES +NOT-COMPLIANT +UNKNOWN +STALE +FAILING +GAPS +NO-OWNER$`).MatchString(out) {
		t.Errorf("overview text lacks the domain table:\n%s", out)
	}
	if k.Services != s.Services || s.Targets == 0 {
		t.Errorf("the fixture should count every service and hold targets: %+v", s)
	}
}

// TestFleetOverviewJSONIsTheQueryOverview: the JSON answer is fleet.Overview,
// meta included.
func TestFleetOverviewJSONIsTheQueryOverview(t *testing.T) {
	root, ev := writeFleetFixture(t)
	ov := overviewJSON(t, "--local", root, "--target-state", ev)
	if ov.Meta.SnapshotID == "" || ov.Meta.AsOf.IsZero() || ov.Summary.Services == 0 || len(ov.EntryPoints) == 0 {
		t.Errorf("overview json = %+v", ov)
	}
}

// TestFleetOverviewNotesFreshnessOff: --freshness 0 says on stderr that
// evidence age was not checked.
func TestFleetOverviewNotesFreshnessOff(t *testing.T) {
	root, ev := writeFleetFixture(t)
	_, stderr, err := execFleet(t, "fleet", "overview", "--local", root, "--target-state", ev, "--freshness", "0")
	if err != nil || !strings.Contains(stderr, "note: ["+fleet.LimitationFreshnessNotEvaluated+"]") {
		t.Errorf("stderr lacks the freshness note (err %v):\n%s", err, stderr)
	}
}

// TestFleetOverviewPartialWarns: an unavailable source makes the answer
// partial, warned on stderr, and the overview names the incomplete source.
func TestFleetOverviewPartialWarns(t *testing.T) {
	root, _ := writeFleetFixture(t)
	out, stderr, err := execFleet(t, "fleet", "overview", "--local", root, "--target-state", filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if !strings.Contains(stderr, "warning: answer is partial") || !strings.Contains(stderr, "SOURCE_UNAVAILABLE") {
		t.Errorf("stderr lacks the partial warning:\n%s", stderr)
	}
	for _, want := range []string{"(1 sources incomplete)", "Incomplete source: ", " unavailable: "} {
		if !strings.Contains(out, want) {
			t.Errorf("overview text lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Next: pacto fleet attention --category "+fleet.SourceHealthIncomplete) {
		t.Errorf("the sources entry point is listed inline, never as an attention command:\n%s", out)
	}
}
