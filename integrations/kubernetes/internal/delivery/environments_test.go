/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
	ctrlrt "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const deliveryNS = "pacto-delivery"

// envFixture is an EnvironmentReconciler over a fake client holding objs,
// with every write recorded by kind.
type envFixture struct {
	r      *EnvironmentReconciler
	c      client.WithWatch
	writes []string
}

func newEnvFixture(t *testing.T, funcs interceptor.Funcs, objs ...client.Object) *envFixture {
	t.Helper()
	f := &envFixture{}
	base := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(objs...).Build()
	f.c = base
	funcs.Create = func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.CreateOption) error {
		f.writes = append(f.writes, "create/"+o.GetObjectKind().GroupVersionKind().Kind+o.GetName())
		return c.Create(ctx, o, opts...)
	}
	funcs.Patch = func(ctx context.Context, c client.WithWatch, o client.Object, p client.Patch, opts ...client.PatchOption) error {
		f.writes = append(f.writes, "patch/"+o.GetName())
		return c.Patch(ctx, o, p, opts...)
	}
	cl := interceptor.NewClient(base, funcs)
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	f.r = &EnvironmentReconciler{Client: cl, APIReader: cl, Namespace: deliveryNS, ClusterID: "cid",
		ClusterName: "eu-1", NameSource: fleet.ClusterNameFromNodeLabel, Ladder: []string{"staging", "production"},
		Now: func() time.Time { return now }, Resync: time.Minute}
	return f
}

func namespace(name string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

// ladderPacto is a reconciled Pacto of service in namespace ns.
func ladderPacto(ns, service string) *unstructured.Unstructured {
	u := pactoObj(service, service, dA, "Compliant", 1)
	u.SetNamespace(ns)
	return u
}

func (f *envFixture) pass(t *testing.T) delivery.EnvironmentsView {
	t.Helper()
	res, err := f.r.Reconcile(t.Context(), reconcile.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if res.RequeueAfter != time.Minute {
		t.Fatalf("requeue %v", res.RequeueAfter)
	}
	return f.view(t)
}

func (f *envFixture) view(t *testing.T) delivery.EnvironmentsView {
	t.Helper()
	cm := &corev1.ConfigMap{}
	if err := f.c.Get(t.Context(), client.ObjectKey{Namespace: deliveryNS, Name: delivery.EnvironmentsConfigMap}, cm); err != nil {
		t.Fatal(err)
	}
	var v delivery.EnvironmentsView
	if err := json.Unmarshal([]byte(cm.Data["view.json"]), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestEnvironmentReconcilerWritesDeclaredLadder(t *testing.T) {
	f := newEnvFixture(t, interceptor.Funcs{})
	v := f.pass(t)
	want := []delivery.LadderRung{
		{Name: "staging", Source: ladderSource, Provenance: delivery.RungDeclared},
		{Name: "production", Upstream: "staging", Source: ladderSource, Provenance: delivery.RungDeclared}}
	if len(v.Declared) != 2 || v.Declared[0] != want[0] || v.Declared[1] != want[1] {
		t.Fatalf("declared %+v", v.Declared)
	}
	if len(v.Proposal) != 0 || v.ProposalDigest != "" || v.Completeness != fleet.CompletenessComplete || v.AsOf != "2026-11-23T10:00:00Z" {
		t.Fatalf("view %+v", v)
	}
	// The next pass merge-patches the ConfigMap it created.
	later := time.Date(2026, 11, 23, 10, 1, 0, 0, time.UTC)
	f.r.Now = func() time.Time { return later }
	if v := f.pass(t); v.AsOf != "2026-11-23T10:01:00Z" {
		t.Fatalf("asOf %s", v.AsOf)
	}
	if len(f.writes) != 2 || f.writes[0] != "create/"+delivery.EnvironmentsConfigMap || f.writes[1] != "patch/"+delivery.EnvironmentsConfigMap {
		t.Fatalf("writes %v: only the view, never a Stage", f.writes)
	}
}

func TestEnvironmentReconcilerProposesFromNamespaces(t *testing.T) {
	f := newEnvFixture(t, interceptor.Funcs{},
		namespace("payments-preview"), namespace("orders-preview"), namespace("payments-staging"), namespace("orders-staging"),
		ladderPacto("payments-staging", "payments"), ladderPacto("orders-staging", "orders"))
	v := f.pass(t)
	if len(v.Proposal) != 1 || v.Proposal[0] != (delivery.LadderRung{Name: "preview", Source: "namespaces *-preview, 2 found",
		Provenance: delivery.RungInferred}) {
		t.Fatalf("proposal %+v", v.Proposal)
	}
	if v.ProposalDigest != delivery.ProposalDigest(v.Proposal) || v.ProposalDigest == "" {
		t.Fatalf("digest %q", v.ProposalDigest)
	}
}

func TestEnvironmentReconcilerKnownServicesFromPactos(t *testing.T) {
	unreconciled := ladderPacto("cart-staging", "cart")
	unstructured.RemoveNestedField(unreconciled.Object, "status", "contract")
	f := newEnvFixture(t, interceptor.Funcs{},
		namespace("payments-preview"), namespace("cart-preview"), namespace("ghost-preview"),
		ladderPacto("payments-staging", "payments"), unreconciled)
	if v := f.pass(t); len(v.Proposal) != 0 {
		t.Fatalf("only reconciled Pactos name known services: %+v", v.Proposal)
	}
	// The cluster's advertised environments reach the proposal: a Pacto
	// labelled with an environment advertises it.
	labelled := ladderPacto("cart-staging", "cart")
	labelled.SetLabels(map[string]string{delivery.EnvironmentLabel: "qa"})
	f = newEnvFixture(t, interceptor.Funcs{}, labelled)
	if v := f.pass(t); len(v.Proposal) != 1 || v.Proposal[0].Name != "qa" {
		t.Fatalf("proposal %+v", v.Proposal)
	}
}

func TestEnvironmentReconcilerOffWritesNothing(t *testing.T) {
	f := newEnvFixture(t, interceptor.Funcs{}, namespace("payments-preview"))
	f.r.Off = true
	if res, err := f.r.Reconcile(t.Context(), reconcile.Request{}); err != nil || res.RequeueAfter != 0 {
		t.Fatalf("res %+v err %v", res, err)
	}
	if len(f.writes) != 0 {
		t.Fatalf("writes %v", f.writes)
	}
	// Off registers nothing: the manager is never touched.
	if err := f.r.SetupWithManager(nil); err != nil {
		t.Fatal(err)
	}
}

func TestNamespaceListForbiddenIsPartial(t *testing.T) {
	forbidden := errors.New("forbidden")
	f := newEnvFixture(t, interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, l client.ObjectList, opts ...client.ListOption) error {
		return forbidden
	}}, namespace("payments-preview"))
	v := f.pass(t)
	if v.Completeness != fleet.CompletenessPartial ||
		v.CompletenessReason != "could not read the namespaces: forbidden; the Pactos: forbidden" {
		t.Fatalf("view %+v", v)
	}
	if len(v.Declared) != 2 || len(v.Proposal) != 0 {
		t.Fatalf("view %+v", v)
	}
}

func TestEnvironmentViewReadFailureIsReturned(t *testing.T) {
	boom := errors.New("boom")
	f := newEnvFixture(t, interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, k client.ObjectKey, o client.Object, opts ...client.GetOption) error {
		return boom
	}})
	if _, err := f.r.Reconcile(t.Context(), reconcile.Request{}); !errors.Is(err, boom) {
		t.Fatalf("err %v", err)
	}
	if len(f.writes) != 0 {
		t.Fatalf("writes %v", f.writes)
	}
}

// TestEnvironmentReconcilerSetup: the reconciler registers, watching Pactos
// only where the cluster serves them, and every event maps to one pass.
func TestEnvironmentReconcilerSetup(t *testing.T) {
	for _, served := range []bool{false, true} {
		mapper := meta.NewDefaultRESTMapper(nil)
		if served {
			mapper.Add(gvkPacto, meta.RESTScopeNamespace)
		}
		skip := true
		mgr, err := ctrlrt.NewManager(&rest.Config{Host: "https://127.0.0.1:1"}, ctrlrt.Options{Scheme: newScheme(),
			Metrics:        metricsserver.Options{BindAddress: "0"},
			Controller:     config.Controller{SkipNameValidation: &skip},
			MapperProvider: func(*rest.Config, *http.Client) (meta.RESTMapper, error) { return mapper, nil }})
		if err != nil {
			t.Fatal(err)
		}
		if err := (&EnvironmentReconciler{}).SetupWithManager(mgr); err != nil {
			t.Fatal(err)
		}
	}
	if got := onePass(t.Context(), namespace("x")); len(got) != 1 || got[0].Name != delivery.EnvironmentsConfigMap {
		t.Fatalf("requests %v", got)
	}
}
