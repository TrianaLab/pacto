package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v4/internal/fleetgen"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

var mapNow = time.Date(2026, 11, 20, 9, 40, 0, 0, time.UTC)

// mapFleet is a generated fleet of n services.
func mapFleet(t *testing.T, n int) *fleet.Query {
	t.Helper()
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return mapNow }},
		fleet.NewMemorySource("fleetgen", fleet.SourceKindKubernetes, fleetgen.Collection(n, 1, mapNow)))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

// runMap answers r over q in format and returns stdout and stderr.
func runMap(t *testing.T, q *fleet.Query, r mapRequest, format string) (string, string, error) {
	t.Helper()
	cmd := &cobra.Command{}
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	err := runFleetMap(cmd, q, r, format)
	return out.String(), errb.String(), err
}

const mapText = `asOf 2026-11-20T09:40:00Z  completeness complete
Declared ↔ observed match: 74% of what Pacto can observe · 100% observed coverage · unknown never counts as matched
  services: 36 matched · 0 expected only · 4 observed only · 0 insufficient
  dependencies: 62 matched · 23 expected only · 6 observed only · 0 insufficient
Contract completeness: 95% · 27 of 36 judged services fully described · most missing: readiness 9, dependencies 1
Grouped by domain: 3 groups · services 40 (4 inferred) · external systems 1
GROUP       KIND      SERVICES  INFERRED  EXTERNAL  MATCH  COMPLETE  ERRORS  WARNINGS  WORST
platform    domain    36        0         0         79%    95%       6       6         non-compliant 4, cross-environment 2, health 3
(unknown)   unknown   4         4         0         0%     -         0       0         -
(external)  external  0         0         1         -      -         0       0         -
`

func TestFleetMapText(t *testing.T) {
	out, stderr, err := runMap(t, mapFleet(t, 40), mapRequest{}, "text")
	if err != nil || out != mapText || stderr != "" {
		t.Errorf("err %v, stderr %q, text:\n%s", err, stderr, out)
	}
	out, _, _ = runMap(t, mapFleet(t, 40), mapRequest{groupBy: fleet.MapGroupByEnvironment}, "text")
	if !strings.Contains(out, "Grouped by environment; environments by name: 6 groups") || !strings.Contains(out, "\nprod-us     environment  36 ") {
		t.Errorf("environment text:\n%s", out)
	}
}

// TestFleetMapTextNothingObserved: with no observation source the match
// is unknown, never 100%, and the team map says when teams nest.
func TestFleetMapTextNothingObserved(t *testing.T) {
	var b bytes.Buffer
	printMapMatch(&b, fleet.MatchTally{Services: fleet.MatchCounts{Insufficient: 3}, Coverage: new(0)})
	if !strings.HasPrefix(b.String(), "Declared ↔ observed match: unknown · Pacto observes nothing yet · 0% observed coverage") {
		t.Errorf("no observation:\n%s", b.String())
	}
	cmd := &cobra.Command{}
	cmd.SetOut(&b)
	cmd.SetErr(&b)
	b.Reset()
	printFleetMap(cmd, &fleet.FleetMap{GroupBy: fleet.MapGroupByTeam, Nested: true, RibbonsTruncated: true, Ribbons: make([]fleet.MapRibbon, 2), Totals: fleet.MapTotals{Ribbons: 5}})
	if !strings.Contains(b.String(), "Grouped by team, each team inside its domain: 0 groups") || !strings.Contains(b.String(), "truncated: the 2 largest of 5 links between groups are kept\n") {
		t.Errorf("nested team text:\n%s", b.String())
	}
}

const groupText = `Group platform (domain): 2 services from offset 1 of 36
SERVICE                    MATCH    SEVERITY  WORST              VERSION  OWNER              IN  OUT  COMPLETE  MISSING
platform-00000             matched  -         -                  1.0.0    platform-core      19  2    100%      -
platform-00021 (not here)  matched  error     cross-environment  -        platform-team-000  3   5    100%      -
truncated: more services follow; ask --offset 3
`

func TestFleetMapGroupText(t *testing.T) {
	q := mapFleet(t, 40)
	out, _, err := runMap(t, q, mapRequest{group: "platform", limit: 2, offset: 1, environment: "dev"}, "text")
	if err != nil || out != groupText {
		t.Errorf("err %v, text:\n%s", err, out)
	}
	out, _, _ = runMap(t, q, mapRequest{group: "(unknown)"}, "text")
	if !strings.Contains(out, " (inferred)  observed-only") || strings.Contains(out, "truncated") {
		t.Errorf("inferred group text:\n%s", out)
	}
	if _, _, err := runMap(t, q, mapRequest{group: "ghost"}, "text"); err == nil {
		t.Error("an unknown group is no error")
	}
}

func TestFleetMapFocusText(t *testing.T) {
	q := mapFleet(t, 40)
	out, _, err := runMap(t, q, mapRequest{focus: "platform-00001"}, "text")
	for _, want := range []string{
		"Focus platform-00001 (group platform): match matched · -\n",
		"Callers at 1 hop: 9 of 19\n  platform-00002  match=matched  error\n",
		"Callees at 1 hop: 3 of 3\n",
		"Calls:\n  platform-00000 -> platform-00001  api ^1.0.0  declared+observed  matched\n",
		"  platform-00021 -> platform-00001  api  observed  observed-not-expected  error CROSS_ENVIRONMENT_CALL: observed calls from ",
	} {
		if err != nil || !strings.Contains(out, want) {
			t.Errorf("err %v, focus text lacks %q:\n%s", err, want, out)
		}
	}
	out, _, _ = runMap(t, q, mapRequest{focus: "platform-00000", hops: 2}, "text")
	if !strings.Contains(out, "Callers at 2 hop: 9 of 11\n") || !strings.Contains(out, "Callees at 2 hop: 2 of 2\n") {
		t.Errorf("two hops:\n%s", out)
	}
	if _, _, err := runMap(t, q, mapRequest{focus: "ghost"}, "text"); err == nil {
		t.Error("an unknown focus is no error")
	}
}

func TestFleetMapJSON(t *testing.T) {
	q := mapFleet(t, 40)
	var m fleet.FleetMap
	if out, _, err := runMap(t, q, mapRequest{groupBy: fleet.MapGroupByTeam}, "json"); err != nil || json.Unmarshal([]byte(out), &m) != nil || m.GroupBy != fleet.MapGroupByTeam {
		t.Errorf("map json = %v:\n%s", err, out)
	}
	var page fleet.MapNodes
	if out, _, err := runMap(t, q, mapRequest{group: "platform", limit: 1}, "json"); err != nil || json.Unmarshal([]byte(out), &page) != nil || len(page.Nodes) != 1 {
		t.Errorf("group json = %v:\n%s", err, out)
	}
	var f fleet.MapFocusAnswer
	if out, _, err := runMap(t, q, mapRequest{focus: "platform-00001"}, "json"); err != nil || json.Unmarshal([]byte(out), &f) != nil || f.Focus.Name != "platform-00001" {
		t.Errorf("focus json = %v:\n%s", err, out)
	}
	if _, _, err := runMap(t, q, mapRequest{groupBy: "planet"}, "json"); err == nil {
		t.Error("an unknown grouping is no error")
	}
}

// TestFleetMapFlags: the command reads its flags, and refuses one the
// question it asks does not read.
func TestFleetMapFlags(t *testing.T) {
	statsOff(t)
	dir := t.TempDir()
	if out, _, err := runRoot(t, defaultDeps(), nil, "fleet", "map", "--local", dir, "--group-by", "team"); err != nil || !strings.Contains(out, "Grouped by team") {
		t.Errorf("fleet map = %v:\n%s", err, out)
	}
	for _, args := range [][]string{
		{"--focus", "a", "--group", "b"}, {"--focus", "a", "--environment", "dev"}, {"--hops", "2"},
		{"--offset", "1"}, {"--limit", "1"}, {"--group", "ghost", "--offset", "1"},
		{"--environment", "nope"}, {"--group-by", "environment", "--group", "prod", "--environment", "staging"},
	} {
		if _, _, err := runRoot(t, defaultDeps(), nil, append([]string{"fleet", "map", "--local", dir}, args...)...); err == nil {
			t.Errorf("fleet map %v is no error", args)
		}
	}
	if _, _, err := runRoot(t, defaultDeps(), nil, "fleet", "map", "--source", "bogus://x"); err == nil {
		t.Error("an unreadable source is no error")
	}
	if _, _, err := runRoot(t, defaultDeps(), nil, "fleet", "map", "--local", dir, "--detected", "k8s:nons"); err == nil || !strings.Contains(err.Error(), "is not k8s:<namespace>/<name>") {
		t.Errorf("a malformed --detected ref: %v", err)
	}
}

// TestFleetMapSameAnswerEverywhere: pacto fleet map, pacto_fleet_map and
// GET /api/fleet/map answer one snapshot with the same tallies and groups.
func TestFleetMapSameAnswerEverywhere(t *testing.T) {
	q := mapFleet(t, 400)
	out, _, err := runMap(t, q, mapRequest{groupBy: fleet.MapGroupByEnvironment}, "json")
	var cliAns fleet.FleetMap
	if err != nil || json.Unmarshal([]byte(out), &cliAns) != nil {
		t.Fatalf("cli: %v", err)
	}
	server, err := pactomcp.New("test", pactomcp.Authoring{}, pactomcp.Modes{Fleet: &pactomcp.FleetMode{Src: func(context.Context) (*fleet.Query, error) { return q, nil }}})
	if err != nil {
		t.Fatal(err)
	}
	_, res := callTool(t, server, "pacto_fleet_map", map[string]any{"group_by": "environment"})
	var mcpAns fleet.FleetMap
	if err := json.Unmarshal([]byte(res.Content[0].(*mcpsdk.TextContent).Text), &mcpAns); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(dashboard.NewServer(dashboard.Options{Fleet: func(context.Context) (*fleet.Query, error) { return q, nil }}).APIHandler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/fleet/map?groupBy=environment")
	if err != nil {
		t.Fatal(err)
	}
	var httpAns fleet.FleetMap
	_ = json.NewDecoder(resp.Body).Decode(&httpAns)
	_ = resp.Body.Close()
	for name, a := range map[string]fleet.FleetMap{"mcp": mcpAns, "http": httpAns} {
		if !reflect.DeepEqual(a.Match, cliAns.Match) || !reflect.DeepEqual(a.Knowledge, cliAns.Knowledge) || a.Totals != cliAns.Totals ||
			!slices.Equal(mapGroupKeys(a), mapGroupKeys(cliAns)) || a.Meta.SnapshotID != cliAns.Meta.SnapshotID {
			t.Errorf("%s answers otherwise than the CLI: groups %v, CLI %v", name, mapGroupKeys(a), mapGroupKeys(cliAns))
		}
	}
	if len(cliAns.Groups) < 4 {
		t.Errorf("too few groups to compare: %v", mapGroupKeys(cliAns))
	}
}

func mapGroupKeys(m fleet.FleetMap) []string {
	out := make([]string, len(m.Groups))
	for i, g := range m.Groups {
		out[i] = g.Key
	}
	return out
}

func TestMapRequestCheck(t *testing.T) {
	for _, r := range []mapRequest{{}, {groupBy: "team", group: "a", environment: "dev"}, {group: "a", offset: 1, limit: 2}, {focus: "a", hops: 2}} {
		if err := r.check(); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
}
