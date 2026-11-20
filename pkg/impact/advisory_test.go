package impact

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const advisoryUsersV1 = `openapi: "3.0.0"
paths:
  /users:
    get:
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
                required: [id, email]
                properties:
                  id: {type: string}
                  email: {type: string}
`

// advisoryFS is a bundle FS whose record infers the OpenAPI spec it holds;
// dropEmail drops the required response field email from it.
func advisoryFS(dropEmail bool) fstest.MapFS {
	spec := advisoryUsersV1
	if dropEmail {
		spec = strings.Replace(strings.Replace(spec, "[id, email]", "[id]", 1), "                  email: {type: string}\n", "", 1)
	}
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Commit: "a", Pacto: "4.6.0", Discovery: "1"},
		Fields: []contract.Inferred{{Field: "interfaces[api]", Value: "openapi openapi/v1.yaml", Source: "openapi/v1.yaml:1",
			Confidence: contract.ConfidenceLikely, Declare: "interfaces:\n  - name: api\n"}}}
	return fstest.MapFS{contract.InferredRecordFile: {Data: rec.Marshal()}, "openapi/v1.yaml": {Data: []byte(spec)}}
}

func TestInferredAdvisoryGateUnverified(t *testing.T) {
	snap := buildChain(t)
	old := svcContract("auth-service", "1.0.0", contract.Owner{Team: "platform"})
	newC := svcContract("auth-service", "1.1.0", contract.Owner{Team: "platform"})
	res := Analyze(context.Background(), old, newC, advisoryFS(false), advisoryFS(true), snap, Options{})
	if res.Classification != "NON_BREAKING" || len(res.BreakingChanges) != 0 || len(res.Advisories) != 1 {
		t.Fatalf("classification %s, breaking %+v, advisories %+v", res.Classification, res.BreakingChanges, res.Advisories)
	}
	if res.Gate != GateUnverified || res.GateCode() != GateCodeInferredPossibleBreak ||
		!strings.Contains(res.GateReason, "possible break on inferred interface api (openapi/v1.yaml:1)") {
		t.Errorf("gate %s %s: %s", res.Gate, res.GateCode(), res.GateReason)
	}
	if l := res.Advisories[0].Line(res.Service); !strings.HasPrefix(l, "possible break (inferred) auth-service api openapi/v1.yaml:1: ") ||
		!strings.HasSuffix(l, "; consumers (inferred, unknown): api-gateway") {
		t.Errorf("line %q", l)
	}
}

func TestInferredAdvisoryNeverClearsOrBlocks(t *testing.T) {
	snap := buildChain(t)
	old := svcContract("auth-service", "1.0.0", contract.Owner{Team: "platform"})
	newC := svcContract("auth-service", "1.1.0", contract.Owner{Team: "platform"})
	// No advisory: clear as before.
	if res := Analyze(context.Background(), old, newC, advisoryFS(false), advisoryFS(false), snap, Options{}); res.Gate != GateClear || len(res.Advisories) != 0 {
		t.Errorf("no advisory: gate %s, advisories %+v", res.Gate, res.Advisories)
	}
	// A blocking BREAKING answer stays blocked and still lists the advisory.
	old.Interfaces = []contract.Interface{{Name: "auth-api", Type: contract.InterfaceTypeGRPC, Ref: "auth.proto"}}
	newC.Service.Version = "2.0.0"
	res := Analyze(context.Background(), old, newC, advisoryFS(false), advisoryFS(true), snap, Options{})
	if res.Gate != GateBlocked || res.GateCode() != GateCodeConsumersIncompatible || len(res.Advisories) != 1 {
		t.Errorf("blocking answer: gate %s %s, advisories %+v", res.Gate, res.GateCode(), res.Advisories)
	}
}

func TestInferredAdvisoryConsumersInferredUnknown(t *testing.T) {
	snap := buildChain(t)
	old := svcContract("auth-service", "1.0.0", contract.Owner{Team: "platform"})
	newC := svcContract("auth-service", "1.1.0", contract.Owner{Team: "platform"})
	plain := Analyze(context.Background(), old, newC, advisoryFS(false), advisoryFS(false), snap, Options{})
	res := Analyze(context.Background(), old, newC, advisoryFS(false), advisoryFS(true), snap, Options{})
	got := res.Advisories[0].Consumers
	if len(got) != 1 || got[0] != (AdvisoryConsumer{Service: "api-gateway", Confidence: ConfidenceInferred, Compatibility: CompatibilityUnknown}) {
		t.Errorf("advisory consumers %+v (only the direct one, inferred and unknown)", got)
	}
	if !reflect.DeepEqual(res.ConsumerTallies, plain.ConsumerTallies) || res.Blocking() != plain.Blocking() || len(res.Consumers) != len(plain.Consumers) {
		t.Errorf("the advisory changed the tallies: %+v vs %+v", res.ConsumerTallies, plain.ConsumerTallies)
	}
	if l := (InferredAdvisory{}).Line("svc"); !strings.HasSuffix(l, "consumers (inferred, unknown): none") {
		t.Errorf("line %q", l)
	}
}
