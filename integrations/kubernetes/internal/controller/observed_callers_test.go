/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pactov1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/loader"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/nodeagent"
	"github.com/trianalab/pacto/v4/pkg/contract"
)

// TestReconcile_ObservedCallersCoverage writes the caller coverage with the
// refresh interval a reader judges its age by, and never carries a complete
// coverage from an older evaluation over: here the agent left no data, so the
// earlier complete coverage turns unavailable.
func TestReconcile_ObservedCallersCoverage(t *testing.T) {
	pacto := &pactov1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "pay", Namespace: "default", UID: "u"},
		Spec: pactov1.PactoSpec{
			ContractRef:          pactov1.ContractRef{OCI: "oci://r/pactos/payments@" + evaluatedDigest},
			Target:               pactov1.TargetRef{ServiceName: "pay"},
			CheckIntervalSeconds: 60,
		},
		Status: pactov1.PactoStatus{ObservedCallersCoverage: &pactov1.CallerCoverage{State: pactov1.CallerCoverageComplete}},
	}
	cl := fake.NewClientBuilder().WithScheme(newSchemeWithApps()).
		WithStatusSubresource(&pactov1.Pacto{}, &pactov1.PactoRevision{}).WithObjects(pacto).Build()
	r := &PactoReconciler{Client: cl, Scheme: cl.Scheme(), Recorder: record.NewFakeRecorder(20),
		KernelEvidence: true, KernelEvidenceNamespace: "pacto-system",
		Loader: &mockLoader{loadFn: func(context.Context, string, string) (*loader.LoadResult, error) {
			return &loader.LoadResult{
				Contract:       &contract.Contract{Service: contract.Service{Name: "payments", Version: "1.0.0"}, Workload: "service"},
				RawYAML:        []byte(validContract),
				ResolvedRef:    "r/pactos/payments@" + evaluatedDigest,
				ResolvedDigest: evaluatedDigest,
			}, nil
		}}}
	if _, err := r.Reconcile(context.Background(), reconcileReq("pay", "default")); err != nil {
		t.Fatal(err)
	}
	got := getPacto(t, r, "pay", "default").Status
	if c := got.ObservedCallersCoverage; c == nil || c.State != pactov1.CallerCoverageUnavailable || c.RefreshSeconds != 60 {
		t.Fatalf("coverage = %+v, want unavailable with a 60s refresh", c)
	}
	if got.ObservedCallers != nil {
		t.Fatalf("callers = %+v", got.ObservedCallers)
	}
}

// TestStatusCarriesExternalsAndFormat3 writes the externals the target's pod
// spec names, their coverage with the refresh interval and the catalog digest,
// in the current status format.
func TestStatusCarriesExternalsAndFormat3(t *testing.T) {
	pacto := &pactov1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "pay", Namespace: "default", UID: "u"},
		Spec: pactov1.PactoSpec{
			ContractRef:          pactov1.ContractRef{OCI: "oci://r/pactos/payments@" + evaluatedDigest},
			Target:               pactov1.TargetRef{ServiceName: "pay", WorkloadRef: &pactov1.WorkloadRef{Name: "pay", Kind: "Deployment"}},
			CheckIntervalSeconds: 60,
		},
	}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "pay", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "app", Env: []corev1.EnvVar{{Name: "STRIPE_API_BASE", Value: "https://api.stripe.com/v1"}},
		}}}}},
	}
	cl := fake.NewClientBuilder().WithScheme(newSchemeWithApps()).
		WithStatusSubresource(&pactov1.Pacto{}, &pactov1.PactoRevision{}).WithObjects(pacto, dep).Build()
	r := &PactoReconciler{Client: cl, Scheme: cl.Scheme(), Recorder: record.NewFakeRecorder(20),
		Loader: &mockLoader{loadFn: func(context.Context, string, string) (*loader.LoadResult, error) {
			return &loader.LoadResult{
				Contract:       &contract.Contract{Service: contract.Service{Name: "payments", Version: "1.0.0"}, Workload: "service"},
				RawYAML:        []byte(validContract),
				ResolvedRef:    "r/pactos/payments@" + evaluatedDigest,
				ResolvedDigest: evaluatedDigest,
			}, nil
		}}}
	if _, err := r.Reconcile(context.Background(), reconcileReq("pay", "default")); err != nil {
		t.Fatal(err)
	}
	got := getPacto(t, r, "pay", "default").Status
	if got.StatusFormat != pactov1.PactoStatusFormat {
		t.Fatalf("statusFormat = %d", got.StatusFormat)
	}
	if len(got.Externals) != 1 || got.Externals[0].Identity != "stripe" || got.Externals[0].Host != "api.stripe.com" {
		t.Fatalf("externals = %+v", got.Externals)
	}
	if c := got.ExternalsCoverage; c == nil || c.Reason != "serviceentry-reader-off" || c.RefreshSeconds != 60 {
		t.Fatalf("coverage = %+v", c)
	}
	if !strings.HasSuffix(got.ExternalCatalog, " (embedded)") {
		t.Fatalf("catalog = %q", got.ExternalCatalog)
	}
}

// TestEvaluateRuntimeWritesObservedEgress writes the externals the node agent
// saw the target's pods call, with their coverage, node counts and the refresh
// interval, in status format 4.
func TestEvaluateRuntimeWritesObservedEgress(t *testing.T) {
	pacto := &pactov1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "pay", Namespace: "default", UID: "u"},
		Spec: pactov1.PactoSpec{
			ContractRef:          pactov1.ContractRef{OCI: "oci://r/pactos/payments@" + evaluatedDigest},
			Target:               pactov1.TargetRef{WorkloadRef: &pactov1.WorkloadRef{Name: "pay", Kind: "Deployment"}},
			CheckIntervalSeconds: 60,
		},
	}
	sel := map[string]string{"app": "pay"}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "pay", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{MatchLabels: sel},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: sel}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}}}},
	}
	now := metav1.Now()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pay-1", Namespace: "default", UID: "pod-1", Labels: sel},
		Spec:       corev1.PodSpec{NodeName: "node-a"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.0.0.5", StartTime: &metav1.Time{Time: now.Add(-time.Hour)}},
	}
	nro := &pactov1.NodeRuntimeObservation{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a", Namespace: "pacto-system", Labels: nodeagent.Labels()},
		Status: pactov1.NodeRuntimeObservationStatus{
			NodeName: "node-a", PublishIntervalSeconds: 30, LastPublished: &now,
			Collection: pactov1.RuntimeCollection{State: pactov1.CollectionRunning},
			DNS:        pactov1.RuntimeDNS{State: pactov1.CollectionRunning},
			Pods: []pactov1.PodRuntimeObservation{{UID: "pod-1", Connections: []pactov1.RuntimeConnection{{
				Address: "54.1.1.1", Host: "api.stripe.com", Port: 443, Successes: 1, LastSuccessAt: &now,
			}}}},
		},
	}
	s := newSchemeWithApps()
	_ = coordinationv1.AddToScheme(s)
	_ = discoveryv1.AddToScheme(s)
	cl := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&pactov1.Pacto{}, &pactov1.PactoRevision{}).WithObjects(pacto, dep, pod, nro).Build()
	r := &PactoReconciler{Client: cl, Scheme: cl.Scheme(), Recorder: record.NewFakeRecorder(20),
		KernelEvidence: true, KernelEvidenceNamespace: "pacto-system",
		Loader: &mockLoader{loadFn: func(context.Context, string, string) (*loader.LoadResult, error) {
			return &loader.LoadResult{
				Contract:       &contract.Contract{Service: contract.Service{Name: "payments", Version: "1.0.0"}, Workload: "service"},
				RawYAML:        []byte(validContract),
				ResolvedRef:    "r/pactos/payments@" + evaluatedDigest,
				ResolvedDigest: evaluatedDigest,
			}, nil
		}}}
	if _, err := r.Reconcile(context.Background(), reconcileReq("pay", "default")); err != nil {
		t.Fatal(err)
	}
	got := getPacto(t, r, "pay", "default").Status
	if got.StatusFormat != 4 {
		t.Fatalf("statusFormat = %d", got.StatusFormat)
	}
	if len(got.ObservedEgress) != 1 || got.ObservedEgress[0].Identity != "stripe" || got.ObservedEgress[0].Source != pactov1.EgressSourceKernel {
		t.Fatalf("egress = %+v", got.ObservedEgress)
	}
	c := got.ObservedEgressCoverage
	if c == nil || c.State != pactov1.CallerCoverageComplete || c.RefreshSeconds != 60 || c.Nodes != 1 || c.NodesWithAgent != 1 {
		t.Fatalf("coverage = %+v", c)
	}
}
