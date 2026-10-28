/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// TestEvalGateReadsTheShippedSuite: the eval gate asks its runner about the
// suite the version under check ships, read through the Stage's apply
// repository, and a Stage or repository it cannot read is Unknown.
func TestEvalGateReadsTheShippedSuite(t *testing.T) {
	agent := stageObj(dA)
	lost := stageObj(dA)
	lost.Name, lost.Spec.ApplyRef.Name = "lost", "no-such-repo"
	e := newEnv(t, agent, lost, ociRepo("payments", dA, "", false))
	b := bundle("1.1.0")
	b.Contract.Checks = []contract.Check{{Name: "evals", File: "evals/suite.yaml"}}
	b.FS = fstest.MapFS{"evals/suite.yaml": {Data: []byte("cases: []\n")}}
	e.reg.bundles[contractRef(c2)] = b

	var asked delivery.GateInput
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Input delivery.GateInput `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		asked = q.Input
		algo, hex, _ := strings.Cut(q.Input.Subjects[0], ":")
		payload, _ := json.Marshal(delivery.EvalStatement{Type: delivery.StatementType, PredicateType: delivery.PredicateEval,
			Subject:   []delivery.Subject{{Name: "pay-agent", Digest: map[string]string{algo: hex}}},
			Predicate: delivery.EvalResult{Challenge: q.Input.Challenge, Suite: q.Input.Suite.Digest, Run: "r", Passed: 1, Total: 1}})
		_ = json.NewEncoder(w).Encode(map[string]any{"mediaType": delivery.BundleMediaType, "dsseEnvelope": map[string]any{
			"payload": payload, "payloadType": delivery.PayloadType, "signatures": []map[string]any{{"sig": "c2ln"}}}})
	}))
	defer srv.Close()
	// pkg/signing tests the runner's signature; this test only reads the suite.
	trusted := func([]byte) (string, error) { return "evals-ci", nil }
	gates := NewGates(e.op.Cluster, map[string]delivery.Gate{"evals": delivery.Eval{URL: srv.URL, Client: srv.Client(), Verify: trusted}, "mira": e.health})

	in := delivery.GateInput{Stage: delivery.StageRef{Namespace: ns, Name: agent.Name}, Subjects: []string{dB}, Challenge: "c1"}
	if v := gates["evals"].Check(context.Background(), in, nil); v.Result != delivery.Pass || asked.Contract != contractRef(c2) ||
		asked.Suite.Digest != "sha256:46f6697e1c211b6750b35512f33b92b69a700b74cda7e469e0bfd8ddd594b752" {
		t.Fatalf("%+v asked %+v", v, asked)
	}
	for name, want := range map[string]string{"gone": "reading the Stage", lost.Name: "reading the apply OCIRepository"} {
		in.Stage.Name = name
		if v := gates["evals"].Check(context.Background(), in, nil); v.Result != delivery.Unknown || !strings.Contains(v.Message, want) {
			t.Errorf("%s: %+v", name, v)
		}
	}
}

// TestMissingSuiteHoldsThePromote: a Stage check whose suite the version does
// not ship holds the promote for a person before anything moves; it is
// neither failed nor passed.
func TestMissingSuiteHoldsThePromote(t *testing.T) {
	e := baseEnv(t, nil)
	s := e.getStage()
	s.Spec.Gates.Verify.Checks = []delivery.GateCheck{{Name: "quality", Provider: "mira", Suite: "evals"}}
	if err := e.c.Update(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	op := opObj("p1", delivery.TypePromote, dA, dB, bot)
	op.Spec.IntentDigest = delivery.IntentDigest(op.Spec, s.Spec)
	e.create(op)
	o := e.drive("p1")
	if o.Status.Phase != delivery.PhaseHeld || o.Status.Plan.Floor.Decision != delivery.AcceptUnknown ||
		!strings.Contains(o.Status.Question, "the check quality runs the suite evals, but the contract of") {
		t.Fatalf("want Held for accept-unknown: %s %+v %q", o.Status.Phase, o.Status.Plan, o.Status.Question)
	}
	if e.getStage().Status.Pointer != dA {
		t.Fatal("a held promote must not move anything")
	}
}
