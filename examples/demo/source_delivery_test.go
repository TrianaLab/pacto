package main

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/trianalab/pacto/v4/examples/story"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// demoServer is the dashboard the wasm binary serves, over the story.
func demoServer(t *testing.T) (*demoDelivery, http.Handler) {
	t.Helper()
	d, err := newDemoDelivery(time.Now, nil)
	if err != nil {
		t.Fatal(err)
	}
	var opts dashboard.Options
	d.wire(&opts)
	mux := http.NewServeMux()
	dashboard.NewServer(opts).RegisterOperations(humago.New(mux, dashboard.APIConfig()))
	return d, mux
}

func call(t *testing.T, h http.Handler, method, path, body string, out any) int {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if out != nil && rec.Code < 300 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("%s %s: %v: %s", method, path, err, rec.Body)
		}
	}
	if rec.Code >= 300 {
		t.Logf("%s %s -> %d %s", method, path, rec.Code, rec.Body)
	}
	return rec.Code
}

func digest(svc, v string) string { return story.ArtifactDigest(svc, v) }

type held struct {
	Held []dashboard.InboxItem `json:"held"`
}

func inbox(t *testing.T, h http.Handler) []dashboard.InboxItem {
	t.Helper()
	var out held
	if code := call(t, h, "GET", "/api/delivery/inbox", "", &out); code != 200 {
		t.Fatalf("inbox %d", code)
	}
	return out.Held
}

// TestDeliveryStory plays the guided path through the dashboard's own
// handlers: the replayed history, the held breaking change, the consumer that
// moves first, who may decide, the approval, the rollback and the records.
func TestDeliveryStory(t *testing.T) {
	d, h := demoServer(t)
	storyReplayed(t, d)
	op, decide := storyHeld(t, d, h)
	storyApproved(t, d, h, decide)
	storyUnknown(t, d, h, op, decide)
	storyRolledBack(t, d, h, op)
}

// TestRejectedUnknownRollsBack: rejecting a version held Unknown after it
// shipped fails it and rolls it back, as the decision panel says.
func TestRejectedUnknownRollsBack(t *testing.T) {
	d, h := demoServer(t)
	op, decide := storyHeld(t, d, h)
	storyApproved(t, d, h, decide)
	if code := call(t, h, "POST", decide, `{"decision":"reject","note":"too few spans to trust it"}`, nil); code != 201 {
		t.Fatalf("reject: %d", code)
	}
	got, _ := d.Get(t.Context(), story.Prod, op.Metadata.Name)
	prod, _ := d.Stage(t.Context(), story.Prod, "payments")
	if got.Status.Outcome != delivery.Failed || prod.Status.Pointer != digest("payments", "2.3.0") {
		t.Fatalf("rejected unknown: %+v, pointer %s", got.Status, prod.Status.Pointer)
	}
}

// The reads hand out views made once per change; a step the promoter takes
// later, outside any request, drops them too.
func TestViewsFollowALaterStep(t *testing.T) {
	var queued []func()
	d, err := newDemoDelivery(time.Now, func(f func()) { queued = append(queued, f) })
	if err != nil {
		t.Fatal(err)
	}
	pointer := func() string {
		stages, _ := d.Stages(t.Context(), story.Prod)
		for _, s := range stages {
			if s.Metadata.Name == "checkout" {
				return s.Status.Pointer
			}
		}
		return ""
	}
	before := pointer()
	if _, _, err := d.Propose(t.Context(), delivery.Request{Namespace: story.Prod, Stage: "checkout", To: digest("checkout", "1.9.0")}); err != nil {
		t.Fatal(err)
	}
	if pointer() != before || len(queued) != 1 {
		t.Fatalf("moved before the step: %s, %d steps", pointer(), len(queued))
	}
	queued[0]()
	if got := pointer(); got != digest("checkout", "1.9.0") {
		t.Fatalf("the step moved checkout to %s, the read still says %s", digest("checkout", "1.9.0"), got)
	}
}

// storyUnknown is B8: too little telemetry leaves 3.0.0 Unknown after it
// shipped, so it waits for carol to accept the unknown.
func storyUnknown(t *testing.T, d *demoDelivery, h http.Handler, op delivery.OperationObject, decide string) {
	t.Helper()
	got, _ := d.Get(t.Context(), story.Prod, op.Metadata.Name)
	if got.Status.Phase != delivery.PhaseHeld || !got.Status.Applied || got.Status.Reason != "VerifyUnknown" ||
		!strings.Contains(got.Status.Question, "Accept the unknown") {
		t.Fatalf("B8 hold: %+v", got.Status)
	}
	if code := call(t, h, "POST", decide, `{"decision":"accept-unknown","note":"spans were thin at night"}`, nil); code != 201 {
		t.Fatalf("accept-unknown: %d", code)
	}
	got, _ = d.Get(t.Context(), story.Prod, op.Metadata.Name)
	if got.Status.Outcome != delivery.Succeeded || got.Status.Verification != delivery.AcceptedUnknown {
		t.Fatalf("B8 accepted: %+v", got.Status)
	}
}

// storyReplayed checks the history replayed at load and what Environments offers.
func storyReplayed(t *testing.T, d *demoDelivery) {
	t.Helper()
	// The replay: B2 rolled payments prod back to 2.3.0 and 2.4.0 failed
	// there; B3's 2.4.2 failed in test; 3.0.0 runs in test.
	prod, _ := d.Stage(t.Context(), story.Prod, "payments")
	if prod.Status.Pointer != digest("payments", "2.3.0") || !slices.Contains(prod.Status.FailedDigests, digest("payments", "2.4.0")) {
		t.Fatalf("payments prod after B2: %+v", prod.Status)
	}
	test, _ := d.Stage(t.Context(), story.Test, "payments")
	if test.Status.Pointer != digest("payments", "3.0.0") || !slices.Contains(test.Status.FailedDigests, digest("payments", "2.4.2")) {
		t.Fatalf("payments test after B4: %+v", test.Status)
	}

	// What Environments offers to promote: 3.0.0 to payments prod, 1.9.0 to
	// checkout prod and fraud's first version to prod.
	stages, _ := d.Stages(t.Context(), story.Prod)
	cands := map[string]string{}
	for _, s := range stages {
		cands[s.Metadata.Name] = versionOf(s.Metadata.Name, s.Status.Candidate)
	}
	if want := map[string]string{"payments": "3.0.0", "checkout": "1.9.0", "fraud": "1.0.0", "orders": "", "pay-agent": ""}; !maps.Equal(cands, want) {
		t.Fatalf("prod candidates %v, want %v", cands, want)
	}

}

// storyHeld is B5: pay-agent proposes 3.0.0 for prod; it is held for
// checkout and orders, and checkout 1.9.0 can move first.
func storyHeld(t *testing.T, d *demoDelivery, h http.Handler) (delivery.OperationObject, string) {
	t.Helper()
	body := `{"namespace":"prod","stage":"payments","to":"` + digest("payments", "3.0.0") + `"}`
	var proposed struct {
		Operation delivery.OperationObject `json:"operation"`
	}
	if code := call(t, h, "POST", "/api/delivery/operations", body, &proposed); code != 201 {
		t.Fatalf("propose %d", code)
	}
	op := proposed.Operation
	if op.Status.Phase != delivery.PhaseHeld || !slices.Equal(op.Status.Plan.Floor.Owners, []string{"checkout", "orders"}) {
		t.Fatalf("B5: %+v", op.Status)
	}
	if o := op.Status.Plan.Order; o == nil || !slices.ContainsFunc(o.MoveFirst, func(m delivery.MoveFirst) bool {
		return m.Candidate != nil && m.Candidate.Version == "1.9.0"
	}) {
		t.Fatalf("B5 order: %+v", op.Status.Plan.Order)
	}
	decide := "/api/delivery/operations/prod/" + op.Metadata.Name + "/decision"
	if code := call(t, h, "POST", decide, `{"decision":"approve"}`, nil); code != 403 {
		t.Fatalf("pay-agent decided: %d", code)
	}
	return op, decide
}

// storyApproved is B6 and B7: checkout moves first, then only orders is asked.
func storyApproved(t *testing.T, d *demoDelivery, h http.Handler, decide string) {
	t.Helper()
	var proposed struct {
		Operation delivery.OperationObject `json:"operation"`
	}

	// B6: checkout 1.9.0 ships with nobody, and the held change now asks
	// only orders.
	body := `{"namespace":"prod","stage":"checkout","to":"` + digest("checkout", "1.9.0") + `"}`
	if code := call(t, h, "POST", "/api/delivery/operations", body, &proposed); code != 201 || proposed.Operation.Status.Phase != delivery.PhaseConcluded {
		t.Fatalf("B6 checkout: %d %+v", code, proposed.Operation.Status)
	}
	if items := inbox(t, h); len(items) != 0 {
		t.Fatalf("an agent's inbox holds what only people answer: %+v", items)
	}
	_ = d.SetPersona("carol", false)
	items := inbox(t, h)
	if len(items) != 1 || !slices.Equal(items[0].Owners, []string{"orders"}) || !strings.Contains(items[0].Question, "Asked of: orders") {
		t.Fatalf("B6 inbox: %+v", items)
	}

	// B7: alice is not on the asked team; carol is, and her sign-in of half
	// an hour ago decides: the simulated Stages ask for no recent sign-in.
	if err := d.SetPersona("alice", true); err != nil {
		t.Fatal(err)
	}
	if code := call(t, h, "POST", decide, `{"decision":"approve"}`, nil); code != 403 {
		t.Fatalf("alice decided: %d", code)
	}
	_ = d.SetPersona("carol", false)
	if code := call(t, h, "POST", decide, `{"decision":"approve"}`, nil); code != 201 {
		t.Fatalf("carol: %d", code)
	}
	prod, _ := d.Stage(t.Context(), story.Prod, "payments")
	if prod.Status.Pointer != digest("payments", "3.0.0") {
		t.Fatalf("3.0.0 did not ship: %+v", prod.Status)
	}

}

// storyRolledBack is B9 and the records: carol rolls op back, and history,
// blame, undo and the refused publish read as the story says.
func storyRolledBack(t *testing.T, d *demoDelivery, h http.Handler, op delivery.OperationObject) {
	t.Helper()
	var proposed struct {
		Operation delivery.OperationObject `json:"operation"`
	}
	// B9: carol rolls it back; automation holds off.
	body := `{"namespace":"prod","stage":"payments","type":"delivery.pacto.trianalab.io/rollback","undoes":"` + op.Metadata.Name + `"}`
	if code := call(t, h, "POST", "/api/delivery/operations", body, &proposed); code != 201 {
		t.Fatalf("rollback %d", code)
	}
	prod, _ := d.Stage(t.Context(), story.Prod, "payments")
	if prod.Status.Pointer != digest("payments", "2.3.0") || !prod.Status.AutoHold {
		t.Fatalf("B9: %+v", prod.Status)
	}

	storyRecords(t, d, op, proposed.Operation.Metadata.Name)
}

// storyRecords: prod reads verified and complete; blame names carol's
// approval of op, the change that brought 3.0.0, and undo plans a rollback of
// rollback.
func storyRecords(t *testing.T, d *demoDelivery, op delivery.OperationObject, rollback string) {
	t.Helper()
	ph, _ := d.History(t.Context(), story.Prod, "payments", nil)
	if ph.Source != delivery.SourceRecords || !ph.Verified || ph.Completeness != "complete" {
		t.Fatalf("prod history: %+v", ph)
	}
	b, _ := d.Blame(t.Context(), story.Prod, "payments", digest("payments", "3.0.0"))
	if b.Change == nil || b.Change.Operation != op.Metadata.Name || len(b.Approvals) != 2 || b.Approvals[0].By != story.Carol.User || b.Approvals[1].Decision != delivery.AcceptUnknown {
		t.Fatalf("blame: %+v", b)
	}
	u, _ := d.Undo(t.Context(), story.Prod, rollback, "")
	if u.Kind != "rollback" || u.Plan == nil {
		t.Fatalf("undo: %+v", u)
	}

	storyProblems(t, d)
}

// storyProblems: the deliberate problems (a gap and an untrusted record in
// payments test, fraud read from status only) and the refused publish (B4).
func storyProblems(t *testing.T, d *demoDelivery) {
	t.Helper()
	th, _ := d.History(t.Context(), story.Test, "payments", nil)
	if th.Completeness != "partial" || len(th.Problems) == 0 || len(th.Unreadable) != 1 {
		t.Fatalf("test history: %+v", th)
	}
	fh, _ := d.History(t.Context(), story.Test, "fraud", nil)
	if fh.Source != delivery.SourceStatus || len(fh.Entries) != 0 { // it observes: GitOps moved it, not an operation
		t.Fatalf("fraud history: %+v", fh)
	}

	// The refused publish (B4).
	r, _ := d.PublishRefusals(t.Context(), "")
	if len(r.Refusals) != 1 || r.Refusals[0].RequiredBump != "major" || r.Refusals[0].Verified {
		t.Fatalf("refusals: %+v", r)
	}
}

// The world at load shows the autonomy ladder from examples/story, one test
// per beat.

// TestReplayFraudEarnsAdvise: fraud observes and Pacto noted it would have
// shipped 1.0.0, which earns the proposal of advise, decided by team risk.
func TestReplayFraudEarnsAdvise(t *testing.T) {
	d, h := demoServer(t)
	fraud, _ := d.Stage(t.Context(), story.Test, "fraud")
	sh := fraud.Status.Shadow
	if fraud.Status.Level != delivery.LevelObserve || fraud.Status.Pointer != "" || len(sh) != 1 || sh[0].Predicted != delivery.PredictProceeds ||
		sh[0].Verification != delivery.Verified || fraud.Status.Proposal == nil || fraud.Status.Proposal.Level != delivery.LevelAdvise {
		t.Fatalf("fraud observes: %+v", fraud.Status)
	}
	body := `{"namespace":"test","stage":"fraud","type":"` + delivery.TypeAutonomy + `","from":"observe","to":"advise"}`
	var p delivery.Plan
	if code := call(t, h, "POST", "/api/delivery/plan", body, &p); code != 200 || p.Predicted != delivery.PredictHeld || !slices.Equal(p.Floor.Owners, []string{"risk"}) {
		t.Fatalf("the raise of fraud: %d %+v", code, p)
	}
}

// TestReplayPaymentsStepsDown: payments in prod stepped down after B2's failure.
func TestReplayPaymentsStepsDown(t *testing.T) {
	d, _ := demoServer(t)
	prod, _ := d.Stage(t.Context(), story.Prod, "payments")
	down := d.op(story.Prod, "payments-autonomy-4")
	if prod.Status.Level != delivery.LevelAutoNonBreaking || down == nil || down.Status.Outcome != delivery.Succeeded ||
		down.Spec.Cause == nil || down.Spec.Cause.Operation != d.op(story.Prod, "payments-promote-2").Metadata.UID {
		t.Fatalf("payments prod steps down after B2: %+v %+v", prod.Status, down)
	}
}

// TestReplayEnvironmentHeld: payments 2.4.1 declares environments but none
// for prod, so the prod promote waited for team payments, and alice rejected it.
func TestReplayEnvironmentHeld(t *testing.T) {
	d, _ := demoServer(t)
	o := d.op(story.Prod, "payments-promote-6")
	i := slices.IndexFunc(o.Status.Verdicts, func(v delivery.Verdict) bool { return v.Gate == delivery.GateEnvironment })
	if o.Status.Outcome != delivery.Rejected || len(o.Status.Approvals) != 1 || o.Status.Approvals[0].Decision != delivery.Reject ||
		!slices.Equal(o.Status.Plan.Floor.Owners, []string{"payments"}) || i < 0 ||
		o.Status.Verdicts[i].Message != "version 2.4.1 declares no environment prod for Stage payments (it declares test)" {
		t.Fatalf("payments 2.4.1 in prod: %+v", o.Status)
	}
}

// TestReplayUnknownCallerAccepted: a workload with no contract held the
// breaking 3.0.0 in test until alice accepted that unknown.
func TestReplayUnknownCallerAccepted(t *testing.T) {
	d, _ := demoServer(t)
	b4 := d.op(story.Test, "payments-promote-12")
	if len(b4.Status.Approvals) != 1 || b4.Status.Approvals[0].Decision != delivery.AcceptUnknown || b4.Status.Outcome != delivery.Succeeded ||
		!strings.Contains(b4.Status.Plan.Question, "ledger-export calls payments at runtime") {
		t.Fatalf("B4 waits on ledger-export's missing contract: %+v", b4.Status)
	}
}

// TestReplaySignedEvals: pay-agent's evals ran through a runner that signed
// its count.
func TestReplaySignedEvals(t *testing.T) {
	d, _ := demoServer(t)
	failed := d.op(story.Test, "pay-agent-promote-17")
	i := slices.IndexFunc(failed.Status.Verdicts, func(v delivery.Verdict) bool { return v.Gate == "evals" })
	if failed.Status.Outcome != delivery.Failed || i < 0 {
		t.Fatalf("pay-agent 1.1.0: %+v", failed.Status)
	}
	v := failed.Status.Verdicts[i]
	if v.Result != delivery.Fail || v.Citation["passed"] != "1" || v.Citation["total"] != "2" || v.Citation["runner"] != story.RunnerName {
		t.Fatalf("the eval verdict: %+v", v)
	}
	if _, _, err := delivery.OpenEvalAnswer([]byte(v.Citation["runnerBundle"]), runners); err != nil {
		t.Fatalf("the runner's signed answer does not verify: %v", err)
	}
}

// TestWasmBuildHasNoKubernetes: the browser demo links no Kubernetes client.
// The delivery it shows is pkg/delivery over fixed data, never internal/app,
// which would bring client-go and several times the download.
func TestWasmBuildHasNoKubernetes(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "go", "list", "-deps", ".")
	cmd.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range strings.Fields(string(out)) {
		if strings.Contains(p, "k8s.io") {
			t.Fatalf("the wasm build links %s", p)
		}
	}
}
