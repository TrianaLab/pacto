package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// stubReads answers every route with the route and query it was asked, or
// with err.
type stubReads struct {
	route string
	q     url.Values
	err   error
}

func (s *stubReads) reads(_ context.Context, route string, q url.Values) (json.RawMessage, error) {
	s.route, s.q = route, q
	if s.err != nil {
		return nil, s.err
	}
	return json.RawMessage(`{"route":"` + route + `"}`), nil
}

func TestDeliveryReadToolsAnnotated(t *testing.T) {
	tools := listTools(t, mustNew("test", Authoring{}, Modes{Delivery: &DeliveryMode{API: &stubDelivery{}, Reads: (&stubReads{}).reads}}))
	for _, route := range []string{"caller", "inbox", "environments", "autonomy", "published", "refusals", "trust"} {
		tl := tools["pacto_delivery_"+route]
		if tl == nil {
			t.Fatalf("pacto_delivery_%s is not registered", route)
		}
		if !tl.Annotations.ReadOnlyHint || tl.Annotations.OpenWorldHint == nil || !*tl.Annotations.OpenWorldHint ||
			!strings.Contains(tl.Description, "runs with your own identity") {
			t.Errorf("pacto_delivery_%s: %+v %q", route, tl.Annotations, tl.Description)
		}
	}
	if !strings.Contains(tools["pacto_delivery_refusals"].Description, "an empty list proves nothing") {
		t.Error("refusals must say an unrecorded empty list proves nothing")
	}
}

// TestRefusalsRouteDescribesPromotes: the refusals tool says it lists the
// promotes the manifest inspector refused beside the refused publishes.
func TestRefusalsRouteDescribesPromotes(t *testing.T) {
	tools := listTools(t, mustNew("test", Authoring{}, Modes{Delivery: &DeliveryMode{API: &stubDelivery{}, Reads: (&stubReads{}).reads}}))
	d := tools["pacto_delivery_refusals"].Description
	if !strings.Contains(d, "publishes and promotes Pacto refused") || !strings.Contains(d, "manifest inspector") {
		t.Errorf("refusals description: %q", d)
	}
}

func TestDeliveryReadToolsCallTheRoute(t *testing.T) {
	r := &stubReads{}
	server := mustNew("test", Authoring{}, Modes{Delivery: &DeliveryMode{API: &stubDelivery{}, Reads: r.reads}})
	res := callToolOn(t, server, "pacto_delivery_inbox", map[string]any{"held": 25, "except": []any{"prod", ""}, "q": "pay", "domain": nil})
	if resultText(t, res) != `{"route":"inbox"}` || r.route != "inbox" {
		t.Fatalf("inbox answered %q for %q", resultText(t, res), r.route)
	}
	want := url.Values{"held": {"25"}, "except": {"prod", ""}, "q": {"pay"}}
	if r.q.Encode() != want.Encode() {
		t.Errorf("query = %v, want %v", r.q, want)
	}
	callToolOn(t, server, "pacto_delivery_caller", map[string]any{})
	if r.route != "caller" || len(r.q) != 0 {
		t.Errorf("caller asked %q %v", r.route, r.q)
	}

	r.err = errors.New("DeliveryNotInstalled: this cluster does not serve delivery")
	res = callToolOn(t, server, "pacto_delivery_environments", map[string]any{"tier": "prod"})
	if !res.IsError || !strings.Contains(resultText(t, res), "DeliveryNotInstalled") {
		t.Errorf("a refusal is the tool's error: %+v", res)
	}
	if res := callToolOn(t, server, "pacto_delivery_environments", map[string]any{"tier": "staging"}); !res.IsError {
		t.Error("a value outside the route's enum is refused")
	}
}

func TestDeliveryReadToolsNeedReads(t *testing.T) {
	session := catalogSession(t, mustNew("test", Authoring{}, Modes{Delivery: &DeliveryMode{API: &stubDelivery{}}}))
	if toolNames(t, session)["pacto_delivery_inbox"] {
		t.Error("without Reads no read tool registers")
	}
	if strings.Contains(session.InitializeResult().Instructions, "pacto_delivery_inbox") {
		t.Error("the instructions name a read tool the server lacks")
	}
}

// docQuery is one revision of payments with docs/overview.md, in no namespace.
func docQuery(t *testing.T) (*fleet.Query, string) {
	t.Helper()
	src := fleet.NewMemorySource("local", "local", &fleet.Collection{Revisions: []fleet.RawRevision{{
		Bundle: &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.4", Workload: contract.WorkloadService,
			Service: contract.Service{Name: "payments", Version: "1.0.0"}},
			FS: fstest.MapFS{"docs/overview.md": {Data: []byte("# Payments\n")}}},
		Digest: "sha256:" + strings.Repeat("a", 64)}}})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, src)
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(snap)
	v, err := q.GetService("payments")
	if err != nil {
		t.Fatal(err)
	}
	return q, string(v.Revisions[0].Key)
}

func TestRevisionDocumentTool(t *testing.T) {
	q, key := docQuery(t)
	call := func(view FleetView, args map[string]any) (string, bool) {
		m := Modes{Fleet: &FleetMode{Src: staticFleet(q), Opts: FleetOptions{View: view}}}
		res := callToolOn(t, mustNew("test", Authoring{}, m), "pacto_fleet_revision_document", args)
		return resultText(t, res), res.IsError
	}
	text, isErr := call(nil, map[string]any{"key": key, "path": "docs/overview.md"})
	var doc fleet.RevisionDocument
	if isErr || json.Unmarshal([]byte(text), &doc) != nil || doc.Document.Content != "# Payments\n" || doc.Meta.AsOf.IsZero() {
		t.Fatalf("visible revision: %v %s", isErr, text)
	}
	hide := func(_ context.Context, _ *auth.TokenInfo, q *fleet.Query) (*fleet.Query, error) {
		return q.Restrict(func(_, _ string) bool { return false }, nil), nil
	}
	if text, isErr := call(hide, map[string]any{"key": key, "path": "docs/overview.md"}); !isErr || !strings.Contains(text, "not found") {
		t.Errorf("a revision the view hides: %v %s", isErr, text)
	}
	if text, isErr := call(nil, map[string]any{"key": key, "path": "pacto.yaml"}); !isErr || !strings.Contains(text, "not found") {
		t.Errorf("a path outside docs: %v %s", isErr, text)
	}
}

func TestDeliveryReadParams(t *testing.T) {
	params, desc, ok := DeliveryReadParams("published")
	var names []string
	for _, p := range params {
		names = append(names, p.Name)
	}
	if !ok || !strings.HasPrefix(desc, "Per service") || strings.Join(names, ",") != "domain,limit,offset,q,service,signed" || params[5].Enum[0] != "yes" {
		t.Errorf("published: %v %q %v", ok, desc, params)
	}
	if _, _, ok := DeliveryReadParams("history"); ok {
		t.Error("history is not a read tool route")
	}
}

// TestDeliveryToolsReadAnotherClusterThroughRoute: history, blame and the
// undo plan of another cluster run the dashboard's route with cluster, and
// never the server's own client.
func TestDeliveryToolsReadAnotherClusterThroughRoute(t *testing.T) {
	r := &stubReads{}
	server := mustNew("test", Authoring{}, Modes{Delivery: &DeliveryMode{API: &stubDelivery{}, Reads: r.reads}})
	for _, c := range []struct {
		tool, route string
		args        map[string]any
		want        url.Values
	}{
		{"pacto_delivery_history", "history", map[string]any{"namespace": "prod", "stage": "payments", "cluster": "uid-eu"},
			url.Values{"namespace": {"prod"}, "stage": {"payments"}, "cluster": {"uid-eu"}}},
		{"pacto_delivery_blame", "blame", map[string]any{"namespace": "prod", "stage": "payments", "digest": "sha256:a", "cluster": "uid-eu"},
			url.Values{"namespace": {"prod"}, "stage": {"payments"}, "digest": {"sha256:a"}, "cluster": {"uid-eu"}}},
		{"pacto_delivery_undo_plan", "undo", map[string]any{"namespace": "prod", "name": "op-1", "cluster": "uid-eu"},
			url.Values{"namespace": {"prod"}, "name": {"op-1"}, "stage": {""}, "cluster": {"uid-eu"}}},
	} {
		res := callToolOn(t, server, c.tool, c.args)
		if res.IsError || r.route != c.route || r.q.Encode() != c.want.Encode() || resultText(t, res) != `{"route":"`+c.route+`"}` {
			t.Errorf("%s: route %s %v -> %s", c.tool, r.route, r.q, resultText(t, res))
		}
	}
	res := callToolOn(t, server, "pacto_delivery_history", map[string]any{"stage": "payments", "cluster": "uid-eu", "since": "2h"})
	if !res.IsError || !strings.Contains(resultText(t, res), "pass only namespace, stage and cluster") {
		t.Errorf("a filter with cluster: %s", resultText(t, res))
	}
	res = callToolOn(t, server, "pacto_delivery_history", map[string]any{"cluster": "uid-eu"})
	if !res.IsError {
		t.Errorf("a walk with cluster: %s", resultText(t, res))
	}
	r.err = errors.New("ClusterUnknown: no cluster uid-eu is read here")
	if res := callToolOn(t, server, "pacto_delivery_blame", map[string]any{"namespace": "prod", "stage": "payments", "cluster": "uid-eu"}); !res.IsError ||
		!strings.Contains(resultText(t, res), "ClusterUnknown") {
		t.Errorf("a refused route: %s", resultText(t, res))
	}
	alone := mustNew("test", Authoring{}, Modes{Delivery: &DeliveryMode{API: &stubDelivery{}}})
	if res := callToolOn(t, alone, "pacto_delivery_undo_plan", map[string]any{"namespace": "prod", "name": "op-1", "cluster": "uid-eu"}); !res.IsError ||
		!strings.Contains(resultText(t, res), "reads no other cluster") {
		t.Errorf("no reads: %s", resultText(t, res))
	}
}
