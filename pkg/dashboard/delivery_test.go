package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/logging"
)

const (
	dRun  = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	dNext = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

// fakeDelivery is a cluster as one caller sees it.
type fakeDelivery struct {
	// policy is the published policy; nil is the acme one most tests use.
	policy    *delivery.ClassPolicy
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
	// history replaces the default answer; known adds digest versions.
	history     *delivery.History
	known       map[string]string
	stageErr    error
	blame       *delivery.Blame
	blameErr    error
	undo        *delivery.Undo
	undoErr     error
	refusals    *PublishRefusals
	refusalsErr error
}

func (f *fakeDelivery) Stage(_ context.Context, ns, name string) (*delivery.StageObject, error) {
	for _, s := range f.stages {
		if s.Metadata.Namespace == ns && s.Metadata.Name == name {
			return &s, f.stageErr
		}
	}
	return nil, Refuse(http.StatusNotFound, "NotFound", name+" not found")
}

func (f *fakeDelivery) Blame(context.Context, string, string, string) (*delivery.Blame, error) {
	return f.blame, f.blameErr
}

func (f *fakeDelivery) Undo(context.Context, string, string, string) (*delivery.Undo, error) {
	return f.undo, f.undoErr
}

func (f *fakeDelivery) PublishRefusals(context.Context, string) (*PublishRefusals, error) {
	return f.refusals, f.refusalsErr
}

// WhoAmI answers with the published policy the apiserver-backed client reads.
func (f *fakeDelivery) WhoAmI(context.Context) (delivery.Caller, error) {
	c := f.caller
	c.Policy = delivery.ClassPolicy{AgentGroups: []string{"pacto:agents"}, TeamGroupPrefix: "acme:"}
	if f.policy != nil {
		c.Policy = *f.policy
	}
	return c, f.callerErr
}

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
	if f.history != nil {
		return f.history, f.historyErr
	}
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
	if v, ok := f.known[digest]; ok {
		return delivery.Revision{Digest: digest, Bundle: &contract.Bundle{Contract: &contract.Contract{Service: contract.Service{Version: v}}}}
	}
	if digest == "sha256:unreadable" {
		return delivery.Revision{Digest: digest, Err: "registry down"}
	}
	// What the real Describe returns for an artifact pacto publish did not make.
	return delivery.Revision{Digest: digest, Err: "the artifact has no digest-pinned annotation", NoContract: true}
}

// A version the dashboard cannot name says why in its log: an artifact it
// could not read, or a Stage it could not read. An artifact read that names
// no contract is not a failure and logs nothing.
func TestVersionsLogWhyTheyAreUnknown(t *testing.T) {
	var buf bytes.Buffer
	ctx := logging.WithLogger(context.Background(), slog.New(slog.NewTextHandler(&buf, nil)))
	st := stage("prod", "payments", "payments", delivery.StageStatus{})
	f := &fakeDelivery{stages: []delivery.StageObject{st}}
	if v := versions(ctx, f, &st, nil, dRun, "sha256:unreadable", dNext); len(v) != 1 || v[dRun] != "1.0.0" {
		t.Fatalf("versions = %v", v)
	}
	if got := buf.String(); !strings.Contains(got, "digest=sha256:unreadable") || !strings.Contains(got, `error="registry down"`) ||
		strings.Contains(got, dNext) || strings.Contains(got, dRun) {
		t.Fatalf("log = %q", got)
	}
	buf.Reset()
	if v := stageVersions(ctx, f, "prod", "payments", dRun); v[dRun] != "1.0.0" || buf.Len() != 0 {
		t.Fatalf("versions = %v, log = %q", v, buf.String())
	}
	f.stageErr = errors.New("forbidden")
	if v := stageVersions(ctx, f, "prod", "payments", dRun); v != nil || !strings.Contains(buf.String(), "stage=payments error=forbidden") {
		t.Fatalf("versions = %v, log = %q", v, buf.String())
	}
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
		Plan: &delivery.Plan{Floor: delivery.Floor{Human: true, Decision: delivery.Approve, Owners: owners, Consumers: []string{"checkout"}},
			Order: &delivery.Order{MoveFirst: []delivery.MoveFirst{{Service: "checkout", Message: "Ship checkout first."}}}}}
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
	f := &fakeDelivery{caller: person("acme:team-payments")}
	base := startDelivery(t, f, a.auth(false, true), true, nil)
	_, caps := call(t, http.MethodGet, base+"/api/capabilities", nil)
	auth, _ := caps["auth"].(map[string]any)
	expect(t, caps["delivery"] == true && caps["miraURL"] == "http://mira", "capabilities %v", caps)
	expect(t, auth["mode"] == "shared" && auth["login"] == true && auth["signedIn"] == true, "capabilities %v", caps)
	_, sess := call(t, http.MethodGet, base+"/api/session", nil)
	caller, _ := sess["caller"].(map[string]any)
	expect(t, sess["mode"] == "shared" && sess["fresh"] == true, "session %v", sess)
	expect(t, sess["csrf"] == "c" && caller["user"] == "https://idp#carol", "session %v", sess)
	expect(t, sess["requireOwnerTeam"] == nil, "an ownerless question is anyone's without the rule: %v", sess)
	f.policy = &delivery.ClassPolicy{RequireOwnerTeam: true}
	_, sess = call(t, http.MethodGet, base+"/api/session", nil)
	expect(t, sess["requireOwnerTeam"] == true, "the session says when an ownerless question is nobody's: %v", sess)
	f.policy = nil
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
	// Where delivery is never served, capabilities do not ask the cluster.
	asked := false
	open := startServer(t, NewServer(Options{UI: testUI(), Auth: a.auth(false, false),
		DeliveryInstalled: func(context.Context) (bool, error) { asked = true; return false, errors.New("slow") },
		Delivery:          func(*http.Request) (DeliveryAPI, error) { return f, nil }}))
	_, caps = call(t, http.MethodGet, open+"/api/capabilities", nil)
	expect(t, caps["delivery"] == false && !asked, "an open dashboard does not ask the cluster: %v %v", caps, asked)
	base = startDelivery(t, f, a.auth(false, true), true, Refuse(http.StatusUnauthorized, "LoginRequired", "sign in"))
	for _, p := range []string{"inbox", "environments", "published", "history", "operations/prod/op", "refusals",
		"blame?namespace=prod&stage=s", "undo?namespace=prod&name=op"} {
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
				Plan: &delivery.Plan{Floor: delivery.Floor{Decision: delivery.Approve}, Order: held().Plan.Order}}),
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
		strings.Join(toStrings(first["owners"]), ",") == "team-checkout" &&
		first["order"].(map[string]any)["moveFirst"].([]any)[0].(map[string]any)["service"] == "checkout",
		"a held item carries its question, the services affected, the teams that may answer and what moves first: %v", first)
	_, hasOrder := inbox["held"].([]any)[1].(map[string]any)["order"]
	expect(t, !hasOrder, "a change that already shipped no longer says what moves first: %v", inbox["held"])
	fr := inbox["frozen"].([]any)
	expect(t, len(fr) == 1 && fr[0].(map[string]any)["service"] == "orphan",
		"frozen Stages of other teams' services are not mine, unowned ones are: %v", fr)
	errored := inbox["errored"].([]any)
	expect(t, len(errored) == 1 && errored[0].(map[string]any)["name"] == "broke-too",
		"errored: only on my (or unowned) Stages that are still frozen: %v", errored)
	f.caller = person("acme:team-payments")
	_, inbox = call(t, http.MethodGet, base+"/api/delivery/inbox", nil)
	fr = inbox["frozen"].([]any)
	expect(t, len(fr) == 2 && fr[0].(map[string]any)["service"] == "payments", "the frozen Stage of my service: %v", fr)
	// My own held change waits on someone else, and an agent decides nothing.
	f.caller = person("acme:team-checkout")
	f.ops[0].Spec.Actor.User = f.caller.User
	_, inbox = call(t, http.MethodGet, base+"/api/delivery/inbox", nil)
	expect(t, names(inbox["held"]) == "anyone:accept-unknown", "my own change is not mine to decide: %v", inbox["held"])
	f.caller = delivery.Caller{Identity: delivery.Identity{User: "bot", Groups: []string{"acme:team-checkout", "pacto:agents"}}, Class: delivery.ClassAgent}
	_, inbox = call(t, http.MethodGet, base+"/api/delivery/inbox", nil)
	expect(t, len(inbox["held"].([]any)) == 0, "an agent is asked nothing: %v", inbox["held"])
	// Under requireOwnerTeam an ownerless question is nobody's but
	// break-glass's, so it is in front of break-glass only.
	f.ops[0].Spec.Actor.User = "someone-else"
	f.policy = &delivery.ClassPolicy{TeamGroupPrefix: "acme:", BreakGlassGroups: []string{"pacto:break-glass"}, RequireOwnerTeam: true}
	f.caller = person("acme:team-checkout")
	_, inbox = call(t, http.MethodGet, base+"/api/delivery/inbox", nil)
	expect(t, names(inbox["held"]) == "for-checkout:approve", "a person cannot answer an ownerless question: %v", inbox["held"])
	f.caller = person("pacto:break-glass")
	f.caller.Class = delivery.ClassBreakGlass
	_, inbox = call(t, http.MethodGet, base+"/api/delivery/inbox", nil)
	expect(t, names(inbox["held"]) == "anyone:accept-unknown", "break-glass sees what only it can resolve: %v", inbox["held"])
}

func TestEnvironments(t *testing.T) {
	f := sampleCluster("acme:team-payments")
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
	f := sampleCluster("acme:team-payments")
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
	f := &fakeDelivery{caller: person("acme:team-checkout"),
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
	f.caller = person("acme:team-ledger")
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
		{"no auth-time", func() string { f.caller = person("acme:team-checkout"); f.caller.AuthTime = ""; return base }, http.StatusForbidden, "AuthTimeUnavailable"},
		{"stale auth-time", func() string {
			f.caller.AuthTime = strconv.FormatInt(time.Now().Add(-11*time.Minute).Unix(), 10)
			return base
		}, http.StatusUnauthorized, "FreshLoginRequired"},
		{"not my team", func() string { f.caller = person("acme:team-payments"); return base }, http.StatusForbidden, "NotYourTeam"},
		{"no team declared", func() string {
			f.policy = &delivery.ClassPolicy{RequireOwnerTeam: true}
			f.ops[0].Status = held()
			return base
		}, http.StatusForbidden, "NoOwnerTeam"},
		{"my own change", func() string {
			f.policy, f.ops[0].Status = nil, held("team-checkout")
			f.caller = person("acme:team-checkout")
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
	f.caller = person("acme:team-checkout")
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

func TestDeliveryNamespaceScope(t *testing.T) {
	f := sampleCluster("acme:team-checkout")
	f.policy = &delivery.ClassPolicy{} // no team prefix: acme:team-checkout is no team here
	a := &authed{state: AuthState{SignedIn: true, Fresh: true}}
	base := startServer(t, NewServer(Options{UI: testUI(), Fleet: ownedFleet(t), Auth: a.auth(false, true), DeliveryNamespace: "prod",
		Delivery: func(*http.Request) (DeliveryAPI, error) { return f, nil }}))
	_, envs := call(t, http.MethodGet, base+"/api/delivery/environments", nil)
	rows := envs["environments"].([]any)
	expect(t, envs["completeness"] == "complete" && len(rows) == 1 && rows[0].(map[string]any)["namespace"] == "prod",
		"only the dashboard's namespace, though the caller may list them all: %v", envs)
	_, inbox := call(t, http.MethodGet, base+"/api/delivery/inbox", nil)
	expect(t, len(inbox["frozen"].([]any)) == 0 && len(inbox["errored"].([]any)) == 0 && names(inbox["held"]) == "anyone:accept-unknown",
		"the unowned frozen Stage and its Errored operation in namespace other are out of scope: %v", inbox)
	f.forbid = map[string]bool{"prod": true}
	_, envs = call(t, http.MethodGet, base+"/api/delivery/environments", nil)
	expect(t, envs["completeness"] == "partial" && strings.Join(toStrings(envs["unreadable"]), ",") == "prod", "refused: %v", envs)
	f.forbid = nil
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/delivery/operations/books/broke", nil},
		{http.MethodGet, "/api/delivery/history?namespace=books&stage=ledger", nil},
		{http.MethodGet, "/api/delivery/blame?namespace=books&stage=ledger", nil},
		{http.MethodGet, "/api/delivery/undo?namespace=books&name=broke", nil},
		{http.MethodPost, "/api/delivery/operations/books/broke/decision", map[string]string{"decision": "approve"}},
		{http.MethodDelete, "/api/delivery/operations/books/broke", nil},
		{http.MethodPost, "/api/delivery/plan", delivery.Request{Namespace: "books", Stage: "ledger"}},
		{http.MethodPost, "/api/delivery/operations", delivery.Request{Namespace: "books", Stage: "ledger"}},
	} {
		code, out := call(t, c.method, base+c.path, c.body)
		expect(t, code == http.StatusNotFound && out["code"] == "OutOfScope", "%s %s: %d %v", c.method, c.path, code, out)
	}
	expect(t, len(f.approved)+len(f.cancelled)+len(f.proposed) == 0, "nothing acted outside the scope")
	code, _ := call(t, http.MethodGet, base+"/api/delivery/history?namespace=prod&stage=payments", nil)
	expect(t, code == http.StatusOK, "history in scope: %d", code)
}

func TestPeopleAndVersionLabels(t *testing.T) {
	a := &authed{state: AuthState{SignedIn: true, Name: "Carol Diaz"}}
	f := sampleCluster("acme:team-checkout")
	f.known = map[string]string{dNext: "2.0.0"}
	base := startDelivery(t, f, a.auth(false, true), true, nil)
	_, sess := call(t, http.MethodGet, base+"/api/session", nil)
	expect(t, sess["name"] == "Carol Diaz", "the session says the person's name: %v", sess)

	// The inbox and the environments say versions, not only digests.
	_, inbox := call(t, http.MethodGet, base+"/api/delivery/inbox", nil)
	first := inbox["held"].([]any)[0].(map[string]any)
	expect(t, first["fromVersion"] == "1.0.0" && first["toVersion"] == "2.0.0", "held item versions: %v", first)
	_, envs := call(t, http.MethodGet, base+"/api/delivery/environments", nil)
	pay := envs["environments"].([]any)[2].(map[string]any)
	expect(t, pay["version"] == "1.0.0" && pay["candidateVersion"] == "2.0.0", "candidate version: %v", pay)

	// History names the versions its digests carry and the people who signed in.
	f.history = &delivery.History{Stage: delivery.StageNow{Name: "payments", Pointer: dRun}, Completeness: "complete",
		Entries: []delivery.HistoryEntry{{Operation: "done", From: dRun, To: dNext, Actor: person().User}, {Operation: "bot", Actor: "system:serviceaccount:a:b"}}}
	_, h := call(t, http.MethodGet, base+"/api/delivery/history", nil)
	vs, people := h["versions"].(map[string]any), h["people"].(map[string]any)
	expect(t, vs[dRun] == "1.0.0" && vs[dNext] == "2.0.0" && len(vs) == 2, "history versions: %v", vs)
	expect(t, people[person().User] == "Carol Diaz" && len(people) == 1, "history people: %v", people)
	_, h = call(t, http.MethodGet, base+"/api/delivery/history?namespace=prod&stage=payments", nil)
	expect(t, h["versions"].(map[string]any)[dNext] == "2.0.0", "one Stage's history versions: %v", h)
	// A Stage that cannot be read still has a history, without versions.
	f.stageErr = errors.New("forbidden")
	code, h := call(t, http.MethodGet, base+"/api/delivery/history?namespace=prod&stage=payments", nil)
	expect(t, code == http.StatusOK && h["versions"] == nil && len(h["histories"].([]any)) == 1, "unreadable Stage: %d %v", code, h)
	f.stageErr = nil

	// One change names the versions and people too.
	f.ops[0].Spec.OnBehalfOf = &delivery.Principal{User: "someone"}
	f.ops[0].Status.Approvals = []delivery.ApprovalRecord{{Name: "a1", By: person().User, Decision: delivery.Approve}}
	_, op := call(t, http.MethodGet, base+"/api/delivery/operations/prod/for-checkout", nil)
	expect(t, op["versions"].(map[string]any)[dRun] == "1.0.0" && op["people"].(map[string]any)[person().User] == "Carol Diaz",
		"operation labels: %v %v", op["versions"], op["people"])
	f.stageErr = errors.New("forbidden")
	_, op = call(t, http.MethodGet, base+"/api/delivery/operations/prod/for-checkout", nil)
	expect(t, op["versions"] == nil, "an unreadable Stage leaves the versions out: %v", op["versions"])
	f.stageErr = nil

	// A held change on a Stage the caller cannot see has no versions.
	f.ops = append(f.ops, operation("prod", "elsewhere", "gone", held("team-checkout")))
	_, inbox = call(t, http.MethodGet, base+"/api/delivery/inbox", nil)
	last := inbox["held"].([]any)[len(inbox["held"].([]any))-1].(map[string]any)
	_, has := last["fromVersion"]
	expect(t, last["name"] == "elsewhere" && !has, "no Stage, no versions: %v", last)
}

func TestRememberIsBounded(t *testing.T) {
	s := &Server{}
	s.remember("", "x")
	s.remember("u", "")
	expect(t, s.people == nil, "nothing to remember without a user and a name")
	for i := range maxPeople {
		s.remember(strconv.Itoa(i), "n")
	}
	s.remember("one-more", "Name")
	expect(t, len(s.people) == 1 && s.named("one-more", "0")["one-more"] == "Name", "a full directory starts over: %d", len(s.people))
}

func TestBlame(t *testing.T) {
	a := &authed{state: AuthState{SignedIn: true, Name: "Carol Diaz"}}
	f := sampleCluster("acme:team-checkout")
	f.known = map[string]string{dNext: "2.0.0"}
	f.blame = &delivery.Blame{Stage: delivery.StageNow{Name: "payments", Pointer: dNext}, Digest: dNext, Completeness: "complete",
		Source: "records", OnBehalfOf: "someone",
		Change:    &delivery.HistoryEntry{Operation: "done", From: dRun, To: dNext, Actor: "system:serviceaccount:agents:pay-agent"},
		Approvals: []delivery.RecordApproval{{By: person().User, Decision: delivery.Approve}}}
	base := startDelivery(t, f, a.auth(false, true), true, nil)
	call(t, http.MethodGet, base+"/api/session", nil) // carol signs in
	code, b := call(t, http.MethodGet, base+"/api/delivery/blame?namespace=prod&stage=payments", nil)
	expect(t, code == http.StatusOK && b["change"].(map[string]any)["operation"] == "done" && b["completeness"] == "complete", "blame %d %v", code, b)
	vs := b["versions"].(map[string]any)
	expect(t, vs[dRun] == "1.0.0" && vs[dNext] == "2.0.0", "blame versions: %v", vs)
	expect(t, b["people"].(map[string]any)[person().User] == "Carol Diaz", "blame people: %v", b["people"])
	// Nothing moved it there: no change, no versions of it.
	f.blame = &delivery.Blame{Digest: "", Completeness: "partial", Note: "the Stage runs nothing yet"}
	f.stageErr = errors.New("forbidden")
	_, b = call(t, http.MethodGet, base+"/api/delivery/blame?namespace=prod&stage=payments", nil)
	expect(t, b["change"] == nil && b["versions"] == nil && b["note"] == "the Stage runs nothing yet", "no change: %v", b)
	f.blameErr = Refuse(http.StatusForbidden, "Forbidden", "no")
	code, b = call(t, http.MethodGet, base+"/api/delivery/blame?namespace=prod&stage=payments", nil)
	expect(t, code == http.StatusForbidden && b["detail"] == "no", "blame refused: %d %v", code, b)
	code, _ = call(t, http.MethodGet, base+"/api/delivery/blame?namespace=prod", nil)
	expect(t, code == http.StatusUnprocessableEntity, "blame needs a Stage: %d", code)
}

func TestUndo(t *testing.T) {
	a := &authed{state: AuthState{SignedIn: true}}
	f := sampleCluster("acme:team-checkout")
	f.undo = &delivery.Undo{Operation: "prod/done", Kind: "promote", Note: "the Stage changed since",
		Plan: &delivery.Plan{IntentDigest: "sha256:i", From: dNext, To: dRun}, Command: "pacto delivery propose"}
	base := startDelivery(t, f, a.auth(false, true), true, nil)
	code, u := call(t, http.MethodGet, base+"/api/delivery/undo?namespace=prod&name=done&stage=payments", nil)
	expect(t, code == http.StatusOK && u["kind"] == "promote" && u["plan"].(map[string]any)["to"] == dRun &&
		u["versions"].(map[string]any)[dRun] == "1.0.0", "undo %d %v", code, u)
	// Without its Stage, or with one that cannot be read, no versions.
	_, u = call(t, http.MethodGet, base+"/api/delivery/undo?namespace=prod&name=done", nil)
	expect(t, u["versions"] == nil, "no Stage named: %v", u)
	f.stageErr = errors.New("forbidden")
	_, u = call(t, http.MethodGet, base+"/api/delivery/undo?namespace=prod&name=done&stage=payments", nil)
	expect(t, u["versions"] == nil, "unreadable Stage: %v", u)
	f.undo = &delivery.Undo{Operation: "prod/done", Kind: "none", Note: "it moved nothing"}
	_, u = call(t, http.MethodGet, base+"/api/delivery/undo?namespace=prod&name=done&stage=payments", nil)
	expect(t, u["kind"] == "none" && u["plan"] == nil, "nothing to undo: %v", u)
	f.undoErr = errors.New("gone")
	code, _ = call(t, http.MethodGet, base+"/api/delivery/undo?namespace=prod&name=done", nil)
	expect(t, code == http.StatusInternalServerError, "undo error: %d", code)
}

func TestRefusals(t *testing.T) {
	a := &authed{state: AuthState{SignedIn: true}}
	f := sampleCluster("acme:team-payments")
	base := startDelivery(t, f, a.auth(false, true), true, nil)
	_, r := call(t, http.MethodGet, base+"/api/delivery/refusals", nil)
	expect(t, r["recorded"] == false && r["completeness"] == "partial" && len(r["refusals"].([]any)) == 0 &&
		strings.Contains(r["note"].(string), "keeps no record"), "no source: %v", r)
	f.refusals = &PublishRefusals{Completeness: "complete"}
	_, r = call(t, http.MethodGet, base+"/api/delivery/refusals", nil)
	expect(t, r["recorded"] == true && r["refusals"] != nil && len(r["refusals"].([]any)) == 0, "recorded, none: %v", r)
	f.refusals = &PublishRefusals{Completeness: "complete"}
	for i := range recentRefusals + 5 {
		f.refusals.Refusals = append(f.refusals.Refusals, PublishRefusal{Service: "payments", Version: fmt.Sprintf("2.4.%d", i),
			Check: "version", RequiredBump: "major", At: fmt.Sprintf("2026-10-22T10:%02d:00Z", i)})
	}
	_, r = call(t, http.MethodGet, base+"/api/delivery/refusals", nil)
	list := r["refusals"].([]any)
	expect(t, len(list) == recentRefusals && list[0].(map[string]any)["version"] == "2.4.24" &&
		list[0].(map[string]any)["requiredBump"] == "major", "newest first, bounded: %d %v", len(list), list[0])
	// Times, not strings: an offset, a fraction and an unreadable value.
	f.refusals = &PublishRefusals{Completeness: "complete", Refusals: []PublishRefusal{
		{Version: "bad", At: "yesterday"}, {Version: "zulu", At: "2026-10-22T10:30:00Z"},
		{Version: "offset", At: "2026-10-22T12:00:00+02:00"}, {Version: "fraction", At: "2026-10-22T10:30:00.5Z"}}}
	_, r = call(t, http.MethodGet, base+"/api/delivery/refusals", nil)
	var order []string
	for _, x := range r["refusals"].([]any) {
		order = append(order, x.(map[string]any)["version"].(string))
	}
	expect(t, slices.Equal(order, []string{"fraction", "zulu", "offset", "bad"}), "time order: %v", order)
	f.refusalsErr = Refuse(http.StatusForbidden, "Forbidden", "no")
	code, _ := call(t, http.MethodGet, base+"/api/delivery/refusals", nil)
	expect(t, code == http.StatusForbidden, "refused: %d", code)
}
