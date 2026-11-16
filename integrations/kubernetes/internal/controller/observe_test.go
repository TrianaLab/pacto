/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/loader"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/observer"
	"github.com/trianalab/pacto/v4/pkg/contract"
)

func labelledNamespace(name, value string) *corev1.Namespace {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if value != "" {
		ns.Labels = map[string]string{observer.LabelObserve: value}
	}
	return ns
}

// namespaceReader serves Namespace reads; getErr, when set, answers every Get instead.
func namespaceReader(t *testing.T, gets *int, getErr error, objs ...client.Object) client.Reader {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(objs...).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.Namespace); ok {
				*gets++
				if getErr != nil {
					return getErr
				}
			}
			return c.Get(ctx, key, obj, opts...)
		},
	}).Build()
}

func TestObserveSettingsReadsNamespaceOnlyWhenAuto(t *testing.T) {
	for _, tc := range []struct {
		probing, metrics observer.Mode
		wantGets         int
	}{
		{observer.ModeOn, observer.ModeOff, 0},
		{"", "", 0},
		{observer.ModeAuto, observer.ModeOff, 1},
		{observer.ModeOn, observer.ModeAuto, 1},
		{observer.ModeAuto, observer.ModeAuto, 1},
	} {
		gets := 0
		r := &PactoReconciler{Probing: tc.probing, MetricsObservation: tc.metrics,
			APIReader: namespaceReader(t, &gets, nil, labelledNamespace("payments", "probing"))}
		if _, _, err := r.observeSettings(context.Background(), "payments"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gets != tc.wantGets {
			t.Errorf("probing=%q metrics=%q: %d namespace reads, want %d", tc.probing, tc.metrics, gets, tc.wantGets)
		}
	}
}

func TestObserveSettingsLabelTurnsProbingOn(t *testing.T) {
	gets := 0
	r := &PactoReconciler{Probing: observer.ModeAuto, MetricsObservation: observer.ModeAuto,
		APIReader: namespaceReader(t, &gets, nil, labelledNamespace("payments", "probing"), labelledNamespace("orders", ""))}
	p, m, err := r.observeSettings(context.Background(), "payments")
	if err != nil || !p.On || m.On {
		t.Fatalf("payments: probing %+v metrics %+v err %v; want probing on, metrics off", p, m, err)
	}
	p, m, err = r.observeSettings(context.Background(), "orders")
	if err != nil || p.On || m.On {
		t.Fatalf("orders: probing %+v metrics %+v err %v; want both off", p, m, err)
	}
}

func TestObserveSettingsForbiddenIsOff(t *testing.T) {
	gets := 0
	forbidden := apierrors.NewForbidden(corev1.Resource("namespaces"), "payments", errors.New("no grant"))
	r := &PactoReconciler{Probing: observer.ModeAuto, MetricsObservation: observer.ModeAuto,
		APIReader: namespaceReader(t, &gets, forbidden)}
	p, m, err := r.observeSettings(context.Background(), "payments")
	if err != nil {
		t.Fatalf("Forbidden is a standing state, want no error, got %v", err)
	}
	if p.On || m.On {
		t.Fatalf("probing %+v metrics %+v; an unreadable namespace never turns a feature on", p, m)
	}
	if !strings.HasSuffix(p.Source, "namespace label unreadable: forbidden") {
		t.Errorf("source %q must name the forbidden read", p.Source)
	}
}

func TestObserveSettingsMetricsLabelNamesMonitorGrant(t *testing.T) {
	gets := 0
	r := &PactoReconciler{Probing: observer.ModeOff, MetricsObservation: observer.ModeAuto,
		APIReader: namespaceReader(t, &gets, nil, labelledNamespace("payments", "metrics"))}
	_, m, err := r.observeSettings(context.Background(), "payments")
	if err != nil || !m.On {
		t.Fatalf("metrics %+v err %v; want on", m, err)
	}
	if !strings.HasSuffix(m.Source, "; ServiceMonitor discovery needs controller.metricsObservation=on") {
		t.Errorf("source %q must name the monitor grant", m.Source)
	}
	r.MetricsObservation = observer.ModeOn
	if _, m, _ = r.observeSettings(context.Background(), "payments"); strings.Contains(m.Source, "ServiceMonitor") {
		t.Errorf("metrics on by flag must not name the monitor grant, got %q", m.Source)
	}
}

func observedPacto(name string) *pactov1alpha1.Pacto {
	return &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", UID: "u"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: validContract},
			Target:      pactov1alpha1.TargetRef{ServiceName: "svc"},
		},
	}
}

func TestObserveSettingsTransientErrorMarksObservationFailed(t *testing.T) {
	const raw = "dial tcp 10.0.0.1:443: i/o timeout secret-detail"
	for _, tc := range []struct {
		name       string
		err        error
		wantStatus metav1.ConditionStatus
		wantReason string
	}{
		{"timeout", apierrors.NewTimeoutError(raw, 1), metav1.ConditionFalse, "timeout"},
		{"forbidden", apierrors.NewForbidden(corev1.Resource("namespaces"), "default", errors.New(raw)), metav1.ConditionTrue, "forbidden"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pacto := observedPacto("transient")
			r := newReconciler()
			r.Scheme = newSchemeWithApps()
			r.Client = fake.NewClientBuilder().WithScheme(r.Scheme).WithStatusSubresource(&pactov1alpha1.Pacto{}).
				WithObjects(pacto, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "default"}}).Build()
			r.Loader = &mockLoader{loadFn: func(_ context.Context, _, _ string) (*loader.LoadResult, error) {
				return &loader.LoadResult{
					Contract: &contract.Contract{Service: contract.Service{Name: "svc", Version: "1.0.0"}, Workload: "service"},
					RawYAML:  []byte(validContract),
				}, nil
			}}
			gets := 0
			r.APIReader = namespaceReader(t, &gets, tc.err)
			r.Probing, r.MetricsObservation = observer.ModeAuto, observer.ModeAuto
			if _, err := r.Reconcile(context.Background(), reconcileReq(pacto.Name, pacto.Namespace)); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			cond := meta.FindStatusCondition(getPacto(t, r, pacto.Name, pacto.Namespace).Status.Conditions, pactov1alpha1.ConditionRuntimeObserved)
			if cond == nil || cond.Status != tc.wantStatus {
				t.Fatalf("RuntimeObserved = %+v, want %s", cond, tc.wantStatus)
			}
			if tc.wantStatus == metav1.ConditionFalse && (cond.Reason != pactov1alpha1.ReasonObservationFailed ||
				!strings.Contains(cond.Message, "namespace default: observe label unreadable")) {
				t.Errorf("condition = %s %q, want ObservationFailed naming the label read", cond.Reason, cond.Message)
			}
			if !strings.Contains(cond.Message, "probing off (--enable-probing=auto, namespace label unreadable: "+tc.wantReason+")") {
				t.Errorf("message %q must name %s", cond.Message, tc.wantReason)
			}
			if strings.Contains(cond.Message, "secret-detail") {
				t.Errorf("message %q leaks the raw error text", cond.Message)
			}
		})
	}
}

func TestRuntimeObservedMessageNamesSource(t *testing.T) {
	p := observer.Setting{On: true, Source: "namespace label pacto.trianalab.io/observe=probing"}
	m := observer.Setting{Source: "--enable-metrics-observation=off"}
	clause := "probing on (namespace label pacto.trianalab.io/observe=probing); metrics off (--enable-metrics-observation=off)"
	if got := observeMessage(p, m); got != clause {
		t.Fatalf("observeMessage = %q, want %q", got, clause)
	}
	r := newReconciler()
	pacto := &pactov1alpha1.Pacto{}
	r.setRuntimeObservedCondition(pacto, nil, clause)
	cond := meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionRuntimeObserved)
	if cond.Status != metav1.ConditionTrue || !strings.HasSuffix(cond.Message, ". Observation: "+clause) {
		t.Errorf("True message %q must end with the clauses", cond.Message)
	}
	r.setRuntimeObservedCondition(pacto, errors.New("workload: boom"), clause)
	cond = meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionRuntimeObserved)
	if cond.Status != metav1.ConditionFalse || !strings.HasSuffix(cond.Message, ". Observation: "+clause) {
		t.Errorf("False message %q must end with the clauses", cond.Message)
	}
}

func TestBuildCollectInputUsesResolvedSettings(t *testing.T) {
	// The reconciler's modes are auto; only the resolved settings reach the collectors.
	r := &PactoReconciler{Probing: observer.ModeAuto, MetricsObservation: observer.ModeAuto}
	for _, tc := range []struct{ probing, metrics bool }{{true, false}, {false, true}, {false, false}} {
		in := r.buildCollectInput(&pactov1alpha1.Pacto{}, &effectiveContract{loaded: &loader.LoadResult{}},
			observer.Setting{On: tc.probing}, observer.Setting{On: tc.metrics})
		if in.EnableProbing != tc.probing || in.EnableMetricsObservation != tc.metrics {
			t.Errorf("input probing=%v metrics=%v, want %v %v", in.EnableProbing, in.EnableMetricsObservation, tc.probing, tc.metrics)
		}
	}
}
