package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const (
	dRun  = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	dNext = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

// fakeDelivery is a cluster as one caller sees it.
type fakeDelivery struct {
	caller    delivery.Caller
	callerErr error
	stages    []delivery.StageObject
	ops       []delivery.OperationObject
	// forbidAll refuses cluster-wide lists; forbid refuses those namespaces.
	forbidAll  bool
	forbid     map[string]bool
	listErr    error
	opsErr     error
	explainErr error
	getErr     error
	historyErr error
	actErr     error
	approved   []string
	cancelled  []string
	proposed   []delivery.Request
}

func (f *fakeDelivery) WhoAmI(context.Context) (delivery.Caller, error) { return f.caller, f.callerErr }

func (f *fakeDelivery) refuse(ns string) error {
	if ns == "" && f.forbidAll || f.forbid[ns] {
		return Refuse(http.StatusForbidden, "Forbidden", "stages is forbidden in "+ns)
	}
	return f.listErr
}

func (f *fakeDelivery) Stages(_ context.Context, ns string) ([]delivery.StageObject, error) {
	if err := f.refuse(ns); err != nil {
		return nil, err
	}
	var out []delivery.StageObject
	for _, s := range f.stages {
		if ns == "" || s.Metadata.Namespace == ns {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeDelivery) Operations(_ context.Context, ns string) ([]delivery.OperationObject, error) {
	if f.opsErr != nil {
		return nil, f.opsErr
	}
	var out []delivery.OperationObject
	for _, o := range f.ops {
		if ns == "" || o.Metadata.Namespace == ns {
			out = append(out, o)
		}
	}
	return out, nil
}

func (f *fakeDelivery) Get(_ context.Context, ns, name string) (*delivery.OperationObject, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	for _, o := range f.ops {
		if o.Metadata.Namespace == ns && o.Metadata.Name == name {
			return &o, nil
		}
	}
	return nil, Refuse(http.StatusNotFound, "NotFound", name+" not found")
}

func (f *fakeDelivery) Explain(_ context.Context, ns, name string) (*delivery.Explanation, error) {
	if f.explainErr != nil {
		return nil, f.explainErr
	}
	return &delivery.Explanation{Operation: ns + "/" + name, Stage: delivery.StageNow{Name: "payments", Pointer: dNext}}, nil
}

func (f *fakeDelivery) History(_ context.Context, ns, stage string) (*delivery.History, error) {
	return &delivery.History{Stage: delivery.StageNow{Name: ns + "/" + stage}, Completeness: "partial"}, f.historyErr
}

func (f *fakeDelivery) Plan(_ context.Context, req delivery.Request) (delivery.Plan, error) {
	return delivery.Plan{IntentDigest: "sha256:i", To: req.To, Predicted: delivery.PredictProceeds}, f.actErr
}

func (f *fakeDelivery) Propose(_ context.Context, req delivery.Request) (*delivery.OperationObject, delivery.Plan, error) {
	if req.IntentDigest == "stale" {
		p := delivery.Plan{IntentDigest: "sha256:fresh"}
		return nil, p, &DeliveryError{Status: http.StatusConflict, Code: "PlanStale", Detail: "moved", Plan: &p}
	}
	f.proposed = append(f.proposed, req)
	return &delivery.OperationObject{Metadata: delivery.ObjectMeta{Name: "op-1"}}, delivery.Plan{IntentDigest: "sha256:i"}, f.actErr
}

func (f *fakeDelivery) Approve(_ context.Context, ns, name string, d delivery.Decision, _ string) (string, error) {
	f.approved = append(f.approved, ns+"/"+name+"="+string(d))
	return name + "-x", f.actErr
}

func (f *fakeDelivery) Cancel(_ context.Context, ns, name string) error {
	f.cancelled = append(f.cancelled, ns+"/"+name)
	return f.actErr
}

func (f *fakeDelivery) Describe(_ context.Context, _ *delivery.StageObject, digest string) delivery.Revision {
	if digest == dRun {
		return delivery.Revision{Digest: digest, ContractRef: "oci://r/payments@sha256:c",
			Bundle: &contract.Bundle{Contract: &contract.Contract{Service: contract.Service{Name: "payments", Version: "1.0.0"}}}}
	}
	return delivery.Revision{Digest: digest}
}

func stage(ns, name, service string, st delivery.StageStatus) delivery.StageObject {
	return delivery.StageObject{Metadata: delivery.ObjectMeta{Namespace: ns, Name: name},
		Spec: delivery.StageSpec{Service: delivery.ServiceRef{Name: service}, Environment: ns}, Status: st}
}

func operation(ns, name, stg string, st delivery.OperationStatus) delivery.OperationObject {
	return delivery.OperationObject{Metadata: delivery.ObjectMeta{Namespace: ns, Name: name},
		Spec: delivery.OperationSpec{Type: delivery.TypePromote, Stage: stg, Params: delivery.Params{From: dRun, To: dNext}}, Status: st}
}

func held(owners ...string) delivery.OperationStatus {
	return delivery.OperationStatus{Phase: delivery.PhaseHeld, Question: "Approve?",
		Plan: &delivery.Plan{Floor: delivery.Floor{Human: true, Decision: delivery.Approve, Owners: owners, Consumers: []string{"checkout"}}}}
}

// ownedFleet is a snapshot with payments (team-payments, in prod) and ledger
// (team-ledger, in books).
func ownedFleet(t *testing.T) func(context.Context) (*fleet.Query, error) {
	t.Helper()
	b := func(name, team string) *contract.Bundle {
		return &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.0",
			Service: contract.Service{Name: name, Version: "1.0.0", Owner: contract.Owner{Team: team}}}}
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("local", "local", &fleet.Collection{
		Revisions: []fleet.RawRevision{{Bundle: b("payments", "team-payments")}, {Bundle: b("ledger", "team-ledger")}},
		Targets: []fleet.RawTarget{{Scope: "prod", Kind: "kubernetes-workload", Name: "payments", Service: "payments"},
			{Scope: "books", Kind: "kubernetes-workload", Name: "ledger", Service: "ledger"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(snap)
	return func(context.Context) (*fleet.Query, error) { return q, nil }
}

type authed struct {
	state AuthState
}

func (a *authed) auth(local, login bool) *Auth {
	return &Auth{Local: local, Login: login, Routes: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }),
		Middleware: func(h http.Handler) http.Handler { return h },
		State:      func(*http.Request) AuthState { return a.state }}
}

func startDelivery(t *testing.T, f *fakeDelivery, auth *Auth, installed bool, factoryErr error) string {
	t.Helper()
	return startServer(t, NewServer(Options{UI: testUI(), Fleet: ownedFleet(t), Auth: auth, MiraURL: "http://mira",
		DeliveryPolicy:    delivery.ClassPolicy{AgentGroups: []string{"pacto:agents"}, TeamGroupPrefix: "acme:"},
		DeliveryInstalled: func(context.Context) (bool, error) { return installed, nil },
		Delivery: func(r *http.Request) (DeliveryAPI, error) {
			if r == nil {
				return nil, errors.New("no request on the context")
			}
			return f, factoryErr
		}}))
}

func call(t *testing.T, method, url string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, rd)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func person(groups ...string) delivery.Caller {
	return delivery.Caller{Identity: delivery.Identity{User: "https://idp#carol", Groups: groups}, Class: delivery.ClassHuman,
		AuthTime: strconv.FormatInt(time.Now().Add(-time.Minute).Unix(), 10)}
}

// expect fails the test unless ok.
func expect(t *testing.T, ok bool, format string, args ...any) {
	t.Helper()
	if !ok {
		t.Fatalf(format, args...)
	}
}

func TestCapabilitiesAndSessionSignedIn(t *testing.T) {
	a := &authed{state: AuthState{SignedIn: true, Fresh: true, CSRF: "c"}}
	f := &fakeDelivery{caller: person("team-payments")}
	base := startDelivery(t, f, a.auth(false, true), true, nil)
	_, caps := call(t, http.MethodGet, base+"/api/capabilities", nil)
	auth, _ := caps["auth"].(map[string]any)
	expect(t, caps["delivery"] == true && caps["miraURL"] == "http://mira", "capabilities %v", caps)
	expect(t, auth["mode"] == "shared" && auth["login"] == true && auth["signedIn"] == true, "capabilities %v", caps)
	_, sess := call(t, http.MethodGet, base+"/api/session", nil)
	caller, _ := sess["caller"].(map[string]any)
	expect(t, sess["mode"] == "shared" && sess["fresh"] == true, "session %v", sess)
	expect(t, sess["csrf"] == "c" && caller["user"] == "https://idp#carol", "session %v", sess)
	code, _ := call(t, http.MethodGet, base+"/auth/login", nil)
	expect(t, code == http.StatusTeapot, "/auth/* goes to the identity layer: %d", code)
	f.callerErr = errors.New("apiserver down")
	_, sess = call(t, http.MethodGet, base+"/api/session", nil)
	expect(t, sess["callerError"] == "apiserver down", "a WhoAmI failure is reported: %v", sess)
}

func TestCapabilitiesAndSessionWithoutDelivery(t *testing.T) {
	a := &authed{state: AuthState{SignedIn: true}}
	f := &fakeDelivery{caller: person()}
	// Shared without sign-in: no delivery.
	base := startDelivery(t, f, a.auth(false, false), true, nil)
	_, caps := call(t, http.MethodGet, base+"/api/capabilities", nil)
	expect(t, caps["delivery"] == false, "a shared dashboard without sign-in has no delivery: %v", caps)
	// Local mode, delivery not installed.
	base = startDelivery(t, f, a.auth(true, false), false, Refuse(http.StatusUnauthorized, "LoginRequired", "sign in"))
	_, caps = call(t, http.MethodGet, base+"/api/capabilities", nil)
	expect(t, caps["delivery"] == false && caps["auth"].(map[string]any)["mode"] == "local", "delivery not installed: %v", caps)
	_, sess := call(t, http.MethodGet, base+"/api/session", nil)
	expect(t, sess["mode"] == "local" && sess["caller"] == nil, "no caller when delivery is not installed: %v", sess)
	// A factory that refuses is reported.
	base = startDelivery(t, f, a.auth(true, false), true, Refuse(http.StatusUnauthorized, "LoginRequired", "sign in"))
	_, sess = call(t, http.MethodGet, base+"/api/session", nil)
	expect(t, strings.Contains(sess["callerError"].(string), "LoginRequired"), "a factory refusal is reported: %v", sess)
	// No identity layer at all.
	srv := startServer(t, NewServer(Options{UI: testUI()}))
	_, sess = call(t, http.MethodGet, srv+"/api/session", nil)
	expect(t, sess["mode"] == "none", "no auth: %v", sess)
	_, caps = call(t, http.MethodGet, srv+"/api/capabilities", nil)
	expect(t, caps["delivery"] == false && caps["auth"].(map[string]any)["mode"] == "none", "no auth: %v", caps)
	code, _ := call(t, http.MethodGet, srv+"/api/delivery/inbox", nil)
	expect(t, code == http.StatusNotFound, "no delivery provider registers no delivery route: %d", code)
}

func TestDeliveryNotInstalledAndRefusals(t *testing.T) {
	a := &authed{state: AuthState{SignedIn: true}}
	f := &fakeDelivery{caller: person()}
	base := startDelivery(t, f, a.auth(false, true), false, nil)
	code, body := call(t, http.MethodGet, base+"/api/delivery/environments", nil)
	if code != http.StatusNotFound || body["code"] != "DeliveryNotInstalled" {
		t.Fatalf("not installed: %d %v", code, body)
	}
	// A cluster that cannot be asked is not a cluster without delivery: the
	// reads say so, the tab stays and the session names the failure.
	down := startServer(t, NewServer(Options{UI: testUI(), Auth: a.auth(false, true),
		DeliveryInstalled: func(context.Context) (bool, error) { return false, errors.New("connection refused") },
		Delivery:          func(*http.Request) (DeliveryAPI, error) { return f, nil }}))
	if code, body = call(t, http.MethodGet, down+"/api/delivery/environments", nil); code != http.StatusServiceUnavailable || body["code"] != "ClusterUnreachable" {
		t.Fatalf("unreachable: %d %v", code, body)
	}
	_, caps := call(t, http.MethodGet, down+"/api/capabilities", nil)
	expect(t, caps["delivery"] == true, "an unreachable cluster keeps the delivery tab: %v", caps)
	_, sess := call(t, http.MethodGet, down+"/api/session", nil)
	expect(t, strings.Contains(fmt.Sprint(sess["callerError"]), "ClusterUnreachable"), "the session names the failure: %v", sess)
	base = startDelivery(t, f, a.auth(false, true), true, Refuse(http.StatusUnauthorized, "LoginRequired", "sign in"))
	for _, p := range []string{"inbox", "environments", "published", "history", "operations/prod/op"} {
		if code, body = call(t, http.MethodGet, base+"/api/delivery/"+p, nil); code != http.StatusUnauthorized || body["code"] != "LoginRequired" {
			t.Errorf("%s: %d %v", p, code, body)
		}
	}
	for _, c := range []struct{ method, path string }{{http.MethodPost, "plan"}, {http.MethodPost, "operations"},
		{http.MethodDelete, "operations/prod/op"}} {
		if code, _ = call(t, c.method, base+"/api/delivery/"+c.path, delivery.Request{}); code != http.StatusUnauthorized {
			t.Errorf("%s %s: %d", c.method, c.path, code)
		}
	}
}

// sampleCluster is three Stages and the operations on them.
func sampleCluster(groups ...string) *fakeDelivery {
	return &fakeDelivery{caller: person(groups...),
		stages: []delivery.StageObject{
			stage("prod", "payments", "payments", delivery.StageStatus{Pointer: dRun, Verified: []string{dRun}, Published: []string{dRun, dNext},
				Candidate: dNext, Frozen: true, FrozenReason: "Errored", Conditions: []delivery.Condition{{Type: "Ready", Status: "True"}}}),
			stage("books", "ledger", "ledger", delivery.StageStatus{Frozen: true, FailedDigests: []string{dNext}}),
			stage("other", "orphan", "orphan", delivery.StageStatus{AutoHold: true, Frozen: true}),
		},
		ops: []delivery.OperationObject{
			operation("prod", "for-checkout", "payments", held("team-checkout")),
			operation("prod", "for-ledger", "payments", held("team-ledger")),
			operation("prod", "anyone", "payments", delivery.OperationStatus{Phase: delivery.PhaseHeld, Applied: true,
				Plan: &delivery.Plan{Floor: delivery.Floor{Decision: delivery.Approve}}}),
			operation("books", "broke", "ledger", delivery.OperationStatus{Phase: delivery.PhaseConcluded, Outcome: delivery.Errored}),
			operation("other", "broke-too", "orphan", delivery.OperationStatus{Phase: delivery.PhaseConcluded, Outcome: delivery.Errored}),
			// Errored long ago on a Stage that was unpaused since, and on a
			// Stage that is gone: history, not the inbox.
			operation("other", "broke-before", "unpaused", delivery.OperationStatus{Phase: delivery.PhaseConcluded, Outcome: delivery.Errored}),
			operation("other", "no-stage", "gone", delivery.OperationStatus{Phase: delivery.PhaseConcluded, Outcome: delivery.Errored}),
		}}
}

func names(items any) string {
	var out []string
	for _, it := range items.([]any) {
		m := it.(map[string]any)
		out = append(out, m["name"].(string)+":"+m["decision"].(string))
	}
	return strings.Join(out, ",")
}

func TestInbox(t *testing.T) {
	f := sampleCluster("acme:team-checkout")
	f.stages = append(f.stages, stage("other", "unpaused", "orphan", delivery.StageStatus{}))
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	_, inbox := call(t, http.MethodGet, base+"/api/delivery/inbox", nil)
	expect(t, inbox["completeness"] == "complete", "inbox %v", inbox)
	expect(t, names(inbox["held"]) == "for-checkout:approve,anyone:accept-unknown",
		"held: only what names my team (or nobody), as accept-unknown once it shipped: %v", inbox["held"])
	first := inbox["held"].([]any)[0].(map[string]any)
	expect(t, first["question"] == "Approve?" && strings.Join(toStrings(first["consumers"]), ",") == "checkout" &&
		strings.Join(toStrings(first["owners"]), ",") == "team-checkout",
		"a held item carries its question, the services affected and the teams that may answer: %v", first)
	fr := inbox["frozen"].([]any)
	expect(t, len(fr) == 1 && fr[0].(map[string]any)["service"] == "orphan",
		"frozen Stages of other teams' services are not mine, unowned ones are: %v", fr)
	errored := inbox["errored"].([]any)
	expect(t, len(errored) == 1 && errored[0].(map[string]any)["name"] == "broke-too",
		"errored: only on my (or unowned) Stages that are still frozen: %v", errored)
	f.caller = person("team-payments")
	_, inbox = call(t, http.MethodGet, base+"/api/delivery/inbox", nil)
	fr = inbox["frozen"].([]any)
	expect(t, len(fr) == 2 && fr[0].(map[string]any)["service"] == "payments", "the frozen Stage of my service: %v", fr)
}

func TestEnvironments(t *testing.T) {
	f := sampleCluster("team-payments")
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	_, envs := call(t, http.MethodGet, base+"/api/delivery/environments", nil)
	rows := envs["environments"].([]any)
	pay := rows[2].(map[string]any)
	expect(t, len(rows) == 3 && pay["service"] == "payments" && pay["version"] == "1.0.0", "environments %v", rows)
	expect(t, pay["verified"] == true && pay["held"] == float64(3) && pay["ready"] == "True", "environments %v", rows)
	expect(t, strings.Join(toStrings(pay["actions"]), ",") == "promote,unfreeze", "actions %v", pay["actions"])
	led := rows[0].(map[string]any)
	expect(t, led["service"] == "ledger" && led["failed"] == false, "ledger row %v", led)
	// An agent may promote but never unfreeze.
	f.caller = delivery.Caller{Identity: delivery.Identity{User: "system:serviceaccount:a:b"}, Class: delivery.ClassAgent}
	_, envs = call(t, http.MethodGet, base+"/api/delivery/environments", nil)
	got := strings.Join(toStrings(envs["environments"].([]any)[2].(map[string]any)["actions"]), ",")
	expect(t, got == "promote", "agent actions %v", got)
}

func TestPublished(t *testing.T) {
	f := sampleCluster("team-payments")
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	_, pub := call(t, http.MethodGet, base+"/api/delivery/published", nil)
	payments := pub["services"].([]any)[2].(map[string]any)
	versions := payments["versions"].([]any)
	first := versions[0].(map[string]any)
	expect(t, payments["service"] == "payments" && len(versions) == 2, "published %v", payments)
	expect(t, first["version"] == "1.0.0" && first["signed"] == true && first["contract"] == "oci://r/payments@sha256:c", "published %v", first)
	expect(t, first["stages"].([]any)[0].(map[string]any)["running"] == true, "published %v", first)
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func TestListsDropNamespacesTheCallerCannotRead(t *testing.T) {
	a := &authed{state: AuthState{SignedIn: true}}
	f := &fakeDelivery{caller: person(), forbidAll: true, forbid: map[string]bool{"books": true},
		stages: []delivery.StageObject{stage("prod", "payments", "payments", delivery.StageStatus{}), stage("books", "ledger", "ledger", delivery.StageStatus{})}}
	base := startDelivery(t, f, a.auth(false, true), true, nil)
	_, envs := call(t, http.MethodGet, base+"/api/delivery/environments", nil)
	if envs["completeness"] != "partial" || strings.Join(toStrings(envs["unreadable"]), ",") != "*,books" || len(envs["environments"].([]any)) != 1 {
		t.Fatalf("a refused namespace is dropped and named: %v", envs)
	}
	// Every namespace the snapshot knows answered, but Stages may live in
	// others: still partial.
	f.forbid = nil
	if _, envs = call(t, http.MethodGet, base+"/api/delivery/environments", nil); fmt.Sprint(envs["completeness"], toStrings(envs["unreadable"])) != "partial[*]" {
		t.Fatalf("namespaces guessed from the snapshot are never complete: %v", envs)
	}
	f.forbid = map[string]bool{"prod": false}
	f.listErr = nil
	f.opsErr = errors.New("boom")
	if code, _ := call(t, http.MethodGet, base+"/api/delivery/environments", nil); code != http.StatusInternalServerError {
		t.Fatalf("an error that is not a refusal fails the read: %d", code)
	}
	f.forbidAll, f.opsErr = false, nil
	f.listErr = errors.New("down")
	if code, _ := call(t, http.MethodGet, base+"/api/delivery/inbox", nil); code != http.StatusInternalServerError {
		t.Fatalf("a cluster-wide list error fails: %d", code)
	}
	// Without a snapshot the namespaces cannot be known.
	f.listErr, f.forbidAll = nil, true
	srv := startServer(t, NewServer(Options{UI: testUI(), Auth: a.auth(false, true),
		Delivery: func(*http.Request) (DeliveryAPI, error) { return f, nil }}))
	_, envs = call(t, http.MethodGet, srv+"/api/delivery/environments", nil)
	if envs["completeness"] != "partial" || strings.Join(toStrings(envs["unreadable"]), ",") != "*" {
		t.Fatalf("no snapshot: %v", envs)
	}
	// WhoAmI failing fails the reads that need the caller.
	f.callerErr = errors.New("who")
	for _, p := range []string{"inbox", "environments"} {
		if code, _ := call(t, http.MethodGet, srv+"/api/delivery/"+p, nil); code != http.StatusInternalServerError {
			t.Errorf("%s: %d", p, code)
		}
	}
	f.callerErr, f.forbidAll = nil, false
	f.listErr = errors.New("down")
	for _, p := range []string{"inbox", "environments", "published", "history"} {
		if code, _ := call(t, http.MethodGet, srv+"/api/delivery/"+p, nil); code != http.StatusInternalServerError {
			t.Errorf("%s list error: %d", p, code)
		}
	}
	f.listErr = nil
	if code, _ := call(t, http.MethodGet, srv+"/api/delivery/inbox", nil); code != http.StatusOK {
		t.Fatalf("inbox with no snapshot shows everything: %d", code)
	}
	f.forbidAll, f.forbid = true, map[string]bool{}
	f.listErr = nil
	srv2 := startServer(t, NewServer(Options{UI: testUI(), Fleet: ownedFleet(t),
		Delivery: func(*http.Request) (DeliveryAPI, error) { return f, nil }}))
	f.forbid = nil
	f.opsErr = errors.New("ops down")
	if code, _ := call(t, http.MethodGet, srv2+"/api/delivery/published", nil); code != http.StatusInternalServerError {
		t.Fatalf("a per-namespace error fails the read: %d", code)
	}
}

func TestHistoryAndOperation(t *testing.T) {
	a := &authed{state: AuthState{SignedIn: true}}
	succeeded := delivery.OperationStatus{Phase: delivery.PhaseConcluded, Outcome: delivery.Succeeded, Applied: true,
		Pointer: delivery.PointerChange{Before: dRun, After: dNext}}
	f := &fakeDelivery{caller: person("team-checkout"),
		stages: []delivery.StageObject{stage("prod", "payments", "payments", delivery.StageStatus{})},
		ops: []delivery.OperationObject{operation("prod", "held", "payments", held("team-checkout")), operation("prod", "done", "payments", succeeded),
			operation("prod", "unknown", "payments", delivery.OperationStatus{Phase: delivery.PhaseHeld, Applied: true})}}
	base := startDelivery(t, f, a.auth(false, true), true, nil)
	_, h := call(t, http.MethodGet, base+"/api/delivery/history", nil)
	if hs := h["histories"].([]any); len(hs) != 1 || hs[0].(map[string]any)["stage"].(map[string]any)["name"] != "prod/payments" ||
		hs[0].(map[string]any)["namespace"] != "prod" {
		t.Fatalf("history of every Stage: %v", h)
	}
	if _, h = call(t, http.MethodGet, base+"/api/delivery/history?namespace=prod&stage=payments", nil); len(h["histories"].([]any)) != 1 {
		t.Fatalf("history of one Stage: %v", h)
	}
	f.historyErr = errors.New("gone")
	if code, _ := call(t, http.MethodGet, base+"/api/delivery/history?namespace=prod&stage=payments", nil); code != http.StatusInternalServerError {
		t.Fatalf("history error: %d", code)
	}
	actions := func(name string) string {
		_, out := call(t, http.MethodGet, base+"/api/delivery/operations/prod/"+name, nil)
		return strings.Join(toStrings(out["actions"]), ",")
	}
	if got := actions("held"); got != "approve,reject,cancel" {
		t.Fatalf("held, my team, signed in: %s", got)
	}
	if got := actions("done"); got != "rollback" {
		t.Fatalf("concluded and still running: %s", got)
	}
	if got := actions("unknown"); got != "accept-unknown,reject" {
		t.Fatalf("held after it shipped: %s", got)
	}
	// Whoever asked for a change, or is the one it acts for, is not offered
	// its decision: the promoter would ignore it.
	f.ops[0].Spec.Actor.User = f.caller.User
	if got := actions("held"); got != "cancel" {
		t.Fatalf("my own change: %s", got)
	}
	f.ops[0].Spec.Actor.User, f.ops[0].Spec.OnBehalfOf = "bot", &delivery.Principal{User: f.caller.User}
	if got := actions("held"); got != "cancel" {
		t.Fatalf("a change made on my behalf: %s", got)
	}
	f.ops[0].Spec.OnBehalfOf = nil
	f.caller = person("team-ledger")
	if got := actions("held"); got != "cancel" {
		t.Fatalf("not my team: %s", got)
	}
	f.callerErr = errors.New("who")
	if code, _ := call(t, http.MethodGet, base+"/api/delivery/operations/prod/held", nil); code != http.StatusInternalServerError {
		t.Fatal("whoami error")
	}
	f.callerErr, f.explainErr = nil, errors.New("x")
	if code, _ := call(t, http.MethodGet, base+"/api/delivery/operations/prod/held", nil); code != http.StatusInternalServerError {
		t.Fatal("explain error")
	}
	f.explainErr, f.getErr = nil, errors.New("y")
	if code, _ := call(t, http.MethodGet, base+"/api/delivery/operations/prod/held", nil); code != http.StatusInternalServerError {
		t.Fatal("get error")
	}
}

func TestPlanProposeCancel(t *testing.T) {
	a := &authed{state: AuthState{SignedIn: true}}
	f := &fakeDelivery{caller: person()}
	base := startDelivery(t, f, a.auth(false, true), true, nil)
	if code, p := call(t, http.MethodPost, base+"/api/delivery/plan", delivery.Request{Namespace: "prod", Stage: "payments", To: dNext}); code != http.StatusOK || p["to"] != dNext {
		t.Fatalf("plan %d %v", code, p)
	}
	code, out := call(t, http.MethodPost, base+"/api/delivery/operations", delivery.Request{Namespace: "prod", Stage: "payments", To: dNext, IntentDigest: "sha256:i"})
	if code != http.StatusCreated || out["operation"].(map[string]any)["metadata"].(map[string]any)["name"] != "op-1" {
		t.Fatalf("propose %d %v", code, out)
	}
	code, out = call(t, http.MethodPost, base+"/api/delivery/operations", delivery.Request{IntentDigest: "stale"})
	if code != http.StatusConflict || out["code"] != "PlanStale" || out["plan"].(map[string]any)["intentDigest"] != "sha256:fresh" {
		t.Fatalf("plan stale carries the fresh plan: %d %v", code, out)
	}
	if code, out = call(t, http.MethodPost, base+"/api/delivery/operations", delivery.Request{Type: delivery.TypeOverride}); code != http.StatusBadRequest || out["code"] != "OverrideFromCLI" {
		t.Fatalf("override stays in the CLI: %d %v", code, out)
	}
	if code, out = call(t, http.MethodDelete, base+"/api/delivery/operations/prod/op-1", nil); code != http.StatusOK || out["cancelled"] != "prod/op-1" {
		t.Fatalf("cancel %d %v", code, out)
	}
	f.actErr = Refuse(http.StatusForbidden, "Forbidden", "the pointer already moved")
	for _, c := range []struct{ method, path string }{{http.MethodPost, "plan"}, {http.MethodPost, "operations"}, {http.MethodDelete, "operations/prod/op-1"}} {
		if code, out = call(t, c.method, base+"/api/delivery/"+c.path, delivery.Request{}); code != http.StatusForbidden || out["detail"] != "the pointer already moved" {
			t.Errorf("%s %s: the apiserver's refusal is verbatim: %d %v", c.method, c.path, code, out)
		}
	}
}

func TestDecide(t *testing.T) {
	a := &authed{state: AuthState{SignedIn: true, Fresh: true}}
	f := &fakeDelivery{caller: person("acme:team-checkout"), ops: []delivery.OperationObject{operation("prod", "held", "payments", held("team-checkout"))}}
	decide := func(base string) (int, map[string]any) {
		return call(t, http.MethodPost, base+"/api/delivery/operations/prod/held/decision", map[string]string{"decision": "approve", "note": "ok"})
	}
	base := startDelivery(t, f, a.auth(false, true), true, nil)
	if code, out := decide(base); code != http.StatusCreated || out["approval"] != "held-x" || f.approved[0] != "prod/held=approve" {
		t.Fatalf("a fresh member of the named team decides: %d %v", code, out)
	}
	// accept-unknown takes the same path to an Approval as approve and reject.
	if code, out := call(t, http.MethodPost, base+"/api/delivery/operations/prod/held/decision", map[string]string{"decision": "accept-unknown"}); code != http.StatusCreated ||
		f.approved[1] != "prod/held=accept-unknown" {
		t.Fatalf("accept-unknown reaches the Approval as the person's decision: %d %v %v", code, out, f.approved)
	}
	cases := []struct {
		name  string
		setup func() string
		code  int
		want  string
	}{
		{"local", func() string { return startDelivery(t, f, a.auth(true, false), true, nil) }, http.StatusForbidden, "ApproveFromCLI"},
		{"no auth", func() string {
			return startServer(t, NewServer(Options{UI: testUI(), Delivery: func(*http.Request) (DeliveryAPI, error) { return f, nil }}))
		}, http.StatusForbidden, "ApproveFromCLI"},
		{"shared without login", func() string { return startDelivery(t, f, a.auth(false, false), true, nil) }, http.StatusForbidden, "LoginNotConfigured"},
		{"signed out", func() string { a.state = AuthState{}; return base }, http.StatusUnauthorized, "LoginRequired"},
		{"not fresh", func() string { a.state = AuthState{SignedIn: true}; return base }, http.StatusUnauthorized, "FreshLoginRequired"},
		{"agent", func() string {
			a.state = AuthState{SignedIn: true, Fresh: true}
			f.caller = person("acme:team-checkout", "pacto:agents")
			return base
		}, http.StatusForbidden, "AgentsMayNotApprove"},
		{"no auth-time", func() string { f.caller = person("team-checkout"); f.caller.AuthTime = ""; return base }, http.StatusForbidden, "AuthTimeUnavailable"},
		{"stale auth-time", func() string {
			f.caller.AuthTime = strconv.FormatInt(time.Now().Add(-11*time.Minute).Unix(), 10)
			return base
		}, http.StatusUnauthorized, "FreshLoginRequired"},
		{"not my team", func() string { f.caller = person("team-payments"); return base }, http.StatusForbidden, "NotYourTeam"},
		{"my own change", func() string {
			f.caller = person("team-checkout")
			f.ops[0].Spec.Actor.User = f.caller.User
			return base
		}, http.StatusForbidden, "OwnChange"},
		{"on my behalf", func() string {
			f.ops[0].Spec.Actor.User, f.ops[0].Spec.OnBehalfOf = "bot", &delivery.Principal{User: f.caller.User}
			return base
		}, http.StatusForbidden, "OwnChange"},
	}
	for _, tc := range cases {
		code, out := decide(tc.setup())
		if code != tc.code || out["code"] != tc.want {
			t.Errorf("%s: %d %v, want %d %s", tc.name, code, out, tc.code, tc.want)
		}
	}
	f.ops[0].Spec.OnBehalfOf = nil
	f.caller = person("team-checkout")
	f.getErr = errors.New("gone")
	if code, _ := decide(base); code != http.StatusInternalServerError {
		t.Fatal("a failed read of the operation fails")
	}
	f.getErr, f.callerErr = nil, errors.New("who")
	if code, _ := decide(base); code != http.StatusInternalServerError {
		t.Fatal("a failed WhoAmI fails")
	}
	f.callerErr, f.actErr = nil, Refuse(http.StatusForbidden, "Forbidden", "approving needs a fresh interactive login")
	if code, out := decide(base); code != http.StatusForbidden || out["detail"] != "approving needs a fresh interactive login" {
		t.Fatalf("the VAP's refusal is verbatim: %d %v", code, out)
	}
	base = startDelivery(t, f, a.auth(false, true), false, nil)
	if code, _ := decide(base); code != http.StatusNotFound {
		t.Fatal("not installed")
	}
}

func TestDeliveryErrorAndRequestContext(t *testing.T) {
	e := Refuse(http.StatusForbidden, "X", "y")
	if e.Error() != "X: y" || e.GetStatus() != http.StatusForbidden || requestFrom(context.Background()) != nil {
		t.Fatal("DeliveryError")
	}
	srv := &Server{}
	if srv.authState(context.Background()) != (AuthState{}) {
		t.Fatal("no auth, no state")
	}
}

func TestCompareVersions(t *testing.T) {
	vs := []string{"", "1.9.0", "not-a-version", "1.10.0", "1.10.0-rc.1"}
	slices.SortFunc(vs, func(a, b string) int { return compareVersions(b, a) })
	if got := strings.Join(vs, ","); got != "1.10.0,1.10.0-rc.1,1.9.0,not-a-version," {
		t.Fatalf("newest first, as versions: %s", got)
	}
}
