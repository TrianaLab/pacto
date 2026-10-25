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
	b.Contract.Agent = &contract.Agent{Evals: "evals/suite.yaml"}
	b.FS = fstest.MapFS{"evals/suite.yaml": {Data: []byte("cases: []\n")}}
	e.reg.bundles[contractRef(c2)] = b

	var asked delivery.GateInput
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Input delivery.GateInput `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		asked = q.Input
		_ = json.NewEncoder(w).Encode(map[string]any{"subject": q.Input.Subjects[0], "suite": q.Input.Evals.Digest, "run": "r", "passed": 1, "total": 1})
	}))
	defer srv.Close()
	gates := NewGates(e.op.Cluster, map[string]delivery.Gate{"evals": delivery.Eval{URL: srv.URL, Client: srv.Client()}, "mira": e.health})

	in := delivery.GateInput{Stage: delivery.StageRef{Namespace: ns, Name: agent.Name}, Subjects: []string{dB}}
	if v := gates["evals"].Check(context.Background(), in, nil); v.Result != delivery.Pass || asked.Contract != contractRef(c2) ||
		asked.Evals.Digest != "sha256:67fd4cc91da7c4d6870049981fed4d367327a4bb687e73666652796d80b533b8" {
		t.Fatalf("%+v asked %+v", v, asked)
	}
	for name, want := range map[string]string{"gone": "reading the Stage", lost.Name: "reading the apply OCIRepository"} {
		in.Stage.Name = name
		if v := gates["evals"].Check(context.Background(), in, nil); v.Result != delivery.Unknown || !strings.Contains(v.Message, want) {
			t.Errorf("%s: %+v", name, v)
		}
	}
}
