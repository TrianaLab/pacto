/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

const (
	ns         = "prod"
	dA         = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	dB         = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	dC         = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	c1         = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	c2         = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	c3         = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
	repo       = "reg.example/apps/payments"
	promoterSA = "system:serviceaccount:pacto-delivery:promoter"
	bot        = "system:serviceaccount:agents:bot"
)

var (
	gvkOCI   = schema.GroupVersionKind{Group: "source.toolkit.fluxcd.io", Version: "v1", Kind: "OCIRepository"}
	gvkKs    = schema.GroupVersionKind{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Kind: "Kustomization"}
	gvkPacto = schema.GroupVersionKind{Group: "pacto.trianalab.io", Version: "v1alpha1", Kind: "Pacto"}
)

// fakeRegistry serves artifact annotations by digest and bundles by contract ref.
type fakeRegistry struct {
	contracts map[string]string // artifact digest -> contract ref
	bundles   map[string]*contract.Bundle
	hosts     []string
	annErr    error
}

func (f *fakeRegistry) Annotations(_ context.Context, ref string) (map[string]string, error) {
	if f.annErr != nil {
		return nil, f.annErr
	}
	_, d, _ := strings.Cut(ref, "@")
	return map[string]string{delivery.ContractAnnotation: f.contracts[d]}, nil
}

func (f *fakeRegistry) Pull(_ context.Context, ref string) (*contract.Bundle, error) {
	if b, ok := f.bundles["oci://"+ref]; ok {
		return b, nil
	}
	return nil, errors.New("not found")
}

func contractRef(d string) string { return "oci://reg.example/pactos/payments@" + d }

func bundle(version string) *contract.Bundle {
	return &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.0",
		Service: contract.Service{Name: "payments", Version: version, Owner: contract.Owner{Team: "team-payments"}}}}
}

// fails names the calls the interceptor turns into errors, as "<verb>/<kind>",
// or "get/<kind>/<name>" for one object.
type fails map[string]bool

type env struct {
	t      *testing.T
	c      client.WithWatch
	op     *OperationReconciler
	st     *StageReconciler
	reg    *fakeRegistry
	now    time.Time
	fails  fails
	health *fakeHealth
	// mu guards fails and reg.hosts: RunChecks runs the gates concurrently.
	mu sync.Mutex
}

// setFail sets a fails entry from inside a running gate.
func (e *env) setFail(key string, v bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fails[key] = v
}

func (e *env) failing(key string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.fails[key]
}

// fakeHealth is a health provider whose answer the test sets; it records
// every input it was asked about.
type fakeHealth struct {
	result delivery.Result
	inputs []delivery.GateInput
	during func() // runs while the check is in progress, as another writer would
	mu     sync.Mutex
}

func (f *fakeHealth) Check(_ context.Context, in delivery.GateInput, _ map[string]string) delivery.Verdict {
	f.mu.Lock()
	f.inputs = append(f.inputs, in)
	f.mu.Unlock()
	if f.during != nil {
		f.during()
	}
	return delivery.Verdict{Result: f.result, Message: "error ratio " + string(f.result),
		Citation: map[string]string{"query": `{"signal":"traces"}`}}
}

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = dv1.AddToScheme(s)
	for _, gvk := range []schema.GroupVersionKind{gvkOCI, gvkKs, gvkPacto, gvkApplication} {
		s.AddKnownTypeWithName(gvk, &unstructured.Unstructured{})
		s.AddKnownTypeWithName(gvk.GroupVersion().WithKind(gvk.Kind+"List"), &unstructured.UnstructuredList{})
	}
	return s
}

func kindOf(obj runtime.Object) string {
	if u, ok := obj.(interface{ GetKind() string }); ok && u.GetKind() != "" {
		return strings.TrimSuffix(u.GetKind(), "List")
	}
	return strings.TrimSuffix(strings.TrimPrefix(fmt.Sprintf("%T", obj), "*v1alpha1."), "List")
}

func newEnv(t *testing.T, objs ...client.Object) *env {
	t.Helper()
	e := &env{t: t, now: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC), fails: fails{}}
	e.reg = &fakeRegistry{
		contracts: map[string]string{dA: contractRef(c1), dB: contractRef(c2), dC: contractRef(c3)},
		bundles:   map[string]*contract.Bundle{contractRef(c1): bundle("1.0.0"), contractRef(c2): bundle("1.1.0"), contractRef(c3): bundle("2.0.0")},
	}
	fail := func(verb string, obj runtime.Object) error {
		if e.failing(verb + "/" + kindOf(obj)) {
			return errors.New("injected " + verb + " failure")
		}
		return nil
	}
	e.c = fake.NewClientBuilder().WithScheme(newScheme()).
		WithStatusSubresource(&dv1.Stage{}, &dv1.Operation{}).
		WithIndex(&dv1.Approval{}, approvalOperationUID, approvalUID).
		WithObjects(objs...).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if err := fail("get", obj); err != nil {
					return err
				}
				if e.failing("get/" + kindOf(obj) + "/" + key.Name) {
					return errors.New("injected get failure")
				}
				return c.Get(ctx, key, obj, opts...)
			},
			List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if err := fail("list", list); err != nil {
					return err
				}
				return c.List(ctx, list, opts...)
			},
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if err := fail("create", obj); err != nil {
					return err
				}
				if obj.GetUID() == "" { // the apiserver assigns one; the fake client does not
					obj.SetUID(types.UID("uid-" + obj.GetName()))
				}
				return c.Create(ctx, obj, opts...)
			},
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				if err := fail("update", obj); err != nil {
					return err
				}
				return c.Update(ctx, obj, opts...)
			},
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				if err := fail("patch", obj); err != nil {
					return err
				}
				return c.Patch(ctx, obj, patch, opts...)
			},
			SubResourceUpdate: func(ctx context.Context, c client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if err := fail("status", obj); err != nil {
					return err
				}
				return c.Status().Update(ctx, obj, opts...)
			},
		}).Build()
	cluster := Cluster{Client: e.c, Registry: func(hosts ...string) delivery.Registry {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.reg.hosts = hosts
		return e.reg
	}}
	policy := delivery.ClassPolicy{ControllerUser: promoterSA, AgentGroups: []string{"pacto:agents"}, BreakGlassGroups: []string{"pacto:break-glass"}}
	clock := func() time.Time { return e.now }
	e.health = &fakeHealth{result: delivery.Pass}
	gates := NewGates(cluster, map[string]delivery.Gate{"mira": e.health})
	e.op = &OperationReconciler{Client: e.c, APIReader: e.c, Cluster: cluster, Policy: policy, Gates: gates, Now: clock, Poll: time.Second, heldChecks: &sync.Map{}}
	e.st = &StageReconciler{Client: e.c, APIReader: e.c, Cluster: cluster, Policy: policy, ClusterID: "cid", Gates: gates, Now: clock, Resync: time.Second, seen: &sync.Map{}}
	return e
}

// stage returns a prod Stage running pointer, with pointer verified.
func stageObj(pointer string) *dv1.Stage {
	s := &dv1.Stage{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "payments-prod"},
		Spec: delivery.StageSpec{Service: delivery.ServiceRef{Name: "payments"}, Environment: "prod",
			ApplyRef: delivery.ApplyRef{Name: "payments"}, Gates: delivery.Gates{Verify: delivery.VerifyGates{Window: "1m"}},
			Candidates: delivery.Candidates{OCIRepositoryRef: &delivery.LocalRef{Name: "payments-candidates"}}},
		// Every version the tests ship was published: Flux verified its signature.
		// The Stage is autonomous: the ladder tests set their own level.
		Status: delivery.StageStatus{Level: delivery.LevelAutonomous, Pointer: pointer, Stream: delivery.Stream{ID: "cid/prod/payments-prod"}, Published: []string{dA, dB, dC}}}
	if pointer != "" {
		s.Status.Verified = []string{pointer}
	}
	return s
}

func ociRepo(name, digest, revision string, insecure bool) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"namespace": ns, "name": name, "labels": map[string]any{ApplyRefLabel: "payments-prod"}},
		"spec":     map[string]any{"url": "oci://" + repo, "insecure": insecure, "ref": map[string]any{}},
	}}
	if digest != "" {
		_ = unstructured.SetNestedField(u.Object, digest, "spec", "ref", "digest")
	}
	if revision != "" {
		_ = unstructured.SetNestedField(u.Object, revision, "status", "artifact", "revision")
	}
	u.SetGroupVersionKind(gvkOCI)
	return u
}

// verifiedRepo is a candidate OCIRepository with spec.verify whose current
// revision Flux verified.
func verifiedRepo(name, revision string) *unstructured.Unstructured {
	u := ociRepo(name, "", revision, false)
	_ = unstructured.SetNestedField(u.Object, "cosign", "spec", "verify", "provider")
	_ = unstructured.SetNestedField(u.Object, "cosign-pub", "spec", "verify", "secretRef", "name")
	_ = unstructured.SetNestedSlice(u.Object, []any{map[string]any{"type": "SourceVerified", "status": "True",
		"message": "verified signature of revision " + revision}}, "status", "conditions")
	return u
}

func kustomization(name, source, ready, reason, applied, attempted string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"namespace": ns, "name": name},
		"spec":     map[string]any{"sourceRef": map[string]any{"kind": "OCIRepository", "name": source}},
		"status": map[string]any{
			"lastAppliedRevision":   applied,
			"lastAttemptedRevision": attempted,
			"conditions":            []any{map[string]any{"type": "Ready", "status": ready, "reason": reason, "message": "m"}},
		},
	}}
	u.SetGroupVersionKind(gvkKs)
	return u
}

func pactoObj(name, service, contractDigest, status string, gen int64, deps ...map[string]any) *unstructured.Unstructured {
	depList := make([]any, 0, len(deps))
	for _, d := range deps {
		depList = append(depList, d)
	}
	u := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"namespace": ns, "name": name},
		"status": map[string]any{
			"contractStatus":   status,
			"lastReconciledAt": "2026-10-20T10:00:00Z",
			"contract": map[string]any{"serviceName": service, "version": "1.0.0", "owner": map[string]any{"team": "team-" + service},
				"resolvedRef": "oci://reg.example/pactos/" + service + "@" + contractDigest},
			"dependencies": depList,
			"evaluatedRevision": map[string]any{"contractDigest": contractDigest,
				"workload": map[string]any{"kind": "Deployment", "name": service, "uid": "w-" + service, "generation": gen}},
		},
	}}
	u.SetGroupVersionKind(gvkPacto)
	return u
}

func deployment(name string, gen, observed int64) *appsv1.Deployment {
	return &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, UID: types.UID("w-" + name), Generation: gen},
		Status: appsv1.DeploymentStatus{ObservedGeneration: observed}}
}

func opObj(name, typ, from, to string, actor string, groups ...string) *dv1.Operation {
	facet := delivery.FacetPointer
	switch typ {
	case delivery.TypeUnfreeze, delivery.TypeFreeze:
		facet = delivery.FacetFreeze
	case delivery.TypeAutonomy:
		facet = delivery.FacetLevel
	case delivery.TypeAcknowledge:
		facet = delivery.FacetUpstream
	}
	o := &dv1.Operation{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, UID: types.UID("uid-" + name),
		CreationTimestamp: metav1.NewTime(time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC))},
		Spec: delivery.OperationSpec{Type: typ, Stage: "payments-prod", Facet: facet, Params: delivery.Params{From: from, To: to},
			Actor: delivery.Actor{User: actor, Groups: groups}}}
	controllerutil.AddFinalizer(o, dv1.RecordFinalizer)
	o.Spec.IntentDigest = delivery.IntentDigest(o.Spec, stageObj("").Spec)
	return o
}

func (e *env) reconcileOp(name string) reconcile.Result {
	e.t.Helper()
	res, err := e.op.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: name}})
	if err != nil {
		e.t.Fatalf("reconcile %s: %v", name, err)
	}
	return res
}

func (e *env) reconcileOpErr(name string) error {
	_, err := e.op.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: name}})
	return err
}

// drive reconciles name until it stops asking to run again right away.
func (e *env) drive(name string) *dv1.Operation {
	e.t.Helper()
	for range 20 {
		if res := e.reconcileOp(name); res.RequeueAfter != immediately {
			break
		}
	}
	return e.getOp(name)
}

func (e *env) getOp(name string) *dv1.Operation {
	e.t.Helper()
	o := &dv1.Operation{}
	if err := e.c.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, o); err != nil {
		e.t.Fatalf("get op %s: %v", name, err)
	}
	return o
}

func (e *env) getStage() *dv1.Stage {
	e.t.Helper()
	s := &dv1.Stage{}
	if err := e.c.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: "payments-prod"}, s); err != nil {
		e.t.Fatal(err)
	}
	return s
}

func (e *env) ociDigest(name string) string {
	e.t.Helper()
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvkOCI)
	if err := e.c.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, u); err != nil {
		e.t.Fatal(err)
	}
	d, _, _ := unstructured.NestedString(u.Object, "spec", "ref", "digest")
	return d
}

// apply simulates Flux applying digest: the OCIRepository fetched it and the
// Kustomization applied it.
func (e *env) apply(digest string) {
	e.t.Helper()
	ctx := context.Background()
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvkOCI)
	_ = e.c.Get(ctx, types.NamespacedName{Namespace: ns, Name: "payments"}, u)
	_ = unstructured.SetNestedField(u.Object, "latest@"+digest, "status", "artifact", "revision")
	if err := e.c.Update(ctx, u); err != nil {
		e.t.Fatal(err)
	}
	k := kustomization("payments", "payments", "True", "ReconciliationSucceeded", "latest@"+digest, "latest@"+digest)
	_ = e.c.Delete(ctx, k)
	if err := e.c.Create(ctx, k); err != nil {
		e.t.Fatal(err)
	}
}

// evaluate simulates the operator evaluating the contract of digest's artifact.
func (e *env) evaluate(contractDigest, status string) {
	e.t.Helper()
	ctx := context.Background()
	p := pactoObj("payments", "payments", contractDigest, status, 2)
	_ = e.c.Delete(ctx, p)
	if err := e.c.Create(ctx, p); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) create(objs ...client.Object) {
	e.t.Helper()
	for _, o := range objs {
		if err := e.c.Create(context.Background(), o); err != nil {
			e.t.Fatal(err)
		}
	}
}

// approve records a decision by a member of both teams the tests route
// questions to.
func (e *env) approve(op *dv1.Operation, name, by string, decision delivery.Decision) {
	e.t.Helper()
	e.approveAs(op, name, by, []string{"team-payments", "team-checkout"}, decision)
}

func (e *env) approveAs(op *dv1.Operation, name, by string, groups []string, decision delivery.Decision) {
	e.t.Helper()
	e.create(&dv1.Approval{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, CreationTimestamp: metav1.NewTime(e.now)},
		Spec: delivery.ApprovalSpec{Operation: op.Name, OperationUID: string(op.UID), IntentDigest: op.Spec.IntentDigest,
			Decision: decision, By: by, Groups: groups, AuthTime: e.now.Add(-time.Minute).Format(time.RFC3339), Note: "ok"}})
}

func wantOutcome(t *testing.T, o *dv1.Operation, outcome delivery.Outcome, reason string) {
	t.Helper()
	if o.Status.Phase != delivery.PhaseConcluded || o.Status.Outcome != outcome || (reason != "" && o.Status.Reason != reason) {
		t.Fatalf("want %s/%s, got phase=%s outcome=%s reason=%s message=%s", outcome, reason, o.Status.Phase, o.Status.Outcome, o.Status.Reason, o.Status.Message)
	}
}

type (
	dv1Op       = dv1.Operation
	dv1Approval = dv1.Approval
)

var errTest = errors.New("test failure")

func metaNow(t time.Time) metav1.Time { return metav1.NewTime(t) }

func keyOf(name string) types.NamespacedName { return types.NamespacedName{Namespace: ns, Name: name} }

func (e *env) getApproval(name string) *dv1.Approval {
	e.t.Helper()
	a := &dv1.Approval{}
	if err := e.c.Get(context.Background(), keyOf(name), a); err != nil {
		e.t.Fatal(err)
	}
	return a
}

// setStage edits the Stage's status directly, as another writer would.
func (e *env) setStage(fn func(*delivery.StageStatus)) {
	e.t.Helper()
	s := e.getStage()
	fn(&s.Status)
	if err := e.c.Status().Update(context.Background(), s); err != nil {
		e.t.Fatal(err)
	}
}
