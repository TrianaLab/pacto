package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

func allServed(string) bool { return true }

func chainCommands(steps []ChainStep) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.Command
	}
	return out
}

func sampleImpact() *impact.Result {
	return &impact.Result{
		SnapshotID: "snap-1", AsOf: time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC),
		Classification: "BREAKING", Gate: impact.GateBlocked, GateReason: "billing breaks",
		Consumers:    []impact.AffectedConsumer{{}, {}},
		Owners:       []string{"team/billing"},
		Completeness: fleet.CompletenessPartial,
		Limitations:  []fleet.Limitation{{Code: "SOURCE_STALE", Message: "k8s is stale"}},
	}
}

func TestEditChainNamesPublishThenPromote(t *testing.T) {
	dir := refRoot(t, orders23, nil)
	res, err := Edit(context.Background(), nil, EditOptions{Served: allServed}, EditInput{Path: dir, Version: strPtr("1.1.0"), DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"pacto impact <published ref> " + dir, "pacto publish --plan", "pacto publish", "pacto delivery plan --type promote --to 1.1.0 --stage <stage>"}
	if got := chainCommands(res.Chain); !slices.Equal(got, want) {
		t.Fatalf("commands %q, want %q", got, want)
	}
	for i, tool := range []string{"pacto_impact", "pacto_publish_plan", "pacto_publish", "pacto_delivery_operation_plan"} {
		if res.Chain[i].Tool != tool || strings.Contains(res.Chain[i].Why, "does not serve") {
			t.Errorf("step %d = %+v, want tool %s", i, res.Chain[i], tool)
		}
	}
	if !strings.Contains(res.Chain[3].Why, "on each Stage of orders") || res.Executor != gitOpsRuns {
		t.Errorf("promote why %q, executor %q", res.Chain[3].Why, res.Executor)
	}
}

func TestEditDryRunImpactFromSnapshot(t *testing.T) {
	dir := refRoot(t, orders23, nil)
	var staged string
	opts := EditOptions{Served: allServed, Impact: func(_ context.Context, oldDir, newDir string) (*impact.Result, error) {
		raw, _ := os.ReadFile(filepath.Join(newDir, "pacto.yaml"))
		if oldDir != dir || !strings.Contains(string(raw), "1.1.0") {
			return nil, fmt.Errorf("old %s, new %s:\n%s", oldDir, newDir, raw)
		}
		staged = newDir
		return sampleImpact(), nil
	}}
	res, err := Edit(context.Background(), nil, opts, EditInput{Path: dir, Version: strPtr("1.1.0"), DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	imp := res.Impact
	if imp == nil || imp.Completeness != "partial" || imp.AsOf != "2026-11-15T09:00:00Z" || imp.SnapshotID != "snap-1" ||
		!slices.Equal(imp.Owners, []string{"team/billing"}) || imp.Affected != 2 || imp.Gate != impact.GateBlocked ||
		!slices.Equal(imp.Limitations, []string{"SOURCE_STALE: k8s is stale"}) || imp.Classification != "BREAKING" {
		t.Fatalf("impact %+v", imp)
	}
	if res.Chain[0].Command != "pacto publish --plan" {
		t.Errorf("a known impact still asks for pacto_impact: %+v", res.Chain)
	}
	if _, err := os.Stat(staged); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stage %s left: %v", staged, err)
	}
}

func TestEditNoSnapshotSaysUnknown(t *testing.T) {
	dir := refRoot(t, orders23, nil)
	res, err := Edit(context.Background(), nil, EditOptions{}, EditInput{Path: dir, Version: strPtr("1.1.0"), DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	first := res.Chain[0]
	if res.Impact != nil || first.Tool != "" || !strings.HasPrefix(first.Why, noSnapshot) || strings.Contains(first.Why, "nobody") {
		t.Fatalf("impact %+v, first step %+v", res.Impact, first)
	}
}

func TestEditImpactErrorIsUnknown(t *testing.T) {
	failing := func(context.Context, string, string) (*impact.Result, error) { return nil, errors.New("registry down") }
	dir := refRoot(t, orders23, nil)
	res, err := Edit(context.Background(), nil, EditOptions{Impact: failing, Served: allServed}, EditInput{Path: dir, Version: strPtr("1.1.0")})
	if err != nil {
		t.Fatal(err)
	}
	first := res.Chain[0]
	if res.Impact != nil || first.Tool != "pacto_impact" || !strings.Contains(first.Why, "registry down") ||
		!strings.Contains(first.Why, "is not known") || strings.Contains(first.Why, "nobody") {
		t.Fatalf("impact %+v, first step %+v", res.Impact, first)
	}

	// A stage that cannot be written is an impact that could not be computed.
	o := osFileOps()
	o.mkdirTemp = func(string, string) (string, error) { return "", errors.New("read-only") }
	called := false
	res, err = o.edit(context.Background(), nil, EditOptions{Impact: func(context.Context, string, string) (*impact.Result, error) {
		called = true
		return sampleImpact(), nil
	}}, EditInput{Path: dir, Version: strPtr("1.2.0"), DryRun: true})
	if err != nil || called || res.Impact != nil || !strings.Contains(res.Chain[0].Why, "failed to stage the edited bundle") {
		t.Fatalf("err %v, called %v, result %+v", err, called, res)
	}
}

func TestEditApprovalsFromGate(t *testing.T) {
	many := make([]string, 25)
	for i := range many {
		many[i] = fmt.Sprintf("team/t%02d", i)
	}
	tests := []struct{ gate, want string }{
		{impact.GateBlocked, "a person from team/billing must approve the promote where an affected consumer runs; the promote's own plan decides"},
		{impact.GateUnverified, "Pacto could not show the change is safe, so the promote may ask a person; the promote's own plan decides"},
		{impact.GateClear, "impact asks nobody; the Stage's level and gates still apply; the promote's own plan decides"},
	}
	for _, tt := range tests {
		if got := approvals(tt.gate, []string{"team/billing"}); got != tt.want {
			t.Errorf("%s: %q", tt.gate, got)
		}
	}
	if got := approvals(impact.GateBlocked, nil); !strings.HasPrefix(got, "a person from the affected consumers' owners must") {
		t.Errorf("blocked with no owners: %q", got)
	}
	r := sampleImpact()
	r.Owners = many
	imp := editImpact(r)
	if len(imp.Owners) != maxEditList || imp.Owners[maxEditList-1] != "and 6 more" || !strings.Contains(imp.Approvals, "team/t18, and 6 more must") {
		t.Errorf("owners %q, approvals %q", imp.Owners, imp.Approvals)
	}
}

func TestEditExecutorForConfigRefs(t *testing.T) {
	dir := refRoot(t, orders23, nil)
	res, err := Edit(context.Background(), realValidator, EditOptions{}, EditInput{Path: dir, AddEnvironments: []EnvironmentInput{{Name: "prod"}}, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Executor != providerRuns {
		t.Errorf("environment executor %q", res.Executor)
	}
	if got := executor(EditInput{AddConfigurationRefs: []ConfigurationRefInput{{Ref: "../pg"}}}); got != providerRuns {
		t.Errorf("configuration ref executor %q", got)
	}
	if got := executor(EditInput{Version: strPtr("2.0.0")}); got != gitOpsRuns {
		t.Errorf("version executor %q", got)
	}
}

func TestEditChainDoesNotWrite(t *testing.T) {
	dir := refRoot(t, orders23, nil)
	res, err := Edit(context.Background(), nil, EditOptions{Impact: func(context.Context, string, string) (*impact.Result, error) {
		return sampleImpact(), nil
	}}, EditInput{Path: dir, Version: strPtr("1.1.0"), DryRun: true})
	if err != nil || res.Impact == nil {
		t.Fatalf("err %v, result %+v", err, res)
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, "pacto.yaml")); string(raw) != orders23 {
		t.Errorf("pacto.yaml changed:\n%s", raw)
	}
	assertNoStagingLeft(t, dir)
}

// editChainOn runs a dry-run pacto_edit on server and returns its result.
func editChainOn(t *testing.T, server *mcpsdk.Server, dir string) EditResult {
	t.Helper()
	var got EditResult
	decode(t, callToolOn(t, server, "pacto_edit", map[string]any{"path": dir, "version": "1.1.0", "dry_run": true}), &got)
	return got
}

func TestEditChainWithoutPublishOrDeliveryTools(t *testing.T) {
	fleetOnly := mustNew("test", Authoring{}, Modes{Fleet: &FleetMode{Src: staticFleet(buildFleetQuery(t)), Opts: FleetOptions{Impact: stubImpact(sampleImpact(), nil, nil)}}})
	got := editChainOn(t, fleetOnly, refRoot(t, orders23, nil))
	if len(got.Chain) != 3 {
		t.Fatalf("chain %+v", got.Chain)
	}
	for _, s := range got.Chain {
		if s.Tool != "" || s.Command == "" || !strings.Contains(s.Why, "; this session does not serve pacto_") {
			t.Errorf("step %+v names an unserved tool", s)
		}
	}

	publish := editChainOn(t, NewPublishServer("test", nil, Authoring{}), refRoot(t, orders23, nil))
	tools := []string{}
	for _, s := range publish.Chain {
		tools = append(tools, s.Tool)
	}
	if want := []string{"", "pacto_publish_plan", "pacto_publish", ""}; !slices.Equal(tools, want) {
		t.Errorf("publish server tools %q, want %q", tools, want)
	}

	// servedBy agrees with what each server registers.
	for name, m := range map[string]Modes{"composed": composedModes(t, false), "none": {}, "publish": {publish: true}} {
		var server = mustNew("test", Authoring{}, m)
		if m.publish {
			server = NewPublishServer("test", nil, Authoring{})
		}
		res, err := catalogSession(t, server).ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		served := servedBy(m)
		for _, tool := range []string{"pacto_impact", "pacto_publish_plan", "pacto_publish", "pacto_delivery_operation_plan"} {
			registered := slices.ContainsFunc(res.Tools, func(x *mcpsdk.Tool) bool { return x.Name == tool })
			if served(tool) != registered {
				t.Errorf("%s: servedBy(%s) = %v, registered %v", name, tool, served(tool), registered)
			}
		}
	}
	if servedBy(Modes{})("pacto_fleet_get") {
		t.Error("servedBy names a tool outside the chain")
	}
}

func TestCreateChain(t *testing.T) {
	var got CreateResult
	decode(t, callTool(t, "pacto_create", map[string]any{"name": "ledger", "path": filepath.Join(t.TempDir(), "ledger"), "dry_run": true}), &got)
	want := []string{"pacto publish --plan", "pacto publish", "pacto delivery plan --type promote --to 0.1.0 --stage <stage>"}
	if cmds := chainCommands(got.Chain); !slices.Equal(cmds, want) || got.Chain[0].Tool != "" {
		t.Fatalf("create chain %+v, want %q", got.Chain, want)
	}
}

func TestFleetServerEditUsesImpactProvider(t *testing.T) {
	src := staticFleet(buildFleetQuery(t))
	with := editChainOn(t, mustNew("test", Authoring{}, Modes{Fleet: &FleetMode{Src: src, Opts: FleetOptions{Impact: stubImpact(sampleImpact(), nil, nil)}}}), refRoot(t, orders23, nil))
	if with.Impact == nil || with.Impact.SnapshotID != "snap-1" {
		t.Fatalf("fleet server with impact: %+v", with.Impact)
	}
	without := editChainOn(t, mustNew("test", Authoring{}, Modes{Fleet: &FleetMode{Src: src}}), refRoot(t, orders23, nil))
	if without.Impact != nil || without.Chain[0].Tool != "" || !strings.HasPrefix(without.Chain[0].Why, noSnapshot) {
		t.Fatalf("fleet server without impact: %+v", without)
	}
	view := func(context.Context, *auth.TokenInfo, *fleet.Query) (*fleet.Query, error) {
		return nil, errors.New("no view")
	}
	refused := editChainOn(t, mustNew("test", Authoring{}, Modes{Fleet: &FleetMode{Src: src, Opts: FleetOptions{Impact: stubImpact(sampleImpact(), nil, nil), View: view}}}), refRoot(t, orders23, nil))
	if refused.Impact != nil || !strings.Contains(refused.Chain[0].Why, "no view") {
		t.Fatalf("a view that fails: %+v", refused)
	}
}
