package fleetsrc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/workloadmanager"
)

// groupedWorkload is a Deployment item with metadata labels and annotations.
func groupedWorkload(ns, name string, labels, annotations map[string]string) string {
	meta := map[string]any{"name": name, "namespace": ns, "labels": labels, "annotations": annotations}
	item, _ := json.Marshal(map[string]any{"metadata": meta, "spec": map[string]any{"template": map[string]any{"spec": map[string]any{
		"containers": []any{map[string]any{"name": name, "image": name + ":1"}}}}}})
	return string(item)
}

func groupOf(t *testing.T, snap *fleet.FleetSnapshot, key string) fleet.InferredGroup {
	t.Helper()
	s := snap.Service(key)
	if s == nil || s.Workload == nil || s.Workload.Group == nil {
		t.Fatalf("no grouped inferred %s", key)
	}
	return *s.Workload.Group
}

func TestGroupingReadsOnlyAllowlistedMetadata(t *testing.T) {
	obj, err := os.ReadFile("../testutil/testdata/leak-deployment.json")
	if err != nil {
		t.Fatal(err)
	}
	snap := buildOne(t, NewWorkloadSource("w", nil, &workloadFake{pages: map[string][2]string{"deployments||": {string(obj)}}}, nil))
	if g := groupOf(t, snap, "k8s:shop/Deployment/billing"); g.Rule != workloadmanager.ArgoCD || g.Namespace != "shop" || g.Name != "billing" {
		t.Errorf("group = %+v", g)
	}
	data, err := json.Marshal(fleet.NewQuery(snap).SnapshotForSerialization())
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"hunter2", "hunter2-note", "hunter2-label", "secret-label", "last-applied-configuration", "fieldsV1", "kubectl-client-side-apply"} {
		if strings.Contains(string(data), leak) {
			t.Errorf("snapshot JSON holds %q", leak)
		}
	}
}

func TestGroupRulesMatchFleet(t *testing.T) {
	now := time.Now()
	rules := []string{workloadmanager.ArgoCD, workloadmanager.FluxKustomization, workloadmanager.FluxHelmRelease, workloadmanager.Helm,
		workloadmanager.PartOf, workloadmanager.Instance, workloadmanager.Workload}
	col := &fleet.Collection{}
	for _, rule := range rules {
		col.InferredServices = append(col.InferredServices, fleet.RawInferredService{Namespace: "shop", Kind: "Deployment", Name: rule, ObservedAt: &now,
			Group: &fleet.InferredGroup{Rule: rule, Namespace: "shop", Name: "owner", From: "label x"}})
	}
	snap := buildOne(t, fleet.NewMemorySource("w", fleet.SourceKindKubernetesWorkloads, col))
	for _, rule := range rules {
		s := snap.Service("k8s:shop/Deployment/" + rule)
		if s == nil || s.Workload.Group.Rule != rule || len(s.Workload.Dropped) != 0 {
			t.Errorf("%s: %+v", rule, s)
		}
	}
}

func TestGroupingArgoTrackingOnLiveDeployment(t *testing.T) {
	item := groupedWorkload("shop", "billing", map[string]string{"app.kubernetes.io/part-of": "storefront"},
		map[string]string{"argocd.argoproj.io/tracking-id": "shop_billing:apps/Deployment:shop/billing"})
	var obj map[string]any
	if err := json.Unmarshal([]byte(item), &obj); err != nil {
		t.Fatal(err)
	}
	in := inferredService("Deployment", obj, time.Now())
	if in.Group == nil || in.Group.Rule != workloadmanager.ArgoCD || in.Group.Name != "billing" {
		t.Errorf("group = %+v", in.Group)
	}
}

func TestWorkloadsGroupedFromMetadata(t *testing.T) {
	items := strings.Join([]string{
		groupedWorkload("shop", "cart", nil, map[string]string{"argocd.argoproj.io/tracking-id": "shop_cart:apps/Deployment:shop/cart"}),
		groupedWorkload("shop", "api", map[string]string{"kustomize.toolkit.fluxcd.io/name": "apps", "kustomize.toolkit.fluxcd.io/namespace": "flux-system"}, nil),
		groupedWorkload("obs", "shipper", map[string]string{"helm.toolkit.fluxcd.io/name": "log-shipper", "helm.toolkit.fluxcd.io/namespace": "observability"}, nil),
		groupedWorkload("shop", "export", nil, map[string]string{"meta.helm.sh/release-name": "exporter", "meta.helm.sh/release-namespace": "shop"}),
		groupedWorkload("shop", "web", map[string]string{"app.kubernetes.io/part-of": "storefront"}, nil),
		groupedWorkload("shop", "search", map[string]string{"app.kubernetes.io/instance": "search-1"}, nil),
		groupedWorkload("shop", "bare", nil, nil),
	}, ",")
	snap := buildOne(t, NewWorkloadSource("w", nil, &workloadFake{pages: map[string][2]string{"deployments||": {items}}}, nil))
	for key, want := range map[string][4]string{
		"shop/Deployment/cart":   {workloadmanager.ArgoCD, "shop", "cart", "annotation argocd.argoproj.io/tracking-id"},
		"shop/Deployment/api":    {workloadmanager.FluxKustomization, "flux-system", "apps", "labels kustomize.toolkit.fluxcd.io/name and namespace"},
		"obs/Deployment/shipper": {workloadmanager.FluxHelmRelease, "observability", "log-shipper", "labels helm.toolkit.fluxcd.io/name and namespace"},
		"shop/Deployment/export": {workloadmanager.Helm, "shop", "exporter", "annotations meta.helm.sh/release-name and release-namespace"},
		"shop/Deployment/web":    {workloadmanager.PartOf, "shop", "storefront", "label app.kubernetes.io/part-of"},
		"shop/Deployment/search": {workloadmanager.Instance, "shop", "search-1", "label app.kubernetes.io/instance"},
		"shop/Deployment/bare":   {workloadmanager.Workload, "shop", "Deployment/bare", workloadmanager.NoGroupingFrom},
	} {
		g := groupOf(t, snap, "k8s:"+key)
		if got := [4]string{g.Rule, g.Namespace, g.Name, g.From}; got != want {
			t.Errorf("%s: group %v, want %v", key, got, want)
		}
	}
}

func TestGroupingObjectReadsManagerNames(t *testing.T) {
	o := groupingObject(k8sclient.WorkloadGVRs["Deployment"], "Deployment", "shop", "cart", map[string]any{
		"managedFields": []any{map[string]any{"manager": "argocd-controller", "fieldsV1": map[string]any{"f:x": "y"}}, map[string]any{"manager": "kube-controller-manager", "subresource": "status"}},
	})
	want := []workloadmanager.FieldManager{{Manager: "argocd-controller"}, {Manager: "kube-controller-manager", Subresource: "status"}}
	if o.Group != "apps" || !reflect.DeepEqual(o.Managers, want) {
		t.Errorf("object = %+v", o)
	}
}

// helmRowOf is the Helm row of snap, if any.
func helmRowOf(snap *fleet.FleetSnapshot) *fleet.Provider {
	for i, p := range snap.Providers {
		if p.ID == fleet.ProviderHelm {
			return &snap.Providers[i]
		}
	}
	return nil
}

func TestHelmRowOnlyWhenHelmGroups(t *testing.T) {
	release := func(ns, name, rel string) string {
		return groupedWorkload(ns, name, nil, map[string]string{"meta.helm.sh/release-name": rel, "meta.helm.sh/release-namespace": ns})
	}
	items := strings.Join([]string{release("shop", "a", "exporter"), release("shop", "b", "exporter"), release("ops", "c", "metrics")}, ",")
	snap := buildOne(t, NewWorkloadSource("w", nil, &workloadFake{pages: map[string][2]string{"deployments||": {items}}}, nil))
	row := helmRowOf(snap)
	if row == nil || row.Evidence != "2 Helm releases" || row.Name != "Helm" || !reflect.DeepEqual(row.UsedFor, []string{fleet.UseServiceGrouping}) || row.Status != fleet.SourceAvailable {
		t.Errorf("Helm row = %+v", row)
	}
	if want := []fleet.ProviderCount{{Kind: fleet.HelmReleaseKind, Namespace: "ops", N: 1}, {Kind: fleet.HelmReleaseKind, Namespace: "shop", N: 1}}; !reflect.DeepEqual(row.Counts, want) {
		t.Errorf("counts = %+v", row.Counts)
	}
	snap = buildOne(t, NewWorkloadSource("w", nil, &workloadFake{pages: map[string][2]string{"deployments||": {groupedWorkload("shop", "a", nil, nil)}}}, nil))
	if row := helmRowOf(snap); row != nil {
		t.Errorf("Helm row without a Helm group = %+v", row)
	}
}

// TestHelmRowCountsWhereTheWorkloadRuns: a release counts in the namespace of
// the workload it was read from, the one a viewer is reviewed in, never the
// namespace its annotation claims.
func TestHelmRowCountsWhereTheWorkloadRuns(t *testing.T) {
	items := groupedWorkload("secret-ns", "a", nil, map[string]string{"meta.helm.sh/release-name": "exporter", "meta.helm.sh/release-namespace": "shop"})
	col, err := NewWorkloadSource("w", nil, &workloadFake{pages: map[string][2]string{"deployments||": {items}}}, nil).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	row, ok := helmRow(col)
	if want := []fleet.ProviderCount{{Kind: fleet.HelmReleaseKind, Namespace: "secret-ns", N: 1}}; !ok || !reflect.DeepEqual(row.Counts, want) {
		t.Errorf("Helm row counts = %+v", row.Counts)
	}
}

func TestHelmRowOnCappedRead(t *testing.T) {
	items := groupedWorkload("shop", "a", nil, map[string]string{"meta.helm.sh/release-name": "exporter", "meta.helm.sh/release-namespace": "shop"}) + "," +
		manyWorkloads("shop", "w", fleet.MaxInferredServices)
	col, err := NewWorkloadSource("w", nil, &workloadFake{pages: map[string][2]string{"deployments||": {items}}}, nil).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(col.Providers) != 2 || col.Providers[1].ID != fleet.ProviderHelm {
		t.Fatalf("rows = %+v", col.Providers)
	}
	if h := col.Providers[1]; h.Status != fleet.SourcePartial || len(h.Limitations) != 1 || h.Limitations[0].Code != fleet.LimitationInferredServicesCapped {
		t.Errorf("a capped read may miss a release, yet the Helm row is %q with %+v", h.Status, h.Limitations)
	}
}

func TestHelmRowLeavesWorkloadsRow(t *testing.T) {
	run := func(annotations map[string]string) (*fleet.Collection, *fleet.FleetSnapshot) {
		fake := &workloadFake{pages: map[string][2]string{"deployments||": {groupedWorkload("shop", "a", nil, annotations)}},
			errs: map[string]error{"cronjobs|": apierrors.NewForbidden(schema.GroupResource{Group: "batch", Resource: "cronjobs"}, "", errors.New("denied"))}}
		src := NewWorkloadSource("w", nil, fake, nil)
		col, err := src.Collect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return col, buildOne(t, NewWorkloadSource("w", nil, fake, nil))
	}
	withCol, withSnap := run(map[string]string{"meta.helm.sh/release-name": "exporter", "meta.helm.sh/release-namespace": "shop"})
	withoutCol, withoutSnap := run(nil)
	if len(withCol.Providers) != 2 || len(withoutCol.Providers) != 1 {
		t.Fatalf("rows with %d, without %d", len(withCol.Providers), len(withoutCol.Providers))
	}
	if !reflect.DeepEqual(withCol.Providers[0], withoutCol.Providers[0]) || !reflect.DeepEqual(withCol.Limitations, withoutCol.Limitations) {
		t.Errorf("workloads row or limitations differ:\n%+v\n%+v", withCol.Providers[0], withoutCol.Providers[0])
	}
	for _, st := range [][]fleet.SourceState{withSnap.Sources, withoutSnap.Sources} {
		for i := range st {
			st[i].LastSuccessfulSync, st[i].ObservedAt = nil, nil
		}
	}
	if !reflect.DeepEqual(withSnap.Sources, withoutSnap.Sources) || withSnap.Completeness != withoutSnap.Completeness {
		t.Errorf("source state differs:\n%+v\n%+v", withSnap.Sources, withoutSnap.Sources)
	}
}
