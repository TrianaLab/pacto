package delivery

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
)

// tieredSchema is a tiered offering's configuration schema: class and
// replicas are tunable, storageGB declares no tier, so it is contract.
const tieredSchema = `{"type":"object","additionalProperties":false,"properties":{
"class":{"enum":["small","large"],"x-pacto-tier":"tunable"},
"storageGB":{"type":"integer","minimum":10,"maximum":500},
"replicas":{"type":"integer","minimum":1,"maximum":5,"x-pacto-tier":"tunable"}}}`

// offeringPlan plans payments 1.1.0, whose app configuration takes its schema
// from the offering postgresql (extra adds files to the offering bundle) and
// whose prod environment file holds prod, on a Stage that asks a person to
// approve every promote.
func offeringPlan(t *testing.T, prod string, extra fstest.MapFS) (Plan, Verdict) {
	t.Helper()
	pg := bundle("postgresql", "16.1.0")
	pg.Contract.PactoVersion, pg.Contract.Service.Offering = "2.5", "postgresql"
	pg.Contract.Configurations = []contract.Configuration{{Name: "default", Schema: "schema.json"}}
	pgFS := fstest.MapFS{"schema.json": {Data: []byte(tieredSchema)}}
	for k, v := range extra {
		pgFS[k] = v
	}
	pg.FS = pgFS
	b := envBundle(map[string]string{"prod": "prod.yaml"}, map[string]string{"prod.yaml": prod}, false)
	b.Contract.Configurations[0].Values = map[string]any{"class": "small", "storageGB": 20}
	reg := mapRegistry{"reg.example/pactos/platform@" + c3: pg, "r/consumer@" + c1: b}
	to := ResolveRevision(t.Context(), reg, "r/a", dB)
	to.ContractRef = rev(dB, c2, b).ContractRef
	from := rev(dA, c1, bundle("payments", "1.0.0"))
	snap, err := Snapshot(t.Context(), nil, nil, []Revision{from, to}, now)
	if err != nil {
		t.Fatal(err)
	}
	stage := stageSpec()
	stage.Approval.Always = true
	p := PlanFor(t.Context(), PlanInput{Spec: promote(dA, dB), Stage: stage, From: from, To: to, Snapshot: snap, Class: ClassAgent,
		Status: StageStatus{Pointer: dA, Verified: []string{dA}, Published: []string{dB}, Level: LevelAutonomous}})
	return p, verdict(t, p, GateEnvironment)
}

// TestPlanEnvironmentRefusesTierNotTunable: an environment that changes a
// contract-tier field of an offering fails pacto.environment with the code,
// and the promote is rejected with nobody asked, even on a Stage that asks a
// person to approve every promote: no approval clears it.
func TestPlanEnvironmentRefusesTierNotTunable(t *testing.T) {
	p, v := offeringPlan(t, "app:\n  storageGB: 100\n", nil)
	if v.Result != Fail || v.Citation["code"] != string(finding.CodeTierNotTunable) ||
		!strings.HasPrefix(v.Message, "prod.yaml:2: TierNotTunable: environment prod: app.storageGB is contract-tier in offering postgresql 16.1.0") ||
		!strings.Contains(v.Message, "Next: set it once in configurations[0].values, or ask team-postgresql to mark it x-pacto-tier: tunable") {
		t.Fatalf("verdict %+v", v)
	}
	if p.Predicted != PredictRejected || p.Floor.Human || p.Question != "" {
		t.Fatalf("a parameter refusal rejects with nobody asked: %+v", p)
	}
}

// TestPlanEnvironmentRefusesParameterCodes: the three other parameter codes
// fail the gate the same way.
func TestPlanEnvironmentRefusesParameterCodes(t *testing.T) {
	for values, code := range map[string]finding.Code{
		"app:\n  replicas: 9\n": finding.CodeParameterOutOfRange,
		"app:\n  replica: 2\n":  finding.CodeParameterNotDeclared,
		"app:\n  class: smal\n": finding.CodeClassNotOffered,
	} {
		if p, v := offeringPlan(t, values, nil); v.Result != Fail || v.Citation["code"] != string(code) || p.Predicted != PredictRejected {
			t.Errorf("%s: verdict %+v, plan %s", code, v, p.Predicted)
		}
	}
}

// TestPlanNonOfferingErrorKeepsApprove: a values error that is no parameter
// code keeps the hold for the service's team to approve.
func TestPlanNonOfferingErrorKeepsApprove(t *testing.T) {
	p := planWith(t, promote(dA, dB), StageStatus{Pointer: dA, Verified: []string{dA}}, rev(dA, c1, bundle("payments", "1.0.0")),
		rev(dB, c2, envBundle(map[string]string{"prod": "prod.yaml"}, map[string]string{"prod.yaml": "app:\n  replicas: many\n"}, true)))
	if v := verdict(t, p, GateEnvironment); v.Result != Pass || v.Citation["code"] != "" || p.Floor.Decision != Approve || p.Predicted != PredictHeld {
		t.Fatalf("verdict %+v, plan %+v", v, p)
	}
	if code, ok := parameterRefusal([]string{"ENVIRONMENT_VALUES_INVALID"}); ok || code != "" {
		t.Fatalf("got %q %v", code, ok)
	}
}

// TestPlanInferredOfferingIsUnknown: values for an inferred offering are
// unverified, so the gate reads Unknown, never Pass, and a person accepts it.
func TestPlanInferredOfferingIsUnknown(t *testing.T) {
	p, v := offeringPlan(t, "app:\n  storageGB: 100\n", fstest.MapFS{contract.InferredRecordFile: {Data: []byte("format: pacto.inferred/v1\n")}})
	if v.Result != Unknown || !strings.Contains(v.Message, "the offering postgresql is inferred") || !p.Floor.Human || p.Predicted != PredictHeld {
		t.Fatalf("verdict %+v, plan %+v", v, p)
	}
}
