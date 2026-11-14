package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/cli"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestFleetFreshnessDefaultsToRecentWindow: every command that builds a fleet
// snapshot checks evidence age with the dashboard's window unless told not to.
func TestFleetFreshnessDefaultsToRecentWindow(t *testing.T) {
	root := cli.NewRootCommand(app.NewService(nil, nil), cli.VersionInfo{Version: "test"})
	for _, path := range [][]string{{"fleet"}, {"mcp"}, {"tui"}, {"impact"}} {
		cmd, _, err := root.Find(path)
		if err != nil {
			t.Fatalf("%v: %v", path, err)
		}
		f := cmd.Flags().Lookup("freshness")
		if f == nil {
			f = cmd.PersistentFlags().Lookup("freshness")
		}
		if f == nil || f.DefValue != fleet.RecentEvidenceWindow.String() {
			t.Errorf("%v --freshness default = %+v, want %s", path, f, fleet.RecentEvidenceWindow)
		}
	}
}

const (
	demoBundles = "../../examples/demo/bundles"
	demoTargets = "../../examples/demo/fleet-targets.yaml"
)

// TestFleetDefaultMarksOldEvidenceStale: the demo target-state fixture carries
// evidence older than a day, which reads stale with no --freshness flag.
func TestFleetDefaultMarksOldEvidenceStale(t *testing.T) {
	out, _, err := execFleet(t, "fleet", "attention", "--local", demoBundles, "--target-state", demoTargets, "--category", "stale", "--output-format", "json")
	if err != nil {
		t.Fatalf("attention: %v", err)
	}
	var res struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Total == 0 {
		t.Errorf("want stale items by default (err %v):\n%s", err, out)
	}
}

// TestFleetAttentionNotesFreshnessOff: --freshness 0 says on stderr that
// evidence age was not checked, and the JSON answer carries the limitation.
func TestFleetAttentionNotesFreshnessOff(t *testing.T) {
	root, ev := writeFleetFixture(t)
	base := []string{"fleet", "attention", "--local", root, "--target-state", ev, "--freshness", "0"}
	_, stderr, err := execFleet(t, base...)
	if err != nil {
		t.Fatalf("attention: %v", err)
	}
	if !strings.Contains(stderr, "note: ["+fleet.LimitationFreshnessNotEvaluated+"]") {
		t.Errorf("stderr lacks the freshness note:\n%s", stderr)
	}
	jout, _, err := execFleet(t, append(base, "--output-format", "json")...)
	if err != nil || !strings.Contains(jout, `"`+fleet.LimitationFreshnessNotEvaluated+`"`) {
		t.Errorf("json lacks the limitation (err %v):\n%s", err, jout)
	}
	_, stderr, err = execFleet(t, "fleet", "attention", "--local", root, "--target-state", ev)
	if err != nil || strings.Contains(stderr, fleet.LimitationFreshnessNotEvaluated) {
		t.Errorf("the default window checks freshness, so no note (err %v):\n%s", err, stderr)
	}
}
