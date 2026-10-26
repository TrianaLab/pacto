package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
	return Eval{URL: srv.URL, Client: srv.Client(), Revision: agentRevision, Verify: trustRunner}, answer, body
}

// runnerSig is the signature trustRunner accepts; pkg/signing tests the crypto.
const runnerSig = "c2ln"

// trustRunner accepts a bundle whose one signature is runnerSig.
func trustRunner(b []byte) (string, error) {
	var e envelope
	_ = json.Unmarshal(b, &e)
	if len(e.DSSEEnvelope.Signatures) != 1 || !strings.Contains(string(e.DSSEEnvelope.Signatures[0]), `"`+runnerSig+`"`) {
		return "", errors.New("no trusted runner key or identity signed it")
	}
	return "evals-runner", nil
}

// signedBy is a runner's answer: a bundle over st with the signature sig.
func signedBy(sig string, st any) string {
	payload, _ := json.Marshal(st)
	b, _ := json.Marshal(map[string]any{"mediaType": BundleMediaType, "dsseEnvelope": map[string]any{
		"payload": payload, "payloadType": PayloadType, "signatures": []map[string]any{{"sig": sig}}}})
	return string(b)
}

// evalStatement is a runner's statement that it ran suite on subject.
func evalStatement(subject string, r EvalResult) EvalStatement {
	if r.Operation == "" {
		r.Operation = gateIn().OperationUID
	}
	st := EvalStatement{Type: StatementType, PredicateType: PredicateEval, Predicate: r}
	if algo, hex, ok := strings.Cut(subject, ":"); ok {
		st.Subject = []Subject{{Name: "version", Digest: map[string]string{algo: hex}}}
	}
	return st
}

// signed is a trusted runner's answer about subject.
func signed(subject string, r EvalResult) string {
	return signedBy(runnerSig, evalStatement(subject, r))
}

// evalRan is a runner answer for a run of passed out of total cases.
func evalRan(passed, total int) string {
	return signed(dA, EvalResult{Suite: suiteDigest, Run: "https://evals.example/runs/7", Passed: passed, Total: total, Message: "two refunds wrong"})
}

func TestEval(t *testing.T) {
	g, answer, body := evalRunner(t)

	*answer = evalRan(50, 50)
	v := g.Check(context.Background(), gateIn(), nil)
	var sent struct {
		Input GateInput `json:"input"`
	}
	_ = json.Unmarshal(*body, &sent)
	want := map[string]string{"run": "https://evals.example/runs/7", "subject": dA, "passed": "50", "total": "50", "runner": "evals-runner",
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
	if runner, err := CheckRunner(v, trustRunner); err != nil || runner != "evals-runner" {
		t.Fatalf("the verdict keeps the runner's signed answer: %q %v", runner, err)
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
	r := func(run string, passed, total int) EvalResult {
		return EvalResult{Suite: suiteDigest, Run: run, Passed: passed, Total: total}
	}
	unknown := map[string]string{
		"another version": signed("sha256:other", r("r", 1, 1)),
		"no subject":      signed("", r("r", 1, 1)),
		"another op":      signed(dA, EvalResult{Operation: "u0", Suite: suiteDigest, Run: "r", Passed: 1, Total: 1}),
		"no run":          signed(dA, r("", 1, 1)),
		"no suite":        signed(dA, EvalResult{Run: "r", Passed: 1, Total: 1}),
		"no cases":        signed(dA, r("r", 0, 0)),
		"too many passed": signed(dA, r("r", 2, 1)),
		"negative":        signed(dA, r("r", -1, 1)),
		"garbage":         `nope`,
		"unsigned":        `{"subject":"` + dA + `","suite":"` + suiteDigest + `","run":"r","passed":1,"total":1}`,
		"untrusted":       signedBy("Zm9yZ2Vk", evalStatement(dA, r("r", 1, 1))),
		"a record":        signedBy(runnerSig, map[string]any{"_type": StatementType, "predicateType": PredicateOperation, "predicate": map[string]any{}}),
		"too large":       signed(dA, EvalResult{Suite: suiteDigest, Run: strings.Repeat("r", maxRunnerAnswer), Passed: 1, Total: 1}),
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
	if v := (Eval{URL: "http://127.0.0.1:1", Client: g.Client, Revision: agentRevision, Verify: trustRunner}).Check(context.Background(), gateIn(), nil); v.Result != Unknown {
		t.Errorf("unreachable: %+v", v)
	}
	g.Verify = nil
	if v := g.Check(context.Background(), gateIn(), nil); v.Result != Unknown || !strings.Contains(v.Message, "trusts no runner key") {
		t.Errorf("no runner trusted: %+v", v)
	}
}

func TestEvalThroughRunChecks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, signed(dA, EvalResult{Suite: suiteDigest, Run: "r", Passed: 3, Total: 3}))
	}))
	defer srv.Close()
	open := func(TrustedKey) (func([]byte) error, error) {
		return func(b []byte) error { _, err := trustRunner(b); return err }, nil
	}
	gates, err := NewGates([]ProviderConfig{{Name: "evals", Type: ProviderEval, URL: srv.URL, Signers: []TrustedKey{{Name: "ci"}}}}, srv.Client(), open)
	if err != nil {
		t.Fatal(err)
	}
	e := gates["evals"].(Eval)
	e.Revision = agentRevision
	gates["evals"] = e
	vs := RunChecks(context.Background(), gates, []GateCheck{{Name: "evals", Provider: "evals"}}, gateIn())
	if Worst(vs) != Pass || vs[0].Citation["provider"] != "evals" || vs[0].Citation["run"] != "r" || vs[0].Citation["runner"] != "ci" {
		t.Fatalf("%+v", vs)
	}
}

// TestEvalSuiteBinding: a verdict counts only on the suite the version ships,
// by its sha256. Another suite, or a contract that cannot be read or ships no
// suite, is Unknown, and the runner is never asked.
func TestEvalSuiteBinding(t *testing.T) {
	g, answer, body := evalRunner(t)
	*answer = signed(dA, EvalResult{Suite: "evals/suite.yaml", Run: "r", Passed: 2, Total: 2})
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
		"":        "the contract oci://r/a@sha256:c0 ships more than one check: name the one to run in the Stage check's suite",
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

// TestEvalSuitePin: a Stage that pins the suite's digest gets Unknown, and
// the runner is never asked, when the version ships other bytes.
func TestEvalSuitePin(t *testing.T) {
	g, answer, body := evalRunner(t)
	*answer = evalRan(2, 2)
	if v := g.Check(context.Background(), gateIn(), map[string]string{"suiteDigest": suiteDigest}); v.Result != Pass {
		t.Fatalf("the pinned suite: %+v", v)
	}
	*body = nil
	pin := "sha256:" + strings.Repeat("0", 64)
	v := g.Check(context.Background(), gateIn(), map[string]string{"suiteDigest": pin})
	if v.Result != Unknown || *body != nil || !strings.Contains(v.Message, "pins the suite evals to "+pin+", but this version ships "+suiteDigest) {
		t.Fatalf("another suite: %+v asked=%v", v, *body != nil)
	}
	g.Revision = func(context.Context, GateInput) Revision {
		return Revision{ContractRef: "oci://r/a@sha256:c0", Bundle: &contract.Bundle{Contract: &contract.Contract{
			Checks: []contract.Check{{Name: "evals", File: "evals/suite.yaml"}}}}}
	}
	if v := g.Check(context.Background(), gateIn(), nil); v.Result != Unknown || !strings.Contains(v.Message, "carries no files") {
		t.Fatalf("a bundle with no files: %+v", v)
	}
}

// TestEvalObservedModel: a model the runner saw that the contract's external
// dependencies do not name fails the check; a Stage that asks which model
// answered gets Unknown when nobody can tell.
func TestEvalObservedModel(t *testing.T) {
	g, answer, _ := evalRunner(t)
	g.Revision = func(context.Context, GateInput) Revision {
		r := agentRevision(context.Background(), GateInput{})
		r.Bundle.Contract.Dependencies = []contract.Dependency{{Name: "orders", Ref: "oci://r/orders"},
			{Name: "model", External: true, Version: "anthropic/claude-haiku-4-5"}}
		return r
	}
	ran := func(model string) string {
		return signed(dA, EvalResult{Suite: suiteDigest, Run: "r", Passed: 2, Total: 2, ObservedModel: model})
	}
	ask := map[string]string{"requireObservedModel": "true"}
	cases := []struct {
		name, model string
		params      map[string]string
		want        Result
		msg         string
	}{
		{"the declared model", "anthropic/claude-haiku-4-5", ask, Pass, ""},
		{"another model", "anthropic/claude-sonnet-4-5", nil, Fail, "the runner saw the model anthropic/claude-sonnet-4-5 answer, but the contract declares anthropic/claude-haiku-4-5"},
		{"not said, not asked", "", nil, Pass, ""},
		{"not said, asked", "", ask, Unknown, "the Stage asks which model answered, and the runner did not say"},
	}
	for _, c := range cases {
		*answer = ran(c.model)
		v := g.Check(context.Background(), gateIn(), c.params)
		if v.Result != c.want || !strings.Contains(v.Message, c.msg) || v.Citation["observedModel"] != c.model {
			t.Errorf("%s: %+v", c.name, v)
		}
	}
	g.Revision = agentRevision // declares no model
	*answer = ran("anthropic/claude-haiku-4-5")
	if v := g.Check(context.Background(), gateIn(), nil); v.Result != Pass {
		t.Errorf("nothing to compare, not asked: %+v", v)
	}
	if v := g.Check(context.Background(), gateIn(), ask); v.Result != Unknown || !strings.Contains(v.Message, "no external dependency with a version") {
		t.Errorf("nothing to compare, asked: %+v", v)
	}
}

// TestCheckRunner: an auditor re-checks the runner's signature on the answer
// a verdict keeps, and that the verdict cites what the runner signed.
func TestCheckRunner(t *testing.T) {
	g, answer, _ := evalRunner(t)
	*answer = evalRan(2, 2)
	v := g.Check(context.Background(), gateIn(), nil)
	v.Citation["passed"] = "3"
	if _, err := CheckRunner(v, trustRunner); err == nil || !strings.Contains(err.Error(), `cites passed "3", but evals-runner signed "2"`) {
		t.Fatalf("an edited count: %v", err)
	}
	if _, err := CheckRunner(v, func([]byte) (string, error) { return "", errors.New("unknown key") }); err == nil {
		t.Fatal("an untrusted runner")
	}
	if _, err := CheckRunner(Verdict{}, trustRunner); err == nil {
		t.Fatal("no signed answer")
	}
}
