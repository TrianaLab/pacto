/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"context"
	"encoding/json"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/loader"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

const evaluatedDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

func reconcileEvaluated(t *testing.T, withWorkload bool) *pactov1alpha1.Pacto {
	t.Helper()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "pay", Namespace: "default", UID: "u"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{OCI: "oci://r/pactos/payments@" + evaluatedDigest},
			Target:      pactov1alpha1.TargetRef{WorkloadRef: &pactov1alpha1.WorkloadRef{Name: "app", Kind: "Deployment"}},
		},
	}
	b := fake.NewClientBuilder().WithScheme(newSchemeWithApps()).
		WithStatusSubresource(&pactov1alpha1.Pacto{}, &pactov1alpha1.PactoRevision{}).WithObjects(pacto)
	if withWorkload {
		b = b.WithObjects(&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default", UID: "w-1", Generation: 3}})
	}
	cl := b.Build()
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
	return getPacto(t, r, "pay", "default")
}

// TestReconcile_EvaluatedRevisionTiesTheResultToTheVersion proves the status
// names the contract digest and the workload generation the verdict came from.
func TestReconcile_EvaluatedRevisionTiesTheResultToTheVersion(t *testing.T) {
	got := reconcileEvaluated(t, true).Status.EvaluatedRevision
	want := &pactov1alpha1.EvaluatedRevision{ContractDigest: evaluatedDigest,
		Workload: &pactov1alpha1.EvaluatedWorkload{Kind: "Deployment", Name: "app", UID: "w-1", Generation: 3}}
	gj, _ := json.Marshal(got)
	wj, _ := json.Marshal(want)
	if string(gj) != string(wj) {
		t.Fatalf("evaluatedRevision = %s, want %s", gj, wj)
	}
	if w := reconcileEvaluated(t, false).Status.EvaluatedRevision; w == nil || w.Workload != nil || w.ContractDigest != evaluatedDigest {
		t.Fatalf("an absent workload is not an evaluated one: %+v", w)
	}
}

// TestPactoStatusFillsTheDeliveryReadContract is the stable read contract: the
// Pacto status the operator writes must decode into every field Pacto delivery
// reads. A rename here fails this test instead of silently starving delivery.
func TestPactoStatusFillsTheDeliveryReadContract(t *testing.T) {
	p := reconcileEvaluated(t, true)
	p.Status.Dependencies = []pactov1alpha1.DependencyInfo{{Name: "ledger", Ref: "oci://r/ledger", Required: true, Compatibility: "^1"}}
	p.Status.Contract.Owner = &pactov1alpha1.OwnerInfo{Team: "team-pay"}
	p.Generation = 2
	p.Status.EvaluatedRevision.KernelCoverage = "Complete"
	p.Status.ObservedCallers = []pactov1alpha1.ObservedCaller{{Namespace: "pay", Kind: "Deployment", Name: "legacy", Source: "ebpf"}}
	p.Status.ObservedCallersCoverage = &pactov1alpha1.CallerCoverage{State: "partial", Reason: "stale-node", RefreshSeconds: 300}
	p.Status.Findings = append(p.Status.Findings, pactov1alpha1.FindingStatus{Code: "DEPENDENCY_UNDECLARED", Severity: "warning",
		Category: "RuntimeDrift", Subject: "dependency/fraud", Message: "m",
		EvidenceRefs: []pactov1alpha1.EvidenceRefStatus{{Source: "k8s", ObservedAt: "t", Collector: "ebpf", DetectedAt: "2026-10-20T10:00:00Z"}}})
	data, _ := json.Marshal(p)
	var v delivery.PactoView
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	s := v.Status
	if v.Metadata.Namespace != "default" || v.Metadata.Name != "pay" || v.Metadata.Generation != 2 ||
		s.ContractStatus == "" || s.LastReconciledAt == "" ||
		s.Contract == nil || s.Contract.ServiceName != "payments" || s.Contract.Version != "1.0.0" || s.Contract.ResolvedRef == "" ||
		s.Contract.Owner == nil || s.Contract.Owner.Team != "team-pay" ||
		len(s.Dependencies) != 1 || s.Dependencies[0].Compatibility != "^1" || !s.Dependencies[0].Required ||
		s.EvaluatedRevision == nil || s.EvaluatedRevision.ContractDigest != evaluatedDigest ||
		s.EvaluatedRevision.Workload.UID != "w-1" || s.EvaluatedRevision.Workload.Generation != 3 ||
		s.EvaluatedRevision.KernelCoverage != "Complete" || !hasKernelFinding(s.Findings) ||
		len(s.ObservedCallers) != 1 || s.ObservedCallers[0] != (delivery.ObservedCallerView{Namespace: "pay", Kind: "Deployment", Name: "legacy", Source: "ebpf"}) ||
		s.ObservedCallersCoverage == nil || *s.ObservedCallersCoverage != (delivery.CallerCoverageView{State: "partial", Reason: "stale-node", RefreshSeconds: 300}) {
		t.Fatalf("the delivery read contract lost a field: %+v", v)
	}
}

func hasKernelFinding(fs []delivery.FindingView) bool {
	for _, f := range fs {
		if f.Code == "DEPENDENCY_UNDECLARED" && f.Subject == "dependency/fraud" && f.Severity == "warning" && f.Message == "m" &&
			len(f.EvidenceRefs) == 1 && f.EvidenceRefs[0].Collector == "ebpf" && f.EvidenceRefs[0].DetectedAt != "" {
			return true
		}
	}
	return false
}

// TestStatusCarriesGuaranteesToDelivery proves the status records what each
// interface promises, its lifecycle and epoch, and what each dependency
// requires, under the names delivery decodes back into the contract types: a
// fleet rebuilt from status then evaluates guarantees as one built from
// bundles does.
func TestStatusCarriesGuaranteesToDelivery(t *testing.T) {
	ifaces := []contract.Interface{
		{Name: "events", Type: "asyncapi", Ref: "asyncapi.yaml", CompatibilityEpoch: 2,
			Guarantees: &contract.Guarantees{Profile: "1", Delivery: "at-least-once", Ordering: "per-key", DedupeWindow: "5m", Retention: "168h"},
			Lifecycle:  &contract.InterfaceLifecycle{Deprecated: "2026-09-01", Sunset: "2027-03-01", Successor: "events-v2"}},
		{Name: "api", Type: "openapi", Ref: "openapi.yaml", Guarantees: &contract.Guarantees{Idempotent: true, Timeout: "30s"}},
		{Name: "plain", Type: "grpc", Ref: "plain.proto"},
	}
	requires := []contract.Requirement{
		{Interface: "api", Guarantees: contract.Guarantees{Idempotent: true, Timeout: "1m"}},
		{Interface: "events", Guarantees: contract.Guarantees{Delivery: "at-least-once"}},
	}
	p := &pactov1alpha1.Pacto{}
	newReconciler().populateContractStatus(p, &loader.LoadResult{Contract: &contract.Contract{
		Service:    contract.Service{Name: "payments", Version: "1.0.0"},
		Interfaces: ifaces,
		Dependencies: []contract.Dependency{
			{Name: "ledger", Ref: "oci://r/ledger", Compatibility: "^1", Requires: requires},
			{Name: "fraud", Ref: "oci://r/fraud"},
		},
	}})
	if g := p.Status.Interfaces[0]; g.CompatibilityEpoch != 2 || g.Lifecycle == nil || g.Lifecycle.Successor != "events-v2" ||
		g.Guarantees == nil || g.Guarantees.Retention != "168h" || p.Status.Interfaces[2].Guarantees != nil || p.Status.Interfaces[2].Lifecycle != nil {
		t.Fatalf("status.interfaces lost a field: %+v", p.Status.Interfaces)
	}
	if d := p.Status.Dependencies; len(d[0].Requires) != 2 || d[0].Requires[0].Timeout != "1m" || len(d[1].Requires) != 0 {
		t.Fatalf("status.dependencies lost requires: %+v", d)
	}
	data, _ := json.Marshal(p)
	var v delivery.PactoView
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	gi, _ := json.Marshal(v.Status.Interfaces)
	wi, _ := json.Marshal(ifaces)
	gr, _ := json.Marshal(v.Status.Dependencies[0].Requires)
	wr, _ := json.Marshal(requires)
	if string(gi) != string(wi) || string(gr) != string(wr) {
		t.Fatalf("delivery decodes\n%s\n%s\nwant\n%s\n%s", gi, gr, wi, wr)
	}
}
