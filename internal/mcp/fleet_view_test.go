package mcp

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// TestViewedFleetTools: with a view, every fleet tool answers from the
// caller's view of the snapshot, impact included; a failed view is the tool's
// error.
func TestViewedFleetTools(t *testing.T) {
	q := buildFleetQuery(t)
	empty := fleet.NewQuery(&fleet.FleetSnapshot{Services: map[fleet.ServiceKey]*fleet.ServiceRecord{}})
	var caller *auth.TokenInfo
	view := func(_ context.Context, c *auth.TokenInfo, _ *fleet.Query) (*fleet.Query, error) {
		caller = c
		if c != nil && c.UserID == "boom" {
			return nil, errors.New("checking permissions failed")
		}
		return empty, nil
	}
	req := func(user string) *mcpsdk.CallToolRequest {
		return &mcpsdk.CallToolRequest{Extra: &mcpsdk.RequestExtra{TokenInfo: &auth.TokenInfo{UserID: user}}}
	}
	res, _, err := viewed(staticFleet(q), view, fleetSearchHandler)(context.Background(), req("alice"), fleetSearchArgs{})
	var out fleet.SearchResult
	decode(t, res, &out)
	if err != nil || out.Total != 0 || caller == nil || caller.UserID != "alice" {
		t.Errorf("search through the view: %d services for %+v, %v", out.Total, caller, err)
	}
	if res, _, _ := viewed(staticFleet(q), view, fleetSearchHandler)(context.Background(), req("boom"), fleetSearchArgs{}); !res.IsError {
		t.Error("a failed view must be the tool's error")
	}
	if _, _, _ = viewed(staticFleet(q), view, fleetSearchHandler)(context.Background(), nil, fleetSearchArgs{}); caller != nil {
		t.Error("no request extra is no caller")
	}

	var got *fleet.FleetSnapshot
	provide := func(_ context.Context, snap *fleet.FleetSnapshot, _, _ string, _ bool, _ string) (*impact.Result, error) {
		got = snap
		return &impact.Result{}, nil
	}
	server := NewFleetServer("test", staticFleet(q), Authoring{}, FleetOptions{Impact: provide, View: view})
	callToolOn(t, server, "pacto_impact", map[string]any{"old_ref": "a", "new_ref": "b"})
	if got != empty.SnapshotForSerialization() {
		t.Error("impact must analyse over the caller's view")
	}
	failing := func(context.Context) (*fleet.Query, error) { return nil, errors.New("no snapshot yet") }
	if res, _, _ := viewed(failing, nil, fleetSearchHandler)(context.Background(), nil, fleetSearchArgs{}); !res.IsError {
		t.Error("a failed source must be the tool's error")
	}
}

// TestImpactUsesTheCurrentSnapshot: pacto_impact analyses over the same
// snapshot the source returned, with and without a view, so it agrees with the
// other fleet tools of the session.
func TestImpactUsesTheCurrentSnapshot(t *testing.T) {
	q := buildFleetQuery(t)
	var got *fleet.FleetSnapshot
	provide := func(_ context.Context, snap *fleet.FleetSnapshot, _, _ string, _ bool, _ string) (*impact.Result, error) {
		got = snap
		return &impact.Result{}, nil
	}
	same := func(_ context.Context, _ *auth.TokenInfo, q *fleet.Query) (*fleet.Query, error) { return q, nil }
	for _, view := range []FleetView{nil, same} {
		got = nil
		callToolOn(t, NewFleetServer("test", staticFleet(q), Authoring{}, FleetOptions{Impact: provide, View: view}), "pacto_impact", map[string]any{"old_ref": "a", "new_ref": "b"})
		if got != q.SnapshotForSerialization() {
			t.Errorf("view %v: impact analysed over %p, want the source's snapshot %p", view != nil, got, q.SnapshotForSerialization())
		}
	}
}

// secretFleet: web runs in shop and vaultsvc in secretns, which calls web and
// declares it; a Deployment in secretns calls web too.
func secretFleet(t *testing.T, extra ...fleet.ObservedEdge) *fleet.Query {
	t.Helper()
	k := fleet.SourceKindKubernetes
	mk := func(name, team, domain string, deps ...string) *contract.Contract {
		var d []contract.Dependency
		for _, dep := range deps {
			d = append(d, contract.Dependency{Name: dep, Ref: "oci://x/" + dep, Required: true})
		}
		return &contract.Contract{PactoVersion: "2.4", Dependencies: d,
			Service: contract.Service{Name: name, Version: "1.0.0", Owner: contract.Owner{Team: team}, Domain: domain}}
	}
	src := fleet.NewMemorySource("k8s", "kubernetes", &fleet.Collection{
		Revisions: []fleet.RawRevision{
			{Bundle: &contract.Bundle{Contract: mk("web", "storefront", "shop")}, Digest: "sha256:w"},
			{Bundle: &contract.Bundle{Contract: mk("vaultsvc", "secretteam", "secretdomain", "web")}, Digest: "sha256:v"},
		},
		Targets: []fleet.RawTarget{
			{Scope: "shop", Kind: k, Name: "web", Service: "web", Compliance: fleet.StatusCompliant},
			{Scope: "secretns", Kind: k, Name: "vaultsvc", Service: "vaultsvc", Compliance: fleet.StatusNonCompliant},
		},
		Observed: append([]fleet.ObservedEdge{
			{From: "vaultsvc", To: "web", Count: 1},
			{From: "k8s:secretns/Deployment/spy", To: "web", Count: 1},
		}, extra...),
		Limitations: []fleet.Limitation{{Code: "X", Message: "something about vaultsvc"}},
	})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return fixedNow }}, src)
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

// TestNewFleetToolsAnswerFromTheView: under a restricted view the entities,
// entity, attention and graph tools never name a hidden service, its owner, its
// namespace or an uncontracted caller from that namespace, and their
// limitations come from the restricted snapshot. (A visible contract's own
// declared dependency keeps the name it declares, by Restrict's design, so the
// hidden service here is only ever a caller.)
func TestNewFleetToolsAnswerFromTheView(t *testing.T) {
	q := secretFleet(t)
	view := func(_ context.Context, _ *auth.TokenInfo, q *fleet.Query) (*fleet.Query, error) {
		return q.Restrict(func(ns string) bool { return ns != "secretns" }), nil
	}
	server := NewFleetServer("test", staticFleet(q), Authoring{}, FleetOptions{View: view})
	calls := []struct {
		tool string
		args map[string]any
	}{
		{"pacto_fleet_entities", map[string]any{}},
		{"pacto_fleet_entities", map[string]any{"kind": "owner"}},
		{"pacto_fleet_entity", map[string]any{"kind": "service", "key": "web"}},
		{"pacto_fleet_entity", map[string]any{"kind": "domain", "key": "shop"}},
		{"pacto_fleet_attention", map[string]any{}},
		{"pacto_fleet_attention", map[string]any{"category": "undeclared"}},
		{"pacto_fleet_graph", map[string]any{"service": "web", "transitive": true}},
		{"pacto_fleet_graph", map[string]any{"service": "web", "direction": "dependents", "transitive": true}},
	}
	for _, c := range calls {
		res := callToolOn(t, server, c.tool, c.args)
		text := resultText(t, res)
		if res.IsError {
			t.Fatalf("%s %v: %s", c.tool, c.args, text)
		}
		for _, hidden := range []string{"vaultsvc", "secretteam", "secretdomain", "secretns", "spy"} {
			if strings.Contains(text, hidden) {
				t.Errorf("%s %v names %q:\n%s", c.tool, c.args, hidden, text)
			}
		}
		var out struct {
			Meta struct {
				Limitations []fleet.Limitation `json:"limitations"`
			} `json:"meta"`
		}
		decode(t, res, &out)
		if !slices.ContainsFunc(out.Meta.Limitations, func(l fleet.Limitation) bool { return l.Code == fleet.LimitationHiddenByPermissions }) {
			t.Errorf("%s %v: limitations %+v lack %s", c.tool, c.args, out.Meta.Limitations, fleet.LimitationHiddenByPermissions)
		}
	}
	for _, args := range []map[string]any{{"kind": "service", "key": "vaultsvc"}, {"kind": "owner", "key": "team:secretteam"}, {"kind": "domain", "key": "secretdomain"}} {
		if res := callToolOn(t, server, "pacto_fleet_entity", args); !res.IsError {
			t.Errorf("entity %v is hidden, got %s", args, resultText(t, res))
		}
	}
}

// TestNeighborhoodToolAnswersFromTheView: for a viewer who may not read the
// cluster scope, pacto_fleet_neighborhood names neither the hidden service, its
// owner, its namespace, a caller from that namespace nor a caller with no
// namespace, and the focus counts the hidden service, the secretns caller and
// the bare caller as hidden dependents.
func TestNeighborhoodToolAnswersFromTheView(t *testing.T) {
	q := secretFleet(t, fleet.ObservedEdge{From: "legacy-batch", To: "web", Count: 2})
	view := func(_ context.Context, _ *auth.TokenInfo, q *fleet.Query) (*fleet.Query, error) {
		return q.Restrict(func(ns string) bool { return ns != "" && ns != "secretns" }), nil
	}
	server := NewFleetServer("test", staticFleet(q), Authoring{}, FleetOptions{View: view})
	for _, dir := range []string{"dependents", "both"} {
		res := callToolOn(t, server, "pacto_fleet_neighborhood", map[string]any{"service": "web", "direction": dir, "depth": 3})
		text := resultText(t, res)
		for _, hidden := range []string{"vaultsvc", "secretteam", "secretdomain", "secretns", "spy", "legacy-batch"} {
			if strings.Contains(text, hidden) {
				t.Errorf("%s names %q:\n%s", dir, hidden, text)
			}
		}
		var nb fleet.Neighborhood
		decode(t, res, &nb)
		for _, n := range nb.Nodes {
			if n.Focus && n.HiddenDependents != 3 {
				t.Errorf("%s: focus hiddenDependents = %d, want vaultsvc, spy and legacy-batch", dir, n.HiddenDependents)
			}
		}
	}
}
