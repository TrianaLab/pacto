package delivery

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// suiteDigest is the sha256 of the suite agentRevision ships.
const suiteDigest = "sha256:67fd4cc91da7c4d6870049981fed4d367327a4bb687e73666652796d80b533b8"

// agentRevision is a version whose contract ships one check, evals/suite.yaml.
func agentRevision(context.Context, GateInput) Revision {
	return Revision{ContractRef: "oci://r/pay-agent@sha256:c0", Bundle: &contract.Bundle{
		Contract: &contract.Contract{Checks: []contract.Check{{Name: "evals", File: "evals/suite.yaml"}}},
		FS:       fstest.MapFS{"evals/suite.yaml": {Data: []byte("cases: []\n")}},
	}}
}

// evalRunner is a fake eval runner: it answers *answer and keeps the last
// request body in *body.
func evalRunner(t *testing.T) (g Eval, answer *string, body *[]byte) {
	answer, body = new(string), new([]byte)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*body, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, *answer)
	}))
	t.Cleanup(srv.Close)
	return Eval{URL: srv.URL, Client: srv.Client(), Revision: agentRevision}, answer, body
}

// evalRan is a runner answer for a run of passed out of total cases.
func evalRan(passed, total int) string {
	b, _ := json.Marshal(evalAnswer{Subject: dA, Suite: suiteDigest, Run: "https://evals.example/runs/7", Passed: passed, Total: total, Message: "two refunds wrong"})
	return string(b)
}

func TestEval(t *testing.T) {
	g, answer, body := evalRunner(t)

	*answer = evalRan(50, 50)
	v := g.Check(context.Background(), gateIn(), nil)
	var sent struct {
		Input GateInput `json:"input"`
	}
	_ = json.Unmarshal(*body, &sent)
	want := map[string]string{"run": "https://evals.example/runs/7", "subject": dA, "passed": "50", "total": "50",
		"suite": suiteDigest, "suiteName": "evals", "suitePath": "evals/suite.yaml", "contract": "oci://r/pay-agent@sha256:c0"}
	for k, w := range want {
		if v.Citation[k] != w {
			t.Errorf("citation %s = %q, want %q", k, v.Citation[k], w)
		}
	}
	if v.Result != Pass || sent.Input.Subjects[0] != dA || sent.Input.Contract != "oci://r/pay-agent@sha256:c0" ||
		*sent.Input.Suite != (Suite{Name: "evals", Path: "evals/suite.yaml", Digest: suiteDigest}) {
		t.Fatalf("all pass: %+v sent=%s", v, *body)
	}
	*answer = evalRan(48, 50)
	if v := g.Check(context.Background(), gateIn(), nil); v.Result != Fail || v.Message != "48 of 50 cases passed, 100% needed: two refunds wrong" {
		t.Fatalf("default needs every case: %+v", v)
	}
	if v := g.Check(context.Background(), gateIn(), map[string]string{"minPassRate": "0.95"}); v.Result != Pass {
		t.Fatalf("95%% needed: %+v", v)
	}
	*answer = evalRan(47, 50)
	if v := g.Check(context.Background(), gateIn(), map[string]string{"minPassRate": "0.95"}); v.Result != Fail || v.Citation["run"] == "" {
		t.Fatalf("below 95%%: %+v", v)
	}
}

// TestEvalRounding: the pass rate compares exactly, and the message rounds the
// percentage needed.
func TestEvalRounding(t *testing.T) {
	g, answer, _ := evalRunner(t)
	*answer = evalRan(7, 100)
	if v := g.Check(context.Background(), gateIn(), map[string]string{"minPassRate": "0.07"}); v.Result != Pass {
		t.Fatalf("exactly 7%% needed: %+v", v)
	}
	*answer = evalRan(28, 100)
	if v := g.Check(context.Background(), gateIn(), map[string]string{"minPassRate": "0.29"}); v.Result != Fail || v.Message != "28 of 100 cases passed, 29% needed: two refunds wrong" {
		t.Fatalf("29%% message: %+v", v)
	}

}

func TestEvalUnknown(t *testing.T) {
	g, answer, _ := evalRunner(t)
	unknown := map[string]string{
		"another version": `{"subject":"sha256:other","suite":"` + suiteDigest + `","run":"r","passed":1,"total":1}`,
		"no subject":      `{"suite":"` + suiteDigest + `","run":"r","passed":1,"total":1}`,
		"no run":          `{"subject":"` + dA + `","suite":"` + suiteDigest + `","passed":1,"total":1}`,
		"no suite":        `{"subject":"` + dA + `","run":"r","passed":1,"total":1}`,
		"no cases":        `{"subject":"` + dA + `","suite":"` + suiteDigest + `","run":"r","passed":0,"total":0}`,
		"too many passed": `{"subject":"` + dA + `","suite":"` + suiteDigest + `","run":"r","passed":2,"total":1}`,
		"negative":        `{"subject":"` + dA + `","suite":"` + suiteDigest + `","run":"r","passed":-1,"total":1}`,
		"garbage":         `nope`,
	}
	for name, a := range unknown {
		*answer = a
		if v := g.Check(context.Background(), gateIn(), nil); v.Result != Unknown {
			t.Errorf("%s: %+v", name, v)
		}
	}
	*answer = evalRan(50, 50)
	// 0 would pass a run where no case passed.
	for _, bad := range []string{"x", "-0.1", "0", "1.5", "NaN"} {
		if v := g.Check(context.Background(), gateIn(), map[string]string{"minPassRate": bad}); v.Result != Unknown {
			t.Errorf("minPassRate %q: %+v", bad, v)
		}
	}
	if v := (Eval{URL: "http://127.0.0.1:1", Client: g.Client, Revision: agentRevision}).Check(context.Background(), gateIn(), nil); v.Result != Unknown {
		t.Errorf("unreachable: %+v", v)
	}
}

func TestEvalThroughRunChecks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"subject":"`+dA+`","suite":"`+suiteDigest+`","run":"r","passed":3,"total":3}`)
	}))
	defer srv.Close()
	gates, err := NewGates([]ProviderConfig{{Name: "evals", Type: ProviderEval, URL: srv.URL}}, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	e := gates["evals"].(Eval)
	e.Revision = agentRevision
	gates["evals"] = e
	vs := RunChecks(context.Background(), gates, []GateCheck{{Name: "evals", Provider: "evals"}}, gateIn())
	if Worst(vs) != Pass || vs[0].Citation["provider"] != "evals" || vs[0].Citation["run"] != "r" {
		t.Fatalf("%+v", vs)
	}
}

// TestEvalSuiteBinding: a verdict counts only on the suite the version ships,
// by its sha256. Another suite, or a contract that cannot be read or ships no
// suite, is Unknown, and the runner is never asked.
func TestEvalSuiteBinding(t *testing.T) {
	g, answer, body := evalRunner(t)
	*answer = `{"subject":"` + dA + `","suite":"evals/suite.yaml","run":"r","passed":2,"total":2}`
	if v := g.Check(context.Background(), gateIn(), nil); v.Result != Unknown ||
		v.Message != `the runner ran the suite "evals/suite.yaml", not `+suiteDigest+`, the evals/suite.yaml the version ships` {
		t.Fatalf("a suite named by path, not digest: %+v", v)
	}
	*answer = evalRan(2, 2)
	bundle := func(c *contract.Contract, files fstest.MapFS) func(context.Context, GateInput) Revision {
		return func(context.Context, GateInput) Revision {
			return Revision{ContractRef: "oci://r/a@sha256:c0", Bundle: &contract.Bundle{Contract: c, FS: files}}
		}
	}
	for name, rev := range map[string]func(context.Context, GateInput) Revision{
		"no reader":     nil,
		"unreadable":    func(context.Context, GateInput) Revision { return Revision{Err: "registry down"} },
		"no checks":     bundle(&contract.Contract{}, fstest.MapFS{}),
		"suite missing": bundle(&contract.Contract{Checks: []contract.Check{{Name: "evals", File: "evals/suite.yaml"}}}, fstest.MapFS{}),
		"nil contract":  bundle(nil, fstest.MapFS{}),
	} {
		*body = nil
		g.Revision = rev
		if v := g.Check(context.Background(), gateIn(), nil); v.Result != Unknown || *body != nil {
			t.Errorf("%s: %+v asked=%v", name, v, *body != nil)
		}
	}
	g.Revision = agentRevision
	if v := g.Check(context.Background(), GateInput{}, nil); v.Result != Unknown {
		t.Errorf("no subject: %+v", v)
	}
}

// TestEvalSuiteByName: the suite param names which check to run; with more
// than one check and no name, or a name the version does not ship, the gate
// holds with a message that says so and never asks the runner.
func TestEvalSuiteByName(t *testing.T) {
	g, answer, body := evalRunner(t)
	two := &contract.Contract{Checks: []contract.Check{{Name: "smoke", File: "smoke.sh"}, {Name: "evals", File: "evals/suite.yaml"}}}
	g.Revision = func(context.Context, GateInput) Revision {
		return Revision{ContractRef: "oci://r/a@sha256:c0", Bundle: &contract.Bundle{Contract: two, FS: fstest.MapFS{
			"smoke.sh": {Data: []byte("true")}, "evals/suite.yaml": {Data: []byte("cases: []\n")}}}}
	}
	*answer = evalRan(2, 2)
	if v := g.Check(context.Background(), gateIn(), map[string]string{"suite": "evals"}); v.Result != Pass || v.Citation["suiteName"] != "evals" {
		t.Fatalf("named suite: %+v", v)
	}
	for params, want := range map[string]string{
		"":        "the contract oci://r/a@sha256:c0 ships more than one check: name the one to run in the suite param",
		"refunds": "the Stage runs the suite refunds, but the contract oci://r/a@sha256:c0 ships no check of that name",
	} {
		*body = nil
		p := map[string]string{}
		if params != "" {
			p["suite"] = params
		}
		if v := g.Check(context.Background(), gateIn(), p); v.Result != Unknown || v.Message != want || *body != nil {
			t.Errorf("suite %q: %+v", params, v)
		}
	}
	g.Revision = func(context.Context, GateInput) Revision {
		return Revision{ContractRef: "oci://r/a@sha256:c0", Bundle: &contract.Bundle{Contract: &contract.Contract{}}}
	}
	if v := g.Check(context.Background(), gateIn(), nil); v.Message != "the contract oci://r/a@sha256:c0 ships no checks, so there is no suite to run" {
		t.Errorf("no checks: %+v", v)
	}
}
