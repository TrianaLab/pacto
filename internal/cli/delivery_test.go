package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/internal/app"
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
		}
		c := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), lists,
			u("delivery.pacto.trianalab.io/v1alpha1", "Stage", "payments-prod", map[string]any{
				"spec": map[string]any{"service": map[string]any{"name": "payments"}, "applyRef": map[string]any{"name": "payments"},
					"candidates": map[string]any{"ociRepositoryRef": map[string]any{"name": "payments"}}},
				"status": map[string]any{"pointer": cliA, "verified": []any{cliA}, "published": []any{cliB}, "frozen": true, "frozenReason": "a rollback failed",
					"autoHold": true, "lastCheck": map[string]any{"operation": "uid-x", "digest": cliA, "at": "2026-10-20T10:00:00Z",
						"verdicts": []any{map[string]any{"gate": "health", "phase": "verify", "result": "Unknown", "message": "too few spans"}}}}}),
			u("source.toolkit.fluxcd.io/v1", "OCIRepository", "payments", map[string]any{"spec": map[string]any{"url": "oci://reg/apps/payments"}}),
			u("delivery.pacto.trianalab.io/v1alpha1", "Operation", "held", map[string]any{
				"spec": map[string]any{"type": delivery.TypePromote, "stage": "payments-prod", "intentDigest": "sha256:i"},
				"status": map[string]any{"phase": "Held", "question": "Approve?", "reason": "AwaitingHuman", "message": "m",
					"plan": map[string]any{"intentDigest": "sha256:i", "type": delivery.TypePromote, "predicted": "Held", "order": map[string]any{
						"moveFirst": []any{map[string]any{"service": "orders", "message": "Ship orders first."}}, "unknown": []any{"the graph is partial"}}},
					"verdicts": []any{map[string]any{"gate": "pacto.diff", "phase": "pre", "result": "Pass", "citation": map[string]any{"query": "{}"}}}}}),
		)
		c.PrependReactor("create", "selfsubjectreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"userInfo": map[string]any{
				"username": user, "extra": map[string]any{"pacto.dev/auth-time": []any{"1800000000"}}}}}}, nil
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
		{"text", []string{"types"}, false, []string{"pacto.dev/promote", "agent,human,controller"}},
		{"json", []string{"types"}, false, []string{`"id": "pacto.dev/promote"`}},
		{"text", []string{"describe", "rollback"}, false, []string{"inverse:        pacto.dev/promote"}},
		{"text", []string{"describe", "pacto.dev/unfreeze"}, false, []string{"inverse:        none"}},
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

func TestDeliveryCommandWriteVerbs(t *testing.T) {
	propose := []string{"propose", "-n", "prod", "--stage", "payments-prod", "--to", cliB}
	checkDeliveryRuns(t, deliveryDeps("system:serviceaccount:agents:bot", nil), []deliveryRun{
		{"text", append(propose, "--name", "p1", "--type", "promote"), false, []string{"proposed prod/p1"}},
		{"json", append(propose, "--name", "p2"), false, []string{`"operation"`}},
		{"text", append(propose, "--intent-digest", "sha256:old"), true, []string{"PlanStale"}},
		{"text", []string{"cancel", "-n", "prod", "held"}, false, []string{"cancel requested for prod/held"}},
		{"text", []string{"cancel", "-n", "prod", "gone"}, true, nil},
		{"text", []string{"approve", "-n", "prod", "held"}, true, []string{"agents may not approve"}},
	})
	checkDeliveryRuns(t, deliveryDeps("alice", nil), []deliveryRun{
		{"text", []string{"approve", "-n", "prod", "held", "--decision", "accept-unknown", "--note", "ok"}, false,
			[]string{"recorded accept-unknown on prod/held as Approval held-1"}},
		{"text", []string{"approve", "-n", "prod", "held", "--decision", "maybe"}, true, []string{"--decision must be one of"}},
	})
}

func TestPrintPlanQuestion(t *testing.T) {
	out := &strings.Builder{}
	printPlan(out, delivery.Plan{Predicted: delivery.PredictHeld, Question: "Approve? Asked of: team-checkout"})
	if !strings.Contains(out.String(), "question:  Approve? Asked of: team-checkout") || !strings.Contains(out.String(), " none -> none") {
		t.Fatal(out.String())
	}
}

func TestDeliveryCommandClusterUnreachable(t *testing.T) {
	d := deliveryDeps("alice", errors.New("no kubeconfig"))
	for _, args := range [][]string{
		{"plan", "--stage", "s", "--to", cliB}, {"propose", "--stage", "s", "--to", cliB}, {"get", "x"}, {"cancel", "x"}, {"approve", "x"},
		{"explain", "x"}, {"history", "--stage", "s"},
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
