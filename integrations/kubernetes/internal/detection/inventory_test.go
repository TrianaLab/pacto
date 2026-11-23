/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package detection

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/trianalab/pacto/v4/pkg/detect"
)

const uid1 = "9f1c4e2a-0000-4000-8000-000000000001"

// inv answers the inventory lists; every request is recorded as
// "<Kind> <namespace>" and a page continues when the namespace is "paged".
type inv struct {
	t       *testing.T
	secrets map[string][]metav1.PartialObjectMetadata
	capi    []metav1.PartialObjectMetadata
	rancher []metav1.PartialObjectMetadata
	ocm     []map[string]any
	ks, hr  []map[string]any
	errs    map[string]error
	calls   []string
}

func objMeta(ns, name string, labels, annotations map[string]string) metav1.PartialObjectMetadata {
	return metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Labels: labels, Annotations: annotations}}
}

func (f *inv) funcs() interceptor.Funcs {
	return interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, l client.ObjectList, opts ...client.ListOption) error {
		o := (&client.ListOptions{}).ApplyOptions(opts)
		switch l := l.(type) {
		case *corev1.SecretList:
			f.t.Error("a typed Secret list requests Secret data")
		case *metav1.PartialObjectMetadataList:
			if k := l.GroupVersionKind().Kind; k == "StatefulSetList" || k == "DeploymentList" {
				return c.List(ctx, l, opts...)
			}
			return f.metaList(l, o)
		case *unstructured.UnstructuredList:
			return f.unstructuredList(l, o)
		}
		return c.List(ctx, l, opts...)
	}}
}

// metaList answers a metadata list.
func (f *inv) metaList(l *metav1.PartialObjectMetadataList, o *client.ListOptions) error {
	gvk := l.GroupVersionKind()
	f.calls = append(f.calls, gvk.Group+"/"+gvk.Kind+" "+o.Namespace)
	if err := f.errs[gvk.Group+"/"+gvk.Kind]; err != nil {
		return err
	}
	switch gvk.Group {
	case "":
		l.Items = f.secrets[o.Namespace]
		if o.Namespace == "paged" && o.Continue == "" {
			l.Items, l.Continue = l.Items[:1], "p2"
		} else if o.Namespace == "paged" {
			l.Items = l.Items[1:]
		}
	case capiGroup:
		l.Items = f.capi
	case "management.cattle.io":
		l.Items = f.rancher
	}
	return nil
}

// unstructuredList answers an unstructured list; a Secret or Rancher one
// fails the test, since it would read whole objects.
func (f *inv) unstructuredList(l *unstructured.UnstructuredList, o *client.ListOptions) error {
	gvk := l.GroupVersionKind()
	if gvk.Kind == "SecretList" || gvk.Group == "management.cattle.io" {
		f.t.Errorf("an unstructured %s list reads whole objects", gvk)
	}
	f.calls = append(f.calls, gvk.Group+"/"+gvk.Kind+" "+o.Namespace)
	if err := f.errs[gvk.Group+"/"+gvk.Kind]; err != nil {
		return err
	}
	items := map[string][]map[string]any{"ManagedClusterList": f.ocm, "KustomizationList": f.ks, "HelmReleaseList": f.hr}[gvk.Kind]
	for _, it := range items {
		l.Items = append(l.Items, unstructured.Unstructured{Object: it})
	}
	return nil
}

// servedOnly serves exactly gvs.
func servedOnly(gvs ...string) func(string) (bool, error) {
	return func(gv string) (bool, error) { return slices.Contains(gvs, gv), nil }
}

func fluxObj(ns, name, field, ref string) map[string]any {
	return map[string]any{"metadata": map[string]any{"namespace": ns, "name": name},
		"spec": map[string]any{"kubeConfig": map[string]any{field: map[string]any{"name": ref}}}}
}

// inventoryRun syncs a detector with the inventory on over f and returns the
// written ConfigMap and its candidates.
func inventoryRun(t *testing.T, f *inv, served func(string) (bool, error), cfg InventoryConfig, objs ...client.Object) (*corev1.ConfigMap, detect.Clusters, *Detector) {
	t.Helper()
	f.t = t
	c := newClient(f.funcs(), append([]client.Object{operatorDeployment()}, objs...)...)
	d := newDetector(c, served)
	d.Inventory = cfg
	if err := d.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	cm, _ := readResult(t, c)
	var cl detect.Clusters
	if data, ok := cm.Data[detect.ClustersConfigMapKey]; ok {
		if err := json.Unmarshal([]byte(data), &cl); err != nil {
			t.Fatal(err)
		}
	}
	return cm, cl, d
}

var allInventories = []string{argoGV, fluxGV, fluxHelmGV, capiGroup + "/v1beta2", ocmGV, rancherGV}

func TestInventoryRequestsSecretMetadataOnly(t *testing.T) {
	f := &inv{
		secrets: map[string][]metav1.PartialObjectMetadata{"argocd": {
			objMeta("argocd", "eu-prod", map[string]string{"argocd.argoproj.io/secret-type": "cluster", "env": "prod"}, map[string]string{detect.ClusterUIDAnnotation: uid1}),
			objMeta("argocd", "repo", map[string]string{"argocd.argoproj.io/secret-type": "repository"}, nil)}},
		capi:    []metav1.PartialObjectMetadata{objMeta("fleet", "eu", nil, nil)},
		rancher: []metav1.PartialObjectMetadata{objMeta("", "c-m-abc", nil, nil)},
		ocm: []map[string]any{{"metadata": map[string]any{"name": "us"},
			"status": map[string]any{"clusterClaims": []any{map[string]any{"name": "id.k8s.io", "value": uid1}, map[string]any{"value": "nameless"}}}}},
	}
	_, cl, _ := inventoryRun(t, f, servedOnly(allInventories...), InventoryConfig{Enabled: true, SecretNamespaces: []string{"argocd"}})
	got := make([]string, 0, len(cl.Candidates))
	for _, c := range cl.Candidates {
		got = append(got, c.Source+" "+c.Kind+" "+c.Name+" "+c.UID)
	}
	want := []string{"argocd Secret eu-prod " + uid1, "capi Cluster eu ", "ocm ManagedCluster us " + uid1, "rancher Cluster c-m-abc "}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("candidates = %q", got)
	}
}

func TestInventoryReadsOnlyGrantedNamespaces(t *testing.T) {
	f := &inv{}
	inventoryRun(t, f, servedOnly(argoGV), InventoryConfig{Enabled: true, SecretNamespaces: []string{"argocd", "flux-system"}})
	var secretCalls []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, "/SecretList") {
			secretCalls = append(secretCalls, c)
		}
	}
	if !reflect.DeepEqual(secretCalls, []string{"/SecretList argocd", "/SecretList flux-system"}) {
		t.Errorf("Secret lists = %q", secretCalls)
	}
	f = &inv{}
	inventoryRun(t, f, servedOnly(argoGV), InventoryConfig{Enabled: true})
	if slices.ContainsFunc(f.calls, func(c string) bool { return strings.HasPrefix(c, "/SecretList") }) {
		t.Errorf("no namespace granted, still listed Secrets: %q", f.calls)
	}
}

func TestInventoryPaginates(t *testing.T) {
	cluster := map[string]string{"argocd.argoproj.io/secret-type": "cluster"}
	f := &inv{secrets: map[string][]metav1.PartialObjectMetadata{"paged": {objMeta("paged", "a", cluster, nil), objMeta("paged", "b", cluster, nil)}}}
	_, cl, _ := inventoryRun(t, f, servedOnly(argoGV), InventoryConfig{Enabled: true, SecretNamespaces: []string{"paged"}})
	if len(cl.Candidates) != 2 || len(f.calls) != 2 {
		t.Errorf("candidates %+v after calls %q", cl.Candidates, f.calls)
	}
	f = &inv{ks: []map[string]any{fluxObj("apps", "k", "secretRef", "eu")}}
	d := &Detector{Reader: newClient(interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, l client.ObjectList, opts ...client.ListOption) error {
		if err := f.funcs().List(ctx, c, l, opts...); err != nil {
			return err
		}
		if (&client.ListOptions{}).ApplyOptions(opts).Continue == "" {
			l.(*unstructured.UnstructuredList).SetContinue("p2")
		}
		return nil
	}})}
	f.t = t
	refs, err := d.fluxRefs(context.Background(), true, false)
	if err != nil || len(refs) != 2 {
		t.Errorf("two pages of Kustomizations: %v, %v", refs, err)
	}
}

func TestInventoryServedChecksEachGroup(t *testing.T) {
	f := &inv{capi: []metav1.PartialObjectMetadata{objMeta("fleet", "eu", nil, nil)}}
	_, cl, _ := inventoryRun(t, f, servedOnly(capiGroup+"/v1beta1"), InventoryConfig{Enabled: true})
	if !reflect.DeepEqual(f.calls, []string{capiGroup + "/ClusterList "}) || len(cl.Sources) != 1 || cl.Sources[0].Source != detect.SourceCAPI {
		t.Errorf("calls %q, sources %+v", f.calls, cl.Sources)
	}
	f = &inv{}
	_, cl, _ = inventoryRun(t, f, func(string) (bool, error) { return false, errors.New("boom") }, InventoryConfig{Enabled: true})
	if len(f.calls) != 0 || len(cl.Sources) != 0 {
		t.Errorf("nothing served: calls %q, sources %+v", f.calls, cl.Sources)
	}
}

func TestInventoryOffWritesNoClustersKey(t *testing.T) {
	f := &inv{}
	cm, _, d := inventoryRun(t, f, servedOnly(allInventories...), InventoryConfig{})
	if _, ok := cm.Data[detect.ClustersConfigMapKey]; ok || len(f.calls) != 0 && slices.ContainsFunc(f.calls, func(c string) bool { return strings.Contains(c, "SecretList") }) {
		t.Errorf("inventory off: keys %v, calls %q", cm.Data, f.calls)
	}
	_, res := readResult(t, d.Writer)
	if slices.ContainsFunc(res.Entries, func(e detect.Entry) bool { return e.Kind == "cluster-inventory" }) {
		t.Errorf("nothing to propose, still an entry: %+v", res.Entries)
	}
}

func TestSyncWritesBothKeysInOneApply(t *testing.T) {
	f := &inv{t: t}
	applies := 0
	funcs := f.funcs()
	funcs.Apply = func(ctx context.Context, c client.WithWatch, o runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
		applies++
		return c.Apply(ctx, o, opts...)
	}
	c := newClient(funcs, operatorDeployment())
	d := newDetector(c, servedOnly(capiGroup+"/v1beta2"))
	d.Inventory = InventoryConfig{Enabled: true}
	_ = d.Sync(context.Background())
	cm, res := readResult(t, c)
	if applies != 1 || cm.Data[detect.ClustersConfigMapKey] == "" || entry(t, res, "cluster-inventory").Value != "0 candidate clusters" {
		t.Errorf("%d applies, keys %v", applies, cm.Data)
	}
}

func TestFluxRefsFromListedObjects(t *testing.T) {
	f := &inv{t: t, ks: []map[string]any{fluxObj("apps", "k", "secretRef", "eu"), {"metadata": map[string]any{"namespace": "apps", "name": "local"}}},
		hr: []map[string]any{fluxObj("edge", "h", "configMapRef", "us")}}
	d := &Detector{Reader: newClient(f.funcs())}
	refs, err := d.fluxRefs(context.Background(), true, true)
	want := []detect.Object{{Kind: "Secret", Namespace: "apps", Name: "eu"}, {Kind: "ConfigMap", Namespace: "edge", Name: "us"}}
	if err != nil || !reflect.DeepEqual(refs, want) {
		t.Errorf("refs = %+v, %v", refs, err)
	}
	f.errs = map[string]error{"helm.toolkit.fluxcd.io/HelmReleaseList": errors.New("boom")}
	if _, err := d.fluxRefs(context.Background(), false, true); err == nil {
		t.Error("a failed list gives no refs")
	}
}

func TestProposedNamespacesAreArgoControlPlaneAndFluxRefs(t *testing.T) {
	ctrl := func(obj client.Object, ns, name, label string) client.Object {
		obj.SetNamespace(ns)
		obj.SetName(name)
		obj.SetLabels(map[string]string{"app.kubernetes.io/name": label})
		return obj
	}
	// An Argo CD workload in a tenant namespace proposes nothing: only the two controllers count.
	f := &inv{ks: []map[string]any{fluxObj("flux-system", "remote", "secretRef", "eu")}}
	_, _, d := inventoryRun(t, f, servedOnly(argoGV, fluxGV), InventoryConfig{},
		ctrl(&appsv1.StatefulSet{}, "argocd", "argocd-application-controller", "argocd-application-controller"),
		ctrl(&appsv1.Deployment{}, "gitops", "argocd-applicationset-controller", "argocd-applicationset-controller"),
		ctrl(&appsv1.Deployment{}, "tenant", "argocd-server", "argocd-server"))
	_, res := readResult(t, d.Writer)
	e := entry(t, res, "cluster-inventory")
	if !reflect.DeepEqual(e.Namespaces, []string{"argocd", "flux-system", "gitops"}) || e.Completeness != detect.Partial || e.Hint == "" {
		t.Errorf("proposal = %+v", e)
	}
	failing := &Detector{Reader: newClient(interceptor.Funcs{List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
		return errors.New("boom")
	}})}
	if got := failing.argoNamespaces(context.Background()); got != nil {
		t.Errorf("a failed read proposed %q", got)
	}
}

func TestInventoryHardenedOnlyFromProfile(t *testing.T) {
	forbidden := apierrors.NewForbidden(schema.GroupResource{Group: capiGroup, Resource: "clusters"}, "", errors.New("no"))
	for _, tc := range []struct {
		profile []string
		want    string
	}{
		{nil, "the operator may not list clusters.cluster.x-k8s.io"},
		{[]string{"hardened"}, "the operator may not list clusters.cluster.x-k8s.io; profile hardened renders no ClusterRole"},
	} {
		f := &inv{t: t, errs: map[string]error{capiGroup + "/ClusterList": forbidden}}
		c := newClient(f.funcs(), operatorDeployment())
		d := newDetector(c, servedOnly(capiGroup+"/v1beta2"), "payments")
		d.Inventory = InventoryConfig{Enabled: true}
		d.Installation, d.LastPush = &detect.Installation{Profile: tc.profile}, func() *detect.PushResult { return nil }
		_, cl := d.detect(context.Background())
		if s := cl.Sources[0]; s.Completeness != detect.Unknown || s.Reason != tc.want {
			t.Errorf("profile %v: %+v", tc.profile, s)
		}
	}
	f := &inv{t: t, errs: map[string]error{"/SecretList": apierrors.NewTimeoutError("slow", 1)}}
	_, cl, _ := inventoryRun(t, f, servedOnly(argoGV), InventoryConfig{Enabled: true, SecretNamespaces: []string{"argocd"}})
	if s := cl.Sources[0]; s.Completeness != detect.Unknown || s.Reason != "reading secrets failed: Timeout" {
		t.Errorf("timeout = %+v", s)
	}
}

// TestEncodeKeysRefusesAnOversizedResult: a Result over its bounds writes no
// key at all, so a reader never sees half a ConfigMap.
func TestEncodeKeysRefusesAnOversizedResult(t *testing.T) {
	if keys, err := encodeKeys(detect.Result{Entries: make([]detect.Entry, detect.MaxEntries+1)}, &detect.Clusters{}); err == nil || keys != nil {
		t.Errorf("keys = %v, err = %v", keys, err)
	}
}
