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
	"github.com/trianalab/pacto/v4/pkg/contract"
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
	want := []string{"pacto impact <published ref> " + dir, "pacto publish --plan " + dir, "pacto publish " + dir, "pacto delivery plan --type promote --to 1.1.0 --stage <stage>"}
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
	// Each publish step names the edited directory, so it never publishes the server's working directory.
	for _, s := range res.Chain[1:3] {
		if s.Args["path"] != dir || !strings.Contains(s.Why, dir) {
			t.Errorf("publish step %+v does not name %s", s, dir)
		}
	}
	// On a dry run the directory still holds the old contract, so the impact step runs after the edit is written.
	if !strings.HasSuffix(res.Chain[0].Why, afterWrite) {
		t.Errorf("dry-run impact step %q", res.Chain[0].Why)
	}
	written, err := Edit(context.Background(), nil, EditOptions{Served: allServed}, EditInput{Path: dir, Version: strPtr("1.1.0")})
	if err != nil || strings.Contains(written.Chain[0].Why, afterWrite) {
		t.Errorf("err %v, written impact step %q", err, written.Chain[0].Why)
	}
}

func TestEditDryRunImpactFromSnapshot(t *testing.T) {
	dir := refRoot(t, orders23, nil)
	var staged string
	opts := EditOptions{Served: allServed, Impact: func(_ context.Context, service, oldDir, newDir string) (*impact.Result, error) {
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
	if res.Chain[0].Command != "pacto publish --plan "+dir {
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
	failing := func(context.Context, string, string, string) (*impact.Result, error) {
		return nil, errors.New("registry down")
	}
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
	res, err = o.edit(context.Background(), nil, EditOptions{Impact: func(context.Context, string, string, string) (*impact.Result, error) {
		called = true
		return sampleImpact(), nil
	}}, EditInput{Path: dir, Version: strPtr("1.2.0"), DryRun: true})
	if err != nil || called || res.Impact != nil || !strings.Contains(res.Chain[0].Why, "failed to stage the edited bundle") {
		t.Fatalf("err %v, called %v, result %+v", err, called, res)
	}
}

func TestEditApprovalsFromGate(t *testing.T) {
	tests := []struct{ gate, want string }{
		{impact.GateUnverified, "Pacto could not show the change is safe, so the promote may ask a person; the promote's own plan decides"},
		{impact.GateClear, "impact asks nobody; the Stage's level and gates still apply; the promote's own plan decides"},
	}
	for _, tt := range tests {
		if got := approvals(&impact.Result{Gate: tt.gate}); got != tt.want {
			t.Errorf("%s: %q", tt.gate, got)
		}
	}
	if got := approvals(&impact.Result{Gate: impact.GateBlocked}); !strings.HasPrefix(got, "a person from the owners of the consumers it breaks must") {
		t.Errorf("blocked with no owners: %q", got)
	}
	many := make([]string, 25)
	for i := range many {
		many[i] = fmt.Sprintf("team/t%02d", i)
	}
	r := sampleImpact()
	r.Owners = many
	if imp := editImpact(r); len(imp.Owners) != maxEditList || imp.Owners[maxEditList-1] != "and 6 more" {
		t.Errorf("owners %q", imp.Owners)
	}
}

// A blocked edit names as approvers only the owners of the consumers it
// breaks, and counts the broken consumers that name no owner.
func TestEditApprovalsNameOnlyBreakOwners(t *testing.T) {
	r := &impact.Result{Gate: impact.GateBlocked, Owners: []string{"team:data", "team:edge", "team:orders"}, Consumers: []impact.AffectedConsumer{
		{Service: "api-gateway", OwnerKey: "team:edge", CompatibilityVerdict: impact.CompatibilityIncompatible},
		{Service: "orders", OwnerKey: "team:orders", CompatibilityVerdict: impact.CompatibilityCompatible},
		{Service: "reporting", OwnerKey: "team:data", Depth: 2, CompatibilityVerdict: impact.CompatibilityUnknown},
		{Service: "billing", OwnerKey: "team:billing", CompatibilityVerdict: impact.CompatibilityCompatible, Unmet: []contract.Mismatch{{}}},
		{Service: "batch", CompatibilityVerdict: impact.CompatibilityIncompatible},
	}}
	want := "a person from the owners of the consumers it breaks (team:billing, team:edge), and 1 broken consumers name no owner must approve the promote where a broken consumer runs; the promote's own plan decides"
	if got := approvals(r); got != want {
		t.Errorf("approvals %q", got)
	}
}

// The edit answer carries the impact's caveats: the versions it compares,
// hidden consumers, golden paths to review, truncated limitations, and a
// revision added because no source held the service, which is never clear.
func TestEditImpactKeepsCaveats(t *testing.T) {
	r := &impact.Result{Service: "orders", OldVersion: "1.0.0", NewVersion: "1.1.0", Gate: impact.GateClear, GateReason: "nobody",
		Hidden: 2, Templates: []string{"golden"}, NewRevisionAdded: true, LimitationsTruncated: true,
		Limitations: []fleet.Limitation{{Code: "SOURCE_STALE", Message: "stale"}}}
	imp := editImpact(r)
	if imp.OldVersion != "1.0.0" || imp.NewVersion != "1.1.0" || imp.Hidden != 2 || !slices.Equal(imp.Templates, []string{"golden"}) ||
		imp.Gate != impact.GateUnverified || !strings.Contains(imp.GateReason, "no fleet source holds orders") ||
		len(imp.Limitations) != 3 || !strings.HasPrefix(imp.Limitations[1], "SERVICE_NOT_IN_FLEET: ") ||
		imp.Limitations[2] != "and more limitations the answer does not carry" || strings.Contains(imp.Approvals, "asks nobody") {
		t.Fatalf("impact %+v", imp)
	}
	if r.Gate != impact.GateClear || len(r.Limitations) != 1 {
		t.Errorf("the provider's answer changed: %+v", r)
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
	res, err := Edit(context.Background(), nil, EditOptions{Impact: func(context.Context, string, string, string) (*impact.Result, error) {
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
	dir := filepath.Join(t.TempDir(), "ledger")
	decode(t, callTool(t, "pacto_create", map[string]any{"name": "ledger", "path": dir, "dry_run": true}), &got)
	want := []string{"pacto publish --plan " + dir, "pacto publish " + dir, "pacto delivery plan --type promote --to 0.1.0 --stage <stage>"}
	if cmds := chainCommands(got.Chain); !slices.Equal(cmds, want) || got.Chain[0].Tool != "" || got.Chain[0].Args["path"] != dir {
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

// An edit keys its service where the fleet holds it, since a local directory
// names no registry; a service the snapshot cannot place leaves impact unknown,
// and a baseline the fleet holds no revision of is never clear.
func TestFleetServerEditPlacesTheService(t *testing.T) {
	orders := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "orders", Version: "1.0.0"}}
	onRegistry := func(name, version string) *fleet.Query {
		c := *orders
		c.Service.Name, c.Service.Version = name, version
		snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("oci", "oci", &fleet.Collection{
			Revisions: []fleet.RawRevision{{Bundle: &contract.Bundle{Contract: &c}, Domain: "ghcr.io/acme", Digest: "sha256:o"}},
		}))
		if err != nil {
			t.Fatal(err)
		}
		return fleet.NewQuery(snap)
	}
	var domain string
	provider := func(_ context.Context, _ *fleet.FleetSnapshot, req ImpactRequest) (*impact.Result, error) {
		domain = req.Domain
		return &impact.Result{Service: "orders", OldVersion: "1.0.0", Gate: impact.GateClear}, nil
	}
	server := func(q *fleet.Query) *mcpsdk.Server {
		return mustNew("test", Authoring{}, Modes{Fleet: &FleetMode{Src: staticFleet(q), Opts: FleetOptions{Impact: provider}}})
	}
	got := editChainOn(t, server(onRegistry("orders", "1.0.0")), refRoot(t, orders23, nil))
	if domain != "ghcr.io/acme" || got.Impact == nil || got.Impact.Gate != impact.GateClear {
		t.Fatalf("domain %q, impact %+v", domain, got.Impact)
	}
	unpublished := editChainOn(t, server(onRegistry("orders", "0.9.0")), refRoot(t, orders23, nil))
	if unpublished.Impact == nil || unpublished.Impact.Gate != impact.GateUnverified || !strings.HasPrefix(unpublished.Impact.Limitations[0], "BASELINE_UNPUBLISHED: ") {
		t.Fatalf("unpublished baseline %+v", unpublished.Impact)
	}
	elsewhere := editChainOn(t, server(onRegistry("payments", "1.0.0")), refRoot(t, orders23, nil))
	if elsewhere.Impact != nil || !strings.Contains(elsewhere.Chain[0].Why, "cannot place orders") || elsewhere.Chain[0].Tool != "pacto_impact" {
		t.Fatalf("a service the fleet does not hold: %+v", elsewhere)
	}
}

// An undeclared service.version names no version in the publish and promote
// steps.
func TestPromoteChainUndeclaredVersion(t *testing.T) {
	steps := promoteChain("svc", "", "/d", nil)
	if !strings.Contains(steps[1].Why, "publish <version> from /d") || !strings.Contains(steps[2].Command, "--to <version>") {
		t.Fatalf("got %+v", steps)
	}
}

// An edit of a contract whose name is inferred places the service by that
// name, has its staged copy named from the real folder, and declares the
// identity publish refuses without before the publish steps.
func TestEditInferredNamePlacesAndDeclares(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "payments")
	for rel, content := range map[string]string{"pacto.yaml": "", "chart/Chart.yaml": "apiVersion: v2\nname: payments\nversion: 0.1.0\n"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var req ImpactRequest
	provider := func(_ context.Context, _ *fleet.FleetSnapshot, r ImpactRequest) (*impact.Result, error) {
		req = r
		return sampleImpact(), nil
	}
	opts := editOptions(Modes{Fleet: &FleetMode{Src: staticFleet(buildFleetQuery(t)), Opts: FleetOptions{Impact: provider}}})
	res, err := Edit(context.Background(), nil, opts, EditInput{Path: dir, Owner: strPtr("team/pay"), DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Impact == nil || req.NewDirName != "payments" || req.OldRef != dir {
		t.Fatalf("impact %+v, request %+v", res.Impact, req)
	}
	var declare *ChainStep
	for i, s := range res.Chain {
		if s.Tool == "pacto_edit" {
			declare = &res.Chain[i]
		}
		if strings.Contains(s.Why, "Stage of ;") {
			t.Errorf("step names no service: %+v", s)
		}
	}
	if declare == nil || declare.Args["name"] != "payments" || !strings.Contains(declare.Command, "service.name and service.version") {
		t.Fatalf("chain %+v", res.Chain)
	}
	if last := res.Chain[len(res.Chain)-1]; !strings.Contains(last.Why, "Stage of payments") {
		t.Errorf("promote step %+v", last)
	}
}
