package cli

import (
	"context"
	"encoding/json"
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
			{Group: "pacto.trianalab.io", Version: "v1alpha1", Resource: "pactos"}:              "PactoList",
		}
		c := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), lists,
			u("delivery.pacto.trianalab.io/v1alpha1", "Stage", "payments-prod", map[string]any{
				"spec":   map[string]any{"service": map[string]any{"name": "payments"}, "applyRef": map[string]any{"name": "payments"}},
				"status": map[string]any{"pointer": cliA, "verified": []any{cliA}}}),
			u("source.toolkit.fluxcd.io/v1", "OCIRepository", "payments", map[string]any{"spec": map[string]any{"url": "oci://reg/apps/payments"}}),
			u("delivery.pacto.trianalab.io/v1alpha1", "Operation", "held", map[string]any{
				"spec":   map[string]any{"type": delivery.TypePromote, "stage": "payments-prod", "intentDigest": "sha256:i"},
				"status": map[string]any{"phase": "Held", "question": "Approve?", "reason": "AwaitingHuman", "message": "m",
					"verdicts": []any{map[string]any{"gate": "pacto.diff", "phase": "pre", "result": "Pass"}}}}),
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

func TestDeliveryCommandReadVerbs(t *testing.T) {
	d := deliveryDeps("alice", nil)
	out, err := runDelivery(t, d, "text", "types")
	if err != nil || !strings.Contains(out, "pacto.dev/promote") || !strings.Contains(out, "agent,human,controller") {
		t.Fatalf("types: %v\n%s", err, out)
	}
	if out, err = runDelivery(t, d, "json", "types"); err != nil || !strings.HasPrefix(strings.TrimSpace(out), "[") {
		t.Fatalf("types json: %v %s", err, out)
	}
	if out, err = runDelivery(t, d, "text", "describe", "rollback"); err != nil || !strings.Contains(out, "inverse:        pacto.dev/promote") {
		t.Fatalf("describe: %v\n%s", err, out)
	}
	if out, err = runDelivery(t, d, "text", "describe", "pacto.dev/unfreeze"); err != nil || !strings.Contains(out, "inverse:        none") {
		t.Fatalf("describe unfreeze: %v\n%s", err, out)
	}
	if _, err = runDelivery(t, d, "text", "describe", "scale"); err == nil {
		t.Fatal("unknown type")
	}
	out, err = runDelivery(t, d, "text", "plan", "-n", "prod", "--stage", "payments-prod", "--to", cliB)
	if err != nil || !strings.Contains(out, "predicted: Proceeds") || !strings.Contains(out, "pacto.impact") {
		t.Fatalf("plan: %v\n%s", err, out)
	}
	if out, err = runDelivery(t, d, "text", "plan", "-n", "prod", "--stage", "payments-prod", "--to", "v2"); err == nil || !strings.Contains(err.Error(), cliB) {
		t.Fatalf("a tag is refused naming its digest: %v %s", err, out)
	}
	if out, err = runDelivery(t, d, "text", "plan", "-n", "prod", "--stage", "payments-prod", "--to", cliB, "--from", cliB); err != nil ||
		!strings.Contains(out, "conflict:  PointerMoved") {
		t.Fatalf("plan conflict: %v\n%s", err, out)
	}
	if out, err = runDelivery(t, d, "text", "get", "-n", "prod", "held"); err != nil || !strings.Contains(out, "question:  Approve?") ||
		!strings.Contains(out, "reason:    AwaitingHuman: m") || !strings.Contains(out, "pacto.diff") {
		t.Fatalf("get: %v\n%s", err, out)
	}
	if _, err = runDelivery(t, d, "text", "get", "-n", "prod", "missing"); err == nil {
		t.Fatal("get missing")
	}
	var plan delivery.Plan
	out, _ = runDelivery(t, d, "json", "plan", "-n", "prod", "--stage", "payments-prod", "--to", cliB)
	if err := json.Unmarshal([]byte(out), &plan); err != nil || plan.IntentDigest == "" {
		t.Fatalf("plan json: %v %s", err, out)
	}
}

func TestDeliveryCommandWriteVerbs(t *testing.T) {
	d := deliveryDeps("system:serviceaccount:agents:bot", nil)
	out, err := runDelivery(t, d, "text", "propose", "-n", "prod", "--stage", "payments-prod", "--to", cliB, "--name", "p1", "--type", "promote")
	if err != nil || !strings.Contains(out, "proposed prod/p1") {
		t.Fatalf("propose: %v\n%s", err, out)
	}
	if out, err = runDelivery(t, d, "json", "propose", "-n", "prod", "--stage", "payments-prod", "--to", cliB, "--name", "p2"); err != nil ||
		!strings.Contains(out, `"operation"`) {
		t.Fatalf("propose json: %v\n%s", err, out)
	}
	if _, err = runDelivery(t, d, "text", "propose", "-n", "prod", "--stage", "payments-prod", "--to", cliB, "--intent-digest", "sha256:old"); err == nil ||
		!strings.Contains(err.Error(), "PlanStale") {
		t.Fatalf("stale: %v", err)
	}
	if out, err = runDelivery(t, d, "text", "cancel", "-n", "prod", "held"); err != nil || !strings.Contains(out, "cancel requested for prod/held") {
		t.Fatalf("cancel: %v %s", err, out)
	}
	if _, err = runDelivery(t, d, "text", "cancel", "-n", "prod", "gone"); err == nil {
		t.Fatal("cancel missing")
	}
	if _, err = runDelivery(t, d, "text", "approve", "-n", "prod", "held"); err == nil || !strings.Contains(err.Error(), "agents may not approve") {
		t.Fatalf("an agent cannot approve: %v", err)
	}
	human := deliveryDeps("alice", nil)
	if out, err = runDelivery(t, human, "text", "approve", "-n", "prod", "held", "--decision", "accept-unknown", "--note", "ok"); err != nil ||
		!strings.Contains(out, "recorded accept-unknown on prod/held as Approval held-1") {
		t.Fatalf("approve: %v %s", err, out)
	}
	if _, err = runDelivery(t, human, "text", "approve", "-n", "prod", "held", "--decision", "maybe"); err == nil {
		t.Fatal("unknown decision")
	}
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
	if err := checkMCPMode(false, false, true, true); err == nil || !strings.Contains(err.Error(), "--delivery") {
		t.Fatalf("modes: %v", err)
	}
	c, _, _ := deliveryClient(cmd, deliveryDeps("alice", nil))
	if p, err := (mcpDelivery{c}).Plan(context.Background(), delivery.Request{Namespace: "prod", Stage: "payments-prod", To: cliB}); err != nil || p.IntentDigest == "" {
		t.Fatalf("adapter: %+v %v", p, err)
	}
	if newDeliveryRegistry("h") == nil {
		t.Fatal("registry")
	}
}
