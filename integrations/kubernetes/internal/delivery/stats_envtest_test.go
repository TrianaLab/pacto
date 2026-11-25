/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus/testutil"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v7/api/delivery/v1"
	"github.com/trianalab/pacto/integrations/kubernetes/v7/internal/runstats"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// statsResult is what concluding a freeze left: the operation's status, the
// Stage's conditions, the reconciles it took and the slowest of them.
type statsResult struct {
	status     delivery.OperationStatus
	conditions []string
	calls      int
	slowest    time.Duration
}

// statsFreeze raises a freeze on stage and reconciles it with r until it
// concludes, at most 20 times.
func statsFreeze(ctx context.Context, t *testing.T, c client.Client, r *OperationReconciler, stage string) statsResult {
	t.Helper()
	op := opObj("freeze-"+stage, delivery.TypeFreeze, dA, "", "alice")
	op.UID, op.Spec.Stage = "", stage
	s := &dv1.Stage{}
	must(t, c.Get(ctx, keyOf(stage), s)) // the apiserver defaults fields the digest covers
	op.Spec.IntentDigest = delivery.IntentDigest(op.Spec, s.Spec)
	must(t, c.Create(ctx, op))
	var res statsResult
	for res.calls < 20 && op.Status.Phase != delivery.PhaseConcluded {
		start := time.Now()
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: op.Name}})
		must(t, err)
		res.slowest, res.calls = max(res.slowest, time.Since(start)), res.calls+1
		must(t, c.Get(ctx, keyOf(op.Name), op))
	}
	must(t, c.Get(ctx, keyOf(stage), s))
	for _, cond := range s.Status.Conditions {
		res.conditions = append(res.conditions, cond.Type+"/"+cond.Status+"/"+cond.Reason+"/"+cond.Message)
	}
	res.status = op.Status
	return res
}

// statsCluster starts an envtest apiserver with an enforcing admission
// policy and the Stages a and b, each running A.
func statsCluster(ctx context.Context, t *testing.T) client.Client {
	t.Helper()
	ctrl.SetLogger(logr.Discard())
	te := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true}
	cfg, err := te.Start()
	must(t, err)
	t.Cleanup(func() { _ = te.Stop() })
	c, err := client.New(cfg, client.Options{Scheme: newScheme()})
	must(t, err)
	must(t, c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}))
	fail := admissionv1.Fail
	// An enforcing policy that matches nothing this test writes: the promoter
	// admits nothing without one.
	must(t, c.Create(ctx, &admissionv1.ValidatingAdmissionPolicy{ObjectMeta: metav1.ObjectMeta{Name: "pd-operation"},
		Spec: admissionv1.ValidatingAdmissionPolicySpec{FailurePolicy: &fail,
			MatchConstraints: &admissionv1.MatchResources{ResourceRules: []admissionv1.NamedRuleWithOperations{{
				RuleWithOperations: admissionv1.RuleWithOperations{Operations: []admissionv1.OperationType{admissionv1.Delete},
					Rule: admissionv1.Rule{APIGroups: []string{""}, APIVersions: []string{"v1"}, Resources: []string{"configmaps"}}}}}},
			Validations: []admissionv1.Validation{{Expression: "true"}}}}))
	must(t, c.Create(ctx, guardBinding()))
	for _, name := range []string{"a", "b"} {
		s := stageObj(dA)
		s.Name, s.Status.Stream.ID = name, "cid/"+ns+"/"+name
		st := s.Status
		must(t, c.Create(ctx, s))
		s.Status = st
		must(t, c.Status().Update(ctx, s))
	}
	return c
}

// TestStatsOutageChangesNoOutcomeOrTiming: with the stats registry hanging
// and the emitter's queue full, a freeze concludes exactly as it does with no
// emitter, in as many reconciles and about as fast, and the record it would
// have written is counted as dropped.
func TestStatsOutageChangesNoOutcomeOrTiming(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS is not set: run make -C integrations/kubernetes ci-test")
	}
	ctx := context.Background()
	c := statsCluster(ctx, t)

	// The stats registry takes every request and never answers until cleanup.
	started, release := make(chan struct{}, 1), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	host := strings.TrimPrefix(srv.URL, "http://")
	em := hangingEmitter(t, host)
	runCtx, stop := context.WithCancel(ctx)
	t.Cleanup(stop) // runs before the server cleanup: cleanups run last-in first-out
	go func() { _ = em.Run(runCtx) }()

	reconciler := func(stats func(*dv1.Operation)) *OperationReconciler {
		cluster := Cluster{Client: c}
		return &OperationReconciler{Client: c, APIReader: c, Cluster: cluster,
			Policy: delivery.ClassPolicy{ControllerUser: promoterSA, AgentGroups: []string{"pacto:agents"}},
			Gates:  NewGates(cluster, nil), Now: time.Now, Poll: time.Second, heldChecks: &sync.Map{},
			AdmissionPolicies: []string{"pd-operation"}, Stats: stats}
	}

	// Fill the queue: the worker holds the first record in a hung publish,
	// then emit until the queue refuses one. Fillers spread over namespaces,
	// so none reaches the per-namespace cap and ns has room.
	filler := &dv1.Operation{ObjectMeta: metav1.ObjectMeta{Namespace: "fill", UID: types.UID("0f6c2a1e-5b7d-4c3a-9e2f-1a2b3c4d5e6f")},
		Spec: delivery.OperationSpec{Type: delivery.TypePromote, Stage: "a", Facet: delivery.FacetPointer},
		Status: delivery.OperationStatus{Outcome: delivery.Succeeded, Times: delivery.Times{Finished: "2026-11-13T09:05:00Z"},
			Policy: &delivery.StageSpec{Service: delivery.ServiceRef{Name: "payments"}, Environment: "prod"}}}
	em.Emit(filler)
	select {
	case <-started:
	case <-time.After(30 * time.Second):
		t.Fatal("the worker never reached the stats registry")
	}
	full := func() float64 { return testutil.ToFloat64(runstats.Dropped.WithLabelValues("queue-full")) }
	noStage := func() float64 { return testutil.ToFloat64(runstats.Dropped.WithLabelValues("no-stage")) }
	for i, before := 0, full(); full() == before; i++ {
		filler.Namespace = fmt.Sprintf("fill-%d", i%16)
		em.Emit(filler)
	}

	a := statsFreeze(ctx, t, c, reconciler(nil), "a")
	fullBefore, noStageBefore := full(), noStage()
	b := statsFreeze(ctx, t, c, reconciler(em.Emit), "b")

	sameConclusion(t, a, b)
	if got := full() - fullBefore; got != 1 {
		t.Fatalf("the freeze's record must be dropped as queue-full once, got %v", got)
	}
	if got := noStage() - noStageBefore; got != 0 {
		t.Fatalf("no record may count as no-stage, got %v", got)
	}
}

// sameConclusion checks the freeze b concluded under a stats outage exactly
// as a did without stats, in as many reconciles and about as fast.
func sameConclusion(t *testing.T, a, b statsResult) {
	t.Helper()
	if a.status.Phase != delivery.PhaseConcluded || a.status.Outcome != delivery.Succeeded {
		t.Fatalf("the freeze without stats did not conclude: %s %s %s", a.status.Phase, a.status.Outcome, a.status.Message)
	}
	if a.status.Outcome != b.status.Outcome || a.status.Verification != b.status.Verification ||
		a.status.Reason != b.status.Reason || a.status.Seq != b.status.Seq || !reflect.DeepEqual(a.conditions, b.conditions) {
		t.Fatalf("a stats outage changed the conclusion:\nwithout %+v %v\nwith    %+v %v", a.status, a.conditions, b.status, b.conditions)
	}
	if a.calls != b.calls {
		t.Fatalf("a stats outage changed the reconciles to conclude: %d without, %d with", a.calls, b.calls)
	}
	if b.slowest > a.slowest+2*time.Second {
		t.Fatalf("a stats outage slowed a reconcile: %s without, %s with", a.slowest, b.slowest)
	}
}

// hangingEmitter publishes to host, with a publish that hangs until cleanup
// however slow the runner.
func hangingEmitter(t *testing.T, host string) *runstats.Emitter {
	t.Helper()
	em, err := runstats.New(host+"/stats", nil, []string{host}, nil, "cid", "test")
	must(t, err)
	em.PublishTimeout = time.Hour
	return em
}

// must fails the test on err; the perfenvtest build uses it too.
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
