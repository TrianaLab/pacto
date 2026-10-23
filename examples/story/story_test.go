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
// validation and is a 2.1 contract with a publish section.
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
		if !res.IsValid() || b.Contract.PactoVersion != "2.1" || b.Contract.Publish == nil {
			t.Errorf("%s: version %s, publish %v, errors %v", p, b.Contract.PactoVersion, b.Contract.Publish != nil, res.Errors)
		}
		n++
		return nil
	})
	if err != nil || n != 9 {
		t.Fatalf("walked %d contracts: %v", n, err)
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
			if b.To != "" || b.Type == delivery.TypeUnfreeze {
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
	r, err := pactify.Init(context.Background(), pactify.Options{Dir: dir, Owner: "risk", Plan: true}, nil)
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
	// The two lines the story adds by hand.
	want.Publish.Sign.TransparencyLog, want.Metadata = nil, nil
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pacto init wrote\n%s\nwhich differs from contracts/fraud/1.0.0", r.Files[i].Content)
	}
}
