package story

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/internal/pactify"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/diff"
	"github.com/trianalab/pacto/v4/pkg/evidence"
	"github.com/trianalab/pacto/v4/pkg/signing"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

func beat(t *testing.T, id string) Beat {
	t.Helper()
	for _, b := range Beats {
		if b.ID == id {
			return b
		}
	}
	t.Fatalf("no beat %s", id)
	return Beat{}
}

func plan(t *testing.T, b Beat) delivery.Plan {
	t.Helper()
	in, err := b.PlanInput(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return delivery.PlanFor(context.Background(), in)
}

// TestEveryContractValidates walks every story contract: it parses, passes
// validation and is a 2.1, 2.2 or 2.3 contract with a publish section.
// payments and its callers are 2.2 or later: payments promises an idempotent
// http interface and orders and checkout require it. payments from 2.4.1 is
// 2.3: it declares environments.
func TestEveryContractValidates(t *testing.T) {
	n := 0
	err := fs.WalkDir(Files, "contracts", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.Name() != "pacto.yaml" {
			return err
		}
		dir := path.Dir(p)
		b, err := Bundle(path.Base(path.Dir(dir)), path.Base(dir))
		if err != nil {
			return err
		}
		res := validation.Validate(b.Contract, b.RawYAML, b.FS)
		if !res.IsValid() || !slices.Contains([]string{"2.1", "2.2", "2.3"}, b.Contract.PactoVersion) || b.Contract.Publish == nil {
			t.Errorf("%s: version %s, publish %v, errors %v", p, b.Contract.PactoVersion, b.Contract.Publish != nil, res.Errors)
		}
		n++
		return nil
	})
	if err != nil || n != 12 {
		t.Fatalf("walked %d contracts: %v", n, err)
	}
}

// TestNoConfigurationLeavesRuntimeUnknown: the Kubernetes demo's Pacto
// resources bind no configuration, and the operator reports a required
// configuration it has no binding for as OBSERVATION_UNSUPPORTED, which makes
// the contract Unknown and holds every verify gate on it. So no story contract
// may require a configuration: payments declares one for its environments to
// check, and it stays optional at runtime.
func TestNoConfigurationLeavesRuntimeUnknown(t *testing.T) {
	err := fs.WalkDir(Files, "contracts", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.Name() != "pacto.yaml" {
			return err
		}
		dir := path.Dir(p)
		b, err := Bundle(path.Base(path.Dir(dir)), path.Base(dir))
		if err != nil {
			return err
		}
		var unbound []evidence.Observation
		for _, c := range b.Contract.Configurations {
			if c.Required { // what the operator's observer emits for a configuration with no binding
				o, _ := evidence.NewUnobserved(evidence.ConfigurationPresent, evidence.SubjectRef{Kind: "configuration", Name: c.Name},
					evidence.Unsupported, evidence.Provenance{Collector: "kubernetes", DetectedAt: Now})
				unbound = append(unbound, o)
			}
		}
		findings, _ := validation.Evaluate(*b.Contract, evidence.EvidenceSet{Observations: unbound})
		for _, f := range findings {
			if f.Subject.Kind == "configuration" {
				t.Errorf("%s: %s %s: %s", p, f.Code, f.Severity, f.Message)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestCallersRequirementsAreMetByEveryPayments keeps refusals out of the story:
// orders and checkout require payments' idempotent http, and every payments
// version promises it, so no beat's plan turns on a refused guarantee.
func TestCallersRequirementsAreMetByEveryPayments(t *testing.T) {
	versions, err := fs.ReadDir(Files, "contracts/payments")
	if err != nil {
		t.Fatal(err)
	}
	for _, caller := range []struct{ svc, ver string }{{"orders", "1.4.0"}, {"checkout", "1.8.0"}, {"checkout", "1.9.0"}} {
		c, err := Bundle(caller.svc, caller.ver)
		if err != nil {
			t.Fatal(err)
		}
		dep := c.Contract.Dependencies[0]
		if len(dep.Requires) == 0 {
			t.Fatalf("%s %s requires nothing of payments", caller.svc, caller.ver)
		}
		for _, v := range versions {
			p, err := Bundle("payments", v.Name())
			if err != nil {
				t.Fatal(err)
			}
			if m := contract.Unmet(caller.svc, dep, p.Contract); len(m) > 0 {
				t.Errorf("payments %s refuses %s %s: %v", v.Name(), caller.svc, caller.ver, m)
			}
		}
	}
}

// TestPlainFraudIsNotPactified keeps the pactify act honest: the starting
// point has what a service already has and no contract.
func TestPlainFraudIsNotPactified(t *testing.T) {
	for _, p := range []string{"plain/fraud/Dockerfile", "plain/fraud/k8s/deployment.yaml", "plain/fraud/k8s/service.yaml"} {
		if _, err := fs.Stat(Files, p); err != nil {
			t.Error(err)
		}
	}
	if _, err := fs.Stat(Files, "plain/fraud/pacto.yaml"); err == nil {
		t.Error("plain/fraud must not be pactified yet")
	}
}

// TestFraudPublishesFromPlainFraud: contracts/fraud/1.0.0 is plain/fraud's
// pacto.yaml once pactified; the build inputs it names live in plain/fraud.
func TestFraudPublishesFromPlainFraud(t *testing.T) {
	b, err := Bundle("fraud", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range b.Contract.Publish.Artifacts {
		switch {
		case a.Build.Dockerfile != nil:
			file := a.Build.Dockerfile.File
			if file == "" {
				file = "Dockerfile"
			}
			if _, err := fs.Stat(Files, path.Join("plain/fraud", a.Build.Dockerfile.Context, file)); err != nil {
				t.Errorf("%s: %v", a.Name, err)
			}
		case strings.Contains(strings.Join(a.Build.Command, " "), "k8s/"):
			for _, f := range strings.Fields(strings.Join(a.Build.Command, " ")) {
				if strings.HasPrefix(f, "k8s/") {
					if _, err := fs.Stat(Files, path.Join("plain/fraud", f)); err != nil {
						t.Errorf("%s: %v", a.Name, err)
					}
				}
			}
		default:
			t.Errorf("%s: unchecked build input", a.Name)
		}
	}
}

func TestGuidedPathIsFiveMoments(t *testing.T) {
	var moments []int
	for _, b := range Beats {
		if b.Says == "" || b.Why == "" || strings.Count(b.Says, ". ") > 0 || strings.Count(b.Why, ". ") > 0 {
			t.Errorf("%s: one sentence each for what it says and why", b.ID)
		}
		if b.Moment > 0 {
			moments = append(moments, b.Moment)
		}
	}
	slices.Sort(moments)
	if !reflect.DeepEqual(moments, []int{1, 2, 3, 4, 5}) {
		t.Fatalf("guided moments %v", moments)
	}
}

// TestBeats proves every beat's outcome with Pacto's own code: the diff and
// version rule (pkg/diff), the plan (delivery.PlanFor, which runs pkg/impact)
// and the verify gates.
func TestBeats(t *testing.T) {
	for _, b := range Beats {
		t.Run(b.ID, func(t *testing.T) {
			got := Outcome{Verify: verify(t, b)}
			bumpRule(t, b, &got)
			if b.Type == delivery.TypeUnfreeze {
				got.Concluded = unfreeze(t, b)
			} else if b.To != "" {
				p := plan(t, b)
				got.Predicted = p.Predicted
				if p.Predicted == delivery.PredictRejected {
					got.Refusal = refusal(p)
				}
				if p.Floor.Human {
					got.Decision, got.Asked = p.Floor.Decision, p.Floor.Owners
				}
				if p.Order != nil {
					for _, m := range p.Order.MoveFirst {
						if m.Candidate != nil {
							got.MoveFirst = append(got.MoveFirst, m.Service+" "+m.Candidate.Version)
						}
					}
				}
			}
			if !reflect.DeepEqual(got, b.Expect) {
				t.Fatalf("\n got  %+v\n want %+v", got, b.Expect)
			}
		})
	}
}

// unfreeze is what the controller concludes for an unfreeze: it plans none,
// so only the permission gate (the same one PlanFor runs) stands between the
// actor and Succeeded/Unverified. That value is mirrored from the operator,
// not computed here: TestFrozenPromoteWaitsAndUnfreeze pins it.
func unfreeze(t *testing.T, b Beat) string {
	t.Helper()
	if p := plan(t, b); p.Predicted != delivery.PredictProceeds {
		return string(delivery.Rejected) + "/" + refusal(p)
	}
	return string(delivery.Succeeded) + "/" + string(delivery.Unverified)
}

// bumpRule fills the classification, the required bump and whether Tried is
// refused, for a promote, and checks To itself keeps the version rule.
func bumpRule(t *testing.T, b Beat, got *Outcome) {
	t.Helper()
	base := b.Base
	if base == "" {
		base = b.From
	}
	if b.Type != "" || base == "" || b.To == "" {
		return
	}
	c, bump := classify(t, b.Service, base, b.To)
	got.Classification, got.RequiredBump = c, bump
	if err := diff.CheckBump(base, b.To, bump); err != nil {
		t.Errorf("%s itself breaks the version rule: %v", b.To, err)
	}
	if b.Tried == "" {
		return
	}
	// Tried is judged by its own contract, not by To's.
	if _, triedBump := classify(t, b.Service, base, b.Tried); diff.CheckBump(base, b.Tried, triedBump) != nil {
		got.Refused = b.Tried
	}
}

// classify diffs service at version against base and gives the
// classification and the smallest bump it needs.
func classify(t *testing.T, service, base, version string) (string, diff.Bump) {
	t.Helper()
	old, err := Bundle(service, base)
	if err != nil {
		t.Fatal(err)
	}
	nu, err := Bundle(service, version)
	if err != nil {
		t.Fatal(err)
	}
	c := diff.Compare(context.Background(), old.Contract, nu.Contract, old.FS, nu.FS).Classification
	return c.String(), diff.RequiredBump(c, base)
}

// refusal is the conflict code or the failing gate that rejected a plan.
func refusal(p delivery.Plan) string {
	if len(p.Conflicts) > 0 {
		code, _, _ := strings.Cut(p.Conflicts[0], ":")
		return code
	}
	for _, v := range p.Verdicts {
		if v.Result == delivery.Fail {
			return v.Gate
		}
	}
	return ""
}

// verify runs the verify gate a beat is about. The gates are Pacto's own; the
// telemetry and the node agent's finding are fixed data standing in for what
// a cluster reports.
func verify(t *testing.T, b Beat) delivery.Result {
	t.Helper()
	switch b.ID {
	case "B2": // 30 of 100 spans failed after 2.4.0 shipped
		return mira(t, 100, 30).Result
	case "B8": // 3 spans in the window, fewer than the 20 needed to judge
		return mira(t, 3, 0).Result
	case "agent": // the runner passed 1 of the suite's 2 cases; the Stage needs 95%
		return evals(t, 1, 2).Result
	case "B3":
		r, err := Revision("payments", "2.4.2")
		if err != nil {
			t.Fatal(err)
		}
		p := pacto(Test, r)
		p.Status.EvaluatedRevision.KernelCoverage = "Complete"
		p.Status.Findings = []delivery.FindingView{{Code: "DEPENDENCY_UNDECLARED", Subject: "dependency/fraud",
			EvidenceRefs: []delivery.EvidenceRefView{{Collector: "ebpf", DetectedAt: Now.Format(time.RFC3339)}}}}
		v := delivery.RuntimeVerdict(delivery.ContractCheck{ContractRef: r.ContractRef, Applied: true, Pactos: []delivery.PactoView{p}})
		if !strings.Contains(v.Message, "dependency/fraud") {
			t.Errorf("the claim check must name fraud: %+v", v)
		}
		return v.Result
	}
	return ""
}

// mira runs the real Mira health gate against a Mira that holds total spans
// of payments in the window, failed of them failed.
func mira(t *testing.T, total, failed int) delivery.Verdict {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Where []map[string]any `json:"where"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		n := total
		for _, term := range q.Where {
			if term["field"] == "status_code" {
				n = failed
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"rows": make([]struct{}, n)})
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v := delivery.Mira{URL: srv.URL, Client: srv.Client()}.Check(ctx, delivery.GateInput{
		Service: delivery.ServiceRef{Name: "payments"}, Window: delivery.Window{From: Now, To: Now.Add(5 * time.Minute)},
	}, nil)
	if v.Citation["query"] == "" {
		t.Errorf("a health verdict cites its query: %+v", v)
	}
	return v
}

// evals runs the real suite gate against a runner that passed passed of
// total cases on the version under check.
func evals(t *testing.T, passed, total int) delivery.Verdict {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Input delivery.GateInput `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		_, _ = w.Write(SignEval(q.Input, "run-1", passed, total))
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ship := func(context.Context, delivery.GateInput) delivery.Revision {
		b, err := Bundle("pay-agent", "1.1.0")
		if err != nil {
			t.Fatal(err)
		}
		return delivery.Revision{ContractRef: "oci://registry.example/story/pay-agent@sha256:c0", Bundle: b}
	}
	verify, err := delivery.RunnersOf([]delivery.TrustedKey{RunnerTrust()}, signing.OpenKey)
	if err != nil {
		t.Fatal(err)
	}
	gates := map[string]delivery.Gate{"evals": delivery.Eval{URL: srv.URL, Client: srv.Client(), Revision: ship, Verify: verify}}
	return delivery.RunChecks(ctx, gates, Checks("pay-agent", Test), delivery.GateInput{
		Service: delivery.ServiceRef{Name: "pay-agent"}, Subjects: []string{ArtifactDigest("pay-agent", "1.1.0")},
	})[0]
}

// TestAgentPassingItsEvalsShips: the eval gate passes 1.0.0, which passed
// every case, so a failed 1.1.0 is the evals' doing.
func TestAgentPassingItsEvalsShips(t *testing.T) {
	if v := evals(t, 2, 2); v.Result != delivery.Pass || v.Citation["run"] == "" || v.Citation["runner"] != RunnerName {
		t.Fatalf("2 of 2 must pass and cite its run: %+v", v)
	}
	b, err := Bundle("pay-agent", "1.1.0")
	if err != nil || len(b.Contract.Checks) != 1 || b.Contract.Checks[0].File == "" {
		t.Fatalf("pay-agent ships its eval suite as a check: %v", err)
	}
}

// environmentVerdict is the pacto.environment verdict of the plan of b.
func environmentVerdict(t *testing.T, b Beat) delivery.Verdict {
	t.Helper()
	for _, v := range plan(t, b).Verdicts {
		if v.Gate == delivery.GateEnvironment {
			return v
		}
	}
	t.Fatalf("%s: no %s verdict", b.ID, delivery.GateEnvironment)
	return delivery.Verdict{}
}

// TestEnvironmentGateAcrossTheStory: payments before 2.4.1 declares no
// environments, so the gate does not apply to B1; 2.4.1 declares only test, so
// prod holds it and the question names the environment it lacks; 3.0.0
// declares prod and its values pass, so B5 holds only for its consumers.
func TestEnvironmentGateAcrossTheStory(t *testing.T) {
	if v := environmentVerdict(t, beat(t, "B1")); v.Result != delivery.Pass || v.Citation["applicable"] != "false" {
		t.Errorf("B1: the gate must not apply to 2.4.0: %+v", v)
	}
	b := beat(t, "environment")
	v := environmentVerdict(t, b)
	want := "version 2.4.1 declares no environment prod for Stage payments (it declares test)"
	if v.Result != delivery.Pass || v.Message != want {
		t.Errorf("environment: verdict %+v, want the message %q", v, want)
	}
	if q := plan(t, b).Question; !strings.HasPrefix(q, want+". Approve shipping") {
		t.Errorf("environment: question %q", q)
	}
	test := b
	test.Environment, test.From, test.Running = Test, "2.4.0", nil
	if v := environmentVerdict(t, test); v.Message != "the values of environment test satisfy the configuration schemas" {
		t.Errorf("2.4.1 in test: %+v", v)
	}
	if v := environmentVerdict(t, beat(t, "B5")); v.Message != "the values of environment prod satisfy the configuration schemas" {
		t.Errorf("B5: %+v", v)
	}
}

// TestOnlyPayments241LacksProd: every payments version that declares
// environments declares one for each Stage a demo delivers it to (test, prod,
// and the Kubernetes demo's prod-eu and prod-us), except 2.4.1, the version
// the environment beat holds.
func TestOnlyPayments241LacksProd(t *testing.T) {
	versions, err := fs.ReadDir(Files, "contracts/payments")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range versions {
		b, err := Bundle("payments", v.Name())
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, e := range b.Contract.Environments {
			names = append(names, e.Name)
		}
		want := []string{Test, Prod, "prod-eu", "prod-us"}
		switch {
		case v.Name() == "2.4.1":
			want = []string{Test}
		case v.Name() < "2.4.1":
			want = nil
		}
		if !slices.Equal(names, want) {
			t.Errorf("payments %s declares environments %v, want %v", v.Name(), names, want)
		}
	}
}

// TestB2RollbackIsBoundAndNeverRetried: the automatic rollback undoes exactly
// the promote of 2.4.0, and 2.4.0 is never promoted on that Stage again.
func TestB2RollbackIsBoundAndNeverRetried(t *testing.T) {
	b := beat(t, "B2")
	in, err := b.PlanInput(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cited := &delivery.CitedOp{Type: delivery.TypePromote, Applied: true,
		Pointer: delivery.PointerChange{Before: in.To.Digest, After: in.From.Digest}}
	if why := delivery.AdmitRollback(in.Spec, in.Status, cited, b.Actor.Class()); why != "" {
		t.Fatal(why)
	}
	if delivery.SetsAutoHold(in.Spec, b.Actor.Class(), in.Status) {
		t.Fatal("the controller's own rollback must not hold automation")
	}
	retry := beat(t, "B1")
	retry.Failed = true
	if p := plan(t, retry); p.Predicted != delivery.PredictRejected || refusal(p) != "FailedDigest" {
		t.Fatalf("2.4.0 must never be promoted again: %+v", p)
	}
}

func TestB3FailedVersionIsNeverRetried(t *testing.T) {
	retry := beat(t, "B3")
	retry.Failed = true
	if p := plan(t, retry); p.Predicted != delivery.PredictRejected || refusal(p) != "FailedDigest" {
		t.Fatalf("2.4.2 must never be promoted again: %+v", p)
	}
}

// TestB3FailedVersionNeverReachesProd: prod takes only what its test Stage
// offers, and test never offers a version that failed there.
func TestB3FailedVersionNeverReachesProd(t *testing.T) {
	b := Beat{ID: "B3-prod", Service: "payments", Environment: Prod, Actor: PayAgent, From: "2.4.0", To: "2.4.2", Running: before3}
	in, err := b.PlanInput(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ran := []string{ArtifactDigest("payments", "2.4.0")}
	in.Upstream = &delivery.StageStatus{Verified: ran, Offered: ran}
	if p := delivery.PlanFor(context.Background(), in); p.Predicted != delivery.PredictRejected || refusal(p) != delivery.GatePublished {
		t.Fatalf("2.4.2 must never reach prod: %+v", p)
	}
}

func TestB4PublishedVersionShipsToTest(t *testing.T) {
	b := beat(t, "B4")
	b.Unpublished = false
	if p := plan(t, b); p.Predicted != delivery.PredictProceeds {
		t.Fatalf("3.0.0 signed by pacto publish ships to test, where nothing depends on payments: %+v", p)
	}
}

// TestB6CheckoutShipsWithNobody: checkout 1.9 itself ships to prod with no
// person, which is what moves it before payments 3.0.0.
func TestB6CheckoutShipsWithNobody(t *testing.T) {
	b := Beat{ID: "B6-checkout", Service: "checkout", Environment: Prod, Actor: PayAgent, From: "1.8.0", To: "1.9.0",
		Running: map[string]string{"payments": "2.3.0", "orders": "1.4.0"}}
	if p := plan(t, b); p.Predicted != delivery.PredictProceeds || p.Floor.Human {
		t.Fatalf("checkout 1.9 accepts what runs and nothing it breaks runs: %+v", p)
	}
}

// TestB7OnlyAPersonOnTheAskedTeamDecides: the plan asks orders; pay-agent is
// not a person, alice is not on orders, carol is.
func TestB7OnlyAPersonOnTheAskedTeamDecides(t *testing.T) {
	asked := plan(t, beat(t, "B7")).Floor.Owners
	may := func(p Persona) bool {
		id := delivery.Identity{User: p.User, Groups: p.Groups}
		return Policy.IsHuman(id) && Policy.InTeams(p.Groups, asked)
	}
	if may(PayAgent) || may(Alice) || !may(Carol) || PayAgent.Class() != delivery.ClassAgent {
		t.Fatalf("asked %v: pay-agent %v, alice %v, carol %v", asked, may(PayAgent), may(Alice), may(Carol))
	}
}

// TestB9PersonsRollbackHoldsAutomation. The records themselves (signing,
// history, blame and undo) are proven by pkg/delivery's record tests; the
// story proves the decision they record.
func TestB9PersonsRollbackHoldsAutomation(t *testing.T) {
	b := beat(t, "B9")
	in, err := b.PlanInput(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if b.Actor.Class() != delivery.ClassHuman || !delivery.SetsAutoHold(in.Spec, b.Actor.Class(), in.Status) {
		t.Fatal("carol's rollback must hold automation off")
	}
}

func TestB10FrozenStageShipsNothingAndOnlyAPersonUnfreezes(t *testing.T) {
	agent := beat(t, "B10")
	agent.Actor = PayAgent
	if p := plan(t, agent); p.Predicted != delivery.PredictRejected || refusal(p) != "NotPermitted" {
		t.Fatalf("an agent may not unfreeze: %+v", p)
	}
	frozen := beat(t, "B1")
	frozen.Frozen = true
	if p := plan(t, frozen); p.Predicted != delivery.PredictWaits {
		t.Fatalf("a frozen Stage ships not even a compatible change: %+v", p)
	}
}

func TestBeatWithoutAPlan(t *testing.T) {
	if _, err := beat(t, "B8").PlanInput(context.Background()); err == nil {
		t.Fatal("B8 plans nothing")
	}
}

// TestPactifyWritesTheFraudContract: pacto init on plain/fraud writes the
// contract the story's fraud 1.0.0 is, so the pactify beat shows what
// pactifying really produces.
func TestPactifyWritesTheFraudContract(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fraud")
	if err := os.CopyFS(dir, os.DirFS("plain/fraud")); err != nil {
		t.Fatal(err)
	}
	r, err := pactify.Init(context.Background(), pactify.Options{Dir: dir, Owner: "risk", NoTransparencyLog: true, Plan: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(r.Files, func(f pactify.File) bool { return f.Path == "pacto.yaml" })
	if i < 0 {
		t.Fatalf("no pacto.yaml in the plan: %+v", r.Files)
	}
	got, err := contract.Parse(strings.NewReader(r.Files[i].Content))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("contracts/fraud/1.0.0/pacto.yaml")
	if err != nil {
		t.Fatal(err)
	}
	want, err := contract.Parse(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	// The summary is the one line the story adds by hand.
	want.Metadata = nil
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pacto init wrote\n%s\nwhich differs from contracts/fraud/1.0.0", r.Files[i].Content)
	}
}

// TestColdStartAsksToAcceptTheUnknown: the node agent saw orders call payments
// with no contract, so the breaking change waits and the question names orders
// and the pacto init command that gives it a contract.
func TestColdStartAsksToAcceptTheUnknown(t *testing.T) {
	b := beat(t, "cold-start")
	for _, s := range b.Uncontracted {
		if _, ok := b.Running[s]; ok {
			t.Errorf("%s runs with a contract and without one", s)
		}
	}
	q := plan(t, b).Question
	for _, want := range []string{"k8s:prod/Deployment/orders", "pacto init --cluster --namespace prod"} {
		if !strings.Contains(q, want) {
			t.Errorf("question %q does not name %q", q, want)
		}
	}
}

// TestRaiseIsDecidedByTheServiceTeam: the advise raise pay-agent asked for is
// answered neither by pay-agent nor by a person outside team risk, only by a
// person of team risk with a fresh login.
func TestRaiseIsDecidedByTheServiceTeam(t *testing.T) {
	b := beat(t, "advise")
	in, err := b.PlanInput(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p := delivery.PlanFor(context.Background(), in)
	spec := in.Spec
	spec.IntentDigest = p.IntentDigest
	st := delivery.OperationStatus{Phase: delivery.PhaseHeld, Plan: &p, Times: delivery.Times{Held: Now.Add(-time.Minute).Format(time.RFC3339)}}
	answer := func(by string, groups ...string) (bool, string) {
		a := delivery.ApprovalSpec{OperationUID: "op-advise", IntentDigest: p.IntentDigest, Decision: delivery.Approve,
			By: by, Groups: groups, AuthTime: Now.Format(time.RFC3339)}
		return delivery.ApprovalEligible(spec, st, "op-advise", a, Now, Policy, 15*time.Minute)
	}
	if ok, _ := answer(PayAgent.User, PayAgent.Groups...); ok {
		t.Error("pay-agent decided its own raise")
	}
	if ok, _ := answer(Alice.User, Alice.Groups...); ok {
		t.Error("alice, outside team risk, decided fraud's raise")
	}
	if ok, why := answer("rita@example.com", "team-risk"); !ok {
		t.Errorf("a person of team risk could not decide: %s", why)
	}
}
