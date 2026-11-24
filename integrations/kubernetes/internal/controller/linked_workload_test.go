/*
Copyright 2026.

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
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pactov1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/linker"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/loader"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
)

const ledgerContract = `
pactoVersion: "2.0"
service:
  name: ledger
  version: 1.0.0
  owner:
    team: team-b
state:
  type: stateless
  dataCriticality: low
  persistence:
    durability: ephemeral
    scope: local
workload: service
`

// annotated is Deployment ns/name carrying the contract annotation ref, with
// a Service selecting its pods.
func annotated(ns, name, ref string) []client.Object {
	labels := map[string]string{"app": name}
	return []client.Object{
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Annotations: map[string]string{linker.AnnotationContract: ref}},
			Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "ghcr.io/acme/" + name + ":1"}}}}}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
			Spec: corev1.ServiceSpec{Selector: labels, Ports: []corev1.ServicePort{{Name: "http", Port: 8080}}}},
	}
}

// A workload no Pacto targets, linked by its annotation, is judged with the
// finding codes a Pacto gets: a dependency on a linked provider scaled to zero
// is DEPENDENCY_UNREACHABLE once its window has run, an unbound interface is
// OBSERVATION_UNSUPPORTED, and no Pacto object is ever created.
func TestLinkedWorkloadEvaluatedWithoutPacto(t *testing.T) {
	payments := linkedLoad(t)
	ledger, err := contract.Parse(strings.NewReader(ledgerContract))
	if err != nil {
		t.Fatal(err)
	}
	s := newSchemeWithApps()
	_ = discoveryv1.AddToScheme(s)
	_ = batchv1.AddToScheme(s)
	objs := append(annotated("shop", "payments", "oci://ghcr.io/acme/contracts/payments:2.3.0"),
		annotated("ledger", "ledger", "oci://"+linkedLedgerRef)...)
	objs = append(objs, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "pacto-system", Name: "pacto-operator", UID: "op"}})
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()
	r := &PactoReconciler{Client: cl, APIReader: cl, Scheme: s, Recorder: record.NewFakeRecorder(20), StabilizationWindow: time.Millisecond,
		Loader: &mockLoader{loadFn: func(ctx context.Context, ref, inline string) (*loader.LoadResult, error) {
			if strings.Contains(ref, "ledger") {
				return &loader.LoadResult{Contract: ledger, RawYAML: []byte(ledgerContract), ResolvedRef: linkedLedgerRef, ResolvedDigest: "sha256:cd"}, nil
			}
			return payments(ctx, ref, inline)
		}}}
	l, _ := linker.ForOperator(linker.Config{DeploymentName: "pacto-operator", Namespace: "pacto-system", Reader: cl, Writer: cl})
	l.Evaluate, l.EvaluateAnonymous = r.EvaluateDetached, r.EvaluateDetached
	r.Linked = l.Siblings

	// The first cycle links both; the second resolves the dependency to the
	// linked ledger and opens its window; the third confirms it.
	for i := range 3 {
		if err := l.Sync(context.Background()); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			time.Sleep(10 * time.Millisecond)
		}
	}
	var st *pactov1.PactoStatus
	for _, v := range l.Current().Targets {
		if v.Metadata.Name == "Deployment/payments" {
			st = &pactov1.PactoStatus{}
			for _, f := range v.Status.Findings {
				st.Findings = append(st.Findings, pactov1.FindingStatus{Code: f.Code, Subject: f.Subject})
			}
		}
	}
	if st == nil {
		t.Fatalf("payments is not linked: %+v", l.Current())
	}
	if !hasFinding(st, finding.CodeDependencyUnreachable, "ledger") {
		t.Errorf("want DEPENDENCY_UNREACHABLE on ledger; findings %v", findingCodes(st))
	}
	if !hasFinding(st, finding.CodeObservationUnsupported, "api") || hasFinding(st, finding.CodeInterfaceAbsent, "api") {
		t.Errorf("an unbound interface must read OBSERVATION_UNSUPPORTED; findings %v", findingCodes(st))
	}
	var pactos pactov1.PactoList
	if err := cl.List(context.Background(), &pactos); err != nil || len(pactos.Items) != 0 {
		t.Fatalf("Pacto objects: %d (%v); want none", len(pactos.Items), err)
	}
}
