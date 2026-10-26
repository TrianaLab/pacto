package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	t0 = time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	t1 = t0.Add(10 * time.Minute)
)

func gateIn() GateInput {
	return GateInput{OperationUID: "u1", Stage: StageRef{Namespace: "prod", Name: "payments"},
		Service: ServiceRef{Name: "payments"}, Facet: FacetPointer, Subjects: []string{dA}, Window: Window{From: t0, To: t1}, Challenge: "c1"}
}

// gateFunc is a Gate from a function, for tests.
type gateFunc func(context.Context, GateInput, map[string]string) Verdict

func (f gateFunc) Check(ctx context.Context, in GateInput, p map[string]string) Verdict {
	return f(ctx, in, p)
}

func TestNewGates(t *testing.T) {
	gates, err := NewGates([]ProviderConfig{{Name: "mira", Type: ProviderMira, URL: "http://mira:4318", AllowPlainHTTP: true},
		{Name: "dd", Type: ProviderHTTP, URL: "https://hooks.example/gate"},
		{Name: "local", Type: ProviderHTTP, URL: "http://127.0.0.1:8080/gate"},
		{Name: "local6", Type: ProviderHTTP, URL: "http://[::1]:8080/gate"},
		{Name: "localhost", Type: ProviderEval, URL: "http://localhost:8080", Signers: ci}}, nil, openAny)
	if err != nil || len(gates) != 5 {
		t.Fatalf("valid providers: %v %v", gates, err)
	}
	if _, ok := gates["mira"].(Mira); !ok {
		t.Errorf("mira is %T", gates["mira"])
	}
	if _, ok := gates["dd"].(Webhook); !ok {
		t.Errorf("dd is %T", gates["dd"])
	}
	if e, ok := gates["localhost"].(Eval); !ok || e.Verify == nil {
		t.Errorf("an eval provider checks its runner's signature: %#v", gates["localhost"])
	}
	bad := map[string][]ProviderConfig{
		"no name":   {{Type: ProviderMira, URL: "http://m"}},
		"reserved":  {{Name: "pacto.runtime", Type: ProviderMira, URL: "http://m"}},
		"duplicate": {{Name: "m", Type: ProviderMira, URL: "https://m"}, {Name: "m", Type: ProviderHTTP, URL: "https://m"}},
		"type":      {{Name: "m", Type: "prometheus", URL: "https://m"}},
		"scheme":    {{Name: "m", Type: ProviderMira, URL: "file:///etc/passwd"}},
		"no host":   {{Name: "m", Type: ProviderMira, URL: "http://"}},
		"unparsed":  {{Name: "m", Type: ProviderMira, URL: "http://[::1"}},
		"userinfo":  {{Name: "m", Type: ProviderHTTP, URL: "https://user:token@gate.internal/check"}},
		"http":      {{Name: "m", Type: ProviderEval, URL: "http://eval-runner:8080", Signers: ci}},
		"unsigned":  {{Name: "m", Type: ProviderEval, URL: "https://eval-runner"}},
		"unnamed":   {{Name: "m", Type: ProviderEval, URL: "https://eval-runner", Signers: []TrustedKey{{PublicKey: "k"}}}},
		"bad key":   {{Name: "m", Type: ProviderEval, URL: "https://eval-runner", Signers: []TrustedKey{{Name: "bad"}}}},
		"signed":    {{Name: "m", Type: ProviderHTTP, URL: "https://m", Signers: ci}},
	}
	for name, cfg := range bad {
		if _, err := NewGates(cfg, http.DefaultClient, openAny); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := NewGates(bad["http"], nil, openAny); err == nil || !strings.Contains(err.Error(), "plain http") {
		t.Errorf("plain http off loopback: %v", err)
	}
	if _, err := NewGates(bad["unsigned"], nil, openAny); err == nil || !strings.Contains(err.Error(), "whoever ran the exam must sign") {
		t.Errorf("an unsigned eval provider: %v", err)
	}
}

// ci is a runner key, and openAny opens any key but one named bad.
var ci = []TrustedKey{{Name: "ci", PublicKey: "k"}}

func openAny(k TrustedKey) (func([]byte) error, error) {
	if k.Name == "bad" {
		return nil, errors.New("no key")
	}
	return func([]byte) error { return nil }, nil
}

func TestParseProviders(t *testing.T) {
	ps, err := ParseProviders(`[{"name":"mira","type":"mira","url":"http://m"}]`)
	if err != nil || len(ps) != 1 || ps[0].URL != "http://m" {
		t.Fatalf("%+v %v", ps, err)
	}
	if ps, err := ParseProviders(""); err != nil || ps != nil {
		t.Fatalf("empty: %+v %v", ps, err)
	}
	if _, err := ParseProviders(`{`); err == nil {
		t.Fatal("bad JSON accepted")
	}
}

// TestRunChecksRunsAtOnce: every check waits until the other started, so run
// one after another they would time out; at once they both pass.
func TestRunChecksRunsAtOnce(t *testing.T) {
	var started sync.WaitGroup
	started.Add(2)
	both := gateFunc(func(ctx context.Context, in GateInput, _ map[string]string) Verdict {
		started.Done()
		done := make(chan struct{})
		go func() { started.Wait(); close(done) }()
		select {
		case <-done:
			return Verdict{Result: Pass, Citation: map[string]string{"query": in.Service.Name}}
		case <-time.After(5 * time.Second):
			return Verdict{Result: Unknown, Message: "ran alone"}
		}
	})
	vs := RunChecks(context.Background(), map[string]Gate{"g": both},
		[]GateCheck{{Name: "a", Provider: "g"}, {Name: "b", Provider: "g"}}, gateIn())
	if len(vs) != 2 || vs[0].Gate != "a" || vs[1].Gate != "b" || vs[0].Result != Pass || vs[1].Result != Pass {
		t.Fatalf("want both checks to pass, in order: %+v", vs)
	}
}

func TestRunChecks(t *testing.T) {
	pass := gateFunc(func(_ context.Context, in GateInput, p map[string]string) Verdict {
		return Verdict{Result: Pass, Message: "ok " + p["k"], Citation: map[string]string{"query": in.Service.Name}}
	})
	empty := gateFunc(func(context.Context, GateInput, map[string]string) Verdict {
		return Verdict{Result: Pass, Message: "ok"}
	})
	gates := map[string]Gate{"good": pass, "empty": empty}
	checks := []GateCheck{{Name: "health", Provider: "good", Params: map[string]string{"k": "v"}},
		{Name: "hollow", Provider: "empty"}, {Name: "nowhere", Provider: "absent"}}
	vs := RunChecks(context.Background(), gates, checks, gateIn())
	if len(vs) != 3 {
		t.Fatalf("%+v", vs)
	}
	if vs[0].Gate != "health" || vs[0].Phase != "verify" || vs[0].Result != Pass || vs[0].Citation["provider"] != "good" ||
		vs[0].Citation["query"] != "payments" || !strings.Contains(vs[0].Citation["input"], `"operationUID":"u1"`) ||
		vs[0].Citation["params"] != `{"k":"v"}` {
		t.Errorf("pass: %+v", vs[0])
	}
	if vs[1].Result != Unknown || !strings.Contains(vs[1].Message, "no citation") {
		t.Errorf("a Pass with no citation must be Unknown: %+v", vs[1])
	}
	if vs[2].Result != Unknown || !strings.Contains(vs[2].Message, `no gate provider named "absent"`) {
		t.Errorf("unknown provider: %+v", vs[2])
	}
}

// TestCheckTimeout: a check that does not answer in time is Unknown, even when
// the gate ignores its context and would have said Pass.
func TestCheckTimeout(t *testing.T) {
	defer func(d time.Duration) { CheckTimeout = d }(CheckTimeout)
	CheckTimeout = 10 * time.Millisecond
	release := make(chan struct{})
	defer close(release)
	slow := gateFunc(func(context.Context, GateInput, map[string]string) Verdict {
		<-release
		return Verdict{Result: Pass, Citation: map[string]string{"q": "x"}}
	})
	v := RunChecks(context.Background(), map[string]Gate{"slow": slow}, []GateCheck{{Name: "health", Provider: "slow"}}, gateIn())[0]
	if v.Result != Unknown || !strings.Contains(v.Message, "did not answer within 10ms") || v.Citation["provider"] != "slow" {
		t.Fatalf("a timed-out check: %+v", v)
	}
}

// TestCheckPanic: a gate that panics is Unknown, not a crashed promoter.
func TestCheckPanic(t *testing.T) {
	boom := gateFunc(func(context.Context, GateInput, map[string]string) Verdict { panic("index out of range") })
	v := RunChecks(context.Background(), map[string]Gate{"boom": boom}, []GateCheck{{Name: "health", Provider: "boom"}}, gateIn())[0]
	if v.Result != Unknown || !strings.Contains(v.Message, "the check failed: index out of range") {
		t.Fatalf("a panicking check: %+v", v)
	}
}

func TestWorst(t *testing.T) {
	vs := []Verdict{{Result: Pass}, {Result: Unknown}}
	if Worst(vs) != Unknown || Worst(nil) != Pass || Worst([]Verdict{{Result: Pass}, {Result: Fail}}) != Fail {
		t.Error("Worst")
	}
}

func TestRerun(t *testing.T) {
	var got GateInput
	var gotParams map[string]string
	fail := gateFunc(func(_ context.Context, in GateInput, p map[string]string) Verdict {
		got, gotParams = in, p
		return Verdict{Result: Fail, Message: "errors", Citation: map[string]string{"q": "x"}}
	})
	gates := map[string]Gate{"mira": fail, GateRuntime: fail}
	checks := []GateCheck{{Name: "health", Provider: "mira", Params: map[string]string{"maxErrorRatio": "0.05"}}}
	cited := RunChecks(context.Background(), gates, checks, gateIn())[0]

	// The cited window is kept; service, subjects and params are this Stage's own.
	base := gateIn()
	base.Window, base.Subjects = Window{From: t0.Add(-time.Minute), To: t1.Add(time.Minute)}, []string{dB}
	cited.Citation["params"] = `{"maxErrorRatio":"0"}`
	v := Rerun(context.Background(), gates, checks, base, cited)
	if v.Result != Fail || v.Gate != "health" || !got.Window.From.Equal(t0) || got.Subjects[0] != dB || gotParams["maxErrorRatio"] != "0.05" {
		t.Fatalf("rerun: %+v input=%+v params=%v", v, got, gotParams)
	}
	// A built-in gate needs no Stage check, and with no cited window it judges the base one.
	if v := Rerun(context.Background(), gates, nil, base, Verdict{Gate: GateRuntime}); v.Result != Fail || !got.Window.From.Equal(base.Window.From) {
		t.Errorf("built-in: %+v", v)
	}
	// A window from before the version under check was applied says nothing
	// about it, and neither does one that ends in the future.
	for name, w := range map[string]Window{
		"before": {From: t0.Add(time.Second), To: t1.Add(time.Minute)},
		"after":  {From: t0.Add(-time.Minute), To: t1.Add(-time.Second)},
	} {
		b := base
		b.Window = w
		if v := Rerun(context.Background(), gates, checks, b, cited); v.Result != Unknown || !strings.Contains(v.Message, "outside") {
			t.Errorf("%s: %+v", name, v)
		}
	}
	for name, c := range map[string]Verdict{
		"unknown gate": {Gate: "latency"},
		"bad input":    {Gate: "health", Citation: map[string]string{"input": "{"}},
	} {
		if v := Rerun(context.Background(), gates, checks, base, c); v.Result != Unknown {
			t.Errorf("%s: %+v", name, v)
		}
	}
	if v := Rerun(context.Background(), map[string]Gate{}, checks, base, Verdict{Gate: "health"}); v.Result != Unknown {
		t.Errorf("provider gone: %+v", v)
	}
}

func TestWebhook(t *testing.T) {
	var body []byte
	answer, status := `{"subject":"`+dA+`","result":"Fail","message":"p99 too high","citation":{"query":"q"},"extra":1}`, 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}))
	defer srv.Close()
	g := Webhook{URL: srv.URL, Client: srv.Client()}
	v := g.Check(context.Background(), gateIn(), map[string]string{"slo": "99"})
	var sent struct {
		Input  GateInput         `json:"input"`
		Params map[string]string `json:"params"`
	}
	_ = json.Unmarshal(body, &sent)
	if v.Result != Fail || v.Citation["query"] != "q" || v.Citation["subject"] != dA || sent.Input.OperationUID != "u1" || sent.Params["slo"] != "99" {
		t.Fatalf("%+v sent=%s", v, body)
	}
	// A Fail needs no citation of its own; it still names the version judged.
	answer = `{"subject":"` + dA + `","result":"Fail","message":"down"}`
	if v := g.Check(context.Background(), gateIn(), nil); v.Result != Fail || v.Citation["subject"] != dA {
		t.Fatalf("a fail with no citation: %+v", v)
	}
	answer = `{"subject":"` + dA + `","result":"Pass"}`
	// A pass with no citation of its own is Unknown through RunChecks: the
	// subject the gate adds is nothing to re-run.
	if vs := RunChecks(context.Background(), map[string]Gate{"w": g}, []GateCheck{{Name: "w", Provider: "w"}}, gateIn()); vs[0].Result != Unknown ||
		!strings.Contains(vs[0].Message, "no citation to re-run") {
		t.Fatalf("a pass with no citation of its own: %+v", vs[0])
	}
	for name, a := range map[string]struct {
		body   string
		status int
	}{"status": {`{}`, 503}, "garbage": {`nope`, 200}, "result": {`{"subject":"` + dA + `","result":"Maybe"}`, 200},
		"no subject": {`{"result":"Pass","citation":{"q":"1"}}`, 200}, "other subject": {`{"subject":"` + dB + `","result":"Fail"}`, 200}} {
		answer, status = a.body, a.status
		if v := g.Check(context.Background(), gateIn(), nil); v.Result != Unknown {
			t.Errorf("%s: %+v", name, v)
		}
	}
	// A provider's url and answer never reach a verdict message beyond its host and path.
	answer, status = `secret body`, 500
	if v := (Webhook{URL: srv.URL + "/check?apikey=k1", Client: srv.Client()}).Check(context.Background(), gateIn(), nil); strings.Contains(v.Message, "k1") ||
		strings.Contains(v.Message, "secret") || !strings.Contains(v.Message, "/check answered 500") {
		t.Errorf("leaky message: %q", v.Message)
	}
	if v := (Webhook{URL: "http://127.0.0.1:1/x?apikey=k1", Client: srv.Client()}).Check(context.Background(), gateIn(), nil); v.Result != Unknown || strings.Contains(v.Message, "k1") {
		t.Errorf("unreachable: %+v", v)
	}
	if v := (Webhook{URL: "http://[::1", Client: srv.Client()}).Check(context.Background(), gateIn(), nil); v.Result != Unknown {
		t.Errorf("bad url: %+v", v)
	}
}

// TestRunChecksSuite: the provider gets the suite from the check's typed
// field only; a "suite" param is dropped, so no check runs a suite the
// pacto.suites pre-gate never looked at.
func TestRunChecksSuite(t *testing.T) {
	echo := gateFunc(func(_ context.Context, _ GateInput, p map[string]string) Verdict {
		return Verdict{Result: Pass, Message: p["suite"], Citation: map[string]string{"run": "1"}}
	})
	gates := map[string]Gate{"runner": echo}
	vs := RunChecks(context.Background(), gates, []GateCheck{
		{Name: "typed", Provider: "runner", Suite: "evals", Params: map[string]string{"suite": "other"}},
		{Name: "param", Provider: "runner", Params: map[string]string{"suite": "other", "suiteDigest": "sha256:x", "requireObservedModel": "true"}},
		{Name: "pinned", Provider: "runner", SuiteDigest: "sha256:y", Params: map[string]string{"suiteDigest": "sha256:x"}},
		{Name: "bare", Provider: "runner", Suite: "evals", SuiteDigest: "sha256:y"},
		{Name: "model", Provider: "runner", RequireObservedModel: true},
	}, gateIn())
	if vs[0].Message != "evals" || vs[0].Citation["params"] != `{"suite":"evals"}` || vs[1].Message != "" ||
		vs[1].Citation["params"] != `{}` || vs[2].Citation["params"] != `{"suiteDigest":"sha256:y"}` ||
		vs[3].Citation["params"] != `{"suite":"evals","suiteDigest":"sha256:y"}` || vs[4].Citation["params"] != `{"requireObservedModel":"true"}` {
		t.Errorf("%+v", vs)
	}
}

// TestGatesNeverFollowARedirect: a provider that answers 307 never gets the
// promoter to post the gate input to a URL of its choosing.
func TestGatesNeverFollowARedirect(t *testing.T) {
	hit := false
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer elsewhere.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/latest/meta-data", http.StatusTemporaryRedirect)
	}))
	defer provider.Close()
	gates, err := NewGates([]ProviderConfig{{Name: "dd", Type: ProviderHTTP, URL: provider.URL}}, provider.Client(), openAny)
	if err != nil {
		t.Fatal(err)
	}
	if v := gates["dd"].Check(context.Background(), gateIn(), nil); v.Result != Unknown || hit || !strings.Contains(v.Message, "answered 307") {
		t.Fatalf("a redirect was followed: %+v (hit %v)", v, hit)
	}
}
