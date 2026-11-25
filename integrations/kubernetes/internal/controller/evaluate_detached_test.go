/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"context"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	pactov1 "github.com/trianalab/pacto/integrations/kubernetes/v7/api/v1"
	"github.com/trianalab/pacto/integrations/kubernetes/v7/internal/loader"
	"github.com/trianalab/pacto/integrations/kubernetes/v7/internal/observer"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// linkedContract is the published contract a linked workload runs: one HTTP interface, one required
// dependency and an unmet readiness gate, so every branch that could write or emit is reached.
const linkedContract = `
pactoVersion: "2.0"
service:
  name: payments
  version: 2.3.0
  owner:
    team: team-a
interfaces:
  - name: api
    type: openapi
    ref: openapi.yaml
dependencies:
  - name: ledger
    ref: oci://ghcr.io/acme/contracts/ledger:1.0.0
    required: true
    compatibility: ^1.0.0
state:
  type: stateless
  dataCriticality: low
  persistence:
    durability: ephemeral
    scope: local
workload: service
`

var linkedBundle = fstest.MapFS{"openapi.yaml": {Data: []byte("openapi: 3.0.0\ninfo:\n  title: payments\n  version: '1'\npaths: {}\n")}}

const linkedLedgerRef = "ghcr.io/acme/contracts/ledger:1.0.0"

func linkedLoad(t *testing.T) func(context.Context, string, string) (*loader.LoadResult, error) {
	t.Helper()
	c, err := contract.Parse(strings.NewReader(linkedContract))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	c.Readiness = rdContract(rdCheck("security-review", 50, contract.StatusNotDone)).Readiness
	return func(context.Context, string, string) (*loader.LoadResult, error) {
		return &loader.LoadResult{Contract: c, RawYAML: []byte(linkedContract), ResolvedDigest: "sha256:ab12", BundleFS: linkedBundle}, nil
	}
}

// linkedPacto is the transient Pacto the linker builds for Deployment shop/payments.
func linkedPacto() *pactov1.Pacto {
	return &pactov1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "payments", Namespace: "shop"},
		Spec: pactov1.PactoSpec{
			ContractRef: pactov1.ContractRef{OCI: "oci://ghcr.io/acme/contracts/payments@sha256:ab12"},
			Target: pactov1.TargetRef{
				ServiceName: "payments",
				WorkloadRef: &pactov1.WorkloadRef{Kind: "Deployment", Name: "payments"},
			},
		},
	}
}

func readyService(ns, name, port string) (*corev1.Service, *discoveryv1.EndpointSlice) {
	ready, p := true, int32(8080)
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": name},
			Ports:    []corev1.ServicePort{{Name: port, Port: 8080}},
		},
	}
	slice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{Name: name + "-x", Namespace: ns, Labels: map[string]string{discoveryv1.LabelServiceName: name}},
		Endpoints:  []discoveryv1.Endpoint{{Addresses: []string{"10.0.0.1"}, Conditions: discoveryv1.EndpointConditions{Ready: &ready}}},
		Ports:      []discoveryv1.EndpointPort{{Name: &port, Port: &p}},
	}
	return svc, slice
}

// writeCounter fails the test's world on any write: every create, update, patch or delete is counted.
type writeCounter struct{ writes int }

func (w *writeCounter) funcs() interceptor.Funcs {
	return interceptor.Funcs{
		Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
			w.writes++
			return nil
		},
		Update: func(context.Context, client.WithWatch, client.Object, ...client.UpdateOption) error {
			w.writes++
			return nil
		},
		Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
			w.writes++
			return nil
		},
		Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
			w.writes++
			return nil
		},
		SubResourceUpdate: func(context.Context, client.Client, string, client.Object, ...client.SubResourceUpdateOption) error {
			w.writes++
			return nil
		},
		SubResourcePatch: func(context.Context, client.Client, string, client.Object, client.Patch, ...client.SubResourcePatchOption) error {
			w.writes++
			return nil
		},
	}
}

func detachedReconciler(t *testing.T, funcs *interceptor.Funcs, objs ...client.Object) (*PactoReconciler, *record.FakeRecorder) {
	t.Helper()
	s := newSchemeWithApps()
	_ = discoveryv1.AddToScheme(s)
	cb := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&pactov1.Pacto{}, &pactov1.PactoRevision{}).WithObjects(objs...)
	if funcs != nil {
		cb = cb.WithInterceptorFuncs(*funcs)
	}
	cl := cb.Build()
	rec := record.NewFakeRecorder(20)
	return &PactoReconciler{
		Client: cl, APIReader: cl, Scheme: s, Recorder: rec,
		Loader:              &mockLoader{loadFn: linkedLoad(t)},
		StabilizationWindow: time.Minute,
	}, rec
}

func linkedWorld() []client.Object {
	svc, slice := readyService("shop", "payments", "http")
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "payments", Namespace: "shop"},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "payments"}},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "ghcr.io/acme/payments:2.3.0"}}},
		}},
	}
	return []client.Object{svc, slice, dep}
}

func findingCodes(st *pactov1.PactoStatus) []string {
	codes := make([]string, 0, len(st.Findings))
	for _, f := range st.Findings {
		codes = append(codes, f.Code+" "+f.Subject)
	}
	slices.Sort(codes)
	return codes
}

func hasFinding(st *pactov1.PactoStatus, code finding.Code, subject string) bool {
	return slices.ContainsFunc(st.Findings, func(f pactov1.FindingStatus) bool {
		return f.Code == string(code) && strings.Contains(f.Subject, subject)
	})
}

func TestEvaluateDetachedWritesNothing(t *testing.T) {
	w := &writeCounter{}
	funcs := w.funcs()
	r, rec := detachedReconciler(t, &funcs, linkedWorld()...)
	p := linkedPacto()

	r.EvaluateDetached(context.Background(), p)

	if w.writes != 0 {
		t.Fatalf("EvaluateDetached wrote %d times; want none", w.writes)
	}
	if events := drainEvents(rec); len(events) != 0 {
		t.Fatalf("EvaluateDetached recorded events %v; want none", events)
	}
	var revisions pactov1.PactoRevisionList
	if err := r.List(context.Background(), &revisions); err != nil || len(revisions.Items) != 0 {
		t.Fatalf("revisions = %d (err %v); want none", len(revisions.Items), err)
	}
	if p.Status.CurrentRevision != "" {
		t.Errorf("CurrentRevision = %q; a transient Pacto has no revision", p.Status.CurrentRevision)
	}
	if p.Status.ContractStatus == "" || p.Status.LastReconciledAt == nil || p.Status.Readiness == nil {
		t.Fatalf("status not judged: %+v", p.Status)
	}
	if c := meta.FindStatusCondition(p.Status.Conditions, pactov1.ConditionReadinessSatisfied); c == nil || c.Status != metav1.ConditionFalse {
		t.Errorf("ReadinessSatisfied = %+v; want False on the transient status", c)
	}
}

func TestEvaluateDetachedMatchesReconcile(t *testing.T) {
	stored := linkedPacto()
	stored.UID = "u"
	r, _ := detachedReconciler(t, nil, append(linkedWorld(), stored)...)
	if _, err := r.Reconcile(context.Background(), reconcileReq("payments", "shop")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	want := getPacto(t, r, "payments", "shop").Status

	transient := linkedPacto()
	r.EvaluateDetached(context.Background(), transient)
	got := transient.Status

	if got.ContractStatus != want.ContractStatus {
		t.Errorf("ContractStatus = %s; Reconcile gave %s", got.ContractStatus, want.ContractStatus)
	}
	if g, w := findingCodes(&got), findingCodes(&want); !slices.Equal(g, w) {
		t.Errorf("findings = %v; Reconcile gave %v", g, w)
	}
	if got.EvaluationCoverage == nil || !equality.Semantic.DeepEqual(got.EvaluationCoverage, want.EvaluationCoverage) {
		t.Errorf("coverage = %+v; Reconcile gave %+v", got.EvaluationCoverage, want.EvaluationCoverage)
	}
}

func TestEvaluateDetachedLoadFailureIsUnknown(t *testing.T) {
	r, rec := detachedReconciler(t, nil)
	r.Loader = &mockLoader{loadFn: func(context.Context, string, string) (*loader.LoadResult, error) {
		return nil, &oci.RegistryUnreachableError{Ref: "ghcr.io/acme/contracts/payments", Err: context.DeadlineExceeded}
	}}
	p := linkedPacto()

	r.EvaluateDetached(context.Background(), p)

	if p.Status.ContractStatus != pactov1.ContractStatusUnknown {
		t.Errorf("ContractStatus = %s; an unreachable registry is Unknown", p.Status.ContractStatus)
	}
	if c := meta.FindStatusCondition(p.Status.Conditions, pactov1.ConditionContractValid); c == nil || c.Status != metav1.ConditionUnknown {
		t.Errorf("ContractValid = %+v; want Unknown", c)
	}
	if p.Status.LastReconciledAt == nil || len(drainEvents(rec)) != 0 {
		t.Errorf("want a stamped status and no event")
	}
}

func TestEvaluateDetachedInvalidContract(t *testing.T) {
	r, _ := detachedReconciler(t, nil)
	r.Loader = &mockLoader{loadFn: func(context.Context, string, string) (*loader.LoadResult, error) {
		return &loader.LoadResult{
			Contract: &contract.Contract{Service: contract.Service{Name: "payments", Version: "2.3.0"}},
			RawYAML:  []byte(`pactoVersion: "1.0"` + "\n"),
		}, nil
	}}
	p := linkedPacto()

	r.EvaluateDetached(context.Background(), p)

	if p.Status.ContractStatus != pactov1.ContractStatusInvalid {
		t.Errorf("ContractStatus = %s; want Invalid", p.Status.ContractStatus)
	}
	if p.Status.Validation == nil || len(p.Status.Validation.Errors) == 0 {
		t.Error("want the validation errors on the transient status")
	}
}

// A dependency on a contract only a linked workload runs resolves to that workload's Service, and a
// provider scaled to zero is a confirmed DEPENDENCY_UNREACHABLE once its window has run.
func TestDependencyResolvesToLinkedSibling(t *testing.T) {
	ledgerSvc, ledgerSlice := readyService("ledger", "ledger", "http")
	linked := func() []observer.LinkedSibling {
		return []observer.LinkedSibling{{Namespace: "ledger", ServiceName: "ledger", ResolvedRef: linkedLedgerRef, Version: "1.0.0"}}
	}

	r, _ := detachedReconciler(t, nil, append(linkedWorld(), ledgerSvc, ledgerSlice)...)
	r.Linked = linked
	p := linkedPacto()
	r.EvaluateDetached(context.Background(), p)
	if hasFinding(&p.Status, finding.CodeDependencyUnreachable, "ledger") || hasFinding(&p.Status, finding.CodeObservationUnsupported, "ledger") {
		t.Fatalf("a ready linked provider is reachable; findings %v", findingCodes(&p.Status))
	}

	r, _ = detachedReconciler(t, nil, append(linkedWorld(), ledgerSvc)...)
	r.Linked = linked
	p = linkedPacto()
	p.Status.ObservationWindows = []pactov1.ObservationWindow{{
		Kind: "dependency", Subject: "ledger", FirstObservedNegativeAt: metav1.NewTime(time.Now().Add(-time.Hour)),
	}}
	r.EvaluateDetached(context.Background(), p)
	if !hasFinding(&p.Status, finding.CodeDependencyUnreachable, "ledger") {
		t.Fatalf("a linked provider with no ready endpoint past its window is unreachable; findings %v", findingCodes(&p.Status))
	}
}

// With no interfaceBindings every interface reads OBSERVATION_UNSUPPORTED, never INTERFACE_ABSENT; name-match
// discovery can only add the positive.
func TestEvaluateDetachedUnboundInterfacesUnsupported(t *testing.T) {
	r, _ := detachedReconciler(t, nil, linkedWorld()...)
	p := linkedPacto()
	r.EvaluateDetached(context.Background(), p)
	if !hasFinding(&p.Status, finding.CodeObservationUnsupported, "api") || hasFinding(&p.Status, finding.CodeInterfaceAbsent, "api") {
		t.Fatalf("unbound interface: findings %v; want OBSERVATION_UNSUPPORTED only", findingCodes(&p.Status))
	}

	svc, slice := readyService("shop", "payments", "api")
	r, _ = detachedReconciler(t, nil, svc, slice)
	r.EnableInterfaceNameMatchDiscovery = true
	p = linkedPacto()
	r.EvaluateDetached(context.Background(), p)
	if hasFinding(&p.Status, finding.CodeObservationUnsupported, "api") || hasFinding(&p.Status, finding.CodeInterfaceAbsent, "api") {
		t.Fatalf("a port named after the interface is observed; findings %v", findingCodes(&p.Status))
	}
}
