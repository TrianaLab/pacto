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
	for _, route := range []string{"caller", "inbox", "environments", "autonomy", "published", "refusals"} {
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
		return q.Restrict(func(string) bool { return false }), nil
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
