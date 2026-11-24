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
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/orgindex"
)

const acceptedDigest = "sha256:" + "bb" + "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

// placeEnv is an envFixture whose promoter reads the organization index of
// an in-process registry, accepts the services in accepted and pulls their
// bundles from bundles.
type placeEnv struct {
	*envFixture
	reg      *testRegistry
	root     string
	bundles  *fakeRegistry
	accepted map[string]*delivery.PublisherVerdict
	asked    []string
}

func newPlaceEnv(t *testing.T, funcs interceptor.Funcs, objs ...client.Object) *placeEnv {
	t.Helper()
	pe := &placeEnv{envFixture: newEnvFixture(t, funcs, objs...), reg: newTestRegistry(t),
		bundles: &fakeRegistry{bundles: map[string]*contract.Bundle{}}, accepted: map[string]*delivery.PublisherVerdict{}}
	pe.root = pe.reg.host + "/acme"
	pe.r.Registry = pe.root
	pe.r.Cluster = Cluster{Client: pe.r.Client, Registry: func(...string) delivery.Registry { return pe.bundles }}
	pe.r.Accepted = func(_ context.Context, svc delivery.ServiceRef, repo string) (*delivery.PublisherVerdict, string, error) {
		pe.asked = append(pe.asked, repo)
		if v := pe.accepted[svc.Name]; v != nil {
			return v, "", nil
		}
		return nil, "no version of " + repo + " has a signer the publisher rule, policy or a pin accepts", nil
	}
	return pe
}

// publish lists name in the index, accepts its bundle (signed from
// github.com/acme/<name>) and serves c as that bundle; nil c lists it only.
func (pe *placeEnv) publish(t *testing.T, name string, c *contract.Contract, files fstest.MapFS) {
	t.Helper()
	repo := pe.root + "/" + name
	e := orgindex.Entry{SchemaVersion: orgindex.SchemaVersion, Name: name, Version: "1.5.0",
		Service: string(fleet.NewServiceKeyDomain(contract.OCIDomain(repo), name)), Bundle: repo + "@sha256:" + strings.Repeat("a", 64)}
	payload, _ := orgindex.Encode(e)
	tag, _ := orgindex.Tag(fleet.ServiceKey(e.Service), e.Version)
	r, _ := ocireferrer.Open(pe.root+"/"+orgindex.RepositoryName, ocireferrer.Options{})
	if err := ocireferrer.PushWriteOnce(t.Context(), r, ocireferrer.Build(orgindex.ArtifactType, orgindex.MediaType, payload, nil, nil), tag); err != nil {
		t.Fatal(err)
	}
	if c == nil {
		return
	}
	pe.accepted[name] = &delivery.PublisherVerdict{Digest: acceptedDigest, Verdict: delivery.VerdictAccepted,
		Identity: &delivery.PublisherIdentity{Repository: "https://github.com/acme/" + name}}
	var fsys = files
	if fsys == nil {
		fsys = fstest.MapFS{}
	}
	pe.bundles.bundles["oci://"+repo+"@"+acceptedDigest] = &contract.Bundle{Contract: c, FS: fsys}
}

// delivered is the contract of name, owned by team-<name>, that deploys a
// manifests artifact, with envs.
func delivered(name string, envs ...contract.Environment) *contract.Contract {
	return &contract.Contract{PactoVersion: "2.6", Environments: envs,
		Service: contract.Service{Name: name, Version: "1.5.0", Owner: contract.Owner{Team: "team-" + name}},
		Publish: &contract.Publish{Bundle: "oci://x/" + name, Deploy: "manifests"}}
}

func (pe *placeEnv) rows(t *testing.T) []delivery.PlacementRow {
	t.Helper()
	cm := &corev1.ConfigMap{}
	if err := pe.c.Get(t.Context(), client.ObjectKey{Namespace: deliveryNS, Name: delivery.PlacementsConfigMap}, cm); err != nil {
		t.Fatal(err)
	}
	var rows []delivery.PlacementRow
	if err := json.Unmarshal([]byte(cm.Data["rows.json"]), &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

// missedHas reports whether the pass that gave v missed want: the
// world-readable view is partial and only counts the objects missed, and the
// private PlacementsConfigMap names them (unreadKey).
func (pe *placeEnv) missedHas(t *testing.T, v delivery.EnvironmentsView, want string) bool {
	t.Helper()
	cm := &corev1.ConfigMap{}
	if err := pe.c.Get(t.Context(), client.ObjectKey{Namespace: deliveryNS, Name: delivery.PlacementsConfigMap}, cm); err != nil {
		t.Fatal(err)
	}
	return v.Completeness == fleet.CompletenessPartial && strings.Contains(v.CompletenessReason, "objects of delivered services") &&
		!strings.Contains(v.CompletenessReason, want) && strings.Contains(cm.Data[unreadKey], want)
}

func rowOf(t *testing.T, rows []delivery.PlacementRow, service, env string) delivery.PlacementRow {
	t.Helper()
	for _, r := range rows {
		if r.Service == service && r.Environment == env {
			return r
		}
	}
	t.Fatalf("no row %s/%s in %+v", service, env, rows)
	return delivery.PlacementRow{}
}

func TestPlacementsRowsPrivate(t *testing.T) {
	pe := newPlaceEnv(t, interceptor.Funcs{}, namespace("staging"))
	pe.publish(t, "payments", delivered("payments"), nil)
	v := pe.pass(t)
	rows := pe.rows(t)
	staging := rowOf(t, rows, "payments", "staging")
	if staging.Source != delivery.DesiredFromLadder || staging.Placement == nil || !staging.Placement.Placed() ||
		staging.Placement.Provenance != delivery.PlacementDefault || !staging.Placement.Confirm {
		t.Fatalf("staging %+v", staging)
	}
	if prod := rowOf(t, rows, "payments", "production"); prod.Placement.Placed() || !strings.Contains(prod.Placement.Refused, "does not exist") {
		t.Fatalf("production %+v", prod.Placement)
	}
	// The world-readable view counts the rows with no Stage (staging has
	// nothing applying it, production is refused) and names no service.
	cm := &corev1.ConfigMap{}
	_ = pe.c.Get(t.Context(), client.ObjectKey{Namespace: deliveryNS, Name: delivery.EnvironmentsConfigMap}, cm)
	if v.UnplacedCount != 2 || v.Idle != "" || strings.Contains(cm.Data["view.json"], "payments") || v.Completeness != fleet.CompletenessComplete {
		t.Fatalf("view %+v: %s", v, cm.Data["view.json"])
	}
}

func TestPlacementsWrittenWithCreateThenMergePatch(t *testing.T) {
	pe := newPlaceEnv(t, interceptor.Funcs{}, namespace("staging"), namespace("production"))
	pe.publish(t, "payments", delivered("payments"), nil)
	pe.pass(t)
	pe.pass(t)
	want := []string{"create/" + delivery.PlacementsConfigMap, "patch/" + delivery.PlacementsConfigMap}
	var got []string
	for _, w := range pe.writes {
		if strings.HasSuffix(w, delivery.PlacementsConfigMap) {
			got = append(got, w)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("writes %v", pe.writes)
	}
	// Both environments are placed, but nothing applies payments there, so
	// neither has a Stage: the view counts what readers of the rows list.
	if v := pe.view(t); v.UnplacedCount != 2 {
		t.Fatalf("view %+v", v)
	}
}

func TestNoRegistryIsIdle(t *testing.T) {
	f := newEnvFixture(t, interceptor.Funcs{})
	v := f.pass(t)
	if !strings.HasPrefix(v.Idle, "no registry is configured") || v.Completeness != fleet.CompletenessComplete {
		t.Fatalf("view %+v", v)
	}
	if slices.ContainsFunc(f.writes, func(w string) bool { return strings.HasSuffix(w, delivery.PlacementsConfigMap) }) {
		t.Fatalf("writes %v", f.writes)
	}
}

func TestIndexUnreadableIsIdleWithReason(t *testing.T) {
	pe := newPlaceEnv(t, interceptor.Funcs{})
	pe.reg.fault = func(*http.Request) bool { return true }
	v := pe.pass(t)
	if !strings.Contains(v.Idle, "the organization index at "+pe.root+"/pacto-index could not be read") ||
		v.Completeness != fleet.CompletenessPartial || !strings.Contains(v.CompletenessReason, "the organization index") {
		t.Fatalf("view %+v", v)
	}
	// A registry the promoter cannot even name is idle too.
	pe.r.Registry = "UPPER CASE"
	if v := pe.pass(t); v.Idle == "" {
		t.Fatalf("view %+v", v)
	}
}

func TestReconcilerReadsIndexBounded(t *testing.T) {
	pe := newPlaceEnv(t, interceptor.Funcs{}, namespace("staging"), namespace("production"))
	for _, n := range []string{"cart", "orders", "payments"} {
		pe.publish(t, n, delivered(n), nil)
	}
	pe.r.maxServices = 2
	v := pe.pass(t)
	if v.Completeness != fleet.CompletenessPartial || !strings.Contains(v.CompletenessReason, "the organization index lists 3 services; only the first 2 were read") {
		t.Fatalf("view %+v", v)
	}
	if len(pe.asked) != 2 {
		t.Fatalf("read past the bound: %v", pe.asked)
	}
}

func TestRefusedIndexEntryNeverUsed(t *testing.T) {
	pe := newPlaceEnv(t, interceptor.Funcs{}, namespace("staging"), namespace("production"))
	pe.publish(t, "payments", delivered("payments"), nil)
	r, _ := ocireferrer.Open(pe.root+"/"+orgindex.RepositoryName, ocireferrer.Options{})
	tag, _ := orgindex.Tag("x/forged", "1.0.0")
	if err := ocireferrer.PushWriteOnce(t.Context(), r, ocireferrer.Build(orgindex.ArtifactType, orgindex.MediaType, []byte("{}"), nil, nil), tag); err != nil {
		t.Fatal(err)
	}
	v := pe.pass(t)
	if !strings.Contains(v.CompletenessReason, "1 organization index entries could not be read or were refused") || len(pe.asked) != 1 {
		t.Fatalf("view %+v asked %v", v, pe.asked)
	}
}

func TestOnlyDeliveredServicesPlaced(t *testing.T) {
	pe := newPlaceEnv(t, interceptor.Funcs{}, namespace("staging"))
	describes := delivered("orders")
	describes.Publish.Deploy = ""
	pe.publish(t, "orders", describes, nil)
	other := delivered("cart")
	other.Service.Name = "billing" // the entry names another service's bundle
	pe.publish(t, "cart", other, nil)
	nopub := delivered("search")
	nopub.Publish = nil
	pe.publish(t, "search", nopub, nil)
	pe.pass(t)
	if rows := pe.rows(t); len(rows) != 0 {
		t.Fatalf("rows %+v", rows)
	}
}

func TestUntrustedBundleNeverPlaces(t *testing.T) {
	pe := newPlaceEnv(t, interceptor.Funcs{}, namespace("staging"))
	pe.publish(t, "payments", nil, nil)
	pe.pass(t)
	rows := pe.rows(t)
	if len(rows) != 1 || rows[0].Placement != nil || !strings.HasPrefix(rows[0].Reason, "waiting: publisher not trusted yet") {
		t.Fatalf("rows %+v", rows)
	}
	// No publisher check accepts nothing either.
	pe.r.Accepted = nil
	pe.pass(t)
	if rows := pe.rows(t); len(rows) != 1 || !strings.Contains(rows[0].Reason, "no publisher check") {
		t.Fatalf("rows %+v", rows)
	}
	// An accepted bundle that cannot be pulled places nothing.
	pe.r.Accepted = func(context.Context, delivery.ServiceRef, string) (*delivery.PublisherVerdict, string, error) {
		return &delivery.PublisherVerdict{Digest: "sha256:" + strings.Repeat("c", 64), Verdict: delivery.VerdictAccepted}, "", nil
	}
	pe.pass(t)
	if rows := pe.rows(t); len(rows) != 1 || !strings.Contains(rows[0].Reason, "could not be read") {
		t.Fatalf("rows %+v", rows)
	}
}

// helmReleaseDeployment is a Deployment of name in ns a Helm release applies.
func helmReleaseDeployment(ns, name string) *appsv1.Deployment {
	return &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name,
		Annotations: map[string]string{"meta.helm.sh/release-name": name, "meta.helm.sh/release-namespace": ns}}}
}

func TestUnsupportedManagerReadsUnknown(t *testing.T) {
	pacto := ladderPacto("staging", "payments")
	_ = unstructured.SetNestedMap(pacto.Object, map[string]any{"serviceName": "payments"}, "spec", "target")
	pe := newPlaceEnv(t, interceptor.Funcs{}, namespace("staging"), helmReleaseDeployment("staging", "payments"), pacto)
	pe.publish(t, "payments", delivered("payments"), nil)
	pe.pass(t)
	p := rowOf(t, pe.rows(t), "payments", "staging").Placement
	if p.Provenance != delivery.PlacementUnknown || p.Placed() || !strings.HasPrefix(p.Evidence, "owned by Helm") {
		t.Fatalf("placement %+v", p)
	}
}

func TestPlacementReadsInferredFromAndOccupants(t *testing.T) {
	rec := []byte("format: " + contract.InferredRecordFormat + "\ninputs: {}\nfields: []\ndeclaration:\n  state: declared\n" +
		"  asker: {subject: ana@acme.com, issuer: https://accounts.google.com, class: human}\n  inferredFrom: k8s:payments-staging/Deployment/api\n")
	if r, err := contract.ParseInferredRecord(rec); err != nil || r.Declaration == nil {
		t.Fatal(err)
	}
	pe := newPlaceEnv(t, interceptor.Funcs{}, namespace("payments-staging"),
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "payments-staging", Name: "api",
			Labels: map[string]string{"kustomize.toolkit.fluxcd.io/name": "api", "kustomize.toolkit.fluxcd.io/namespace": "flux-system"}}},
		&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Namespace: "payments-staging", Name: "cache"}})
	pe.publish(t, "payments", delivered("payments"), fstest.MapFS{contract.InferredRecordFile: {Data: rec}})
	pe.pass(t)
	// The StatefulSet nobody describes holds the namespace.
	p := rowOf(t, pe.rows(t), "payments", "staging").Placement
	if p.Placed() || p.Refused != "namespace payments-staging holds workloads of an unknown owner" {
		t.Fatalf("placement %+v", p)
	}
}

func TestPlacementObservesAdoptedApplier(t *testing.T) {
	pacto := ladderPacto("payments-staging", "payments")
	_ = unstructured.SetNestedMap(pacto.Object, map[string]any{"serviceName": "payments", "workloadRef": map[string]any{"kind": "Deployment", "name": "api"}}, "spec", "target")
	pe := newPlaceEnv(t, interceptor.Funcs{}, namespace("payments-staging"),
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "payments-staging", Name: "api",
			Labels: map[string]string{"kustomize.toolkit.fluxcd.io/name": "api", "kustomize.toolkit.fluxcd.io/namespace": "flux-system"}}})
	_ = unstructured.SetNestedField(pacto.Object, "oci://"+pe.root+"/payments@sha256:aa", "status", "contract", "resolvedRef")
	if err := pe.c.Create(t.Context(), pacto); err != nil {
		t.Fatal(err)
	}
	pe.publish(t, "payments", delivered("payments"), nil)
	pe.pass(t)
	p := rowOf(t, pe.rows(t), "payments", "staging").Placement
	// A Pacto's team comes from the bundle it references: a person confirms.
	if p.Namespace != "payments-staging" || p.Provenance != delivery.PlacementObserved || p.Source != delivery.PlacementFromPacto || !p.Confirm {
		t.Fatalf("placement %+v", p)
	}
}

func TestPlacementRowsReasons(t *testing.T) {
	pe := newPlaceEnv(t, interceptor.Funcs{}, namespace("staging"), namespace("production"),
		&dv1.Stage{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "orders-staging"},
			Spec: delivery.StageSpec{Service: delivery.ServiceRef{Domain: "", Name: "orders"}, Environment: "staging"}})
	pe.r.ClusterName = "eu-prod" // hosts production only
	pe.publish(t, "payments", delivered("payments"), nil)
	pe.publish(t, "checkout", delivered("checkout", contract.Environment{Name: "production"},
		contract.Environment{Name: "pr-1", Lifetime: contract.EnvironmentTemporary, Expires: "2026-11-01T00:00:00Z"}), nil)
	pe.pass(t)
	rows := pe.rows(t)
	if r := rowOf(t, rows, "payments", "production"); r.Placement != nil || r.Reason != "upstream staging is not in this cluster: remote delivery is not available yet" {
		t.Fatalf("production %+v", r)
	}
	// checkout keeps production but opts out of staging, which feeds it:
	// the reason says so instead of telling the reader to wait.
	if r := rowOf(t, rows, "checkout", "production"); r.Placement != nil ||
		r.Reason != "the contract opts out of staging, which feeds production: add staging to its environments[], since Pacto never skips an upstream gate" {
		t.Fatalf("checkout production %+v", r)
	}
	if len(rows) != 2 {
		t.Fatalf("rows %+v", rows) // pr-1 is not hosted here
	}
	pe.r.ClusterName = "eu-1"
	pe.pass(t)
	if r := rowOf(t, pe.rows(t), "checkout", "pr-1"); r.Reason != "expired at 2026-11-01T00:00:00Z: retiring" {
		t.Fatalf("pr-1 %+v", r)
	}
}

func TestPlacementPolicyAndGitOpsReachTheResolver(t *testing.T) {
	app := &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"namespace": "argocd", "name": "orders"},
		"spec": map[string]any{"destination": map[string]any{"namespace": "orders-staging"},
			"sources": []any{map[string]any{"repoURL": "https://github.com/acme/orders.git"}, "junk"}}}}
	app.SetGroupVersionKind(gvkApplication)
	gitRepo := &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"namespace": "flux-system", "name": "cart"},
		"spec": map[string]any{"url": "ssh://git@github.com/acme/cart"}}}
	gitRepo.SetGroupVersionKind(gvkGitRepository)
	ks := &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"namespace": "flux-system", "name": "cart"},
		"spec": map[string]any{"targetNamespace": "cart-staging", "sourceRef": map[string]any{"kind": "GitRepository", "name": "cart"}}}}
	ks.SetGroupVersionKind(gvkKs)
	single := &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"namespace": "argocd", "name": "single"},
		"spec": map[string]any{"source": map[string]any{"repoURL": "https://github.com/acme/none"}}}}
	single.SetGroupVersionKind(gvkApplication)
	pe := newPlaceEnv(t, interceptor.Funcs{}, namespace("staging"), namespace("production"), namespace("orders-staging"),
		namespace("cart-staging"), namespace("prod-payments"), app, gitRepo, ks, single)
	pe.r.Cluster.CacheApplications = true
	pe.r.Placement = func(context.Context) (*delivery.PlacementPolicy, string) {
		return &delivery.PlacementPolicy{Rules: []delivery.PlacementRule{{Name: "prod", Services: []string{"payments"}, Environments: []string{"production"}, Namespace: "prod-{service}"}}}, ""
	}
	for _, n := range []string{"payments", "orders", "cart"} {
		pe.publish(t, n, delivered(n), nil)
	}
	v := pe.pass(t)
	rows := pe.rows(t)
	if p := rowOf(t, rows, "payments", "production").Placement; p.Namespace != "prod-payments" || p.Provenance != delivery.PlacementDeclared {
		t.Fatalf("policy %+v", p)
	}
	if p := rowOf(t, rows, "orders", "staging").Placement; p.Namespace != "orders-staging" || p.Source != delivery.PlacementFromGitOps {
		t.Fatalf("argo %+v", p)
	}
	if p := rowOf(t, rows, "cart", "staging").Placement; p.Namespace != "cart-staging" || p.Source != delivery.PlacementFromGitOps {
		t.Fatalf("flux %+v", p)
	}
	if v.Completeness != fleet.CompletenessComplete {
		t.Fatalf("view %+v", v)
	}
	// A policy that cannot be read is partial, and so is a GitOps kind that
	// cannot be listed; --flux=false lists no Flux object.
	pe.r.Placement = func(context.Context) (*delivery.PlacementPolicy, string) {
		return nil, "the placement policy x could not be read (503)"
	}
	pe.r.Cluster.NoFlux = true
	if v := pe.pass(t); !strings.Contains(v.CompletenessReason, "the placement policy x could not be read") {
		t.Fatalf("view %+v", v)
	}
	// An unread policy is never absent: what a later step would give reads
	// unknown, so nothing lands in the default namespace meanwhile.
	for _, r := range pe.rows(t) {
		if r.Placement == nil || r.Placement.Provenance != delivery.PlacementUnknown || !strings.HasPrefix(r.Placement.Evidence, "the placement policy could not be read") {
			t.Fatalf("unread policy %+v", r.Placement)
		}
	}
}

func TestPlacementReadFailures(t *testing.T) {
	boom := errors.New("boom")
	failing := map[string]bool{}
	pe := newPlaceEnv(t, interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, l client.ObjectList, opts ...client.ListOption) error {
			kind := l.GetObjectKind().GroupVersionKind().Kind
			if _, ok := l.(*dv1.StageList); ok {
				kind = "StageList"
			}
			if failing[kind] {
				return boom
			}
			return c.List(ctx, l, opts...)
		},
		Get: func(ctx context.Context, c client.WithWatch, k client.ObjectKey, o client.Object, opts ...client.GetOption) error {
			if failing["get"] && k.Name == "payments" {
				return boom
			}
			return c.Get(ctx, k, o, opts...)
		}}, namespace("staging"), namespace("production"))
	pe.r.Cluster.CacheApplications = true
	pe.publish(t, "payments", delivered("payments"), nil)

	failing["StageList"] = true
	if v := pe.pass(t); !strings.HasPrefix(v.Idle, "the Stages could not be listed") || !strings.Contains(v.CompletenessReason, "the Stages: boom") {
		t.Fatalf("stages %+v", v)
	}
	// Applications that cannot be listed are partial, and leave every
	// placement past the GitOps step unknown.
	failing["StageList"], failing["ApplicationList"] = false, true
	if v := pe.pass(t); !strings.Contains(v.CompletenessReason, "the Applications: boom") ||
		rowOf(t, pe.rows(t), "payments", "staging").Placement.Provenance != delivery.PlacementUnknown {
		t.Errorf("reason %q", v.CompletenessReason)
	}
	failing["ApplicationList"], failing["DeploymentList"], failing["get"] = false, true, true
	v := pe.pass(t)
	// What names a service's namespace or workload stays private: the
	// world-readable view only counts it.
	for _, want := range []string{"the workloads in staging: boom", "Deployment staging/payments: boom"} {
		if !pe.missedHas(t, v, want) {
			t.Errorf("reason %q: %q not private", v.CompletenessReason, want)
		}
	}
	cm := &corev1.ConfigMap{}
	_ = pe.c.Get(t.Context(), client.ObjectKey{Namespace: deliveryNS, Name: delivery.EnvironmentsConfigMap}, cm)
	if strings.Contains(cm.Data["view.json"], "payments") {
		t.Fatalf("the view names a service: %s", cm.Data["view.json"])
	}
	// An unreadable workload or namespace never places.
	if p := rowOf(t, pe.rows(t), "payments", "staging").Placement; p.Placed() {
		t.Fatalf("placement %+v", p)
	}
}

func TestPlacementRowsBounded(t *testing.T) {
	saved := maxPlacementRows
	t.Cleanup(func() { maxPlacementRows = saved })
	maxPlacementRows = 1
	pe := newPlaceEnv(t, interceptor.Funcs{}, namespace("staging"), namespace("production"))
	pe.publish(t, "payments", delivered("payments"), nil)
	v := pe.pass(t)
	if len(pe.rows(t)) != 1 || !strings.Contains(v.CompletenessReason, "2 placement rows; only the first 1 are kept") {
		t.Fatalf("view %+v", v)
	}
}

func TestPlacementWriteFailureIsReturned(t *testing.T) {
	boom := errors.New("boom")
	reads := 0
	pe := newPlaceEnv(t, interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, k client.ObjectKey, o client.Object, opts ...client.GetOption) error {
		if k.Name == delivery.PlacementsConfigMap {
			if reads++; reads > 1 { // the state reads; the write fails
				return boom
			}
		}
		return c.Get(ctx, k, o, opts...)
	}})
	pe.publish(t, "payments", nil, nil)
	if _, err := pe.r.Reconcile(t.Context(), reconcile.Request{}); !errors.Is(err, boom) {
		t.Fatalf("err %v", err)
	}
}

func TestUnreadableStateActsOnNothing(t *testing.T) {
	boom := errors.New("boom")
	fail := true
	pe := newPlaceEnv(t, interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, k client.ObjectKey, o client.Object, opts ...client.GetOption) error {
		if fail && k.Name == delivery.PlacementsConfigMap {
			return boom
		}
		return c.Get(ctx, k, o, opts...)
	}}, namespace("staging"))
	pe.publish(t, "payments", delivered("payments"), nil)
	if v := pe.pass(t); !strings.Contains(v.Idle, "the private state in pacto-delivery-placements could not be read (boom)") ||
		!strings.Contains(v.CompletenessReason, "the private state: boom") {
		t.Fatalf("view %+v", v)
	}
	// A state that does not decode is unread too: Pacto cannot tell its
	// Stages from hand-written ones.
	fail = false
	if err := pe.c.Create(t.Context(), &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: deliveryNS, Name: delivery.PlacementsConfigMap},
		Data: map[string]string{"stages.json": "{"}}); err != nil {
		t.Fatal(err)
	}
	if v := pe.pass(t); !strings.Contains(v.Idle, "stages.json: unexpected end of JSON input") {
		t.Fatalf("view %+v", v)
	}
}

func TestHandWrittenStageRowIsPlaced(t *testing.T) {
	pe := newPlaceEnv(t, interceptor.Funcs{}, namespace("staging"))
	if err := pe.c.Create(t.Context(), &dv1.Stage{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "payments-production"},
		Spec: delivery.StageSpec{Service: delivery.ServiceRef{Domain: pe.root, Name: "payments"}, Environment: "production"}}); err != nil {
		t.Fatal(err)
	}
	pe.publish(t, "payments", delivered("payments"), nil)
	pe.pass(t)
	p := rowOf(t, pe.rows(t), "payments", "production").Placement
	if p.Namespace != "team-a" || p.Source != delivery.PlacementFromStage || !p.Placed() {
		t.Fatalf("placement %+v", p)
	}
}
