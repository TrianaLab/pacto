package delivery

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// evalRunner is a fake eval runner: it answers *answer and keeps the last
// request body in *body.
func evalRunner(t *testing.T) (g Eval, answer *string, body *[]byte) {
	answer, body = new(string), new([]byte)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*body, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, *answer)
	}))
	t.Cleanup(srv.Close)
	return Eval{URL: srv.URL, Client: srv.Client()}, answer, body
}

// evalRan is a runner answer for a run of passed out of total cases.
func evalRan(passed, total int) string {
	b, _ := json.Marshal(evalAnswer{Subject: dA, Suite: "sha256:5e", Run: "https://evals.example/runs/7", Passed: passed, Total: total, Message: "two refunds wrong"})
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
	if v.Result != Pass || v.Citation["run"] != "https://evals.example/runs/7" || v.Citation["subject"] != dA ||
		v.Citation["passed"] != "50" || v.Citation["total"] != "50" || v.Citation["suite"] != "sha256:5e" || sent.Input.Subjects[0] != dA {
		t.Fatalf("all pass: %+v sent=%s", v, *body)
	}
	*answer = evalRan(48, 50)
	if v := g.Check(context.Background(), gateIn(), nil); v.Result != Fail || v.Message != "48 of 50 eval cases passed, 100% needed: two refunds wrong" {
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
	if v := g.Check(context.Background(), gateIn(), map[string]string{"minPassRate": "0.29"}); v.Result != Fail || v.Message != "28 of 100 eval cases passed, 29% needed: two refunds wrong" {
		t.Fatalf("29%% message: %+v", v)
	}

}

func TestEvalUnknown(t *testing.T) {
	g, answer, _ := evalRunner(t)
	unknown := map[string]string{
		"another version": `{"subject":"sha256:other","suite":"s","run":"r","passed":1,"total":1}`,
		"no subject":      `{"suite":"s","run":"r","passed":1,"total":1}`,
		"no run":          `{"subject":"` + dA + `","suite":"s","passed":1,"total":1}`,
		"no suite":        `{"subject":"` + dA + `","run":"r","passed":1,"total":1}`,
		"no cases":        `{"subject":"` + dA + `","suite":"s","run":"r","passed":0,"total":0}`,
		"too many passed": `{"subject":"` + dA + `","suite":"s","run":"r","passed":2,"total":1}`,
		"negative":        `{"subject":"` + dA + `","suite":"s","run":"r","passed":-1,"total":1}`,
		"garbage":         `nope`,
	}
	for name, a := range unknown {
		*answer = a
		if v := g.Check(context.Background(), gateIn(), nil); v.Result != Unknown {
			t.Errorf("%s: %+v", name, v)
		}
	}
	*answer = evalRan(50, 50)
	for _, bad := range []string{"x", "-0.1", "1.5", "NaN"} {
		if v := g.Check(context.Background(), gateIn(), map[string]string{"minPassRate": bad}); v.Result != Unknown {
			t.Errorf("minPassRate %q: %+v", bad, v)
		}
	}
	if v := (Eval{URL: "http://127.0.0.1:1", Client: g.Client}).Check(context.Background(), gateIn(), nil); v.Result != Unknown {
		t.Errorf("unreachable: %+v", v)
	}
}

func TestEvalThroughRunChecks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"subject":"`+dA+`","suite":"s","run":"r","passed":3,"total":3}`)
	}))
	defer srv.Close()
	gates, err := NewGates([]ProviderConfig{{Name: "evals", Type: ProviderEval, URL: srv.URL}}, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	vs := RunChecks(context.Background(), gates, []GateCheck{{Name: "evals", Provider: "evals"}}, gateIn())
	if Worst(vs) != Pass || vs[0].Citation["provider"] != "evals" || vs[0].Citation["run"] != "r" {
		t.Fatalf("%+v", vs)
	}
}
