package dashboard

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
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
	policy *delivery.ClassPolicy
	caller delivery.Caller
	// owner is the team the contract of dRun declares; contract replaces
	// the contract reference its artifact names.
	owner     string
	contract  string
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
	planned    []delivery.Request
	// history replaces the default answer; known adds digest versions.
	history     *delivery.History
	known       map[string]string
	stageErr    error
	blame       *delivery.Blame
	blameErr    error
	undo        *delivery.Undo
	undoErr     error
	record      *delivery.RecordView
	recordErr   error
	refusals    *PublishRefusals
	refusalsErr error
	// deny refuses the caller's access reviews "verb resource namespace"
	// ("verb resource" for every namespace); canErr fails every review.
	deny   map[string]bool
	canErr error
	cans   atomic.Int32
	// stageNS answers StageNamespaces; nsErr fails it.
	stageNS []string
	nsErr   error
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

func (f *fakeDelivery) Record(context.Context, string, string, string) (*delivery.RecordView, error) {
	return f.record, f.recordErr
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

func (f *fakeDelivery) History(_ context.Context, ns, stage string, _ *delivery.StageObject) (*delivery.History, error) {
	if f.history != nil {
		return f.history, f.historyErr
	}
	return &delivery.History{Stage: delivery.StageNow{Name: ns + "/" + stage}, Completeness: "partial"}, f.historyErr
}

// fullPlan sets every delivery.Plan field, so a test can tell one the
// answer drops.
func fullPlan(to string) delivery.Plan {
	return delivery.Plan{IntentDigest: "sha256:i", Type: delivery.TypePromote, From: dRun, To: to, ToVersion: "2.0.0",
		Verdicts: []delivery.Verdict{{Gate: delivery.GateDiff, Result: delivery.Pass}}, Floor: delivery.Floor{Human: true},
		Question: "Approve?", Conflicts: []string{"StageFrozen: hold"}, Predicted: delivery.PredictHeld, ImpactDigest: "sha256:m",
		Order: &delivery.Order{}, Precedent: &delivery.Precedent{}}
}

func (f *fakeDelivery) Plan(_ context.Context, req delivery.Request) (delivery.PlanAnswer, error) {
	f.planned = append(f.planned, req)
	return delivery.PlanAnswer{Plan: fullPlan(req.To), Next: []string{"a person approves it"}}, f.actErr
}

func (f *fakeDelivery) Propose(_ context.Context, req delivery.Request) (*delivery.OperationObject, delivery.PlanAnswer, error) {
	if req.IntentDigest == "stale" {
		p := delivery.PlanAnswer{Plan: delivery.Plan{IntentDigest: "sha256:fresh"}, Next: []string{"fresh next"}}
		return nil, p, &DeliveryError{Status: http.StatusConflict, Code: "PlanStale", Detail: "moved", Plan: &p}
	}
	f.proposed = append(f.proposed, req)
	return &delivery.OperationObject{Metadata: delivery.ObjectMeta{Name: "op-1"}},
		delivery.PlanAnswer{Plan: delivery.Plan{IntentDigest: "sha256:i"}, Next: []string{"proposed next"}}, f.actErr
}

func (f *fakeDelivery) Approve(_ context.Context, ns, name string, d delivery.Decision, _ string) (string, error) {
	f.approved = append(f.approved, ns+"/"+name+"="+string(d))
	return name + "-x", f.actErr
}

func (f *fakeDelivery) Can(_ context.Context, verb, resource, ns string) (bool, error) {
	f.cans.Add(1)
	what := verb + " " + resource
	return !f.deny[what] && !f.deny[what+" "+ns], f.canErr
}

func (f *fakeDelivery) StageNamespaces(context.Context) ([]string, error) { return f.stageNS, f.nsErr }

func (f *fakeDelivery) Cancel(_ context.Context, ns, name string) error {
	f.cancelled = append(f.cancelled, ns+"/"+name)
	return f.actErr
}

func (f *fakeDelivery) Describe(_ context.Context, _ *delivery.StageObject, digest string) delivery.Revision {
	if digest == dRun {
		return delivery.Revision{Digest: digest, ContractRef: cmp.Or(f.contract, "oci://r/payments@sha256:c"),
			Bundle: &contract.Bundle{Contract: &contract.Contract{Service: contract.Service{Name: "payments", Version: "1.0.0", Owner: contract.Owner{Team: f.owner}}}}}
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
	// "autonomous" is an autonomy change's level name: not a digest, so it is
	// never described even though this fake would answer for it.
	f := &fakeDelivery{stages: []delivery.StageObject{st}, known: map[string]string{"autonomous": "9.9.9"}}
	srv := NewServer(Options{})
	if v := srv.stageLabels(ctx, f, "prod", "payments", dRun, "sha256:unreadable", dNext, "autonomous").Versions; len(v) != 1 || v[dRun] != "1.0.0" {
		t.Fatalf("versions = %v", v)
	}
	if got := buf.String(); !strings.Contains(got, "digest=sha256:unreadable") || !strings.Contains(got, `error="registry down"`) ||
		strings.Contains(got, dNext) || strings.Contains(got, dRun) {
		t.Fatalf("log = %q", got)
	}
	buf.Reset()
	if v := srv.stageLabels(ctx, f, "prod", "payments", dRun).Versions; v[dRun] != "1.0.0" || buf.Len() != 0 {
		t.Fatalf("versions = %v, log = %q", v, buf.String())
	}
	f.stageErr = errors.New("forbidden")
	if l := srv.stageLabels(ctx, f, "prod", "payments", dRun); l.Versions != nil || l.Revisions != nil ||
		!strings.Contains(buf.String(), "stage=payments error=forbidden") {
		t.Fatalf("labels = %v, log = %q", l, buf.String())
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
	return startDeliveryWith(t, f, Options{Fleet: ownedFleet(t), Auth: auth}, installed, factoryErr)
}

// startDeliveryWith serves f with o's fleet, view and auth.
func startDeliveryWith(t *testing.T, f *fakeDelivery, o Options, installed bool, factoryErr error) string {
	t.Helper()
	o.UI, o.MiraURL = testUI(), "http://mira"
	o.DeliveryInstalled = func(context.Context) (bool, error) { return installed, nil }
	o.Delivery = func(r *http.Request) (DeliveryAPI, error) {
		if r == nil {
			return nil, errors.New("no request on the context")
		}
		return f, factoryErr
	}
	return startServer(t, NewServer(o))
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
	code, body = call(t, http.MethodGet, down+"/api/delivery/caller", nil)
	expect(t, code == http.StatusServiceUnavailable && body["code"] == "ClusterUnreachable", "the caller names the failure: %d %v", code, body)
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
			stage("prod", "payments", "payments", delivery.StageStatus{Level: delivery.LevelAdvise, Pointer: dRun, Verified: []string{dRun}, Published: []string{dRun, dNext},
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
	// Promote creates an Operation: RBAC that refuses that offers nothing.
	f.deny = map[string]bool{"create operations " + pay["namespace"].(string): true}
	_, envs = call(t, http.MethodGet, base+"/api/delivery/environments", nil)
	got = strings.Join(toStrings(envs["environments"].([]any)[2].(map[string]any)["actions"]), ",")
	expect(t, got == "", "read-only actions %v", got)
	f.deny = nil
	// A person outside the team the running contract names is never offered
	// an unfreeze the promoter would refuse (NotYourTeam); the team is, and
	// so is break-glass. With no team named, RequireOwnerTeam offers nobody.
	f.owner = "team-payments"
	for groups, want := range map[string]string{"acme:team-checkout": "promote", "acme:team-payments": "promote,unfreeze"} {
		f.caller = person(groups)
		_, envs = call(t, http.MethodGet, base+"/api/delivery/environments", nil)
		got = strings.Join(toStrings(envs["environments"].([]any)[2].(map[string]any)["actions"]), ",")
		expect(t, got == want, "%s actions %v", groups, got)
	}
	f.caller = person("pacto:break-glass")
	f.caller.Class = delivery.ClassBreakGlass
	_, envs = call(t, http.MethodGet, base+"/api/delivery/environments", nil)
	got = strings.Join(toStrings(envs["environments"].([]any)[2].(map[string]any)["actions"]), ",")
	expect(t, strings.Contains(got, "unfreeze"), "break-glass actions %v", got)
	f.owner, f.policy = "", &delivery.ClassPolicy{TeamGroupPrefix: "acme:", RequireOwnerTeam: true}
	f.caller = person("acme:team-payments")
	_, envs = call(t, http.MethodGet, base+"/api/delivery/environments", nil)
	got = strings.Join(toStrings(envs["environments"].([]any)[2].(map[string]any)["actions"]), ",")
	expect(t, got == "promote", "ownerless under requireOwnerTeam %v", got)
	// A caller whose RBAC refuses creating Operations there is offered nothing.
	f.deny = map[string]bool{"create operations": true}
	_, envs = call(t, http.MethodGet, base+"/api/delivery/environments", nil)
	got = strings.Join(toStrings(envs["environments"].([]any)[2].(map[string]any)["actions"]), ",")
	expect(t, got == "", "no RBAC to create Operations %v", got)
}

func TestPublished(t *testing.T) {
	f := sampleCluster("acme:team-payments")
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	_, pub := call(t, http.MethodGet, base+"/api/delivery/published", nil)
	payments := pub["services"].([]any)[2].(map[string]any)
	versions := payments["versions"].([]any)
	first := versions[0].(map[string]any)
	expect(t, payments["service"] == "payments" && len(versions) == 2, "published %v", payments)
	expect(t, first["version"] == "1.0.0" && first["signature"] == "signed" && first["contract"] == "oci://r/payments@sha256:c", "published %v", first)
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
	// A host that says where the caller may list Stages is asked instead of
	// the snapshot, so a namespace with Stages and no Pacto yet is read, and
	// the answer is complete.
	f.forbidAll, f.listErr, f.stageNS = true, nil, []string{"books"}
	f.forbid = nil
	if _, envs = call(t, http.MethodGet, base+"/api/delivery/environments", nil); envs["completeness"] != "complete" ||
		len(envs["environments"].([]any)) != 1 || envs["environments"].([]any)[0].(map[string]any)["namespace"] != "books" {
		t.Fatalf("the namespaces the caller may list Stages in: %v", envs)
	}
	f.nsErr = errors.New("down")
	if code, _ := call(t, http.MethodGet, base+"/api/delivery/environments", nil); code != http.StatusInternalServerError {
		t.Fatalf("a failed namespace read fails: %d", code)
	}
	f.stageNS, f.nsErr = nil, nil
	f.listErr = nil
	checkListsWithoutSnapshot(t, a, f)
}

// checkListsWithoutSnapshot reads with no fleet snapshot, where the namespaces
// cannot be known, and with one whose per-namespace reads fail.
func checkListsWithoutSnapshot(t *testing.T, a *authed, f *fakeDelivery) {
	t.Helper()
	f.forbidAll = true
	srv := startServer(t, NewServer(Options{UI: testUI(), Auth: a.auth(false, true),
		Delivery: func(*http.Request) (DeliveryAPI, error) { return f, nil }}))
	_, envs := call(t, http.MethodGet, srv+"/api/delivery/environments", nil)
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
	if code, _ := call(t, http.MethodGet, srv2+"/api/delivery/environments", nil); code != http.StatusInternalServerError {
		t.Fatalf("a per-namespace error fails the read: %d", code)
	}
}

// checkHistory reads the history of every Stage and of one, and a failed read.
func checkHistory(t *testing.T, base string, f *fakeDelivery) {
	t.Helper()
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
}

// checkOfferFollowsFreshness: the decisions offered, and the decisionFreshness
// a change carries, follow the login freshness its Stage publishes.
func checkOfferFollowsFreshness(t *testing.T, base string, f *fakeDelivery, actions func(string) string) {
	t.Helper()
	// With no login freshness published, a login the cluster cannot date
	// decides, and the change says no login is too old.
	f.caller.AuthTime = ""
	if got := actions("held"); got != "approve,reject,cancel" {
		t.Fatalf("no login time, no freshness: %s", got)
	}
	_, out := call(t, http.MethodGet, base+"/api/delivery/operations/prod/held", nil)
	expect(t, out["decisionFreshness"] == nil, "no freshness published: %v", out["decisionFreshness"])
	// Where the Stage asks for a fresh login, a cluster that reports no login
	// time counts nobody's decision, so it offers none (the UI says approving
	// is not possible here), and the change says how old a login may be.
	f.stages[0].Status.AuthFreshness = "15m0s"
	if got := actions("held"); got != "cancel" {
		t.Fatalf("no login time on this cluster: %s", got)
	}
	_, out = call(t, http.MethodGet, base+"/api/delivery/operations/prod/held", nil)
	expect(t, out["decisionFreshness"] == float64(600), "two thirds of 15 minutes, in seconds: %v", out["decisionFreshness"])
	f.stages[0].Status.AuthFreshness = ""
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
	checkHistory(t, base, f)
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
	// What the caller's own RBAC refuses is never offered: deciding creates
	// an Approval, cancelling deletes the Operation, a rollback creates one.
	// A review that fails offers nothing.
	f.deny = map[string]bool{"create approvals prod": true, "delete operations": true}
	if got := actions("held"); got != "" {
		t.Fatalf("may neither decide nor cancel: %s", got)
	}
	f.deny = map[string]bool{"create operations prod": true}
	if got := actions("done"); got != "" {
		t.Fatalf("may not create the rollback: %s", got)
	}
	f.deny, f.canErr = nil, errors.New("down")
	if got := actions("held"); got != "" {
		t.Fatalf("a failed review offers nothing: %s", got)
	}
	f.canErr = nil
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
	// A pasted token is offered no decision, since the decision would be refused.
	a.state = AuthState{SignedIn: true, Method: "token"}
	if got := actions("held"); got != "cancel" {
		t.Fatalf("a token session: %s", got)
	}
	a.state = AuthState{SignedIn: true}
	checkOfferFollowsFreshness(t, base, f, actions)
	f.caller = person("acme:team-ledger")
	if got := actions("held"); got != "cancel" {
		t.Fatalf("not my team: %s", got)
	}
	// RBAC narrows the offer: a read-only caller is offered nothing, and an
	// access review that fails offers nothing either.
	f.caller = person("acme:team-checkout")
	f.deny = map[string]bool{"create approvals prod": true, "delete operations prod": true, "create operations prod": true}
	if got := actions("held") + "|" + actions("done"); got != "|" {
		t.Fatalf("read-only caller: %s", got)
	}
	f.canErr = errors.New("review down")
	if got := actions("held"); got != "" {
		t.Fatalf("a failed access review: %s", got)
	}
	f.deny, f.canErr = nil, nil
	// An agent cancels only what it proposed itself.
	f.caller = delivery.Caller{Identity: delivery.Identity{User: "system:serviceaccount:a:bot"}, Class: delivery.ClassAgent}
	if got := actions("held"); got != "" {
		t.Fatalf("an agent on another's change: %s", got)
	}
	f.ops[0].Spec.Actor.User = f.caller.User
	if got := actions("held"); got != "cancel" {
		t.Fatalf("an agent on its own change: %s", got)
	}
	checkOperationErrors(t, base, f)
}

// checkOperationErrors fails an operation read when who the caller is, the
// explanation or the Operation itself cannot be read.
func checkOperationErrors(t *testing.T, base string, f *fakeDelivery) {
	t.Helper()
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

// TestPlanAnswerKeepsPlanFields: /plan answers every delivery.Plan key the
// promoter fills, plus next.
func TestPlanAnswerKeepsPlanFields(t *testing.T) {
	a := &authed{state: AuthState{SignedIn: true}}
	base := startDelivery(t, &fakeDelivery{caller: person()}, a.auth(false, true), true, nil)
	code, p := call(t, http.MethodPost, base+"/api/delivery/plan", delivery.Request{Namespace: "prod", Stage: "payments", To: dNext})
	expect(t, code == http.StatusOK, "plan %d %v", code, p)
	var want map[string]any
	b, _ := json.Marshal(fullPlan(dNext))
	_ = json.Unmarshal(b, &want)
	for k := range want {
		expect(t, p[k] != nil, "plan dropped %q: %v", k, p)
	}
	expect(t, len(p) == len(want)+1 && p["next"].([]any)[0] == "a person approves it", "plan adds only next: %v", p)
}

func TestProposeAnswersNext(t *testing.T) {
	a := &authed{state: AuthState{SignedIn: true}}
	base := startDelivery(t, &fakeDelivery{caller: person()}, a.auth(false, true), true, nil)
	code, out := call(t, http.MethodPost, base+"/api/delivery/operations", delivery.Request{Namespace: "prod", Stage: "payments", To: dNext, IntentDigest: "sha256:i"})
	expect(t, code == http.StatusCreated && out["plan"].(map[string]any)["next"].([]any)[0] == "proposed next", "propose %d %v", code, out)
}

func TestPlanStaleCarriesNext(t *testing.T) {
	a := &authed{state: AuthState{SignedIn: true}}
	base := startDelivery(t, &fakeDelivery{caller: person()}, a.auth(false, true), true, nil)
	code, out := call(t, http.MethodPost, base+"/api/delivery/operations", delivery.Request{IntentDigest: "stale"})
	p, _ := out["plan"].(map[string]any)
	expect(t, code == http.StatusConflict && p["intentDigest"] == "sha256:fresh" && p["next"].([]any)[0] == "fresh next",
		"the fresh plan of a PlanStale refusal says what proposing it leads to: %d %v", code, out)
}

// checkDecideFollowsThePromoter: a decision the promoter would not count is
// refused with its reason, a Stage the caller cannot read refuses it, and the
// sign-in margin follows the login freshness the Stage publishes.
func checkDecideFollowsThePromoter(t *testing.T, base string, f *fakeDelivery, decide func(string) (int, map[string]any)) {
	t.Helper()
	// A Stage the caller cannot read leaves its login freshness unknown: no
	// decision is recorded that the promoter might not count.
	recorded := len(f.approved)
	f.stageErr = Refuse(http.StatusForbidden, "Forbidden", "stages is forbidden")
	if code, out := decide(base); code != http.StatusForbidden || out["detail"] != "stages is forbidden" || len(f.approved) != recorded {
		t.Fatalf("an unreadable Stage refuses the decision: %d %v", code, out)
	}
	f.stageErr = nil
	// accept-unknown on a change that needs an approval is refused with the
	// promoter's own reason: it would never count.
	if code, out := call(t, http.MethodPost, base+"/api/delivery/operations/prod/held/decision", map[string]string{"decision": "accept-unknown"}); code != http.StatusForbidden ||
		out["code"] != "NeedsApproval" || len(f.approved) != 1 {
		t.Fatalf("an accepted unknown where an approval is needed: %d %v %v", code, out, f.approved)
	}
	// accept-unknown takes the same path to an Approval as approve and reject.
	f.ops[0].Status.Plan.Floor.Decision = delivery.AcceptUnknown
	if code, out := call(t, http.MethodPost, base+"/api/delivery/operations/prod/held/decision", map[string]string{"decision": "accept-unknown"}); code != http.StatusCreated ||
		f.approved[1] != "prod/held=accept-unknown" {
		t.Fatalf("accept-unknown reaches the Approval as the person's decision: %d %v %v", code, out, f.approved)
	}
	f.ops[0].Status.Plan.Floor.Decision = delivery.Approve
	// The margin follows the login freshness the Stage publishes: two thirds
	// of 30 minutes keeps an 11-minute-old login fresh enough.
	f.stages = []delivery.StageObject{{Metadata: delivery.ObjectMeta{Namespace: "prod", Name: "payments"}, Status: delivery.StageStatus{AuthFreshness: "30m0s"}}}
	f.caller.AuthTime = strconv.FormatInt(time.Now().Add(-11*time.Minute).Unix(), 10)
	if code, out := decide(base); code != http.StatusCreated {
		t.Fatalf("a login inside the published freshness decides: %d %v", code, out)
	}
	f.stages[0].Status.AuthFreshness, f.caller = "", person("acme:team-checkout")
}

func TestDecide(t *testing.T) {
	a := &authed{state: AuthState{SignedIn: true}}
	f := &fakeDelivery{caller: person("acme:team-checkout"), ops: []delivery.OperationObject{operation("prod", "held", "payments", held("team-checkout"))},
		stages: []delivery.StageObject{{Metadata: delivery.ObjectMeta{Namespace: "prod", Name: "payments"}}}}
	decide := func(base string) (int, map[string]any) {
		return call(t, http.MethodPost, base+"/api/delivery/operations/prod/held/decision", map[string]string{"decision": "approve", "note": "ok"})
	}
	base := startDelivery(t, f, a.auth(false, true), true, nil)
	if code, out := decide(base); code != http.StatusCreated || out["approval"] != "held-x" || f.approved[0] != "prod/held=approve" {
		t.Fatalf("a fresh member of the named team decides: %d %v", code, out)
	}
	checkDecideFollowsThePromoter(t, base, f, decide)
	// With no login freshness published, a login the cluster cannot date decides.
	f.caller.AuthTime = ""
	if code, out := decide(base); code != http.StatusCreated {
		t.Fatalf("no freshness, any signed-in session decides: %d %v", code, out)
	}
	// Every case below is on a Stage that asks for a fresh login.
	f.caller = person("acme:team-checkout")
	f.stages = []delivery.StageObject{{Metadata: delivery.ObjectMeta{Namespace: "prod", Name: "payments"}, Status: delivery.StageStatus{AuthFreshness: "15m0s"}}}
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
		{"a pasted token", func() string { a.state = AuthState{SignedIn: true, Method: "token"}; return base }, http.StatusForbidden, "TokenCannotDecide"},
		{"a pasted token where only tokens sign in", func() string {
			au := a.auth(false, false)
			au.TokenLogin = true
			return startDelivery(t, f, au, true, nil)
		}, http.StatusForbidden, "TokenCannotDecide"},
		{"agent", func() string {
			a.state = AuthState{SignedIn: true}
			f.caller = person("acme:team-checkout", "pacto:agents")
			return base
		}, http.StatusForbidden, "AgentsMayNotApprove"},
		{"no auth-time", func() string { f.caller = person("acme:team-checkout"); f.caller.AuthTime = ""; return base }, http.StatusForbidden, "AuthTimeUnavailable"},
		{"stale auth-time", func() string {
			f.caller.AuthTime = strconv.FormatInt(time.Now().Add(-11*time.Minute).Unix(), 10)
			return base
		}, http.StatusUnauthorized, "FreshLoginRequired"},
		{"a login from the future", func() string {
			f.caller.AuthTime = strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
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
	if !reflect.DeepEqual(srv.authState(context.Background()), AuthState{}) {
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
	a := &authed{state: AuthState{SignedIn: true}}
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
		{http.MethodGet, "/api/delivery/progress?namespace=books&stage=ledger", nil},
		{http.MethodPost, "/api/delivery/operations/books/broke/decision", map[string]string{"decision": "approve"}},
		{http.MethodDelete, "/api/delivery/operations/books/broke", nil},
		{http.MethodPost, "/api/delivery/plan", delivery.Request{Namespace: "books", Stage: "ledger"}},
		{http.MethodPost, "/api/delivery/operations", delivery.Request{Namespace: "books", Stage: "ledger"}},
	} {
		code, out := call(t, c.method, base+c.path, c.body)
		expect(t, code == http.StatusNotFound && out["code"] == "OutOfScope", "%s %s: %d %v", c.method, c.path, code, out)
	}
	expect(t, len(f.approved)+len(f.cancelled)+len(f.proposed) == 0, "nothing acted outside the scope")
	// A change in scope whose Stage promotes from a Stage outside it names
	// that upstream without reading it.
	f.stages[0].Spec.Candidates = delivery.Candidates{UpstreamStage: &delivery.StageRef{Namespace: "books", Name: "ledger"}}
	f.stages[1].Spec.Environment = "staging"
	_, op := call(t, http.MethodGet, base+"/api/delivery/operations/prod/for-checkout", nil)
	up, _ := op["upstream"].(map[string]any)
	expect(t, up["problem"] == "outOfScope" && up["environment"] == nil, "the change view reads no upstream out of scope: %v", op["upstream"])
	code, _ := call(t, http.MethodGet, base+"/api/delivery/history?namespace=prod&stage=payments", nil)
	expect(t, code == http.StatusOK, "history in scope: %d", code)
}

func TestPeopleAndVersionLabels(t *testing.T) {
	a := &authed{state: AuthState{SignedIn: true, Name: "Carol Diaz", User: person().User}}
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
		Entries: []delivery.HistoryEntry{{Operation: "bot", Actor: "system:serviceaccount:a:b"}, {Operation: "done", From: dRun, To: dNext, Actor: person().User}}}
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
	a := &authed{state: AuthState{SignedIn: true, Name: "Carol Diaz", User: person().User}}
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

func TestDeliveryRecord(t *testing.T) {
	a := &authed{state: AuthState{SignedIn: true, Name: "Carol Diaz", User: person().User}}
	f := sampleCluster("acme:team-checkout")
	f.known = map[string]string{dNext: "2.0.0"}
	f.record = &delivery.RecordView{Namespace: "prod", Stage: "payments", Record: "sha256:r", Verified: true, Trust: "sha256:t",
		Entry: delivery.HistoryEntry{Operation: "done", From: dRun, To: dNext, Actor: person().User,
			Approvals: []delivery.RecordApproval{{By: person().User, Decision: delivery.Approve}}},
		Verdicts: []delivery.ExplainedVerdict{{Verdict: delivery.Verdict{Gate: delivery.GateDiff, Result: delivery.Pass}, Rerun: "pacto delivery plan"}}}
	base := startDelivery(t, f, a.auth(false, true), true, nil)
	call(t, http.MethodGet, base+"/api/session", nil) // carol signs in
	code, b := call(t, http.MethodGet, base+"/api/delivery/record?namespace=prod&stage=payments&id=sha256:r", nil)
	expect(t, code == http.StatusOK && b["record"] == "sha256:r" && b["verified"] == true && b["entry"].(map[string]any)["operation"] == "done" &&
		len(b["verdicts"].([]any)) == 1, "record %d %v", code, b)
	vs := b["versions"].(map[string]any)
	expect(t, vs[dRun] == "1.0.0" && vs[dNext] == "2.0.0", "record versions: %v", vs)
	expect(t, b["people"].(map[string]any)[person().User] == "Carol Diaz", "record people: %v", b["people"])
	f.recordErr = Refuse(http.StatusUnprocessableEntity, "Refused", "record sha256:x is not in the stream")
	code, b = call(t, http.MethodGet, base+"/api/delivery/record?namespace=prod&stage=payments&id=sha256:x", nil)
	expect(t, code == http.StatusUnprocessableEntity && b["detail"] == "record sha256:x is not in the stream", "record refused: %d %v", code, b)
	code, _ = call(t, http.MethodGet, base+"/api/delivery/record?namespace=prod&stage=payments", nil)
	expect(t, code == http.StatusUnprocessableEntity, "record needs an id: %d", code)
	// The dashboard cannot reach delivery at all.
	base = startDelivery(t, f, a.auth(false, true), false, nil)
	code, _ = call(t, http.MethodGet, base+"/api/delivery/record?namespace=prod&stage=payments&id=sha256:r", nil)
	expect(t, code == http.StatusNotFound, "not installed: %d", code)
}

// TestDeliveryRecordOutOfScope: a dashboard scoped to one namespace answers
// a record of another as if it were not there, and never reads it.
func TestDeliveryRecordOutOfScope(t *testing.T) {
	f := sampleCluster()
	f.record = &delivery.RecordView{Record: "sha256:r"}
	base := startServer(t, NewServer(Options{UI: testUI(), Fleet: ownedFleet(t), Auth: (&authed{state: AuthState{SignedIn: true}}).auth(false, true),
		DeliveryNamespace: "prod", Delivery: func(*http.Request) (DeliveryAPI, error) { return f, nil }}))
	code, b := call(t, http.MethodGet, base+"/api/delivery/record?namespace=books&stage=ledger&id=sha256:r", nil)
	expect(t, code == http.StatusNotFound && b["code"] == "OutOfScope", "out of scope: %d %v", code, b)
}

func TestUndo(t *testing.T) {
	a := &authed{state: AuthState{SignedIn: true}}
	f := sampleCluster("acme:team-checkout")
	f.undo = &delivery.Undo{Operation: "prod/done", Kind: "promote", Note: "the Stage changed since",
		Plan: &delivery.PlanAnswer{Plan: delivery.Plan{IntentDigest: "sha256:i", From: dNext, To: dRun}}, Command: "pacto delivery propose"}
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

// TestEnvironmentRowJSONKeepsFields: the shared row keeps every key the
// board's row had before it moved to pkg/delivery, upstream included.
func TestEnvironmentRowJSONKeepsFields(t *testing.T) {
	row := EnvironmentRow{StageRow: delivery.StageRow{Namespace: "n", Stage: "s", Service: "svc", Domain: "d", Environment: "e", Pointer: dRun,
		Version: "1", Verified: true, Failed: true, Frozen: true, FrozenReason: "r", AutoHold: true, Held: 1, Candidate: dNext,
		CandidateVersion: "2", Ready: "False: x", ReadyMessage: "m", Actions: []string{}, Level: "advise", State: "failed",
		ContractRef: "oci://r/c@sha256:c", ReadyFix: "f", Watched: &delivery.Watch{Status: "True"}, LastCheck: &delivery.CheckSummary{},
		HeldUnknown: true, Offers: []delivery.Offer{}, Upstream: &delivery.Upstream{}},
		Shadow: []ShadowDecision{{}}, Revision: "svc@1"}
	b, _ := json.Marshal(row)
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	for _, k := range []string{"namespace", "stage", "service", "domain", "environment", "pointer", "version", "verified", "failed", "frozen",
		"frozenReason", "autoHold", "held", "candidate", "candidateVersion", "ready", "readyMessage", "actions", "level", "upstream", "shadow",
		"state", "contractRef", "readyFix", "watched", "lastCheck", "heldUnknown", "offers", "revision"} {
		if _, ok := got[k]; !ok {
			t.Errorf("%s missing from %s", k, b)
		}
	}
}

// TestListingJSONUnchanged: the dashboard's Listing marshals to the shared
// fields only, and still answers read from its unexported scope.
func TestListingJSONUnchanged(t *testing.T) {
	l := Listing{Listing: delivery.Listing{AsOf: "t", Completeness: "partial", Unreadable: []string{"books"}}, listed: []string{"prod"}, scope: "prod"}
	b, _ := json.Marshal(l)
	if string(b) != `{"asOf":"t","completeness":"partial","unreadable":["books"]}` {
		t.Fatalf("listing %s", b)
	}
	if !l.read("prod") || l.read("books") || !(Listing{everywhere: true}).read("x") {
		t.Fatal("read answers from the scope")
	}
}

// TestDescribedKeepsContractWithoutVersion: an artifact that names a
// contract whose bundle could not be read keeps its entry, with why.
func TestDescribedKeepsContractWithoutVersion(t *testing.T) {
	st := stage("prod", "payments", "payments", delivery.StageStatus{})
	f := &fakeDelivery{owner: "team-payments"}
	got := describedAll(context.Background(), f, nil, digestsOn{&st, []string{dRun, "sha256:unreadable", "advise"}})
	if got[dRun] != (delivery.Described{Version: "1.0.0", Team: "team-payments", Contract: "oci://r/payments@sha256:c"}) {
		t.Errorf("dRun %+v", got[dRun])
	}
	if r, ok := got["sha256:unreadable"]; !ok || r.Version != "" || r.Err != "registry down" {
		t.Errorf("unreadable %+v %v", r, ok)
	}
	if _, ok := got["advise"]; ok {
		t.Error("a level name is never described")
	}
	if v := versionsIn(nil, got); len(v) != 1 || v[dRun] != "1.0.0" {
		t.Errorf("versions %v", v)
	}
}

// TestDeliveryRoutesAreReadsExceptExisting: every delivery route that is not
// a GET is one of the four writes that existed before offers: offers only
// describe, and nothing new can change what runs.
func TestDeliveryRoutesAreReadsExceptExisting(t *testing.T) {
	base := startDelivery(t, sampleCluster(), (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	_, doc := call(t, http.MethodGet, base+"/openapi.json", nil)
	var writes []string
	for path, ops := range doc["paths"].(map[string]any) {
		if !strings.HasPrefix(path, "/api/delivery") {
			continue
		}
		for method, op := range ops.(map[string]any) {
			if method != "get" {
				writes = append(writes, op.(map[string]any)["operationId"].(string))
			}
		}
	}
	slices.Sort(writes)
	if strings.Join(writes, ",") != "delivery-cancel,delivery-decide,delivery-plan,delivery-propose" {
		t.Fatalf("delivery writes: %v", writes)
	}
}

const (
	dOld      = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	dContract = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

// offerIn is the offer of action on the row of service, nil when none.
func offerIn(t *testing.T, body map[string]any, service, action string) map[string]any {
	t.Helper()
	for _, r := range body["environments"].([]any) {
		row := r.(map[string]any)
		if row["service"] != service {
			continue
		}
		for _, o := range row["offers"].([]any) {
			if o.(map[string]any)["action"] == action {
				return o.(map[string]any)
			}
		}
	}
	return nil
}

// TestEnvironmentsUndoOnlyWithService: undo and freeze read history and ask
// RBAC, so only a request that names a service is offered them.
func TestEnvironmentsUndoOnlyWithService(t *testing.T) {
	f := sampleCluster("acme:team-payments")
	f.owner = "team-payments"
	f.blame = &delivery.Blame{Change: &delivery.HistoryEntry{Operation: "ship", Type: delivery.TypePromote,
		Pointer: &delivery.PointerChange{Before: dOld, After: dRun}}}
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	_, board := call(t, http.MethodGet, base+"/api/delivery/environments", nil)
	expect(t, offerIn(t, board, "payments", "undo") == nil && offerIn(t, board, "ledger", "freeze") == nil, "board offers %v", board["environments"])
	_, one := call(t, http.MethodGet, base+"/api/delivery/environments?service=payments", nil)
	undo := offerIn(t, one, "payments", "undo")
	expect(t, undo != nil && undo["available"] == true && undo["to"] == dOld && undo["operation"] == "ship", "undo %v", undo)
	f.blameErr = errors.New("history down")
	_, one = call(t, http.MethodGet, base+"/api/delivery/environments?service=payments", nil)
	undo = offerIn(t, one, "payments", "undo")
	expect(t, undo != nil && undo["available"] == false && undo["reason"] == "could not read its history: history down", "undo %v", undo)
	f.blameErr = nil
	f.deny = map[string]bool{"create operations prod": true}
	_, one = call(t, http.MethodGet, base+"/api/delivery/environments?service=payments", nil)
	undo = offerIn(t, one, "payments", "undo")
	expect(t, undo["available"] == false && undo["reason"] == "your Kubernetes RBAC does not let you create Operations in prod", "undo %v", undo)
}

// revisionFleet is a snapshot holding revs, each with a target in its
// service's namespace (prod for payments).
func revisionFleet(t *testing.T, revs ...fleet.RawRevision) func(context.Context) (*fleet.Query, error) {
	t.Helper()
	var targets []fleet.RawTarget
	for _, r := range revs {
		name := r.Bundle.Contract.Service.Name
		targets = append(targets, fleet.RawTarget{Scope: map[string]string{"payments": "prod"}[name] + name, Kind: "kubernetes-workload", Name: name, Service: name})
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("local", "local", &fleet.Collection{Revisions: revs, Targets: targets}))
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(snap)
	return func(context.Context) (*fleet.Query, error) { return q, nil }
}

func contractRev(service, digest string) fleet.RawRevision {
	return fleet.RawRevision{Digest: digest, Bundle: &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.0",
		Service: contract.Service{Name: service, Version: "1.0.0", Owner: contract.Owner{Team: "team-" + service}}}}}
}

// revisionKeyOf is the key of service's one revision in the fleet.
func revisionKeyOf(t *testing.T, fl func(context.Context) (*fleet.Query, error), service string) string {
	t.Helper()
	q, _ := fl(context.Background())
	for k, r := range q.SnapshotForSerialization().Revisions {
		if r.Service == service {
			return string(k)
		}
	}
	t.Fatalf("no revision of %s", service)
	return ""
}

func rowOf(t *testing.T, body map[string]any, service string) map[string]any {
	t.Helper()
	for _, r := range body["environments"].([]any) {
		if r.(map[string]any)["service"] == service {
			return r.(map[string]any)
		}
	}
	t.Fatalf("no row of %s in %v", service, body)
	return nil
}

// TestEnvironmentsRevisionJoin: a row links the fleet revision whose
// contract digest its running artifact names, of its own service only.
func TestEnvironmentsRevisionJoin(t *testing.T) {
	f := sampleCluster("acme:team-payments")
	f.contract = "oci://r/payments@" + dContract
	auth := (&authed{state: AuthState{SignedIn: true}}).auth(false, true)
	fl := revisionFleet(t, contractRev("payments", dContract))
	base := startDeliveryWith(t, f, Options{Fleet: fl, Auth: auth}, true, nil)
	_, envs := call(t, http.MethodGet, base+"/api/delivery/environments", nil)
	row := rowOf(t, envs, "payments")
	expect(t, row["revision"] == revisionKeyOf(t, fl, "payments") && row["contractRef"] == f.contract, "row %v", row)
	base = startDeliveryWith(t, f, Options{Fleet: revisionFleet(t, contractRev("ledger", dContract)), Auth: auth}, true, nil)
	_, envs = call(t, http.MethodGet, base+"/api/delivery/environments", nil)
	expect(t, rowOf(t, envs, "payments")["revision"] == nil, "another service's revision is never linked: %v", rowOf(t, envs, "payments"))
	// The same contract in two domains, and a Stage that names none: either
	// could be meant, so neither is linked.
	east, west := contractRev("payments", dContract), contractRev("payments", dContract)
	east.Domain, west.Domain = "east.io", "west.io"
	base = startDeliveryWith(t, f, Options{Fleet: revisionFleet(t, east, west), Auth: auth}, true, nil)
	_, envs = call(t, http.MethodGet, base+"/api/delivery/environments", nil)
	expect(t, rowOf(t, envs, "payments")["revision"] == nil, "an ambiguous revision is never linked: %v", rowOf(t, envs, "payments"))
}

// TestLabelsRevisions: every answer that names versions also names the fleet
// revision of each digest it can.
func TestLabelsRevisions(t *testing.T) {
	f := sampleCluster("acme:team-payments")
	f.contract = "oci://r/payments@" + dContract
	fl := revisionFleet(t, contractRev("payments", dContract))
	key := revisionKeyOf(t, fl, "payments")
	base := startDeliveryWith(t, f, Options{Fleet: fl, Auth: (&authed{state: AuthState{SignedIn: true}}).auth(false, true)}, true, nil)
	f.history = &delivery.History{Stage: delivery.StageNow{Pointer: dRun}, Entries: []delivery.HistoryEntry{{Operation: "ship", From: dOld, To: dRun}}}
	f.blame = &delivery.Blame{Digest: dRun, Stage: delivery.StageNow{Pointer: dRun}}
	f.undo = &delivery.Undo{Plan: &delivery.PlanAnswer{Plan: delivery.Plan{From: dRun, To: dOld}}}
	for _, path := range []string{"history?namespace=prod&stage=payments", "history", "blame?namespace=prod&stage=payments",
		"undo?namespace=prod&name=ship&stage=payments", "operations/prod/for-checkout"} {
		_, out := call(t, http.MethodGet, base+"/api/delivery/"+path, nil)
		revs, _ := out["revisions"].(map[string]any)
		expect(t, revs[dRun] == key && len(revs) == 1, "%s revisions %v", path, out["revisions"])
	}
}

// TestLabelsRevisionsRestricted: a revision hidden from the viewer is never
// linked, and with no fleet nothing is.
func TestLabelsRevisionsRestricted(t *testing.T) {
	f := sampleCluster("acme:team-payments")
	f.contract = "oci://r/payments@" + dContract
	f.history = &delivery.History{Stage: delivery.StageNow{Pointer: dRun}}
	auth := (&authed{state: AuthState{SignedIn: true}}).auth(false, true)
	hidden := func(_ context.Context, q *fleet.Query) (*fleet.Query, error) {
		return q.Restrict(func(ns string) bool { return ns != "prod" && ns != "" }), nil
	}
	for name, o := range map[string]Options{
		"hidden":   {Fleet: revisionFleet(t, contractRev("payments", dContract)), View: hidden, Auth: auth},
		"no fleet": {Auth: auth},
	} {
		base := startDeliveryWith(t, f, o, true, nil)
		_, envs := call(t, http.MethodGet, base+"/api/delivery/environments?service=payments", nil)
		expect(t, rowOf(t, envs, "payments")["revision"] == nil, "%s: row %v", name, rowOf(t, envs, "payments"))
		_, h := call(t, http.MethodGet, base+"/api/delivery/history?namespace=prod&stage=payments", nil)
		expect(t, h["revisions"] == nil, "%s: labels %v", name, h["revisions"])
	}
}

// historyFake is payments in prod running dRun, which the change ship put
// there, and two older lines: one tied to INC-1 by a latency alert, one with
// no cause.
func historyFake(caller delivery.Caller) *fakeDelivery {
	at := func(h int) string { return time.Date(2026, 11, 5, h, 0, 0, 0, time.UTC).Format(time.RFC3339) }
	return &fakeDelivery{caller: caller,
		stages: []delivery.StageObject{stage("prod", "payments", "payments", delivery.StageStatus{Pointer: dRun})},
		history: &delivery.History{Stage: delivery.StageNow{Name: "payments", Pointer: dRun}, Completeness: "complete", Note: "n",
			Entries: []delivery.HistoryEntry{
				{Seq: 1, Operation: "old", Type: delivery.TypePromote, Finished: at(8), Pointer: &delivery.PointerChange{Before: "", After: dOld}},
				{Seq: 2, Operation: "tied", Type: delivery.TypePromote, Finished: at(9), CorrelationID: "INC-1",
					Cause: &delivery.Cause{Reason: "Latency alert"}, Pointer: &delivery.PointerChange{Before: dOld, After: dNext}},
				{Seq: 3, Operation: "ship", Type: delivery.TypePromote, Finished: at(10), Pointer: &delivery.PointerChange{Before: dNext, After: dRun}},
			}}}
}

// entryOps is the operation of each entry of the first history of body.
func entryOps(body map[string]any) []string {
	var out []string
	for _, e := range body["histories"].([]any)[0].(map[string]any)["entries"].([]any) {
		out = append(out, e.(map[string]any)["operation"].(string))
	}
	return out
}

// TestHistoryCauseAndCorrelation: cause and correlationID keep only the
// changes they name, in both modes, and the note says so.
func TestHistoryCauseAndCorrelation(t *testing.T) {
	f := historyFake(person())
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	for _, q := range []string{"namespace=prod&stage=payments&correlationID=INC-1", "cause=LATENCY"} {
		_, body := call(t, http.MethodGet, base+"/api/delivery/history?"+q, nil)
		h := body["histories"].([]any)[0].(map[string]any)
		expect(t, slices.Equal(entryOps(body), []string{"tied"}) && strings.Contains(h["note"].(string), "; only changes "), "%s: %v", q, h)
	}
	_, body := call(t, http.MethodGet, base+"/api/delivery/history?namespace=prod&stage=payments&q=INC-1", nil)
	expect(t, slices.Equal(entryOps(body), []string{"tied"}), "q matches the correlation id: %v", entryOps(body))
	_, body = call(t, http.MethodGet, base+"/api/delivery/history?namespace=prod&stage=payments&q=latency", nil)
	expect(t, slices.Equal(entryOps(body), []string{"tied"}), "q matches the cause: %v", entryOps(body))
	_, body = call(t, http.MethodGet, base+"/api/delivery/history?namespace=prod&stage=payments&cause=nothing", nil)
	expect(t, len(entryOps(body)) == 0, "nothing kept: %v", body)
}

// historyUndo is the undo offer of op on the first history of body, nil
// when none.
func historyUndo(body map[string]any, op string) map[string]any {
	offers, _ := body["histories"].([]any)[0].(map[string]any)["undoOffers"].(map[string]any)
	o, _ := offers[op].(map[string]any)
	return o
}

// TestHistoryUndoOffers: each change on the page that moved the Stage says
// whether the caller may undo it, gated like the service page's offer.
func TestHistoryUndoOffers(t *testing.T) {
	f := historyFake(person())
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	one := base + "/api/delivery/history?namespace=prod&stage=payments"
	_, body := call(t, http.MethodGet, one, nil)
	ship := historyUndo(body, "ship")
	expect(t, ship["available"] == true && ship["to"] == dNext && ship["action"] == "undo", "ship %v", ship)
	old := historyUndo(body, "old")
	expect(t, old["available"] == false && old["reason"] != "", "old %v", old)
	// An agent never undoes a rollback the Stage runs.
	f.history.Entries[2].Type = delivery.TypeRollback
	f.caller = delivery.Caller{Identity: delivery.Identity{User: "system:serviceaccount:a:bot"}, Class: delivery.ClassAgent}
	_, body = call(t, http.MethodGet, one, nil)
	ship = historyUndo(body, "ship")
	expect(t, ship["available"] == false && strings.Contains(ship["reason"].(string), "only a person or break-glass"), "agent %v", ship)
	f.history.Entries[2].Type, f.caller = delivery.TypePromote, person()
	f.deny = map[string]bool{"create operations prod": true}
	_, body = call(t, http.MethodGet, one, nil)
	for _, op := range []string{"ship", "tied", "old"} {
		o := historyUndo(body, op)
		expect(t, o["available"] == false && o["reason"] != "", "denied %s: %v", op, o)
	}
	expect(t, historyUndo(body, "ship")["reason"] == "your Kubernetes RBAC does not let you create Operations in prod", "denied %v", historyUndo(body, "ship"))
	f.deny, f.stageErr = nil, nil
	f.stages = nil
	_, body = call(t, http.MethodGet, one, nil)
	h := body["histories"].([]any)[0].(map[string]any)
	expect(t, h["undoOffers"] == nil && strings.Contains(h["note"].(string), "undo offers need the Stage, which could not be read"), "unread Stage %v", h)
	f.stages, f.callerErr = historyFake(person()).stages, errors.New("whoami down")
	_, body = call(t, http.MethodGet, one, nil)
	h = body["histories"].([]any)[0].(map[string]any)
	expect(t, h["undoOffers"] == nil && strings.Contains(h["note"].(string), "whoami down"), "unread caller %v", h)
}

// TestHistoryUndoOffersEveryStage: without a stage each paged Stage's newest
// change carries its offer, with one review per namespace.
func TestHistoryUndoOffersEveryStage(t *testing.T) {
	f := historyFake(person())
	f.stages = append(f.stages, stage("prod", "payments-eu", "payments", delivery.StageStatus{Pointer: dRun}))
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	before := f.cans.Load()
	_, body := call(t, http.MethodGet, base+"/api/delivery/history?service=payments", nil)
	hs := body["histories"].([]any)
	expect(t, len(hs) == 2, "two Stages: %v", hs)
	for _, h := range hs {
		o := h.(map[string]any)["undoOffers"].(map[string]any)["ship"].(map[string]any)
		expect(t, o["available"] == true, "every-Stage offer %v", o)
	}
	expect(t, f.cans.Load()-before == 1, "one review for prod, got %d", f.cans.Load()-before)
}

// TestHistoryTimelineFiltered: the timeline counts only the changes the
// filter keeps, in both modes.
func TestHistoryTimelineFiltered(t *testing.T) {
	f := historyFake(person())
	f.ops = []delivery.OperationObject{
		{Metadata: delivery.ObjectMeta{Namespace: "prod", Name: "tied"}, Spec: delivery.OperationSpec{Type: delivery.TypePromote, Stage: "payments",
			CorrelationID: "INC-1"}, Status: delivery.OperationStatus{Phase: delivery.PhaseConcluded, Times: delivery.Times{Finished: "2026-11-05T09:00:00Z"}}},
		{Metadata: delivery.ObjectMeta{Namespace: "prod", Name: "ship"}, Spec: delivery.OperationSpec{Type: delivery.TypePromote, Stage: "payments"},
			Status: delivery.OperationStatus{Phase: delivery.PhaseConcluded, Times: delivery.Times{Finished: "2026-11-05T10:00:00Z"}}},
	}
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	total := func(tl map[string]any) float64 {
		var n float64
		for _, v := range tl["totals"].(map[string]any) {
			n += v.(float64)
		}
		u, _ := tl["untimed"].(float64)
		return n + u
	}
	window := "&since=2026-11-05T07:00:00Z&until=2026-11-05T11:00:00Z"
	for _, timed := range []string{"", "&timed=true"} {
		_, all := call(t, http.MethodGet, base+"/api/delivery/history?namespace=prod&stage=payments"+window+timed, nil)
		_, kept := call(t, http.MethodGet, base+"/api/delivery/history?namespace=prod&stage=payments&correlationID=INC-1"+window+timed, nil)
		a := total(all["histories"].([]any)[0].(map[string]any)["timeline"].(map[string]any))
		k := total(kept["histories"].([]any)[0].(map[string]any)["timeline"].(map[string]any))
		expect(t, a == 3 && k == 1, "one Stage%s: %v unfiltered, %v kept", timed, a, k)
	}
	_, all := call(t, http.MethodGet, base+"/api/delivery/history?"+window[1:], nil)
	_, kept := call(t, http.MethodGet, base+"/api/delivery/history?correlationID=INC-1"+window, nil)
	a, k := total(all["timeline"].(map[string]any)), total(kept["timeline"].(map[string]any))
	expect(t, a == 2 && k == 1, "every Stage: %v unfiltered, %v kept", a, k)
}
