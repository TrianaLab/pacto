package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// fakeDeclarer records what the routes hand it.
type fakeDeclarer struct {
	write   bool
	plans   []DeclareWorkload
	accept  func(DeclareAcceptInput) (*DeclareAcceptAnswer, error)
	err     error
	targets []AcceptTarget
}

func (f *fakeDeclarer) Plan(_ *http.Request, w DeclareWorkload, in DeclareInput) (*DeclareAnswer, error) {
	f.plans = append(f.plans, w)
	if f.err != nil {
		return nil, f.err
	}
	return &DeclareAnswer{Plan: &DeclarePlan{Service: w.Name, IntentDigest: "sha256:i", Ranges: []DeclareRange{}},
		Write:   DeclareWrite{Available: f.write, Reason: map[bool]string{false: "the installation gave the dashboard no push credential: run the command"}[f.write]},
		Signing: DeclareSigning{Mode: "none", Reason: "run the command in your terminal to sign"}, Command: "pacto declare " + w.Name}, nil
}

func (f *fakeDeclarer) Accept(_ *http.Request, w DeclareWorkload, in DeclareAcceptInput) (*DeclareAcceptAnswer, error) {
	f.plans = append(f.plans, w)
	return f.accept(in)
}

func (f *fakeDeclarer) CanWrite() bool { return f.write }

func (f *fakeDeclarer) AcceptOffer(_ *http.Request, intent string) AcceptExecuteOffer {
	return AcceptExecuteOffer{Signing: DeclareSigning{Mode: "fresh-login", Identity: "ana@acme.com"}, Write: DeclareWrite{Available: f.write}, SignTicket: "ticket-" + intent}
}

func (f *fakeDeclarer) AcceptRanges(_ *http.Request, t AcceptTarget, in AcceptExecuteInput) (*AcceptExecuteAnswer, error) {
	f.targets = append(f.targets, t)
	if f.err != nil {
		return nil, f.err
	}
	return &AcceptExecuteAnswer{Draft: "x/" + t.Rev.Contract.Service.Name + ":pacto-accept-draft-1", Command: in.Mode}, nil
}

// declareServer serves inferredSnap (cart is inferred, orders declared)
// through view with d as the declarer.
func declareServer(t *testing.T, auth *Auth, view func(context.Context, *fleet.Query) (*fleet.Query, error), d Declarer) string {
	t.Helper()
	q := fleet.NewQuery(inferredSnap(t))
	return serveOptions(t, Options{Fleet: func(context.Context) (*fleet.Query, error) { return q, nil }, View: view, Auth: auth, Declare: d})
}

func openAuth() *Auth {
	return &Auth{Routes: http.NotFoundHandler(), Middleware: func(h http.Handler) http.Handler { return h },
		State: func(*http.Request) AuthState { return AuthState{} }}
}

func TestDeclarePlanNeedsIdentity(t *testing.T) {
	for name, auth := range map[string]*Auth{"open host": openAuth(), "no identity layer": nil, "signed out": viewerAuth(false, AuthState{})} {
		d := &fakeDeclarer{write: true}
		base := declareServer(t, auth, nil, d)
		code, out := call(t, http.MethodPost, base+"/api/fleet/declare", map[string]string{"serviceKey": cartKey})
		if code != http.StatusForbidden || out["code"] != "IdentityRequired" || len(d.plans) != 0 {
			t.Errorf("%s: %d %v %v", name, code, out, d.plans)
		}
		code, _ = call(t, http.MethodPost, base+"/api/fleet/declare/accept", map[string]any{"serviceKey": cartKey, "intentDigest": "sha256:i", "mode": "draft"})
		if code != http.StatusForbidden || len(d.plans) != 0 {
			t.Errorf("%s: accept answered %d", name, code)
		}
	}
}

func TestDeclareLinkSessionRefused(t *testing.T) {
	d := &fakeDeclarer{write: true}
	base := declareServer(t, viewerAuth(false, AuthState{SignedIn: true, FleetOnly: true}), nil, d)
	code, out := call(t, http.MethodPost, base+"/api/fleet/declare", map[string]string{"serviceKey": cartKey})
	if code != http.StatusForbidden || out["code"] != "SignInLinkReadsFleet" || len(d.plans) != 0 {
		t.Errorf("%d %v", code, out)
	}
}

func TestDeclareCapabilityFalseOnOpenHost(t *testing.T) {
	for _, c := range []struct {
		name string
		auth *Auth
		d    Declarer
		want bool
	}{
		{"open host", openAuth(), &fakeDeclarer{}, false},
		{"local host", viewerAuth(true, AuthState{}), &fakeDeclarer{}, true},
		{"signed in", viewerAuth(false, AuthState{SignedIn: true}), &fakeDeclarer{}, true},
		{"no declarer", viewerAuth(true, AuthState{}), nil, false},
	} {
		base := declareServer(t, c.auth, nil, c.d)
		var caps struct{ Declare bool }
		getJSON(t, base+"/api/capabilities", http.StatusOK, &caps)
		if caps.Declare != c.want {
			t.Errorf("%s: declare = %v", c.name, caps.Declare)
		}
	}
}

func TestDeclarePlanHiddenServiceNotFound(t *testing.T) {
	d := &fakeDeclarer{write: true}
	hideAll := func(_ context.Context, q *fleet.Query) (*fleet.Query, error) {
		return q.Restrict(func(_, _ string) bool { return false }, nil), nil
	}
	base := declareServer(t, viewerAuth(false, AuthState{SignedIn: true}), hideAll, d)
	code, hidden := call(t, http.MethodPost, base+"/api/fleet/declare", map[string]string{"serviceKey": cartKey})
	_, missing := call(t, http.MethodPost, base+"/api/fleet/declare", map[string]string{"serviceKey": "no-such-svc"})
	if code != http.StatusNotFound || hidden["detail"] != missing["detail"] || len(d.plans) != 0 {
		t.Errorf("hidden %d %v and missing %v must answer the same 404", code, hidden, missing)
	}
	// A declared service is no inferred one.
	base = declareServer(t, viewerAuth(true, AuthState{}), nil, d)
	if code, out := call(t, http.MethodPost, base+"/api/fleet/declare", map[string]string{"serviceKey": "orders"}); code != http.StatusNotFound || out["detail"] != missing["detail"] {
		t.Errorf("declared: %d %v", code, out)
	}
}

func TestDeclarePlanResolvesWorkload(t *testing.T) {
	d := &fakeDeclarer{write: true}
	base := declareServer(t, viewerAuth(true, AuthState{}), nil, d)
	var out DeclareAnswer
	postJSON(t, base+"/api/fleet/declare", map[string]any{"serviceKey": cartKey, "accept": []string{"orders"}}, http.StatusOK, &out)
	if len(d.plans) != 1 || d.plans[0] != (DeclareWorkload{Key: cartKey, Name: "cart", Namespace: "shop", Kind: "Deployment"}) {
		t.Errorf("planned %+v", d.plans)
	}
	if out.Plan == nil || out.Command != "pacto declare cart" || !out.Write.Available {
		t.Errorf("answer %+v", out)
	}
	d.err = errors.New("boom")
	if code, _ := call(t, http.MethodPost, base+"/api/fleet/declare", map[string]string{"serviceKey": cartKey}); code != http.StatusInternalServerError {
		t.Errorf("a failed plan answered %d", code)
	}
}

func TestDeclareRoutesAbsentWithoutDeclarer(t *testing.T) {
	base := declareServer(t, viewerAuth(true, AuthState{}), nil, nil)
	for _, path := range []string{"/api/fleet/declare", "/api/fleet/declare/accept"} {
		if code, _ := call(t, http.MethodPost, base+path, map[string]string{"serviceKey": cartKey}); code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
			t.Errorf("%s answered %d", path, code)
		}
	}
}

func TestDeclareAcceptRouteAbsentWithoutPushCredential(t *testing.T) {
	d := &fakeDeclarer{}
	base := declareServer(t, viewerAuth(false, AuthState{SignedIn: true}), nil, d)
	var out DeclareAnswer
	postJSON(t, base+"/api/fleet/declare", map[string]string{"serviceKey": cartKey}, http.StatusOK, &out)
	if out.Write.Available || !strings.Contains(out.Write.Reason, "push credential") {
		t.Errorf("write = %+v", out.Write)
	}
	code, _ := call(t, http.MethodPost, base+"/api/fleet/declare/accept", map[string]any{"serviceKey": cartKey, "intentDigest": "sha256:i", "mode": "draft"})
	if code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
		t.Errorf("accept answered %d without a push credential", code)
	}
}

func TestDeclarePlanStaleAnswersFreshPlan(t *testing.T) {
	d := &fakeDeclarer{write: true, accept: func(in DeclareAcceptInput) (*DeclareAcceptAnswer, error) {
		if in.IntentDigest != "sha256:old" || in.Mode != "sign" || in.FromDraft == nil || in.FromDraft.Tag != "draft-2" {
			t.Errorf("accept got %+v", in)
		}
		return &DeclareAcceptAnswer{Refusal: &DeclareRefusal{Code: "PlanStale", Message: "the plan changed"},
			Plan: &DeclareAnswer{Plan: &DeclarePlan{IntentDigest: "sha256:new", Ranges: []DeclareRange{}}}}, nil
	}}
	base := declareServer(t, viewerAuth(true, AuthState{}), nil, d)
	var out DeclareAcceptAnswer
	postJSON(t, base+"/api/fleet/declare/accept", map[string]any{"serviceKey": cartKey, "intentDigest": "sha256:old", "mode": "sign",
		"fromDraft": map[string]string{"tag": "draft-2", "digest": "sha256:" + strings.Repeat("a", 64)}}, http.StatusOK, &out)
	if out.Refusal == nil || out.Refusal.Code != "PlanStale" || out.Plan == nil || out.Plan.Plan.IntentDigest != "sha256:new" {
		t.Errorf("answer %+v", out)
	}
	d.accept = func(DeclareAcceptInput) (*DeclareAcceptAnswer, error) { return nil, errors.New("boom") }
	if code, _ := call(t, http.MethodPost, base+"/api/fleet/declare/accept", map[string]any{"serviceKey": cartKey, "intentDigest": "x", "mode": "draft"}); code != http.StatusInternalServerError {
		t.Errorf("a failed accept answered %d", code)
	}
	if code, _ := call(t, http.MethodPost, base+"/api/fleet/declare/accept", map[string]any{"serviceKey": "orders", "intentDigest": "x", "mode": "draft"}); code != http.StatusNotFound {
		t.Errorf("accepting a declared service answered %d", code)
	}
}

func TestDeclareTextTruncates(t *testing.T) {
	if got := NewDeclareText([]byte("a: b\n")); got.Truncated || got.Text != "a: b\n" {
		t.Errorf("%+v", got)
	}
	if got := NewDeclareText([]byte(strings.Repeat("x", MaxDeclareText+1))); !got.Truncated || len(got.Text) != MaxDeclareText {
		t.Errorf("truncated %v, %d bytes", got.Truncated, len(got.Text))
	}
}

func TestOpenAPIDeclareSchemas(t *testing.T) {
	raw, err := ExportOpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	paths := doc["paths"].(map[string]any)
	for _, p := range []string{"/api/fleet/declare", "/api/fleet/declare/accept"} {
		if paths[p] == nil {
			t.Errorf("no %s", p)
		}
	}
	for _, c := range []struct {
		schema, field string
		want          []string
	}{
		{"DeclareSigning", "mode", sortedSet("fresh-login", "none")},
		{"DeclareAcceptInput", "mode", sortedSet("sign", "draft")},
	} {
		if got := sortedKeys(schemaFieldEnum(t, doc, c.schema, c.field)); !equalStrs(got, c.want) {
			t.Errorf("%s.%s enum = %v, want %v", c.schema, c.field, got, c.want)
		}
	}
	codes := sortedKeys(schemaFieldEnum(t, doc, "DeclareRefusal", "code"))
	// The owner codes are 513-S4's own strings: its refusal reaches the
	// answer unchanged.
	for _, c := range append([]string{"PlanStale", "IdentityRequired", "SignInLinkReadsFleet", "ReviewFailed", "ClusterNotReviewable", "NotAllowed"}, delivery.OwnerRightCodes...) {
		if !slices.Contains(codes, c) {
			t.Errorf("DeclareRefusal.code has no %s: %v", c, codes)
		}
	}
	var s schemaDeclarer
	if _, err := s.Plan(nil, DeclareWorkload{}, DeclareInput{}); err == nil {
		t.Error("the schema declarer plans nothing")
	}
	if _, err := s.Accept(nil, DeclareWorkload{}, DeclareAcceptInput{}); err == nil {
		t.Error("the schema declarer accepts nothing")
	}
	if _, err := s.AcceptRanges(nil, AcceptTarget{}, AcceptExecuteInput{}); err == nil || s.AcceptOffer(nil, "") != (AcceptExecuteOffer{}) {
		t.Error("the schema declarer executes nothing")
	}
}

// cartKey is cart's service key.
var cartKey = string(fleet.NewServiceKey(cartName))

func TestDeclareFleetUnreadable(t *testing.T) {
	d := &fakeDeclarer{write: true}
	base := serveOptions(t, Options{Fleet: func(context.Context) (*fleet.Query, error) { return nil, errors.New("down") },
		Auth: viewerAuth(true, AuthState{}), Declare: d})
	if code, _ := call(t, http.MethodPost, base+"/api/fleet/declare", map[string]string{"serviceKey": cartKey}); code < 500 || len(d.plans) != 0 {
		t.Errorf("an unreadable fleet answered %d", code)
	}
}
