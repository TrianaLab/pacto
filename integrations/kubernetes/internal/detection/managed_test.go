/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package detection

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// cluster is a fake cluster of unstructured objects by list kind, read two
// per page, and the <group>/<plural> kinds a SelfSubjectAccessReview denies.
type cluster struct {
	objects map[string][]map[string]any
	denied  []string
	// forbidden and failing are list kinds whose List is Forbidden or fails.
	forbidden, failing []string
	reviews            int
}

func (f *cluster) funcs() interceptor.Funcs {
	return interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, l client.ObjectList, opts ...client.ListOption) error {
			u, ok := l.(*unstructured.UnstructuredList)
			if !ok {
				return c.List(ctx, l, opts...)
			}
			kind := u.GroupVersionKind().Kind
			switch {
			case slices.Contains(f.forbidden, kind):
				return apierrors.NewForbidden(schema.GroupResource{}, "", errors.New("no"))
			case slices.Contains(f.failing, kind):
				return errors.New("dial tcp 10.0.0.1:443")
			}
			items, ok := f.objects[kind]
			if !ok {
				return &meta.NoKindMatchError{GroupKind: u.GroupVersionKind().GroupKind()}
			}
			o := (&client.ListOptions{}).ApplyOptions(opts)
			start, _ := strconv.Atoi(o.Continue)
			end := min(start+2, len(items))
			for _, it := range items[start:end] {
				u.Items = append(u.Items, unstructured.Unstructured{Object: it})
			}
			if end < len(items) {
				u.SetContinue(strconv.Itoa(end))
			}
			return nil
		},
		Create: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.CreateOption) error {
			r, ok := o.(*authorizationv1.SelfSubjectAccessReview)
			if !ok {
				return c.Create(ctx, o, opts...)
			}
			f.reviews++
			a := r.Spec.ResourceAttributes
			if a.Verb != "list" || a.Namespace != "" {
				return fmt.Errorf("review %+v", a)
			}
			r.Status.Allowed = !slices.Contains(f.denied, a.Group+"/"+a.Resource)
			return nil
		},
	}
}

// served are the catalog kinds discovery serves in these tests.
func served(context.Context) (map[string][]metav1.APIResource, error) {
	return map[string][]metav1.APIResource{
		"rds.aws.upbound.io/v1beta1":        {{Name: "instances", Kind: "Instance"}},
		"rds.services.k8s.aws/v1alpha1":     {{Name: "dbinstances", Kind: "DBInstance"}},
		"sql.cnrm.cloud.google.com/v1beta1": {{Name: "sqlinstances", Kind: "SQLInstance"}},
	}, nil
}

// xrd is a CompositeResourceDefinition of platform.example.org.
func xrd() map[string]any {
	return map[string]any{"spec": map[string]any{"group": "platform.example.org",
		"names":      map[string]any{"kind": "XPostgreSQLInstance", "plural": "xpostgresqlinstances"},
		"claimNames": map[string]any{"kind": "PostgreSQLInstance", "plural": "postgresqlinstances"},
		"versions":   []any{map[string]any{"name": "v1beta1", "served": true}, map[string]any{"name": "v1alpha1", "served": true, "referenceable": true}}}}
}

func objects() map[string][]map[string]any {
	return map[string][]map[string]any{
		"CompositeResourceDefinitionList": {xrd(), {"spec": map[string]any{"group": "x.io", "names": map[string]any{"kind": "XOther"}}}},
		"XPostgreSQLInstanceList": {{"metadata": map[string]any{"name": "orders-xr", "uid": "xr-uid"},
			"spec": map[string]any{"claimRef": map[string]any{"kind": "PostgreSQLInstance", "namespace": "shop", "name": "orders-db"}}},
			{"metadata": map[string]any{"name": "child", "uid": "child-uid", "ownerReferences": []any{map[string]any{"uid": "xr-uid"}}}}},
		"PostgreSQLInstanceList": {{"metadata": map[string]any{"namespace": "shop", "name": "orders-db"},
			"spec": map[string]any{"writeConnectionSecretToRef": map[string]any{"name": "orders-db-conn"}}},
			{"metadata": map[string]any{"namespace": "shop", "name": "no-secret"}}},
		"InstanceList": {
			{"metadata": map[string]any{"name": "orders-x", "labels": map[string]any{"crossplane.io/claim-name": "orders-db", "crossplane.io/claim-namespace": "shop"},
				"ownerReferences": []any{map[string]any{"uid": "child-uid"}}},
				"spec":   map[string]any{"writeConnectionSecretToRef": map[string]any{"namespace": "crossplane-system", "name": "orders-x-conn"}},
				"status": map[string]any{"atProvider": map[string]any{"arn": "arn:aws:rds:eu-west-1:123456789012:db:orders-db", "engine": "postgres", "engineVersion": "15"}}},
			{"metadata": map[string]any{"name": "forged", "labels": map[string]any{"crossplane.io/claim-name": "orders-db", "crossplane.io/claim-namespace": "shop"}}},
		},
		"DBInstanceList":  {{"metadata": map[string]any{"namespace": "payments", "name": "ledger"}}},
		"SQLInstanceList": {{"metadata": map[string]any{"namespace": "analytics", "name": "events"}}},
	}
}

// managedDetector is a detector over f with managed reads on.
func managedDetector(f *cluster, watched ...string) (*Detector, client.WithWatch) {
	c := newClient(f.funcs(), operatorDeployment())
	d := newDetector(c, servedAll, watched...)
	d.Readers.Managed = served
	return d, c
}

func managedResult(t *testing.T, f *cluster, watched ...string) detect.Result {
	t.Helper()
	d, c := managedDetector(f, watched...)
	if err := d.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, res := readResult(t, c)
	return res
}

func TestDetectorListsManagedResources(t *testing.T) {
	f := &cluster{objects: objects()}
	res := managedResult(t, f)
	e := entry(t, res, fleet.ProviderManagedResources)
	if e.Completeness != detect.Complete || e.Value != "4 managed resources" ||
		e.Evidence != "lists the catalog kinds of rds.aws.upbound.io, rds.services.k8s.aws, sql.cnrm.cloud.google.com" || f.reviews != 3 {
		t.Fatalf("entry = %+v (%d reviews)", e, f.reviews)
	}
	got := make([]string, 0, len(res.ManagedResources))
	for _, r := range res.ManagedResources {
		got = append(got, r.Source+" "+r.Identity+" "+r.Claim+" "+r.Engine)
		if r.Cluster != "cluster-uid" || r.ClusterName != "kind-pacto" || !r.ObservedAt.Equal(now) {
			t.Errorf("provenance %+v", r)
		}
	}
	want := []string{"crossplane aws/rds/123456789012/eu-west-1/orders-db PostgreSQLInstance shop/orders-db postgres 15", "crossplane aws/rds  ",
		"ack aws/rds  ", "config-connector gcp/cloudsql  "}
	if !slices.Equal(got, want) || res.ManagedResourcesTotal != 4 {
		t.Errorf("resources = %q (total %d)", got, res.ManagedResourcesTotal)
	}
}

// TestDetectorSkipsKindsOutsideTheCatalog: a kind discovery offers that no
// catalog row names is not kept and is counted only as unclassified.
func TestDetectorSkipsKindsOutsideTheCatalog(t *testing.T) {
	d, c := managedDetector(&cluster{objects: map[string][]map[string]any{"ThingList": {{"metadata": map[string]any{"name": "x"}}}}})
	d.Readers.Managed = func(context.Context) (map[string][]metav1.APIResource, error) {
		return map[string][]metav1.APIResource{"example.org/v1": {{Name: "things", Kind: "Thing"}}}, nil
	}
	_ = d.Sync(context.Background())
	if _, res := readResult(t, c); res.ManagedResources != nil || res.ManagedResourcesTotal != 0 || res.ManagedResourcesUnclassified != 1 {
		t.Errorf("result = %+v", res)
	}
}

func TestDetectorClaimSecretJoined(t *testing.T) {
	res := managedResult(t, &cluster{objects: objects()})
	if r := res.ManagedResources[0]; !slices.Equal(r.ConnectionSecrets, []string{"crossplane-system/orders-x-conn", "shop/orders-db-conn"}) {
		t.Errorf("secrets = %v", r.ConnectionSecrets)
	}
}

func TestDetectorCountsUnverifiedClaims(t *testing.T) {
	res := managedResult(t, &cluster{objects: objects()})
	e := entry(t, res, fleet.ProviderManagedResources)
	if !slices.Equal(e.Details, []string{detect.ClaimUnverified + ": 1 resources name a claim Pacto could not confirm with a composite it read; their claim labels are ignored"}) ||
		res.ManagedResources[1].Claim != "" {
		t.Errorf("entry = %+v, forged = %+v", e, res.ManagedResources[1])
	}
}

func TestDetectorReportsUnreadGroup(t *testing.T) {
	f := &cluster{objects: objects(), denied: []string{"rds.services.k8s.aws/dbinstances"}, forbidden: []string{"PostgreSQLInstanceList"}}
	res := managedResult(t, f)
	e := entry(t, res, fleet.ProviderManagedResources)
	if e.Completeness != detect.Partial || e.Hint != detect.ManagedUpgradeHint ||
		!slices.Contains(e.Details, "unread platform.example.org/postgresqlinstances") || !slices.Contains(e.Details, "unread rds.services.k8s.aws/dbinstances") {
		t.Fatalf("entry = %+v", e)
	}
	if slices.ContainsFunc(res.ManagedResources, func(r fleet.RawManagedResource) bool { return r.Source == "ack" }) {
		t.Error("a denied kind was listed")
	}
	// The XRDs themselves may be unlisted: no claim is verified, the rest reads.
	f = &cluster{objects: objects(), forbidden: []string{"CompositeResourceDefinitionList"}}
	if e := entry(t, managedResult(t, f), fleet.ProviderManagedResources); !slices.Contains(e.Details, "unread apiextensions.crossplane.io/compositeresourcedefinitions") {
		t.Errorf("xrd forbidden = %+v", e)
	}
	// No Crossplane: no XRD API, nothing unread.
	o := objects()
	delete(o, "CompositeResourceDefinitionList")
	if e := entry(t, managedResult(t, &cluster{objects: o}), fleet.ProviderManagedResources); e.Completeness != detect.Complete {
		t.Errorf("no xrd api = %+v", e)
	}
}

func TestDetectorManagedFailuresAreUnknown(t *testing.T) {
	for _, failing := range []string{"CompositeResourceDefinitionList", "XPostgreSQLInstanceList", "InstanceList"} {
		e := entry(t, managedResult(t, &cluster{objects: objects(), failing: []string{failing}}), fleet.ProviderManagedResources)
		if e.Completeness != detect.Unknown || strings.Contains(e.Reason, "10.0.0.1") {
			t.Errorf("%s failing = %+v", failing, e)
		}
	}
	d, c := managedDetector(&cluster{objects: objects()})
	d.Readers.Managed = func(context.Context) (map[string][]metav1.APIResource, error) {
		return nil, errors.New("dial tcp 10.0.0.1:443")
	}
	_ = d.Sync(context.Background())
	if _, res := readResult(t, c); entry(t, res, fleet.ProviderManagedResources).Completeness != detect.Unknown {
		t.Errorf("discovery failed = %+v", res.Entries)
	}
	// Without the reader no managed-resources entry is written.
	d.Readers.Managed = nil
	_ = d.Sync(context.Background())
	if _, res := readResult(t, c); slices.ContainsFunc(res.Entries, func(e detect.Entry) bool { return e.Kind == fleet.ProviderManagedResources }) {
		t.Error("an entry without the reader")
	}
}

func TestDetectorScopedSkipsManaged(t *testing.T) {
	f := &cluster{objects: objects()}
	res := managedResult(t, f, "shop")
	if e := entry(t, res, fleet.ProviderManagedResources); e.Completeness != detect.Unknown || res.ManagedResources != nil || f.reviews != 0 {
		t.Errorf("scoped = %+v, %d reviews", e, f.reviews)
	}
}

func TestDetectorCapsManagedResources(t *testing.T) {
	o := objects()
	o["InstanceList"] = make([]map[string]any, detect.MaxManagedResources+1)
	for i := range o["InstanceList"] {
		o["InstanceList"][i] = map[string]any{"metadata": map[string]any{"name": fmt.Sprint("db-", i)}}
	}
	res := managedResult(t, &cluster{objects: o})
	if len(res.ManagedResources) != detect.MaxManagedResources || res.ManagedResourcesTotal != detect.MaxManagedResources+3 || !res.ManagedCapped() {
		t.Errorf("kept %d of %d", len(res.ManagedResources), res.ManagedResourcesTotal)
	}
}

func TestDetectorReportsSecretRefs(t *testing.T) {
	p := pod("shop", "orders-1", "orders-5d9", "ghcr.io/acme/orders:1")
	p.Labels = map[string]string{"pod-template-hash": "5d9"}
	p.Spec.Containers[0].Env = []corev1.EnvVar{{Name: "DB", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: "orders-db-conn"}, Key: "url"}}}}
	p.Spec.Volumes = []corev1.Volume{{Name: "v", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "orders-db-conn"}}}}
	p2 := p.DeepCopy()
	p2.Name, p2.UID = "orders-2", "orders-2"
	bare := pod("shop", "job", "", "ghcr.io/acme/job:1")
	bare.Spec.Containers[0].EnvFrom = []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "job-env"}}}}
	c := newClient(interceptor.Funcs{}, operatorDeployment(), p, p2, bare)
	_ = newDetector(c, servedAll).Sync(context.Background())
	_, res := readResult(t, c)
	want := []fleet.RawSecretRef{
		{Cluster: "cluster-uid", ClusterName: "kind-pacto", Namespace: "shop", Kind: "Pod", Name: "job", Secret: "job-env"},
		{Cluster: "cluster-uid", ClusterName: "kind-pacto", Namespace: "shop", Kind: "Deployment", Name: "orders", Secret: "orders-db-conn"},
	}
	slices.SortFunc(res.SecretRefs, func(a, b fleet.RawSecretRef) int { return strings.Compare(a.Name, b.Name) })
	if !slices.Equal(res.SecretRefs, want) {
		t.Errorf("refs = %+v", res.SecretRefs)
	}
	if res.SecretRefsTotal != 2 || res.SecretRefsCapped() {
		t.Errorf("total %d", res.SecretRefsTotal)
	}
	// Past the bound no ref is kept, and each is counted once.
	refs := secretRefs{seen: map[fleet.RawSecretRef]bool{}, out: make([]fleet.RawSecretRef, detect.MaxSecretRefs)}
	refs.add(detect.Object{}, bare)
	refs.add(detect.Object{}, bare)
	if len(refs.out) != detect.MaxSecretRefs || refs.total != 1 {
		t.Errorf("kept %d refs, counted %d", len(refs.out), refs.total)
	}
}

// TestDetectorFailedAccessReviewIsUnknown: a review that fails says nothing
// about whether the operator may list a kind: the entry is unknown, never a
// kind unread for lack of a grant.
func TestDetectorFailedAccessReviewIsUnknown(t *testing.T) {
	f := &cluster{objects: objects()}
	funcs := f.funcs()
	funcs.Create = func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.CreateOption) error {
		if _, ok := o.(*authorizationv1.SelfSubjectAccessReview); ok {
			return errors.New("dial tcp 10.0.0.1:443")
		}
		return c.Create(ctx, o, opts...)
	}
	c := newClient(funcs, operatorDeployment())
	d := newDetector(c, servedAll)
	d.Readers.Managed = served
	if err := d.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, res := readResult(t, c)
	if e := entry(t, res, fleet.ProviderManagedResources); e.Completeness != detect.Unknown || !strings.Contains(e.Reason, "the access review failed") || len(e.Details) != 0 {
		t.Errorf("entry = %+v", e)
	}
}

// discoveryAPIs serves legacy discovery: the core API, one catalog group
// with a list-capable kind, a watch-only kind and a subresource, and one
// group outside the catalog; failing groups answer 500.
func discoveryAPIs(t *testing.T, failing ...string) *rest.Config {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		for _, g := range failing {
			if strings.HasPrefix(r.URL.Path, "/apis/"+g+"/") {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		}
		switch r.URL.Path {
		case "/api":
			_, _ = w.Write([]byte(`{"kind":"APIVersions","versions":["v1"]}`))
		case "/api/v1":
			_, _ = w.Write([]byte(`{"kind":"APIResourceList","groupVersion":"v1","resources":[{"name":"pods","kind":"Pod","namespaced":true,"verbs":["list"]}]}`))
		case "/apis":
			_, _ = w.Write([]byte(`{"kind":"APIGroupList","groups":[` +
				`{"name":"rds.aws.upbound.io","versions":[{"groupVersion":"rds.aws.upbound.io/v1beta1","version":"v1beta1"}],"preferredVersion":{"groupVersion":"rds.aws.upbound.io/v1beta1","version":"v1beta1"}},` +
				`{"name":"example.org","versions":[{"groupVersion":"example.org/v1","version":"v1"}],"preferredVersion":{"groupVersion":"example.org/v1","version":"v1"}}]}`))
		case "/apis/rds.aws.upbound.io/v1beta1":
			_, _ = w.Write([]byte(`{"kind":"APIResourceList","groupVersion":"rds.aws.upbound.io/v1beta1","resources":[` +
				`{"name":"instances","kind":"Instance","verbs":["list","watch"]},{"name":"instances/status","kind":"Instance","verbs":["get"]},` +
				`{"name":"clusters","kind":"Cluster","verbs":["watch"]},{"name":"subnetgroups","kind":"SubnetGroup","verbs":["list"]}]}`))
		case "/apis/example.org/v1":
			_, _ = w.Write([]byte(`{"kind":"APIResourceList","groupVersion":"example.org/v1","resources":[{"name":"things","kind":"Thing","verbs":["list"]}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return &rest.Config{Host: srv.URL}
}

func TestManagedForListsCatalogKinds(t *testing.T) {
	got, err := ManagedFor(discoveryAPIs(t))(context.Background())
	if err != nil || len(got) != 1 || len(got["rds.aws.upbound.io/v1beta1"]) != 1 || got["rds.aws.upbound.io/v1beta1"][0].Name != "instances" {
		t.Fatalf("served = %+v, %v", got, err)
	}
	// A group outside the catalog that fails is ignored; a catalog group is not.
	if _, err := ManagedFor(discoveryAPIs(t, "example.org"))(context.Background()); err != nil {
		t.Errorf("a foreign group failure: %v", err)
	}
	if _, err := ManagedFor(discoveryAPIs(t, "rds.aws.upbound.io"))(context.Background()); err == nil {
		t.Error("a catalog group that failed read as not served")
	}
	if _, err := ManagedFor(discoveryServer(t, http.StatusForbidden))(context.Background()); err == nil {
		t.Error("a forbidden discovery read as nothing served")
	}
	bad := &rest.Config{Host: "https://127.0.0.1:1", TLSClientConfig: rest.TLSClientConfig{CAFile: "/nonexistent/ca.crt"}}
	if _, err := ManagedFor(bad)(context.Background()); err == nil {
		t.Error("an unbuildable client read as nothing served")
	}
}

func TestXRDVersion(t *testing.T) {
	for _, tc := range []struct {
		versions []any
		want     string
	}{
		{[]any{map[string]any{"name": "v1", "served": true}}, "v1"},
		{[]any{map[string]any{"name": "v1", "served": false}}, ""},
		{nil, ""},
	} {
		u := &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{"versions": tc.versions}}}
		if got := xrdVersion(u); got != tc.want {
			t.Errorf("%v = %q", tc.versions, got)
		}
	}
}
