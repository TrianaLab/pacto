/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	cevent "sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// contractOf is the accepted contract of name the registry serves, which a
// test edits between passes.
func (pe *placeEnv) contractOf(name string) *contract.Contract {
	return pe.bundles.bundles["oci://"+pe.root+"/"+name+"@"+acceptedDigest].Contract
}

// at moves the reconciler's clock.
func (pe *placeEnv) at(t time.Time) { pe.r.Now = func() time.Time { return t } }

// entry is the state entry of the managed Stage ns/name, or nil.
func (pe *placeEnv) entry(t *testing.T, ns, name string) *managedObject {
	t.Helper()
	for _, o := range pe.state(t).Objects {
		if o.Kind == kindStage && o.Namespace == ns && o.Name == name {
			return &o
		}
	}
	return nil
}

func TestRetiringSinceWrittenOnRemoval(t *testing.T) {
	pe := twoRungs(t, nil)
	pe.pass(t)
	// The contract now names its exact set: production is opted out.
	pe.contractOf("payments").Environments = []contract.Environment{{Name: "staging"}}
	pe.pass(t)
	if e := pe.entry(t, "payments-production", "payments-production"); e == nil || e.RetiringSince != "2026-11-23T10:00:00Z" {
		t.Fatalf("entry %+v", e)
	}
	if got := pe.stage(t, "payments-production", "payments-production").Annotations[delivery.RetiringSinceAnnotation]; got != "2026-11-23T10:00:00Z" {
		t.Fatalf("annotation %q", got)
	}
	if e := pe.entry(t, "payments-staging", "payments-staging"); e.RetiringSince != "" {
		t.Fatalf("staging %+v", e)
	}
	// A later pass keeps the first mark.
	pe.at(time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC))
	pe.pass(t)
	if e := pe.entry(t, "payments-production", "payments-production"); e.RetiringSince != "2026-11-23T10:00:00Z" {
		t.Fatalf("entry %+v", e)
	}
}

func TestRetireAfterGraceInCluster(t *testing.T) {
	// D5: a temporary environment expiring 2026-11-20 is retired on 2026-11-27.
	pe := twoRungs(t, nil)
	pe.contractOf("payments").Environments = []contract.Environment{
		{Name: "staging", Lifetime: contract.EnvironmentTemporary, Expires: "2026-11-20T00:00:00Z"}, {Name: "production"}}
	pe.at(time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC))
	pe.pass(t)
	rec := record.NewFakeRecorder(10)
	pe.r.Events = rec
	pe.seed(t, "payments-staging", "payments-staging")
	pe.at(time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC))
	pe.pass(t)
	if e := pe.entry(t, "payments-staging", "payments-staging"); e == nil || e.RetiringSince != "2026-11-20T00:00:00Z" {
		t.Fatalf("entry %+v", e)
	}
	pe.at(time.Date(2026, 11, 26, 23, 59, 0, 0, time.UTC))
	pe.pass(t)
	if !pe.has(t, &dv1.Stage{}, "payments-staging", "payments-staging") {
		t.Fatal("retired before the grace")
	}
	pe.at(time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC))
	pe.pass(t)
	if pe.has(t, &dv1.Stage{}, "payments-staging", "payments-staging") || pe.entry(t, "payments-staging", "payments-staging") != nil {
		t.Fatal("not retired")
	}
	if !pe.has(t, &dv1.Stage{}, "payments-production", "payments-production") {
		t.Fatal("production retired")
	}
	if ev := drain(rec); len(ev) != 2 || !strings.HasPrefix(ev[1], "Normal EnvironmentDeleted Stage payments-staging/payments-staging") {
		t.Fatalf("events %v", ev)
	}
}

func TestDesiredAgainClearsRetiring(t *testing.T) {
	pe := twoRungs(t, nil)
	pe.pass(t)
	c := pe.contractOf("payments")
	c.Environments = []contract.Environment{{Name: "staging"}}
	pe.pass(t)
	c.Environments = nil
	pe.pass(t)
	if e := pe.entry(t, "payments-production", "payments-production"); e.RetiringSince != "" {
		t.Fatalf("entry %+v", e)
	}
	if _, ok := pe.stage(t, "payments-production", "payments-production").Annotations[delivery.RetiringSinceAnnotation]; ok {
		t.Fatal("annotation kept")
	}
}

// retiringIdp is payments under profile idp, placed by a signed rule in
// payments-<environment>, namespaces Pacto created, with a workload in
// production; production then leaves the contract and the grace passes.
func retiringIdp(t *testing.T, funcs interceptor.Funcs) *placeEnv {
	t.Helper()
	pe := idpEnv(t, contract.ArtifactManifests, funcs)
	pe.r.CreateNamespaces = true
	pe.r.Placement = func(context.Context) (*delivery.PlacementPolicy, string) {
		return &delivery.PlacementPolicy{Rules: []delivery.PlacementRule{{Name: "all", Environments: []string{"*"}, Namespace: "{service}-{environment}"}}}, ""
	}
	pe.pass(t)
	if err := pe.c.Create(t.Context(), &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "payments-production", Name: "payments"}}); err != nil {
		t.Fatal(err)
	}
	pe.contractOf("payments").Environments = []contract.Environment{{Name: "staging"}}
	pe.pass(t)
	pe.at(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC))
	return pe
}

func TestRetireDeletesStageAndAppliersOnly(t *testing.T) {
	pe := retiringIdp(t, interceptor.Funcs{})
	pe.pass(t)
	for _, o := range []struct {
		obj  client.Object
		name string
	}{{&dv1.Stage{}, "payments-production"}, {pe.fluxObj("OCIRepository"), "payments-production"},
		{pe.fluxObj("Kustomization"), "payments-production"}, {&corev1.ServiceAccount{}, "payments-deployer"},
		{&rbacv1.RoleBinding{}, "payments-deployer"}} {
		if pe.has(t, o.obj, "payments-production", o.name) {
			t.Fatalf("%T %s kept", o.obj, o.name)
		}
	}
	// Staging keeps everything, and the state holds no production object
	// but its namespace.
	if !pe.has(t, &dv1.Stage{}, "payments-staging", "payments-staging") || !pe.has(t, &corev1.ServiceAccount{}, "payments-staging", "payments-deployer") {
		t.Fatal("staging retired")
	}
	for _, o := range pe.state(t).Objects {
		if o.Namespace == "payments-production" {
			t.Fatalf("state %+v", o)
		}
	}
}

// fluxObj is an empty Flux object of kind, to Get into.
func (pe *placeEnv) fluxObj(kind string) client.Object {
	o := &metav1.PartialObjectMetadata{}
	o.SetGroupVersionKind(fluxGVK(kind))
	return o
}

func TestRetireNeverDeletesNamespaceOrWorkload(t *testing.T) {
	pe := retiringIdp(t, interceptor.Funcs{})
	pe.pass(t)
	if !pe.has(t, &corev1.Namespace{}, "", "payments-production") || !pe.has(t, &appsv1.Deployment{}, "payments-production", "payments") {
		t.Fatal("a namespace or a workload was deleted")
	}
	if !slices.ContainsFunc(pe.state(t).Objects, func(o managedObject) bool { return o.Kind == kindNamespace && o.Name == "payments-production" }) {
		t.Fatal("the namespace left the state")
	}
}

func TestRetireKeepsWhatIsNotPactos(t *testing.T) {
	// A Stage someone recreated under the same name is not Pacto's: the
	// entry leaves the state and the Stage stays.
	pe := twoRungs(t, nil)
	pe.pass(t)
	pe.contractOf("payments").Environments = []contract.Environment{{Name: "staging"}}
	pe.pass(t)
	s := pe.stage(t, "payments-production", "payments-production")
	if err := pe.c.Delete(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	// The fake client gives a recreated Stage the same UID: an entry with
	// another UID stands for the Stage Pacto created.
	o := managedObject{Kind: kindStage, Namespace: "payments-production", Name: "payments-production", UID: "older",
		Service: "payments", Domain: pe.root, Environment: "production", RetiringSince: "2026-11-01T00:00:00Z"}
	if err := pe.r.deleteOwned(t.Context(), o); err != nil { // gone: nothing to do
		t.Fatal(err)
	}
	s.ResourceVersion = ""
	if err := pe.c.Create(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if err := pe.r.deleteOwned(t.Context(), o); err != nil || !pe.has(t, &dv1.Stage{}, "payments-production", "payments-production") {
		t.Fatalf("deleted a Stage not Pacto's: %v", err)
	}
}

func TestRetireFailuresKeepTheStage(t *testing.T) {
	boom := errors.New("apiserver down")
	pe := retiringIdp(t, interceptor.Funcs{Delete: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.DeleteOption) error {
		if o.GetObjectKind().GroupVersionKind().Kind == "Kustomization" {
			return boom
		}
		return c.Delete(ctx, o, opts...)
	}})
	v := pe.pass(t)
	if !pe.has(t, &dv1.Stage{}, "payments-production", "payments-production") || pe.entry(t, "payments-production", "payments-production") == nil ||
		!strings.Contains(v.CompletenessReason, "the Kustomization payments-production/payments-production could not be retired: apiserver down") {
		t.Fatalf("view %+v", v)
	}
	// A read that fails keeps it too.
	pe2 := retiringIdp(t, interceptor.Funcs{})
	pe2.r.APIReader = interceptor.NewClient(pe2.c, interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, k client.ObjectKey, o client.Object, opts ...client.GetOption) error {
		if o.GetObjectKind().GroupVersionKind().Kind == "Kustomization" {
			return boom
		}
		return c.Get(ctx, k, o, opts...)
	}})
	v = pe2.pass(t)
	if !pe2.has(t, &dv1.Stage{}, "payments-production", "payments-production") ||
		!strings.Contains(v.CompletenessReason, "could not be retired: apiserver down") {
		t.Fatalf("view %+v", v)
	}
}

func TestRetiringMarkFailureIsPartial(t *testing.T) {
	pe := twoRungs(t, nil)
	pe.pass(t)
	pe.contractOf("payments").Environments = []contract.Environment{{Name: "staging"}}
	pe.r.Client = interceptor.NewClient(pe.c, interceptor.Funcs{Patch: func(ctx context.Context, c client.WithWatch, o client.Object, p client.Patch, opts ...client.PatchOption) error {
		if _, ok := o.(*dv1.Stage); ok {
			return errors.New("denied")
		}
		return c.Patch(ctx, o, p, opts...)
	}})
	v := pe.pass(t)
	if !strings.Contains(v.CompletenessReason, "the Stage payments-production/payments-production could not be marked: denied") {
		t.Fatalf("view %+v", v)
	}
}

func TestPartialIndexNeverRetires(t *testing.T) {
	pe := twoRungs(t, nil)
	pe.publish(t, "zeta", nil, nil) // listed after payments
	pe.pass(t)
	pe.contractOf("payments").Environments = []contract.Environment{{Name: "staging"}}
	pe.r.maxServices, pe.r.RetireAfter = 1, 0
	v := pe.pass(t)
	if v.Completeness != fleet.CompletenessPartial || pe.entry(t, "payments-production", "payments-production").RetiringSince != "" {
		t.Fatalf("view %+v", v)
	}
	// Read in full, it is marked and, with no grace, retired.
	pe.r.maxServices = 0
	pe.pass(t)
	if pe.has(t, &dv1.Stage{}, "payments-production", "payments-production") {
		t.Fatal("not retired")
	}
}

func TestPartialIndexKeepsRetiringStages(t *testing.T) {
	// Marked on a full read, a Stage is not deleted on a partial one.
	pe := twoRungs(t, nil)
	pe.publish(t, "zeta", nil, nil)
	pe.pass(t)
	pe.contractOf("payments").Environments = []contract.Environment{{Name: "staging"}}
	pe.pass(t)
	pe.r.maxServices = 1
	pe.at(time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC))
	pe.pass(t)
	if !pe.has(t, &dv1.Stage{}, "payments-production", "payments-production") {
		t.Fatal("retired on a partial read")
	}
}

func TestPartialLadderNeverRetires(t *testing.T) {
	pe := twoRungs(t, nil)
	pe.pass(t)
	le := newLadderEnv(t)
	pe.r.Policy, pe.r.LadderRepository, pe.r.RetireAfter = le.r.Policy, "Not A Repository", 0
	pe.contractOf("payments").Environments = []contract.Environment{{Name: "staging"}}
	pe.pass(t)
	if pe.entry(t, "payments-production", "payments-production").RetiringSince != "" {
		t.Fatal("marked on a partial installation ladder")
	}
}

func TestServiceUnreadNeverRetires(t *testing.T) {
	// payments' accepted contract cannot be read now: unknown, never gone.
	pe := twoRungs(t, nil)
	pe.pass(t)
	pe.r.RetireAfter = 0
	verdict := pe.accepted["payments"]
	delete(pe.accepted, "payments")
	pe.pass(t)
	if !pe.has(t, &dv1.Stage{}, "payments-production", "payments-production") || pe.entry(t, "payments-staging", "payments-staging").RetiringSince != "" {
		t.Fatal("retired a service it could not read")
	}
	// A contract that names another service is as unknown.
	pe.accepted["payments"] = verdict
	pe.contractOf("payments").Service.Name = "other"
	pe.pass(t)
	if !pe.has(t, &dv1.Stage{}, "payments-production", "payments-production") {
		t.Fatal("retired a service whose contract names another")
	}
}

func TestServiceGoneFromIndexRetires(t *testing.T) {
	// A contract that no longer deploys anything is read in full: every
	// Stage of it retires.
	pe := twoRungs(t, nil)
	pe.pass(t)
	pe.contractOf("payments").Publish.Deploy = ""
	pe.r.RetireAfter = 0
	pe.pass(t)
	if pe.has(t, &dv1.Stage{}, "payments-staging", "payments-staging") || len(pe.state(t).Objects) != 0 {
		t.Fatalf("state %+v", pe.state(t))
	}
}

// seed gives the managed Stage ns/name its stream id, as the StageReconciler
// does.
func (pe *placeEnv) seed(t *testing.T, ns, name string) *dv1.Stage {
	t.Helper()
	s := pe.stage(t, ns, name)
	s.Status.Stream.ID = "cid/" + ns + "/" + name
	if err := pe.c.Update(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestManagedStageEventsFromState(t *testing.T) {
	srv, got := sink(t, http.StatusAccepted)
	pe := twoRungs(t, nil)
	rec := record.NewFakeRecorder(100)
	rec.IncludeObject = true
	pe.r.Events, pe.r.CDEvents = rec, &Events{Sink: srv.URL, Source: "/pacto-delivery/cid"}
	stages := &StageReconciler{Events: rec}
	pe.pass(t)
	if ev := drain(rec); len(ev) != 0 {
		t.Fatalf("an unseeded Stage reported: %v", ev)
	}
	s := pe.seed(t, "payments-staging", "payments-staging")
	pe.pass(t)
	pe.pass(t)
	ev := drain(rec)
	if len(ev) != 1 || !strings.HasPrefix(ev[0], "Normal EnvironmentCreated Stage payments-staging/payments-staging for payments in staging was created at ") {
		t.Fatalf("created: %v", ev)
	}
	if c := receive(t, got); c["type"] != eventEnvironmentCreated || subjectOf(c)["id"] != "cid/payments-staging/payments-staging" {
		t.Fatalf("CDEvent %v", c)
	}
	if e := pe.entry(t, "payments-staging", "payments-staging"); e.Stream != "cid/payments-staging/payments-staging" || e.CreatedAt == "" {
		t.Fatalf("entry %+v", e)
	}
	if err := pe.c.Delete(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	stages.stageDeleted(t.Context(), cevent.DeleteEvent{Object: s}, nil) // a managed Stage: the StageReconciler says nothing
	pe.pass(t)
	pe.pass(t)
	ev = drain(rec)
	if len(ev) != 1 || !strings.HasPrefix(ev[0], "Normal EnvironmentDeleted Stage payments-staging/payments-staging for payments in staging was deleted") ||
		!strings.Contains(ev[0], "kind=Namespace") {
		t.Fatalf("deleted: %v", ev)
	}
	if d := receive(t, got); d["type"] != eventEnvironmentDeleted || subjectOf(d)["id"] != "cid/payments-staging/payments-staging" {
		t.Fatalf("CDEvent %v", d)
	}
}

func TestMissedDeleteWhileDownEmitted(t *testing.T) {
	srv, got := sink(t, http.StatusAccepted)
	pe := newPlaceEnv(t, interceptor.Funcs{})
	pe.publish(t, "orders", nil, nil)
	// The state a promoter left before it stopped: a Stage since deleted.
	gone := managedObject{Kind: kindStage, Namespace: "payments-staging", Name: "payments-staging", UID: "u-gone", Service: "payments",
		Domain: "acme", Environment: "staging", Stream: "cid/payments-staging/payments-staging", CreatedAt: "2026-11-01T00:00:00Z"}
	pe.writeState(t, managedState{Objects: []managedObject{gone, {Kind: kindStage, Namespace: "x", Name: "never-seeded", UID: "u-x"}},
		Confirmations: []placementConfirmation{{Stage: "u-gone", By: "alice"}}})
	rec := record.NewFakeRecorder(10)
	rec.IncludeObject = true
	pe.r.Events, pe.r.CDEvents = rec, &Events{Sink: srv.URL, Source: "/pacto-delivery/cid"}
	pe.pass(t)
	ev := drain(rec)
	if len(ev) != 1 || ev[0] != "Normal EnvironmentDeleted Stage payments-staging/payments-staging for payments in staging was deleted; "+
		"other Stages for the same environment are unaffected involvedObject{kind=Namespace,apiVersion=v1}" {
		t.Fatalf("events %v", ev)
	}
	d := receive(t, got)
	if d["type"] != eventEnvironmentDeleted || d["id"] != "u-gone/deleted" || subjectOf(d)["id"] != gone.Stream ||
		customOf(d)["createdAt"] != "2026-11-01T00:00:00Z" {
		t.Fatalf("CDEvent %v", d)
	}
	if st := pe.state(t); len(st.Objects) != 0 || len(st.Confirmations) != 0 {
		t.Fatalf("state %+v", st)
	}
}

// writeState stores s as the private state.
func (pe *placeEnv) writeState(t *testing.T, s managedState) {
	t.Helper()
	if err := pe.c.Create(t.Context(), &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: deliveryNS, Name: delivery.PlacementsConfigMap},
		Data: map[string]string{"rows.json": "[]"}}); err != nil {
		t.Fatal(err)
	}
	if err := pe.r.writeState(t.Context(), s); err != nil {
		t.Fatal(err)
	}
}

func TestEventsWaitForTheStateWrite(t *testing.T) {
	pe := newPlaceEnv(t, interceptor.Funcs{})
	pe.publish(t, "orders", nil, nil)
	pe.writeState(t, managedState{Objects: []managedObject{{Kind: kindStage, Namespace: "a", Name: "b", UID: "u", Stream: "cid/a/b"}}})
	rec := record.NewFakeRecorder(10)
	pe.r.Events = rec
	pe.r.Client = interceptor.NewClient(pe.c, interceptor.Funcs{Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
		return errors.New("denied")
	}})
	if _, err := pe.r.Reconcile(t.Context(), reconcile.Request{}); err == nil {
		t.Fatal("no error")
	}
	if ev := drain(rec); len(ev) != 0 {
		t.Fatalf("reported before the state was written: %v", ev)
	}
}

func TestStateBounded(t *testing.T) {
	pe := newPlaceEnv(t, interceptor.Funcs{})
	pe.publish(t, "orders", nil, nil)
	defer func(n int) { maxManagedObjects = n }(maxManagedObjects)
	maxManagedObjects = 2
	objs := []managedObject{
		{Kind: kindNamespace, Name: "n", UID: "keep-ns"},
		{Kind: kindStage, Namespace: "a", Name: "newer", UID: "newer", Service: "orders", RetiringSince: "2026-11-22T00:00:00Z"},
		{Kind: kindStage, Namespace: "a", Name: "older", UID: "older", Service: "orders", RetiringSince: "2026-11-21T00:00:00Z"},
	}
	pe.writeState(t, managedState{Objects: objs})
	pe.r.APIReader = interceptor.NewClient(pe.c, interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, l client.ObjectList, opts ...client.ListOption) error {
		if err := c.List(ctx, l, opts...); err != nil {
			return err
		}
		if sl, ok := l.(*dv1.StageList); ok { // both Stages still exist
			for _, n := range []string{"newer", "older"} {
				sl.Items = append(sl.Items, dv1.Stage{ObjectMeta: metav1.ObjectMeta{Namespace: "a", Name: n, UID: types.UID(n)}})
			}
		}
		return nil
	}})
	v := pe.pass(t)
	got := pe.state(t).Objects
	if len(got) != 2 || got[0].UID != "keep-ns" || got[1].UID != "newer" || v.Completeness != fleet.CompletenessPartial ||
		!strings.Contains(v.CompletenessReason, "the private state holds 3 objects, over its bound of 2") {
		t.Fatalf("state %+v view %+v", got, v)
	}
}
