/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package observer

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pactoapi "github.com/trianalab/pacto/integrations/kubernetes/v7/api/v1"
	"github.com/trianalab/pacto/v4/pkg/contract"
)

const linkedRef = "ghcr.io/acme/contracts/payments:2.3.0"

var linkedDep = contract.Dependency{Name: "payments", Ref: "oci://ghcr.io/acme/contracts/payments:2.3.0", Compatibility: "^2.0.0"}

func crProvider(ns, svc string) *pactoapi.Pacto {
	return &pactoapi.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "payments", Namespace: ns},
		Spec:       pactoapi.PactoSpec{Target: pactoapi.TargetRef{ServiceName: svc}},
		Status:     pactoapi.PactoStatus{Contract: &pactoapi.ContractInfo{ResolvedRef: linkedRef, Version: "2.3.0"}},
	}
}

func linkedObserver(t *testing.T, linked []LinkedSibling, objs ...runtime.Object) *Observer {
	t.Helper()
	s := runtime.NewScheme()
	_ = pactoapi.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	return New(fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(objs...).Build()).
		WithLinked(func() []LinkedSibling { return linked })
}

func resolveLinked(t *testing.T, o *Observer) (*dependencyTarget, bool) {
	t.Helper()
	target, ambiguous, err := o.resolveDependencyToSibling(context.Background(), "shop", linkedDep)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return target, ambiguous
}

func TestDependencyResolvesToLinkedSiblingWhenNoCR(t *testing.T) {
	target, ambiguous := resolveLinked(t, linkedObserver(t, []LinkedSibling{
		{Namespace: "payments", ServiceName: "payments", ResolvedRef: linkedRef, Version: "2.3.0"},
		{Namespace: "billing", ServiceName: "billing", ResolvedRef: "ghcr.io/acme/contracts/billing:1.0.0", Version: "1.0.0"},
	}))
	if ambiguous || target == nil || target.namespace != "payments" || target.serviceName != "payments" || target.version != "2.3.0" {
		t.Fatalf("target = %+v, ambiguous = %v; want payments/payments 2.3.0", target, ambiguous)
	}
}

// A workload someone links in the consumer's namespace never takes the provider away from its CR in
// another namespace: the namespace-first rule applies among linked siblings alone.
func TestLinkedSiblingNeverShadowsRemoteCR(t *testing.T) {
	target, ambiguous := resolveLinked(t, linkedObserver(t,
		[]LinkedSibling{{Namespace: "shop", ServiceName: "impostor", ResolvedRef: linkedRef, Version: "2.3.0"}},
		crProvider("payments", "payments-cr")))
	if ambiguous || target == nil || target.namespace != "payments" || target.serviceName != "payments-cr" {
		t.Fatalf("target = %+v, ambiguous = %v; want the CR's payments/payments-cr", target, ambiguous)
	}
}

func TestLinkedSiblingNeverMakesCRAmbiguous(t *testing.T) {
	target, ambiguous := resolveLinked(t, linkedObserver(t,
		[]LinkedSibling{{Namespace: "other", ServiceName: "payments", ResolvedRef: linkedRef, Version: "2.3.0"}},
		crProvider("payments", "payments-cr")))
	if ambiguous || target == nil || target.serviceName != "payments-cr" {
		t.Fatalf("target = %+v, ambiguous = %v; want the CR, never Insufficient", target, ambiguous)
	}
}

func TestLinkedSiblingsFollowNamespaceFirstAndAmbiguity(t *testing.T) {
	local := LinkedSibling{Namespace: "shop", ServiceName: "payments", ResolvedRef: linkedRef, Version: "2.3.0"}
	remote := LinkedSibling{Namespace: "prod", ServiceName: "payments", ResolvedRef: linkedRef, Version: "2.3.0"}
	if target, _ := resolveLinked(t, linkedObserver(t, []LinkedSibling{remote, local})); target == nil || target.namespace != "shop" {
		t.Fatalf("target = %+v; want the linked sibling in the consumer's namespace", target)
	}
	other := LinkedSibling{Namespace: "prod", ServiceName: "payments-b", ResolvedRef: linkedRef, Version: "2.4.0"}
	if target, ambiguous := resolveLinked(t, linkedObserver(t, []LinkedSibling{remote, other})); target != nil || !ambiguous {
		t.Fatalf("target = %+v, ambiguous = %v; two linked targets inside the range are ambiguous", target, ambiguous)
	}
}

func TestWithLinkedNilResolvesNothing(t *testing.T) {
	s := runtime.NewScheme()
	_ = pactoapi.AddToScheme(s)
	o := New(fake.NewClientBuilder().WithScheme(s).Build()).WithLinked(nil)
	target, ambiguous, err := o.resolveDependencyToSibling(context.Background(), "shop", linkedDep)
	if err != nil || ambiguous || target != nil {
		t.Fatalf("target = %+v, ambiguous = %v, err = %v; want no match", target, ambiguous, err)
	}
}
