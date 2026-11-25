package mcp

import (
	"bytes"
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
	// bodies answer by route; absent answers {"route": route}.
	bodies map[string]string
}

func (s *stubReads) reads(_ context.Context, route string, q url.Values) (json.RawMessage, error) {
	s.route, s.q = route, q
	if s.err != nil {
		return nil, s.err
	}
	if b, ok := s.bodies[route]; ok {
		return json.RawMessage(b), nil
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

// TestDeliveryToolsReadAnotherClusterThroughRoute: the reads of another
// cluster run the dashboard's route with cluster, never the server's own
// client, and answer in the shape the server's own cluster answers in.
func TestDeliveryToolsReadAnotherClusterThroughRoute(t *testing.T) {
	r := &stubReads{bodies: map[string]string{
		"history": `{"histories":[{"cluster":"uid-eu","namespace":"prod","stage":{"name":"payments"},"entries":[{"operation":"op-1"}],` +
			`"entriesPage":{"total":1}}],"at":{"stages":[]},"revisions":{"sha256:a":"rev"}}`,
		"blame":                `{"digest":"sha256:a","revisions":{"sha256:a":"rev"}}`,
		"undo":                 `{"operation":"prod/op-1","kind":"none"}`,
		"record":               `{"record":"sha256:r"}`,
		"operations/prod/op-1": `{"operation":{"metadata":{"name":"op-1","namespace":"prod"}},"explanation":{"operation":"prod/op-1"}}`,
	}}
	server := mustNew("test", Authoring{}, Modes{Delivery: &DeliveryMode{API: &stubDelivery{}, Reads: r.reads}})
	for _, c := range []struct {
		tool, route string
		args        map[string]any
		want        url.Values
		has         []string
	}{
		{"pacto_delivery_history", "history", map[string]any{"namespace": "prod", "stage": "payments", "cluster": "uid-eu", "since": "2h", "correlation_id": "inc-1"},
			url.Values{"namespace": {"prod"}, "stage": {"payments"}, "cluster": {"uid-eu"}, "limit": {"200"}, "since": {"2h"}, "correlationID": {"inc-1"}},
			[]string{`"entries":[`, `"revisions":{"sha256:a":"rev"}`, `"entriesPage":{"total":1}`}},
		{"pacto_delivery_history", "history", map[string]any{"namespace": "prod", "stage": "payments", "cluster": "uid-eu", "at": "2h"},
			url.Values{"namespace": {"prod"}, "stage": {"payments"}, "cluster": {"uid-eu"}, "limit": {"200"}, "at": {"2h"}}, []string{`"stages":[]`}},
		{"pacto_delivery_blame", "blame", map[string]any{"namespace": "prod", "stage": "payments", "digest": "sha256:a", "cluster": "uid-eu"},
			url.Values{"namespace": {"prod"}, "stage": {"payments"}, "digest": {"sha256:a"}, "cluster": {"uid-eu"}}, []string{`"revision":"rev"`}},
		{"pacto_delivery_undo_plan", "undo", map[string]any{"namespace": "prod", "name": "op-1", "cluster": "uid-eu"},
			url.Values{"namespace": {"prod"}, "name": {"op-1"}, "stage": {""}, "cluster": {"uid-eu"}}, []string{`"kind":"none"`}},
		{"pacto_delivery_record_get", "record", map[string]any{"namespace": "prod", "stage": "payments", "record": "sha256:r", "cluster": "uid-eu"},
			url.Values{"namespace": {"prod"}, "stage": {"payments"}, "id": {"sha256:r"}, "cluster": {"uid-eu"}}, []string{`"record":"sha256:r"`}},
		{"pacto_delivery_get", "operations/prod/op-1", map[string]any{"namespace": "prod", "name": "op-1", "cluster": "uid-eu"},
			url.Values{"cluster": {"uid-eu"}}, []string{`"name":"op-1"`}},
		{"pacto_delivery_explain", "operations/prod/op-1", map[string]any{"namespace": "prod", "name": "op-1", "cluster": "uid-eu"},
			url.Values{"cluster": {"uid-eu"}}, []string{`"operation":"prod/op-1"`}},
	} {
		res := callToolOn(t, server, c.tool, c.args)
		var compact bytes.Buffer
		_ = json.Compact(&compact, []byte(resultText(t, res)))
		text := compact.String()
		if res.IsError || r.route != c.route || r.q.Encode() != c.want.Encode() || strings.Contains(text, "histories") {
			t.Errorf("%s: route %s %v -> %s", c.tool, r.route, r.q, text)
		}
		for _, h := range c.has {
			if !strings.Contains(text, h) {
				t.Errorf("%s: no %s in %s", c.tool, h, text)
			}
		}
	}
}

// TestClusterReadsRefuseWhatTheyCannotAnswer: a cluster read of history
// needs one Stage, a refused route is the tool's error and a server that
// reads no other cluster says so.
func TestClusterReadsRefuseWhatTheyCannotAnswer(t *testing.T) {
	r := &stubReads{}
	server := mustNew("test", Authoring{}, Modes{Delivery: &DeliveryMode{API: &stubDelivery{}, Reads: r.reads}})
	for _, args := range []map[string]any{{"stage": "payments", "cluster": "uid-eu"}, {"cluster": "uid-eu"},
		{"namespace": "prod", "stage": "payments", "service": "payments", "cluster": "uid-eu"}} {
		if res := callToolOn(t, server, "pacto_delivery_history", args); !res.IsError || !strings.Contains(resultText(t, res), "pass namespace and stage") {
			t.Errorf("%v: %s", args, resultText(t, res))
		}
	}
	r.err = errors.New("ClusterUnknown: no cluster uid-eu is read here")
	for _, tool := range []string{"pacto_delivery_history", "pacto_delivery_blame"} {
		if res := callToolOn(t, server, tool, map[string]any{"namespace": "prod", "stage": "payments", "cluster": "uid-eu"}); !res.IsError ||
			!strings.Contains(resultText(t, res), "ClusterUnknown") {
			t.Errorf("%s: a refused route: %s", tool, resultText(t, res))
		}
	}
	if res := callToolOn(t, server, "pacto_delivery_get", map[string]any{"namespace": "prod", "name": "op-1", "cluster": "uid-eu"}); !res.IsError {
		t.Errorf("get: a refused route: %s", resultText(t, res))
	}
	alone := mustNew("test", Authoring{}, Modes{Delivery: &DeliveryMode{API: &stubDelivery{}}})
	if res := callToolOn(t, alone, "pacto_delivery_undo_plan", map[string]any{"namespace": "prod", "name": "op-1", "cluster": "uid-eu"}); !res.IsError ||
		!strings.Contains(resultText(t, res), "reads no other cluster") {
		t.Errorf("no reads: %s", resultText(t, res))
	}
}
