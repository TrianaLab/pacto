package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

const (
	cliA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cliB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	cliC = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
)

type cliRegistry struct{}

func (cliRegistry) Annotations(context.Context, string) (map[string]string, error) {
	return map[string]string{delivery.ContractAnnotation: "oci://reg/pactos/payments@" + cliC}, nil
}

func (cliRegistry) Pull(context.Context, string) (*contract.Bundle, error) {
	return &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "payments", Version: "1.0.0"}}}, nil
}

func (cliRegistry) Resolve(context.Context, string) (string, error) { return cliB, nil }

func u(apiVersion, kind, name string, fields map[string]any) *unstructured.Unstructured {
	o := &unstructured.Unstructured{Object: map[string]any{"apiVersion": apiVersion, "kind": kind,
		"metadata": map[string]any{"namespace": "prod", "name": name, "uid": "uid-" + name}}}
	for k, v := range fields {
		o.Object[k] = v
	}
	return o
}

// deliveryDeps is a cluster with one Stage running A and one held operation, seen
// by user.
func deliveryDeps(user string, dynErr error) deps {
	d := defaultDeps()
	d.newDeliveryRegistry = func(...string) app.DeliveryRegistry { return cliRegistry{} }
	d.newDynamic = func() (dynamic.Interface, error) {
		lists := map[schema.GroupVersionResource]string{
			{Group: "delivery.pacto.trianalab.io", Version: "v1alpha1", Resource: "operations"}: "OperationList",
			{Group: "delivery.pacto.trianalab.io", Version: "v1alpha1", Resource: "stages"}:     "StageList",
			{Group: "pacto.trianalab.io", Version: "v1alpha1", Resource: "pactos"}:              "PactoList",
			{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Resource: "kustomizations"}:   "KustomizationList",
		}
		c := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), lists,
			u("delivery.pacto.trianalab.io/v1alpha1", "Stage", "payments-prod", map[string]any{
				"spec": map[string]any{"service": map[string]any{"name": "payments"}, "applyRef": map[string]any{"name": "payments"},
					"candidates": map[string]any{"ociRepositoryRef": map[string]any{"name": "payments"}}},
				"status": map[string]any{"rung": "autonomous", "pointer": cliA, "verified": []any{cliA}, "published": []any{cliB}, "frozen": true, "frozenReason": "a rollback failed",
					"autoHold": true, "lastCheck": map[string]any{"operation": "uid-x", "digest": cliA, "at": "2026-10-20T10:00:00Z",
						"verdicts": []any{map[string]any{"gate": "health", "phase": "verify", "result": "Unknown", "message": "too few spans"}}}}}),
			u("source.toolkit.fluxcd.io/v1", "OCIRepository", "payments", map[string]any{"spec": map[string]any{"url": "oci://reg/apps/payments"}}),
			u("delivery.pacto.trianalab.io/v1alpha1", "Operation", "held", map[string]any{
				"spec": map[string]any{"type": delivery.TypePromote, "stage": "payments-prod", "intentDigest": "sha256:i"},
				"status": map[string]any{"phase": "Held", "question": "Approve?", "reason": "AwaitingHuman", "message": "m",
					"plan": map[string]any{"intentDigest": "sha256:i", "type": delivery.TypePromote, "predicted": "Held",
						"floor": map[string]any{"human": true, "decision": "accept-unknown", "owners": []any{"team-payments"}}, "order": map[string]any{
							"moveFirst": []any{map[string]any{"service": "orders", "message": "Ship orders first."}}, "unknown": []any{"the graph is partial"}}},
					"verdicts": []any{map[string]any{"gate": "pacto.diff", "phase": "pre", "result": "Pass", "citation": map[string]any{"query": "{}", "runnerBundle": "SIGNED-ANSWER"}},
						map[string]any{"gate": "x", "phase": "pre", "result": "Pass"}}}}),
		)
		c.PrependReactor("create", "selfsubjectreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"userInfo": map[string]any{
				"username": user, "groups": []any{"team-payments"},
				"extra": map[string]any{"delivery.pacto.trianalab.io/auth-time": []any{strconv.FormatInt(time.Now().Unix(), 10)}}}}}}, nil
		})
		c.PrependReactor("create", "approvals", func(a k8stesting.Action) (bool, runtime.Object, error) {
			o := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured)
			o.SetName("held-1")
			return true, o, nil
		})
		return c, dynErr
	}
	return d
}

// approveDeps is deliveryDeps for `approve`, which never uses the ambient
// credentials: it logs in again and builds its client from that token alone.
func approveDeps(user string, dynErr error) deps {
	d := deliveryDeps(user, dynErr)
	d.clusterConfig = func() (*rest.Config, error) { return &rest.Config{Host: "https://k8s", BearerToken: "ambient"}, dynErr }
	d.freshLogin = func(_ context.Context, c authn.FreshConfig) (string, error) {
		c.Prompt("https://idp/auth?prompt=login")
		return "fresh-" + c.ClientID, nil
	}
	d.dynamicFor = func(c *rest.Config) (dynamic.Interface, error) {
		if c.BearerToken != "fresh-pacto-approve" || c.Host != "https://k8s" {
			return nil, fmt.Errorf("approve ran with bearer %q on %q", c.BearerToken, c.Host)
		}
		return d.newDynamic()
	}
	return d
}

func runDelivery(t *testing.T, d deps, format string, args ...string) (string, error) {
	t.Helper()
	v := viper.New()
	v.Set(outputFormatKey, format)
	cmd := newDeliveryCommand(v, d)
	out := &strings.Builder{}
	cmd.SetOut(out)
	cmd.SetErr(&strings.Builder{})
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// deliveryRun is one invocation and what it must say: on stdout, or in the
// error when it must fail.
type deliveryRun struct {
	format  string
	args    []string
	wantErr bool
	want    []string
}

func checkDeliveryRuns(t *testing.T, d deps, runs []deliveryRun) {
	t.Helper()
	for _, r := range runs {
		out, err := runDelivery(t, d, r.format, r.args...)
		if (err != nil) != r.wantErr {
			t.Fatalf("%v: err = %v, want error %v\n%s", r.args, err, r.wantErr, out)
		}
		if err != nil {
			out = err.Error()
		}
		for _, w := range r.want {
			if !strings.Contains(out, w) {
				t.Fatalf("%v: output lacks %q:\n%s", r.args, w, out)
			}
		}
	}
}

func TestDeliveryCommandReadVerbs(t *testing.T) {
	plan := []string{"plan", "-n", "prod", "--stage", "payments-prod"}
	checkDeliveryRuns(t, deliveryDeps("alice", nil), []deliveryRun{
		{"text", []string{"types"}, false, []string{"delivery.pacto.trianalab.io/promote", "agent,human,controller"}},
		{"json", []string{"types"}, false, []string{`"id": "delivery.pacto.trianalab.io/promote"`}},
		{"text", []string{"describe", "rollback"}, false, []string{"inverse:        delivery.pacto.trianalab.io/promote"}},
		{"text", []string{"describe", "delivery.pacto.trianalab.io/unfreeze"}, false, []string{"inverse:        none"}},
		{"text", []string{"describe", "scale"}, true, []string{"unknown operation type"}},
		{"text", append(plan, "--to", cliB), false, []string{"predicted: Waits", "pacto.impact", "StageFrozen"}},
		{"json", append(plan, "--to", cliB), false, []string{`"intentDigest": "sha256:`}},
		{"text", append(plan, "--to", "v2"), true, []string{cliB}},
		{"text", append(plan, "--to", cliB, "--from", cliB), false, []string{"conflict:  PointerMoved"}},
		{"text", []string{"get", "-n", "prod", "held"}, false, []string{"question:  Approve?", "reason:    AwaitingHuman: m", "pacto.diff"}},
		{"text", []string{"get", "-n", "prod", "missing"}, true, nil},
		{"text", append(plan, "--to", cliB, "--cause-verdict", "{"), true, []string{"--cause-verdict is not a verdict"}},
		{"text", []string{"propose", "-n", "prod", "--stage", "payments-prod", "--to", cliB, "--cause-verdict", "{"}, true, []string{"--cause-verdict is not a verdict"}},
		{"text", append(plan, "--to", cliB, "--cause-verdict", `{"gate":"health","result":"Fail"}`), false, []string{"predicted: Waits"}},
		{"text", []string{"explain", "-n", "prod", "held"}, false, []string{"prod/held", "waits for a person: Approve?",
			"pacto.diff", "query: {}", "re-run: pacto delivery plan", "stage payments-prod runs " + cliA, "frozen: a rollback failed",
			"on auto-hold", "last re-check 2026-10-20T10:00:00Z of " + cliA, "health         Unknown  too few spans",
			"move first: Ship orders first.", "may be incomplete: the graph is partial"}},
		{"json", []string{"explain", "-n", "prod", "held"}, false, []string{`"summary"`, `"rerun"`, `"moveFirst"`}},
		{"text", []string{"explain", "-n", "prod", "missing"}, true, nil},
		{"text", []string{"history", "-n", "prod", "--stage", "payments-prod"}, false, []string{"SEQ", "held", "promote", "Held",
			"partial: read from the Operation objects"}},
		{"json", []string{"history", "-n", "prod", "--stage", "payments-prod"}, false, []string{`"completeness": "partial"`}},
		{"text", []string{"history", "-n", "prod", "--stage", "missing"}, true, nil},
	})
}

// A verdict with nothing to run again prints no re-run line.
func TestDeliveryExplainOmitsEmptyRerun(t *testing.T) {
	out, err := runDelivery(t, deliveryDeps("alice", nil), "text", "explain", "-n", "prod", "held")
	if err != nil || strings.Count(out, "re-run:") != 1 || strings.Contains(out, "re-run: \n") {
		t.Fatalf("want one re-run line: %v\n%s", err, out)
	}
}

func TestDeliveryCommandWriteVerbs(t *testing.T) {
	propose := []string{"propose", "-n", "prod", "--stage", "payments-prod", "--to", cliB}
	checkDeliveryRuns(t, approveDeps("system:serviceaccount:agents:bot", nil), []deliveryRun{
		{"text", append(propose, "--name", "p1", "--type", "promote"), false, []string{"proposed prod/p1"}},
		{"json", append(propose, "--name", "p2"), false, []string{`"operation"`}},
		{"text", append(propose, "--intent-digest", "sha256:old"), true, []string{"PlanStale"}},
		{"text", []string{"cancel", "-n", "prod", "held"}, false, []string{"cancel requested for prod/held"}},
		{"text", []string{"cancel", "-n", "prod", "gone"}, true, nil},
		{"text", []string{"approve", "-n", "prod", "held", "--oidc-issuer-url", "https://idp"}, true, []string{"agents may not approve"}},
	})
	checkDeliveryRuns(t, approveDeps("alice", nil), []deliveryRun{
		{"text", []string{"approve", "-n", "prod", "held", "--decision", "accept-unknown", "--note", "ok", "--oidc-issuer-url", "https://idp"}, false,
			[]string{"recorded accept-unknown on prod/held as Approval held-1"}},
		{"text", []string{"approve", "-n", "prod", "held", "--decision", "maybe"}, true, []string{"--decision must be one of"}},
		{"text", []string{"approve", "-n", "prod", "held"}, true, []string{"approving needs a fresh login of your own", "--oidc-issuer-url"}},
	})
}

// approve logs the person in again with its own client and sends the
// Approval with that token, never the kubeconfig's.
func TestDeliveryApproveFreshLogin(t *testing.T) {
	d := approveDeps("alice", nil)
	var got authn.FreshConfig
	login := d.freshLogin
	d.freshLogin = func(ctx context.Context, c authn.FreshConfig) (string, error) {
		got = c
		return login(ctx, c)
	}
	t.Setenv("PACTO_DELIVERY_OIDC_CLIENT_SECRET", "s3cret")
	v := viper.New()
	v.SetEnvPrefix("PACTO")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	v.AutomaticEnv()
	cmd := newDeliveryCommand(v, d)
	out, errOut := &strings.Builder{}, &strings.Builder{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"approve", "-n", "prod", "held", "--oidc-issuer-url", "https://idp", "--oidc-extra-scopes", "a,b", "--oidc-redirect-port", "8000"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got.IssuerURL != "https://idp" || got.ClientID != "pacto-approve" || got.ClientSecret != "s3cret" ||
		strings.Join(got.ExtraScopes, ",") != "a,b" || got.Port != 8000 {
		t.Fatalf("login config %+v", got)
	}
	if !strings.Contains(errOut.String(), "To record approve on prod/held, log in again in your browser") ||
		!strings.Contains(errOut.String(), "https://idp/auth?prompt=login") || !strings.Contains(out.String(), "as Approval held-1") {
		t.Fatalf("stderr %q stdout %q", errOut.String(), out.String())
	}

	fail := func(mod func(*deps), want string) {
		t.Helper()
		d := approveDeps("alice", nil)
		mod(&d)
		if _, err := runDelivery(t, d, "text", "approve", "-n", "prod", "held", "--oidc-issuer-url", "https://idp"); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("want %q, got %v", want, err)
		}
	}
	fail(func(d *deps) {
		d.freshLogin = func(context.Context, authn.FreshConfig) (string, error) { return "", errors.New("login refused") }
	}, "login refused")
	fail(func(d *deps) {
		d.dynamicFor = func(*rest.Config) (dynamic.Interface, error) { return nil, errors.New("bad config") }
	}, "bad config")
	fail(func(d *deps) {
		d.clusterConfig = func() (*rest.Config, error) { return nil, errors.New("no kubeconfig") }
	}, "no kubeconfig")
	fail(func(d *deps) { t.Setenv(trustEnv, "/nonexistent/trust.pem") }, "trust")
}

func TestPrintPlanQuestion(t *testing.T) {
	out := &strings.Builder{}
	printPlan(out, delivery.Plan{Predicted: delivery.PredictHeld, Question: "Approve? Asked of: team-checkout"})
	if !strings.Contains(out.String(), "question:  Approve? Asked of: team-checkout") || !strings.Contains(out.String(), " none -> none") {
		t.Fatal(out.String())
	}
}

func TestDeliveryCommandClusterUnreachable(t *testing.T) {
	d := approveDeps("alice", errors.New("no kubeconfig"))
	for _, args := range [][]string{
		{"plan", "--stage", "s", "--to", cliB}, {"propose", "--stage", "s", "--to", cliB}, {"get", "x"}, {"cancel", "x"},
		{"approve", "x", "--oidc-issuer-url", "https://idp"},
		{"explain", "x"}, {"history", "--stage", "s"}, {"blame", "--stage", "s"}, {"undo", "x"}, {"record", "verify", "--stage", "s"},
	} {
		if _, err := runDelivery(t, d, "text", args...); err == nil || !strings.Contains(err.Error(), "no kubeconfig") {
			t.Fatalf("%v: %v", args, err)
		}
	}
}

func TestMCPDeliveryMode(t *testing.T) {
	cmd := newMCPCommand(app.NewService(nil, nil), "v", deliveryDeps("system:serviceaccount:agents:bot", nil))
	_ = cmd.Flags().Set("delivery", "true")
	if _, err := buildMCPServer(cmd, app.NewService(nil, nil), "v", nil, deliveryDeps("system:serviceaccount:agents:bot", nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := buildMCPServer(cmd, app.NewService(nil, nil), "v", nil, deliveryDeps("alice", errors.New("down"))); err == nil {
		t.Fatal("an unreachable cluster must fail")
	}
	broken := deliveryDeps("alice", nil)
	inner := broken.newDynamic
	broken.newDynamic = func() (dynamic.Interface, error) {
		c, _ := inner()
		c.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "selfsubjectreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("unauthorized")
		})
		return c, nil
	}
	if _, err := buildMCPServer(cmd, app.NewService(nil, nil), "v", nil, broken); err == nil {
		t.Fatal("whoami failure must fail")
	}
	if err := checkMCPMode(false, false, true, true, false); err == nil || !strings.Contains(err.Error(), "--delivery") {
		t.Fatalf("modes: %v", err)
	}
	c, _, _ := deliveryClient(cmd, deliveryDeps("alice", nil))
	if p, err := (mcpDelivery{c}).Plan(context.Background(), delivery.Request{Namespace: "prod", Stage: "payments-prod", To: cliB}); err != nil || p.IntentDigest == "" {
		t.Fatalf("adapter: %+v %v", p, err)
	}
	if newDeliveryRegistry("h") == nil {
		t.Fatal("registry")
	}
	over := newMCPCommand(app.NewService(nil, nil), "v", deliveryDeps("system:serviceaccount:agents:bot", nil))
	over.SetArgs([]string{"--delivery", "-t", "http"})
	over.SetOut(&strings.Builder{})
	over.SetErr(&strings.Builder{})
	if err := over.Execute(); !errors.Is(err, errDeliveryOverHTTP) {
		t.Fatalf("delivery over HTTP must be refused: %v", err)
	}
}

// The runner's signed answer is for -o json; the text says who ran the exam.
func TestDeliveryExplainHidesTheSignedAnswer(t *testing.T) {
	out, err := runDelivery(t, deliveryDeps("alice", nil), "text", "explain", "-n", "prod", "held")
	if err != nil || strings.Contains(out, "SIGNED-ANSWER") {
		t.Fatalf("the signed answer printed: %v\n%s", err, out)
	}
}

func TestPrintVerificationNamesTheRunner(t *testing.T) {
	var b strings.Builder
	printVerification(&b, &delivery.StreamVerification{Records: []delivery.RecordCheck{{Seq: 1, Kind: delivery.RecordOperation, Runners: []delivery.RunnerCheck{
		{Gate: "evals", Runner: "evals-ci", OK: true}, {Gate: "smoke", Problem: "no runner key is trusted"}}}}})
	for _, want := range []string{"evals: the exam was run and signed by evals-ci", "smoke: the runner's signature does not verify (no runner key is trusted)"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("lacks %q:\n%s", want, b.String())
		}
	}
}
