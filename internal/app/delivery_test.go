package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

const (
	dlA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	dlB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	dlC = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	dlD = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	dlE = "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	dlF = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
)

type fakeDeliveryRegistry struct {
	hosts      []string
	resolveErr error
	// unbounded is set when a tag lookup came with no deadline: against a
	// registry that is down it would wait as long as the transport retries.
	unbounded bool
}

func (f *fakeDeliveryRegistry) Annotations(_ context.Context, ref string) (map[string]string, error) {
	repo, d, _ := strings.Cut(ref, "@")
	m := map[string]string{dlA: "oci://reg/pactos/payments@" + dlC, dlB: "oci://reg/pactos/payments@" + dlD}
	// orders' artifact lives only in its own apply repository.
	if repo == "reg.example/apps/orders" {
		m[dlE] = "oci://reg/pactos/orders@" + dlF
	}
	return map[string]string{delivery.ContractAnnotation: m[d]}, nil
}

func (f *fakeDeliveryRegistry) Pull(_ context.Context, ref string) (*contract.Bundle, error) {
	if strings.HasSuffix(ref, dlF) {
		return &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "orders", Version: "1.9.0"},
			Dependencies: []contract.Dependency{{Name: "payments", Ref: "oci://reg/pactos/payments", Compatibility: ">=1.0.0 <2.0.0"}}}}, nil
	}
	v := "1.0.0"
	if strings.HasSuffix(ref, dlD) {
		v = "1.1.0"
	}
	return &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "payments", Version: v}}}, nil
}

func (f *fakeDeliveryRegistry) Resolve(ctx context.Context, _ string) (string, error) {
	if _, ok := ctx.Deadline(); !ok {
		f.unbounded = true
	}
	return dlB, f.resolveErr
}

func obj(apiVersion, kind, ns, name string, fields map[string]any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{"apiVersion": apiVersion, "kind": kind,
		"metadata": map[string]any{"namespace": ns, "name": name, "uid": "uid-" + name}}}
	for k, v := range fields {
		u.Object[k] = v
	}
	return u
}

func deliveryFixture(t *testing.T, user string, groups []string, extra map[string]any, objs ...runtime.Object) (*Delivery, *dynamicfake.FakeDynamicClient, *fakeDeliveryRegistry) {
	t.Helper()
	stage := obj("delivery.pacto.trianalab.io/v1alpha1", "Stage", "prod", "payments-prod", map[string]any{
		"spec": map[string]any{"service": map[string]any{"name": "payments"}, "environment": "prod", "applyRef": map[string]any{"name": "payments"},
			"candidates": map[string]any{"upstreamStage": map[string]any{"name": "payments-test", "namespace": "test"}}},
		"status": map[string]any{"pointer": dlA, "verified": []any{dlA}},
	})
	// The upstream Stage offers B (it passed pacto.published there and
	// verified), so B passes the published gate here.
	upstream := obj("delivery.pacto.trianalab.io/v1alpha1", "Stage", "test", "payments-test", map[string]any{
		"status": map[string]any{"verified": []any{dlB}, "offered": []any{dlB}},
	})
	repo := obj("source.toolkit.fluxcd.io/v1", "OCIRepository", "prod", "payments", map[string]any{
		"spec": map[string]any{"url": "oci://reg.example/apps/payments", "insecure": true},
	})
	all := append([]runtime.Object{stage, upstream, repo}, objs...)
	lists := map[schema.GroupVersionResource]string{gvrStages: "StageList", gvrOperations: "OperationList", gvrApprovals: "ApprovalList",
		gvrPactos: "PactoList", gvrOCIRepository: "OCIRepositoryList", gvrKustomization: "KustomizationList"}
	c := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), lists, all...)
	c.PrependReactor("create", "selfsubjectreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		gs := make([]any, 0, len(groups))
		for _, g := range groups {
			gs = append(gs, g)
		}
		return true, &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"userInfo": map[string]any{
			"username": user, "groups": gs, "extra": extra}}}}, nil
	})
	reg := &fakeDeliveryRegistry{}
	d := &Delivery{Dynamic: c, Registry: func(hosts ...string) DeliveryRegistry { reg.hosts = hosts; return reg },
		Policy: delivery.ClassPolicy{AgentGroups: []string{"pacto:agents"}}, Now: func() time.Time { return time.Unix(1_800_000_000, 0) }}
	return d, c, reg
}

// wantErrContaining fails unless err says want.
func wantErrContaining(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("want an error containing %q, got %v", want, err)
	}
}

func TestDeliveryPlanAndPropose(t *testing.T) {
	d, _, reg := deliveryFixture(t, "system:serviceaccount:agents:bot", []string{"system:authenticated"}, nil)
	ctx := context.Background()
	req := delivery.Request{Namespace: "prod", Stage: "payments-prod", To: dlB, Reason: "ship", OnBehalfOf: "alice"}
	plan, spec, err := d.Plan(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Predicted != delivery.PredictProceeds || spec.Params.From != dlA || spec.Actor.User != "system:serviceaccount:agents:bot" ||
		spec.OnBehalfOf.User != "alice" || reg.hosts[0] != "reg.example" {
		t.Fatalf("plan %+v spec %+v hosts %v", plan, spec, reg.hosts)
	}
	req.IntentDigest = "sha256:0"
	var stale *PlanStaleError
	if _, _, err := d.Propose(ctx, req); !errors.As(err, &stale) || stale.Plan.IntentDigest != plan.IntentDigest || !strings.Contains(err.Error(), "PlanStale") {
		t.Fatalf("want PlanStale with the fresh plan, got %v", err)
	}
}

func TestDeliveryProposeGetCancel(t *testing.T) {
	d, _, _ := deliveryFixture(t, "system:serviceaccount:agents:bot", []string{"system:authenticated"}, nil)
	ctx := context.Background()
	req := delivery.Request{Namespace: "prod", Stage: "payments-prod", To: dlB, Reason: "ship", OnBehalfOf: "alice"}
	plan, _, err := d.Plan(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	req.IntentDigest, req.Name = plan.IntentDigest, "p1"
	op, _, err := d.Propose(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if op.Metadata.Name != "p1" || op.Spec.IntentDigest != plan.IntentDigest || op.Metadata.Finalizers[0] != recordFinalizer ||
		op.Metadata.Labels["delivery.pacto.trianalab.io/stage"] != "payments-prod" {
		t.Fatalf("created %+v", op)
	}
	got, err := d.Get(ctx, "prod", "p1")
	if err != nil || got.Spec.Params.To != dlB {
		t.Fatalf("get: %+v %v", got, err)
	}
	if err := d.Cancel(ctx, "prod", "p1"); err != nil {
		t.Fatal(err)
	}
	if err := d.Cancel(ctx, "prod", "p1"); err == nil {
		t.Fatal("cancelling a missing operation must fail")
	}
}

func TestDeliveryProposeGeneratesNameAndRollback(t *testing.T) {
	cited := obj("delivery.pacto.trianalab.io/v1alpha1", "Operation", "prod", "p0", map[string]any{
		"status": map[string]any{"pointer": map[string]any{"before": dlB, "after": dlA}},
	})
	d, c, _ := deliveryFixture(t, "alice", nil, nil, cited)
	var generated string
	c.PrependReactor("create", "operations", func(a k8stesting.Action) (bool, runtime.Object, error) {
		u := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured)
		generated = u.GetGenerateName()
		u.SetName(generated + "x")
		return true, u, nil
	})
	op, plan, err := d.Propose(context.Background(), delivery.Request{Namespace: "prod", Stage: "payments-prod", Type: delivery.TypeRollback, Undoes: "p0"})
	if err != nil {
		t.Fatal(err)
	}
	if generated != "payments-prod-rollback-" || op.Spec.Params.From != dlA || op.Spec.Params.To != dlB || op.Spec.Params.Undoes != "uid-p0" ||
		plan.Predicted != delivery.PredictProceeds {
		t.Fatalf("rollback: %q %+v %+v", generated, op.Spec, plan)
	}
	if _, _, err := d.Plan(context.Background(), delivery.Request{Namespace: "prod", Stage: "payments-prod", Type: delivery.TypeRollback, Undoes: "gone"}); err == nil {
		t.Fatal("a rollback citing nothing must fail")
	}
	op, _, err = d.Propose(context.Background(), delivery.Request{Namespace: "prod", Stage: "payments-prod", Type: delivery.TypeUnfreeze})
	if err != nil || op.Spec.Facet != delivery.FacetFreeze {
		t.Fatalf("unfreeze: %+v %v", op, err)
	}
}

func TestDeliveryRefusals(t *testing.T) {
	d, _, reg := deliveryFixture(t, "alice", nil, nil)
	ctx := context.Background()
	base := delivery.Request{Namespace: "prod", Stage: "payments-prod"}
	if _, _, err := d.Plan(ctx, delivery.Request{Namespace: "prod", Stage: "missing", To: dlB}); err == nil {
		t.Fatal("missing stage")
	}
	r := base
	r.Type = "pacto.dev/scale"
	_, _, err := d.Plan(ctx, r)
	wantErrContaining(t, err, "unknown operation type")
	_, _, err = d.Plan(ctx, base)
	wantErrContaining(t, err, "--to is required")
	r = base
	r.To = "v2"
	_, _, err = d.Plan(ctx, r)
	wantErrContaining(t, err, "currently resolves to "+dlB)
	if reg.unbounded {
		t.Fatal("the tag lookup has no time limit")
	}
	reg.resolveErr = errors.New("no such tag")
	_, _, err = d.Plan(ctx, r)
	wantErrContaining(t, err, "no such tag")
	r.From, r.To = dlB, dlA
	if p, spec, err := d.Plan(ctx, r); err != nil || spec.Params.From != dlB || p.Predicted != delivery.PredictRejected {
		t.Fatalf("explicit from: %+v %v", p, err)
	}
}

// A consumer running in prod that does not accept the new version holds it,
// and the plan names the verified version of that consumer to ship first,
// read through the consumer's own apply OCIRepository.
func TestDeliveryPlanSaysWhatMovesFirst(t *testing.T) {
	orders := obj("pacto.trianalab.io/v1alpha1", "Pacto", "prod", "orders", map[string]any{"status": map[string]any{
		"contractStatus": "Compliant",
		"contract":       map[string]any{"serviceName": "orders", "version": "1.8.0", "resolvedRef": "oci://reg/pactos/orders@" + dlC},
		"dependencies":   []any{map[string]any{"name": "payments", "ref": "oci://reg/pactos/payments", "compatibility": "~1.0.0"}},
	}})
	ordersStage := obj("delivery.pacto.trianalab.io/v1alpha1", "Stage", "prod", "orders", map[string]any{
		"spec": map[string]any{"service": map[string]any{"name": "orders"}, "environment": "prod", "applyRef": map[string]any{"name": "orders"},
			"candidates": map[string]any{"upstreamStage": map[string]any{"name": "orders", "namespace": "test"}}},
	})
	ordersTest := obj("delivery.pacto.trianalab.io/v1alpha1", "Stage", "test", "orders", map[string]any{
		"spec":   map[string]any{"service": map[string]any{"name": "orders"}, "environment": "test", "applyRef": map[string]any{"name": "orders"}},
		"status": map[string]any{"verified": []any{dlE}, "offered": []any{dlE}},
	})
	ordersRepo := obj("source.toolkit.fluxcd.io/v1", "OCIRepository", "prod", "orders", map[string]any{
		"spec": map[string]any{"url": "oci://reg.example/apps/orders"},
	})
	d, _, _ := deliveryFixture(t, "system:serviceaccount:agents:bot", nil, nil, orders, ordersStage, ordersTest, ordersRepo)
	p, _, err := d.Plan(context.Background(), delivery.Request{Namespace: "prod", Stage: "payments-prod", To: dlB})
	if err != nil {
		t.Fatal(err)
	}
	if p.Predicted != delivery.PredictHeld || p.Order == nil || len(p.Order.MoveFirst) != 1 {
		t.Fatalf("plan %+v", p)
	}
	if m := p.Order.MoveFirst[0]; m.Service != "orders" || m.Version != "1.8.0" || m.Candidate == nil || m.Candidate.Digest != dlE ||
		m.Candidate.Version != "1.9.0" || m.Stage.Namespace != "prod" {
		t.Fatalf("move first %+v", m)
	}
}

// A consumer counts unless Pacto can place it in another environment from
// where it runs: a Kustomization applying a test Stage's version into its
// namespace. With the Kustomizations unreadable it counts again.
func TestDeliveryPlanPlacesConsumersByWhereTheyRun(t *testing.T) {
	orders := obj("pacto.trianalab.io/v1alpha1", "Pacto", "orders-test", "orders", map[string]any{"status": map[string]any{
		"contractStatus": "Compliant",
		"contract":       map[string]any{"serviceName": "orders", "version": "1.8.0", "resolvedRef": "oci://reg/pactos/orders@" + dlC},
		"dependencies":   []any{map[string]any{"name": "payments", "ref": "oci://reg/pactos/payments", "compatibility": "~1.0.0"}},
	}})
	d, c, _ := deliveryFixture(t, "system:serviceaccount:agents:bot", nil, nil, orders)
	ctx := context.Background()
	r := delivery.Request{Namespace: "prod", Stage: "payments-prod", To: dlB}
	if p, _, err := d.Plan(ctx, r); err != nil || p.Predicted != delivery.PredictHeld {
		t.Fatalf("a consumer Pacto cannot place holds: %+v %v", p, err)
	}
	k := obj("kustomize.toolkit.fluxcd.io/v1", "Kustomization", "test", "payments", map[string]any{
		"spec": map[string]any{"targetNamespace": "orders-test", "sourceRef": map[string]any{"kind": "OCIRepository", "name": "payments"}},
	})
	if _, err := c.Resource(gvrKustomization).Namespace("test").Create(ctx, k, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	test := obj("delivery.pacto.trianalab.io/v1alpha1", "Stage", "test", "payments-t", map[string]any{
		"spec": map[string]any{"service": map[string]any{"name": "payments"}, "environment": "test", "applyRef": map[string]any{"name": "payments"}},
	})
	if _, err := c.Resource(gvrStages).Namespace("test").Create(ctx, test, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if p, _, err := d.Plan(ctx, r); err != nil || p.Predicted != delivery.PredictProceeds {
		t.Fatalf("a consumer running in test does not hold prod: %+v %v", p, err)
	}
	c.PrependReactor("list", "kustomizations", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})
	if p, _, err := d.Plan(ctx, r); err != nil || p.Predicted != delivery.PredictHeld {
		t.Fatalf("with the Kustomizations unreadable every consumer counts: %+v %v", p, err)
	}
}

func TestDeliveryUnreadableClusterState(t *testing.T) {
	d, c, _ := deliveryFixture(t, "alice", nil, nil)
	ctx := context.Background()
	c.PrependReactor("get", "ocirepositories", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})
	r := delivery.Request{Namespace: "prod", Stage: "payments-prod"}
	r.To = "v2"
	_, _, err := d.Plan(ctx, r)
	wantErrContaining(t, err, "forbidden")
	r.To = dlB
	if p, _, err := d.Plan(ctx, r); err != nil || p.Predicted != delivery.PredictHeld {
		t.Fatalf("an unreadable OCIRepository holds: %+v %v", p, err)
	}
	c.PrependReactor("list", "pactos", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("no CRD")
	})
	if p, _, err := d.Plan(ctx, r); err != nil || p.Predicted != delivery.PredictHeld {
		t.Fatalf("an unlistable Pacto set holds: %+v %v", p, err)
	}
	c.PrependReactor("create", "operations", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("denied by admission")
	})
	_, _, err = d.Propose(ctx, r)
	wantErrContaining(t, err, "denied by admission")
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := d.Plan(cctx, r); err == nil {
		t.Fatal("a cancelled snapshot build must fail")
	}
	if _, _, err := d.Propose(cctx, r); err == nil {
		t.Fatal("propose surfaces the plan failure")
	}
}

func TestDeliverySecureRegistry(t *testing.T) {
	d, c, reg := deliveryFixture(t, "alice", nil, nil)
	ctx := context.Background()
	ri := c.Resource(gvrOCIRepository).Namespace("prod")
	u, _ := ri.Get(ctx, "payments", metav1.GetOptions{})
	_ = unstructured.SetNestedField(u.Object, false, "spec", "insecure")
	if _, err := ri.Update(ctx, u, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.Plan(ctx, delivery.Request{Namespace: "prod", Stage: "payments-prod", To: dlB}); err != nil || len(reg.hosts) != 0 {
		t.Fatalf("a secure OCIRepository asks for no plain-HTTP host: %v %v", reg.hosts, err)
	}
}

func TestDeliveryWhoAmIAndApprove(t *testing.T) {
	op := obj("delivery.pacto.trianalab.io/v1alpha1", "Operation", "prod", "p1", map[string]any{
		"spec": map[string]any{"intentDigest": "sha256:i"},
	})
	ctx := context.Background()
	d, c, _ := deliveryFixture(t, "alice", []string{"devs"}, map[string]any{authTimeExtra: []any{"2026-10-20T10:00:00Z"}}, op)
	who, err := d.WhoAmI(ctx)
	if err != nil || who.User != "alice" || who.Class != delivery.ClassHuman || who.AuthTime != "2026-10-20T10:00:00Z" {
		t.Fatalf("whoami: %+v %v", who, err)
	}
	c.PrependReactor("create", "approvals", func(a k8stesting.Action) (bool, runtime.Object, error) {
		u := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured)
		spec := u.Object["spec"].(map[string]any)
		if spec["by"] != "alice" || spec["operationUID"] != "uid-p1" || spec["intentDigest"] != "sha256:i" || spec["authTime"] != "2026-10-20T10:00:00Z" ||
			fmt.Sprint(spec["groups"]) != "[devs]" {
			t.Errorf("approval spec %+v", spec)
		}
		u.SetName("p1-x")
		return true, u, nil
	})
	if name, err := d.Approve(ctx, "prod", "p1", delivery.Approve, "ok"); err != nil || name != "p1-x" {
		t.Fatalf("approve: %q %v", name, err)
	}
	if _, err := d.Approve(ctx, "prod", "gone", delivery.Approve, ""); err == nil {
		t.Fatal("approving a missing operation")
	}
}

func TestDeliveryApproveRefusals(t *testing.T) {
	op := obj("delivery.pacto.trianalab.io/v1alpha1", "Operation", "prod", "p1", map[string]any{
		"spec": map[string]any{"intentDigest": "sha256:i"},
	})
	ctx := context.Background()
	d, _, _ := deliveryFixture(t, "system:serviceaccount:agents:bot", nil, nil, op)
	_, err := d.Approve(ctx, "prod", "p1", delivery.Approve, "")
	wantErrContaining(t, err, "agents may not approve")
	d, _, _ = deliveryFixture(t, "bob", nil, nil, op)
	_, err = d.Approve(ctx, "prod", "p1", delivery.Approve, "")
	wantErrContaining(t, err, "fresh interactive login")
	d, c, _ := deliveryFixture(t, "bob", nil, map[string]any{authTimeExtra: []any{"1"}}, op)
	c.PrependReactor("create", "approvals", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("denied")
	})
	_, err = d.Approve(ctx, "prod", "p1", delivery.Approve, "")
	wantErrContaining(t, err, "denied")
	c.PrependReactor("create", "selfsubjectreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("unauthorized")
	})
	if _, err := d.WhoAmI(ctx); err == nil {
		t.Fatal("whoami failure")
	}
	if _, err := d.Approve(ctx, "prod", "p1", delivery.Approve, ""); err == nil {
		t.Fatal("approve surfaces whoami failure")
	}
	if _, _, err := d.Plan(ctx, delivery.Request{Namespace: "prod", Stage: "payments-prod", To: dlB}); err == nil {
		t.Fatal("plan surfaces whoami failure")
	}
}

// TestDeliveryUpstreamStage: the versions the upstream Stage offers are what
// the published gate admits; one in the Stage's own namespace when it names no
// namespace, and refused when it cannot be read.
func TestDeliveryUpstreamStage(t *testing.T) {
	d, c, _ := deliveryFixture(t, "alice", nil, nil)
	ctx := context.Background()
	stage, err := d.Stage(ctx, "prod", "payments-prod")
	if err != nil {
		t.Fatal(err)
	}
	if up := d.upstream(ctx, stage); up == nil || up.Verified[0] != dlB {
		t.Fatalf("upstream: %+v", up)
	}
	stage.Spec.Candidates.UpstreamStage.Namespace = ""
	if up := d.upstream(ctx, stage); up != nil {
		t.Fatalf("an upstream in prod does not exist: %+v", up)
	}
	stage.Spec.Candidates = delivery.Candidates{}
	if up := d.upstream(ctx, stage); up != nil {
		t.Fatal("no upstream")
	}
	c.PrependReactor("get", "stages", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetNamespace() == "test" {
			return true, nil, errors.New("forbidden")
		}
		return false, nil, nil
	})
	p, _, err := d.Plan(ctx, delivery.Request{Namespace: "prod", Stage: "payments-prod", To: dlB})
	if err != nil || p.Verdicts[0].Gate != delivery.GatePublished || p.Verdicts[0].Result != delivery.Fail {
		t.Fatalf("an unreadable upstream: %+v %v", p.Verdicts, err)
	}
}

func TestDeliveryLists(t *testing.T) {
	d, c, _ := deliveryFixture(t, "alice", nil, nil, obj("delivery.pacto.trianalab.io/v1alpha1", "Operation", "prod", "op1", nil))
	ctx := context.Background()
	all, err := d.Stages(ctx, "")
	if err != nil || len(all) != 2 {
		t.Fatalf("every namespace: %d %v", len(all), err)
	}
	prod, _ := d.Stages(ctx, "prod")
	if len(prod) != 1 || prod[0].Spec.Service.Name != "payments" {
		t.Fatalf("one namespace: %+v", prod)
	}
	if ops, err := d.Operations(ctx, "prod"); err != nil || len(ops) != 1 {
		t.Fatalf("operations: %+v %v", ops, err)
	}
	c.PrependReactor("list", "operations", func(k8stesting.Action) (bool, runtime.Object, error) { return true, nil, errors.New("forbidden") })
	wantErrContaining(t, func() error { _, err := d.Operations(ctx, ""); return err }(), "every namespace")
}

func TestDeliveryDescribe(t *testing.T) {
	d, _, _ := deliveryFixture(t, "alice", nil, nil)
	ctx := context.Background()
	prod, _ := d.Stages(ctx, "prod")
	if r := d.Describe(ctx, &prod[0], dlB); r.Bundle == nil || r.Bundle.Contract.Service.Version != "1.1.0" || r.ContractRef == "" {
		t.Fatalf("describe: %+v", r)
	}
	gone := prod[0]
	gone.Spec.ApplyRef.Name = "missing"
	if r := d.Describe(ctx, &gone, dlB); r.Err == "" || r.Bundle != nil {
		t.Fatalf("an unreadable OCIRepository: %+v", r)
	}
}
