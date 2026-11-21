package fleetsrc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/kubecluster"
	"github.com/trianalab/pacto/v4/pkg/workloadmanager"
)

// workloadFake is a K8sClient serving workload pages. pages maps
// "resource|namespace|token" to a page's items JSON and next token; errs maps
// "resource|namespace" to a list error. It records every GVR and namespace
// asked for.
type workloadFake struct {
	pages map[string][2]string
	errs  map[string]error
	asked []string
	gvrs  map[schema.GroupVersionResource]bool
	// versions are the API groups discovery serves; discErr fails it.
	versions   map[string]string
	discErr    error
	discovered []string
}

// PreferredVersions serves versions, or fails with discErr; it records the
// groups asked for.
func (f *workloadFake) PreferredVersions(_ context.Context, groups ...string) (map[string]string, error) {
	f.discovered = append(f.discovered, groups...)
	return f.versions, f.discErr
}

func (f *workloadFake) DiscoverCRD(context.Context) (*k8sclient.CRDDiscovery, error) {
	return nil, errors.New("the workload source never discovers the CRD")
}

func (f *workloadFake) ListJSON(context.Context, k8sclient.CRDDiscovery, string) ([]byte, error) {
	return nil, errors.New("the workload source never lists Pactos")
}

func (f *workloadFake) ClusterIdentity(context.Context, string) (kubecluster.Identity, error) {
	return kubecluster.Identity{}, errors.New("unused")
}

func (f *workloadFake) ListPage(_ context.Context, gvr schema.GroupVersionResource, ns, token string) ([]byte, string, error) {
	if f.gvrs == nil {
		f.gvrs = map[schema.GroupVersionResource]bool{}
	}
	f.gvrs[gvr] = true
	f.asked = append(f.asked, gvr.Resource+"|"+ns+"|"+token)
	if err := f.errs[gvr.Resource+"|"+ns]; err != nil {
		return nil, "", err
	}
	p, ok := f.pages[gvr.Resource+"|"+ns+"|"+token]
	if !ok {
		return []byte(`{"items":[]}`), "", nil
	}
	return []byte(`{"items":[` + p[0] + `]}`), p[1], nil
}

func workloadJSON(ns, name, image string) string {
	return fmt.Sprintf(`{"metadata":{"name":%q,"namespace":%q},"spec":{"template":{"spec":{"containers":[{"name":%q,"image":%q,"ports":[{"containerPort":8080}],"readinessProbe":{"httpGet":{"path":"/ready","port":8080}}}]}}}}`, name, ns, name, image)
}

func names(col *fleet.Collection) []string {
	var out []string
	for _, in := range col.InferredServices {
		out = append(out, in.Namespace+"/"+in.Kind+"/"+in.Name)
	}
	return out
}

func fourKindsSource() *WorkloadSource {
	cronjob := `{"metadata":{"name":"nightly","namespace":"shop"},"spec":{"jobTemplate":{"spec":{"template":{"spec":{"containers":[{"name":"nightly","image":"batch:1"}]}}}}}}`
	return NewWorkloadSource("", &fleet.Cluster{ID: "c1"}, &workloadFake{pages: map[string][2]string{
		"deployments||":  {workloadJSON("shop", "cart", "cart:1")},
		"statefulsets||": {workloadJSON("shop", "db", "pg:16")},
		"daemonsets||":   {workloadJSON("kube-system", "agent", "agent:2")},
		"cronjobs||":     {cronjob},
	}}, nil)
}

func TestWorkloadSourceReadsFourKinds(t *testing.T) {
	src := fourKindsSource()
	if src.ID() != "k8s-workloads" || src.Kind() != fleet.SourceKindKubernetesWorkloads {
		t.Errorf("id %q kind %q", src.ID(), src.Kind())
	}
	col, err := src.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"shop/Deployment/cart", "shop/StatefulSet/db", "kube-system/DaemonSet/agent", "shop/CronJob/nightly"}
	if !slices.Equal(names(col), want) || col.State != nil || len(col.Limitations) != 0 || col.Cluster.ID != "c1" {
		t.Fatalf("collection = %v %+v %+v", names(col), col.State, col.Limitations)
	}
	if nightly := col.InferredServices[3]; nightly.Image == nil || nightly.Image.From != "CronJob spec.jobTemplate.spec.template.spec.containers[name=nightly].image" {
		t.Errorf("nightly = %+v", nightly)
	}
	snap := buildOne(t, src)
	if s := snap.Service("k8s:shop/Deployment/cart"); s == nil || !s.Inferred() || s.Workload.Cluster != "c1" {
		t.Errorf("snapshot service = %+v", s)
	}
	if len(snap.Clusters) != 1 || snap.Clusters[0].ReportedBy != "" {
		t.Errorf("clusters = %+v; the workload source reads the cluster itself", snap.Clusters)
	}
}

func TestWorkloadSourceValuesNameTheirPath(t *testing.T) {
	col, err := fourKindsSource().Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cart := col.InferredServices[0]
	if cart.Image == nil || cart.Image.Value != "cart:1" || cart.Image.From != "Deployment spec.template.spec.containers[name=cart].image" || cart.ObservedAt == nil {
		t.Errorf("cart = %+v", cart)
	}
	if len(cart.Ports) != 1 || cart.Ports[0].Port != 8080 || !strings.HasPrefix(cart.Ports[0].From, "Deployment spec.") {
		t.Errorf("ports = %+v", cart.Ports)
	}
	if len(cart.Probes) != 1 || cart.Probes[0].Path != "/ready" || cart.Probes[0].Port != "8080" || !strings.HasPrefix(cart.Probes[0].From, "Deployment spec.") {
		t.Errorf("probes = %+v", cart.Probes)
	}
}

func TestWorkloadSourceListsOnlyWorkloads(t *testing.T) {
	f := &workloadFake{}
	if _, err := NewWorkloadSource("w", nil, f, []string{"b", "a", "", "a"}).Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.gvrs) != len(k8sclient.WorkloadGVRs) {
		t.Errorf("asked %v, want exactly %v", f.gvrs, k8sclient.WorkloadGVRs)
	}
	for g := range f.gvrs {
		if !slices.Contains(slices.Collect(maps.Values(k8sclient.WorkloadGVRs)), g) {
			t.Errorf("listed %v, which is no workload", g)
		}
	}
	want := []string{"deployments|a|", "statefulsets|a|", "daemonsets|a|", "cronjobs|a|", "deployments|b|", "statefulsets|b|", "daemonsets|b|", "cronjobs|b|"}
	if !slices.Equal(f.asked, want) {
		t.Errorf("asked %v, want %v", f.asked, want)
	}
}

func TestWorkloadSourcePaginates(t *testing.T) {
	f := &workloadFake{pages: map[string][2]string{
		"deployments||":   {workloadJSON("shop", "a", "a:1"), "t1"},
		"deployments||t1": {workloadJSON("shop", "b", "b:1"), "t2"},
		"deployments||t2": {workloadJSON("shop", "c", "c:1")},
	}}
	col, err := NewWorkloadSource("w", nil, f, nil).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := names(col); !slices.Equal(got, []string{"shop/Deployment/a", "shop/Deployment/b", "shop/Deployment/c"}) {
		t.Errorf("read %v", got)
	}
	if !slices.Equal(f.asked[:3], []string{"deployments||", "deployments||t1", "deployments||t2"}) {
		t.Errorf("asked %v", f.asked)
	}
}

func TestWorkloadSourceCapped(t *testing.T) {
	items := make([]string, fleet.MaxInferredServices)
	for i := range items {
		items[i] = workloadJSON("shop", fmt.Sprintf("w%d", i), "img:1")
	}
	f := &workloadFake{pages: map[string][2]string{
		"deployments||":   {strings.Join(items, ","), "t1"},
		"deployments||t1": {workloadJSON("shop", "over", "img:1"), "t2"},
	}}
	src := NewWorkloadSource("w", nil, f, nil)
	col, err := src.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(col.InferredServices) != fleet.MaxInferredServices || len(limitationsOf(col, fleet.LimitationInferredServicesCapped)) != 1 {
		t.Fatalf("read %d, limitations %+v", len(col.InferredServices), col.Limitations)
	}
	if slices.Contains(f.asked, "deployments||t2") || slices.Contains(f.asked, "statefulsets||") {
		t.Errorf("kept reading past the bound: %v", f.asked)
	}
	if st := buildOne(t, src).Sources[0]; st.Status != fleet.SourcePartial || st.InferredCount != fleet.MaxInferredServices {
		t.Errorf("state = %+v, want partial", st)
	}
	scoped, err := NewWorkloadSource("w", nil, &workloadFake{pages: map[string][2]string{"deployments|shop|": f.pages["deployments||"], "deployments|shop|t1": f.pages["deployments||t1"]}}, []string{"shop"}).Collect(context.Background())
	if err != nil || scoped.State == nil || scoped.State.Status != fleet.SourcePartial || len(scoped.State.Namespaces) != 0 {
		t.Errorf("scoped capped = %+v, %v; want partial with no namespace read whole", scoped.State, err)
	}
}

func TestWorkloadSourceNamespaceScoped(t *testing.T) {
	f := &workloadFake{pages: map[string][2]string{"deployments|shop|": {workloadJSON("shop", "cart", "cart:1")}}}
	src := NewWorkloadSource("w", nil, f, []string{"shop", "ops"})
	col, err := src.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if col.State == nil || col.State.Status != fleet.SourcePartial || !slices.Equal(col.State.Namespaces, []string{"ops", "shop"}) || len(limitationsOf(col, fleet.LimitationK8sNamespaceScoped)) != 1 {
		t.Errorf("collection = %+v %+v; want the partial scope", col.State, col.Limitations)
	}
	if snap := buildOne(t, src); snap.Completeness != fleet.CompletenessPartial {
		t.Errorf("completeness = %s, want partial", snap.Completeness)
	}
}

func TestWorkloadSourceOneKindForbidden(t *testing.T) {
	forbidden := apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "daemonsets"}, "", errors.New("denied"))
	f := &workloadFake{
		pages: map[string][2]string{"deployments|shop|": {workloadJSON("shop", "cart", "cart:1")}},
		errs:  map[string]error{"daemonsets|shop": forbidden},
	}
	src := NewWorkloadSource("w", nil, f, []string{"shop", "ops"})
	col, err := src.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	unread := limitationsOf(col, fleet.LimitationK8sNamespaceUnreadable)
	if !slices.Equal(names(col), []string{"shop/Deployment/cart"}) || len(unread) != 1 || !strings.Contains(unread[0], "shop: forbidden listing DaemonSet") {
		t.Errorf("collection %v, unreadable %v", names(col), unread)
	}
	if !slices.Equal(col.State.Namespaces, []string{"ops"}) {
		t.Errorf("namespaces read whole = %v, want only ops", col.State.Namespaces)
	}
	unscoped := &workloadFake{errs: map[string]error{"cronjobs|": forbidden}}
	if st := buildOne(t, NewWorkloadSource("w", nil, unscoped, nil)).Sources[0]; st.Status != fleet.SourcePartial {
		t.Errorf("one kind forbidden cluster-wide = %+v, want partial", st)
	}
	whole, err := NewWorkloadSource("w", nil, unscoped, nil).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if lims := limitationsOf(whole, fleet.LimitationSourcePartial); len(lims) != 1 || lims[0] != "source w could not list CronJobs in any namespace: forbidden" {
		t.Errorf("cluster-wide limitation %v", lims)
	}
}

func TestWorkloadSourceAllListsFail(t *testing.T) {
	errs := map[string]error{}
	for _, r := range []string{"deployments", "statefulsets", "daemonsets", "cronjobs"} {
		errs[r+"|"] = errors.New("connection refused")
	}
	if _, err := NewWorkloadSource("w", nil, &workloadFake{errs: errs}, nil).Collect(context.Background()); err == nil {
		t.Error("every list failed and the source returned no error")
	}
	bad := &workloadFake{pages: map[string][2]string{}}
	for _, r := range []string{"deployments", "statefulsets", "daemonsets", "cronjobs"} {
		bad.pages[r+"||"] = [2]string{`not json`}
	}
	if _, err := NewWorkloadSource("w", nil, bad, nil).Collect(context.Background()); err == nil {
		t.Error("every page was undecodable and the source returned no error")
	}
}

// leakStrings are the strings of the Decision 9 object
// (internal/testutil/testdata/leak-deployment.json) that no answer may
// carry.
var leakStrings = []string{"DB_PASSWORD", "hunter2", "API_KEY", "billing-creds", "token", "billing-config", "s3cr3t-hdr", "s3cr3t-query", "s3cr3t-exec", "s3cr3t-arg", "last-applied"}

func TestWorkloadEnvNeverReachesSnapshot(t *testing.T) {
	obj, err := os.ReadFile("../testutil/testdata/leak-deployment.json")
	if err != nil {
		t.Fatal(err)
	}
	src := NewWorkloadSource("w", nil, &workloadFake{pages: map[string][2]string{"deployments||": {string(obj)}}}, nil)
	snap := buildOne(t, src)
	s := snap.Service("k8s:shop/Deployment/billing")
	if s == nil || s.Workload.Image == nil || len(s.Workload.Probes) != 2 || s.Workload.Probes[0].Path != "/healthz" {
		t.Fatalf("billing = %+v", s)
	}
	data, err := json.Marshal(fleet.NewQuery(snap).SnapshotForSerialization())
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range leakStrings {
		if strings.Contains(string(data), leak) {
			t.Errorf("snapshot JSON holds %q", leak)
		}
	}
}

// manyWorkloads is n workload objects in ns, joined as page items.
func manyWorkloads(ns, prefix string, n int) string {
	items := make([]string, n)
	for i := range items {
		items[i] = workloadJSON(ns, fmt.Sprintf("%s%d", prefix, i), "img:1")
	}
	return strings.Join(items, ",")
}

func TestWorkloadsRowCountsPerKind(t *testing.T) {
	src := NewWorkloadSource("c1-workloads", &fleet.Cluster{ID: "c1", Name: "eu-prod"}, &workloadFake{pages: map[string][2]string{
		"deployments||": {manyWorkloads("shop", "d", 400) + "," + manyWorkloads("ops", "o", 12)},
		"daemonsets||":  {manyWorkloads("kube-system", "ds", 9)},
		"cronjobs||":    {manyWorkloads("shop", "cj", 31)},
	}}, nil)
	snap := buildOne(t, src)
	row := providerRow(snap, fleet.ProviderKubernetesWorkloads)
	if row == nil {
		t.Fatalf("providers = %+v, want the workloads row", snap.Providers)
	}
	if row.Evidence != "412 Deployments, 9 DaemonSets, 31 CronJobs" || row.Name != "Kubernetes workloads" || row.Status != fleet.SourceAvailable {
		t.Errorf("row = %+v", row)
	}
	if !slices.Equal(row.UsedFor, []string{fleet.UseInferredServices}) || row.Source != "c1-workloads" || row.Cluster != "eu-prod" || len(row.Limitations) != 0 {
		t.Errorf("row = %+v", row)
	}
	want := []fleet.ProviderCount{{Kind: "Deployment", Namespace: "shop", N: 400}, {Kind: "Deployment", Namespace: "ops", N: 12}, {Kind: "DaemonSet", Namespace: "kube-system", N: 9}, {Kind: "CronJob", Namespace: "shop", N: 31}}
	if !slices.Equal(row.Counts, want) {
		t.Errorf("counts = %+v, want %+v", row.Counts, want)
	}
	empty := buildOne(t, NewWorkloadSource("w", nil, &workloadFake{}, nil))
	if r := providerRow(empty, fleet.ProviderKubernetesWorkloads); r == nil || r.Evidence != "no workloads found" || r.Counts == nil {
		t.Errorf("an empty cluster row = %+v", r)
	}
}

func TestWorkloadsRowUnreadableKindNotZero(t *testing.T) {
	forbidden := apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "daemonsets"}, "", errors.New("denied"))
	src := NewWorkloadSource("w", nil, &workloadFake{
		pages: map[string][2]string{"deployments||": {workloadJSON("shop", "cart", "cart:1")}},
		errs:  map[string]error{"daemonsets|": forbidden},
	}, nil)
	row := providerRow(buildOne(t, src), fleet.ProviderKubernetesWorkloads)
	if row == nil || row.Evidence != "1 Deployment, DaemonSets unreadable" || row.Status != fleet.SourcePartial || !hasCode(row.Limitations, fleet.LimitationSourcePartial) {
		t.Fatalf("row = %+v", row)
	}
	// Forbidden in one of two namespaces: the kind is counted where it was
	// read, the row is partial and names the namespace it could not read.
	scoped := NewWorkloadSource("w", nil, &workloadFake{
		pages: map[string][2]string{"daemonsets|ops|": {workloadJSON("ops", "agent", "agent:1")}},
		errs:  map[string]error{"daemonsets|shop": forbidden},
	}, []string{"shop", "ops"})
	row = providerRow(buildOne(t, scoped), fleet.ProviderKubernetesWorkloads)
	if row == nil || row.Evidence != "at least 1 DaemonSet (some unreadable)" || row.Status != fleet.SourcePartial || !hasCode(row.Limitations, fleet.LimitationK8sNamespaceUnreadable) {
		t.Fatalf("scoped row = %+v", row)
	}
}

func TestWorkloadsListErrorIsNotEmpty(t *testing.T) {
	errs := map[string]error{}
	for _, r := range []string{"deployments", "statefulsets", "daemonsets", "cronjobs"} {
		errs[r+"|"] = errors.New("connection refused")
	}
	snap := buildOne(t, NewWorkloadSource("w", nil, &workloadFake{errs: errs}, nil))
	if len(snap.Providers) != 0 || snap.Sources[0].Status != fleet.SourceUnavailable {
		t.Fatalf("providers %+v sources %+v; want no row and the source unavailable", snap.Providers, snap.Sources)
	}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "no workloads found") {
		t.Error("a cluster no list could read reads as empty")
	}
}

func TestWorkloadsRowCapped(t *testing.T) {
	src := NewWorkloadSource("w", nil, &workloadFake{pages: map[string][2]string{
		"deployments||": {manyWorkloads("shop", "w", fleet.MaxInferredServices+1)},
	}}, nil)
	row := providerRow(buildOne(t, src), fleet.ProviderKubernetesWorkloads)
	if row == nil || row.Status != fleet.SourcePartial || !hasCode(row.Limitations, fleet.LimitationInferredServicesCapped) {
		t.Fatalf("capped row = %+v", row)
	}
	// The kinds the cap left unlisted read as unreadable, never as none.
	if want := "at least 5000 Deployments (some unreadable), StatefulSets unreadable, DaemonSets unreadable, CronJobs unreadable"; row.Evidence != want {
		t.Errorf("capped evidence = %q, want %q", row.Evidence, want)
	}
}

func TestProviderEvidenceNamesNoNamespace(t *testing.T) {
	forbidden := apierrors.NewForbidden(schema.GroupResource{Group: "batch", Resource: "cronjobs"}, "", errors.New("denied"))
	workloads := NewWorkloadSource("w", nil, &workloadFake{
		pages: map[string][2]string{
			"deployments|team-secret-x|": {workloadJSON("team-secret-x", "a", "a:1")},
			"statefulsets|payments-y|":   {workloadJSON("payments-y", "b", "b:1")},
		},
		errs: map[string]error{"cronjobs|payments-y": forbidden},
	}, []string{"team-secret-x", "payments-y"})
	gitops := NewWorkloadSource("g", nil, &workloadFake{versions: gitopsServed,
		pages: map[string][2]string{
			"applications|team-secret-x|":    {appJSON("team-secret-x", "a", `{}`)},
			"gitrepositories|team-secret-x|": {gitRepoJSON("team-secret-x", "r", "https://github.com/acme/r")},
		},
		errs: map[string]error{"helmreleases|payments-y": forbidden, "kustomizations|payments-y": forbidden},
	}, []string{"team-secret-x", "payments-y"})
	crd := NewK8sSource("k", nil, &nsFakeClient{found: true, lists: map[string]string{"team-secret-x": pactoIn("team-secret-x", "a")}, errs: map[string]error{"payments-y": errors.New("boom")}}, []string{"team-secret-x", "payments-y"})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, workloads, gitops, crd)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Providers) != 5 {
		t.Fatalf("providers = %+v", snap.Providers)
	}
	for _, p := range snap.Providers {
		if strings.Contains(p.Evidence, "team-secret-x") || strings.Contains(p.Evidence, "payments-y") {
			t.Errorf("row %s evidence names a namespace: %q", p.ID, p.Evidence)
		}
	}
}

func TestWorkloadsGroupedEndToEnd(t *testing.T) {
	items := strings.Join([]string{
		tracked("shop", "cart", "cart"),
		groupedWorkload("shop", "api", map[string]string{"kustomize.toolkit.fluxcd.io/name": "apps", "kustomize.toolkit.fluxcd.io/namespace": "flux-system"}, nil),
		groupedWorkload("logging", "shipper", map[string]string{"helm.toolkit.fluxcd.io/name": "log-shipper", "helm.toolkit.fluxcd.io/namespace": "observability"}, nil),
		groupedWorkload("shop", "export", nil, map[string]string{"meta.helm.sh/release-name": "exporter", "meta.helm.sh/release-namespace": "shop"}),
	}, ",")
	fake := &workloadFake{versions: gitopsServed, pages: map[string][2]string{
		"deployments||":    {items},
		"applications||":   {appJSON("argocd", "cart", `{"source":{"repoURL":"https://github.com/acme/shop","path":"deploy/cart"}}`, listedAs("shop", "cart"))},
		"kustomizations||": {kustomizationJSON("flux-system", "apps", `{"kind":"GitRepository","name":"platform"}`, "./apps", "shop_api_apps_Deployment")},
		"helmreleases||": {helmReleaseJSON("observability", "log-shipper",
			`{"targetNamespace":"logging","chart":{"spec":{"chart":"./charts/shipper","sourceRef":{"kind":"GitRepository","name":"platform","namespace":"flux-system"}}}}`)},
		"gitrepositories||": {gitRepoJSON("flux-system", "platform", "https://github.com/acme/platform")},
	}}
	snap := buildOne(t, NewWorkloadSource("w", nil, fake, nil))
	for key, want := range map[string][2]string{
		"k8s:shop/Deployment/cart":       {"https://github.com/acme/shop", "Application argocd/cart spec.source.repoURL"},
		"k8s:shop/Deployment/api":        {"https://github.com/acme/platform", "GitRepository flux-system/platform spec.url"},
		"k8s:logging/Deployment/shipper": {"https://github.com/acme/platform", "GitRepository flux-system/platform spec.url"},
	} {
		w := workloadOf(t, snap, key)
		if len(w.Repositories) != 1 || w.Repositories[0].URL != want[0] || w.Repositories[0].From != want[1] || w.RepositoryUnknown != "" {
			t.Errorf("%s: %+v %q", key, w.Repositories, w.RepositoryUnknown)
		}
	}
	if w := workloadOf(t, snap, "k8s:shop/Deployment/export"); w.Repositories != nil || w.RepositoryUnknown != "" || w.Group.Rule != "helm" {
		t.Errorf("a Helm group names no repository and no reason: %+v", w)
	}
	if argo, flux := providerRow(snap, fleet.ProviderArgoCD), providerRow(snap, fleet.ProviderFlux); argo == nil || flux == nil ||
		argo.Evidence != "1 Application" || flux.Evidence != "1 GitRepository, 1 HelmRelease, 1 Kustomization" {
		t.Errorf("rows = %+v %+v", argo, flux)
	}
}

func TestDiscoveryErrorKeepsGroupsWithReason(t *testing.T) {
	items := strings.Join([]string{
		tracked("shop", "cart", "cart"),
		groupedWorkload("shop", "api", map[string]string{"kustomize.toolkit.fluxcd.io/name": "apps", "kustomize.toolkit.fluxcd.io/namespace": "flux-system"}, nil),
		groupedWorkload("shop", "export", nil, map[string]string{"meta.helm.sh/release-name": "exporter", "meta.helm.sh/release-namespace": "shop"}),
	}, ",")
	run := func(discErr error) *fleet.FleetSnapshot {
		return buildOne(t, NewWorkloadSource("w", nil, &workloadFake{discErr: discErr, pages: map[string][2]string{"deployments||": {items}}}, nil))
	}
	snap, clean := run(errors.New("discovery unreachable")), run(nil)
	for _, key := range []string{"k8s:shop/Deployment/cart", "k8s:shop/Deployment/api"} {
		if w := workloadOf(t, snap, key); w.Group.Rule == workloadmanager.Workload || w.RepositoryUnknown != discoveryUnknown {
			t.Errorf("%s: %+v %q", key, w.Group, w.RepositoryUnknown)
		}
	}
	if w := workloadOf(t, snap, "k8s:shop/Deployment/export"); w.RepositoryUnknown != "" {
		t.Errorf("a Helm group says %q", w.RepositoryUnknown)
	}
	if providerRow(snap, fleet.ProviderArgoCD) != nil || providerRow(snap, fleet.ProviderFlux) != nil || providerRow(snap, fleet.ProviderHelm) == nil {
		t.Errorf("rows = %+v", snap.Providers)
	}
	if !sameSourceState(snap, clean) {
		t.Errorf("source state differs:\n%+v\n%+v", snap.Sources, clean.Sources)
	}
}

// sameSourceState reports whether a and b hold the same source states, the
// same workloads row and the same completeness, timestamps aside.
func sameSourceState(a, b *fleet.FleetSnapshot) bool {
	for _, st := range [][]fleet.SourceState{a.Sources, b.Sources} {
		for i := range st {
			st[i].LastSuccessfulSync, st[i].ObservedAt = nil, nil
		}
	}
	return reflect.DeepEqual(a.Sources, b.Sources) && a.Completeness == b.Completeness &&
		reflect.DeepEqual(providerRow(a, fleet.ProviderKubernetesWorkloads), providerRow(b, fleet.ProviderKubernetesWorkloads))
}

func TestGroupingFailureLeavesSourceHealth(t *testing.T) {
	forbidden := apierrors.NewForbidden(schema.GroupResource{Group: "argoproj.io", Resource: "applications"}, "", errors.New("denied"))
	run := func(versions map[string]string) (*fleet.Collection, *fleet.FleetSnapshot) {
		fake := &workloadFake{versions: versions, pages: map[string][2]string{"deployments||": {tracked("shop", "cart", "argocd_cart")}},
			errs: map[string]error{"applications|": forbidden}}
		col, err := NewWorkloadSource("w", nil, fake, nil).Collect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return col, buildOne(t, NewWorkloadSource("w", nil, fake, nil))
	}
	withCol, with := run(map[string]string{"argoproj.io": "v1alpha1"})
	withoutCol, without := run(nil)
	argo := providerRow(with, fleet.ProviderArgoCD)
	if argo == nil || argo.Status != fleet.SourcePartial || argo.Evidence != "Applications unreadable" || !hasCode(argo.Limitations, fleet.LimitationSourcePartial) {
		t.Fatalf("Argo CD row = %+v", argo)
	}
	if !reflect.DeepEqual(withCol.Limitations, withoutCol.Limitations) || !sameSourceState(with, without) {
		t.Errorf("a forbidden Application list changed the workload source:\n%+v\n%+v", with.Sources, without.Sources)
	}
}

func TestArgoRowCountsAndZero(t *testing.T) {
	row := func(apps string) *fleet.Provider {
		fake := &workloadFake{versions: map[string]string{"argoproj.io": "v1alpha1"}, pages: map[string][2]string{"applications||": {apps}}}
		return providerRow(buildOne(t, NewWorkloadSource("w", &fleet.Cluster{ID: "c1", Name: "eu-prod"}, fake, nil)), fleet.ProviderArgoCD)
	}
	empty := row("")
	want := []fleet.ProviderObject{{Kind: "CustomResourceDefinition", Name: "applications.argoproj.io"}}
	if empty == nil || empty.Evidence != "no Applications found" || empty.Name != "Argo CD" || empty.Status != fleet.SourceAvailable || !reflect.DeepEqual(empty.Objects, want) ||
		!slices.Equal(empty.UsedFor, []string{fleet.UseServiceGrouping, fleet.UseRepositoryProvenance}) || empty.Cluster != "eu-prod" {
		t.Errorf("served with no Application = %+v", empty)
	}
	three := row(strings.Join([]string{appJSON("argocd", "a", `{}`), appJSON("argocd", "b", `{}`), appJSON("argocd", "c", `{}`)}, ","))
	if three == nil || three.Evidence != "3 Applications" {
		t.Errorf("three Applications = %+v", three)
	}
}

func TestArgoRowUnreadableNotZero(t *testing.T) {
	forbidden := apierrors.NewForbidden(schema.GroupResource{Group: "argoproj.io", Resource: "applications"}, "", errors.New("denied"))
	fake := &workloadFake{versions: map[string]string{"argoproj.io": "v1alpha1"},
		pages: map[string][2]string{"applications|ops|": {appJSON("ops", "a", `{}`)}},
		errs:  map[string]error{"applications|shop": forbidden}}
	snap := buildOne(t, NewWorkloadSource("w", nil, fake, []string{"shop", "ops"}))
	row := providerRow(snap, fleet.ProviderArgoCD)
	if row == nil || row.Evidence != "at least 1 Application (some unreadable)" || row.Status != fleet.SourcePartial || !hasCode(row.Limitations, fleet.LimitationK8sNamespaceUnreadable) {
		t.Fatalf("row = %+v", row)
	}
	fake = &workloadFake{versions: map[string]string{"argoproj.io": "v1alpha1"}, errs: map[string]error{"applications|": forbidden}}
	if row := providerRow(buildOne(t, NewWorkloadSource("w", nil, fake, nil)), fleet.ProviderArgoCD); row == nil || strings.Contains(row.Evidence, "no Applications") {
		t.Errorf("a forbidden list reads as none: %+v", row)
	}
}

func TestFluxRowServedKindsOnly(t *testing.T) {
	notFound := apierrors.NewNotFound(schema.GroupResource{Group: "argoproj.io", Resource: "applications"}, "")
	fake := &workloadFake{versions: map[string]string{"argoproj.io": "v1alpha1", "helm.toolkit.fluxcd.io": "v2beta2", "source.toolkit.fluxcd.io": "v1"},
		errs: map[string]error{"applications|": notFound}}
	snap := buildOne(t, NewWorkloadSource("w", nil, fake, nil))
	if row := providerRow(snap, fleet.ProviderArgoCD); row != nil {
		t.Errorf("Argo Rollouts alone serves argoproj.io and gave an Argo CD row: %+v", row)
	}
	flux := providerRow(snap, fleet.ProviderFlux)
	want := []fleet.ProviderObject{{Kind: "CustomResourceDefinition", Name: "gitrepositories.source.toolkit.fluxcd.io"}, {Kind: "CustomResourceDefinition", Name: "helmreleases.helm.toolkit.fluxcd.io"}}
	if flux == nil || !reflect.DeepEqual(flux.Objects, want) || flux.Evidence != "no GitRepositories or HelmReleases found" {
		t.Errorf("Flux row = %+v", flux)
	}
	for gvr := range fake.gvrs {
		if gvr.Resource == "kustomizations" {
			t.Error("listed a kind the cluster does not serve")
		}
		if gvr.Resource == "helmreleases" && gvr.Version != "v2beta2" {
			t.Errorf("listed %v, not the preferred version", gvr)
		}
	}
}

// promoterFingerprints are an installation's promoters, as the chart writes
// them: data, never code.
var promoterFingerprints = []workloadmanager.Fingerprint{
	{Product: "Promoter", CRD: "stages.promoter.example.com"},
	{Product: "Promoter", CRD: "warehouses.promoter.example.com"},
	{Product: "Updater", Annotation: "updater.example.com/image-list"},
	{Product: "Portal", Label: "portal.example.com/promoted"},
}

func promoterRowsOf(col *fleet.Collection) []fleet.Provider {
	return slices.DeleteFunc(slices.Clone(col.Providers), func(p fleet.Provider) bool { return !slices.Contains(p.UsedFor, fleet.UsePromotion) })
}

func TestPromoterRowFromCRD(t *testing.T) {
	f := &workloadFake{versions: map[string]string{"promoter.example.com": "v1alpha1"}}
	col, err := NewWorkloadSource("w", nil, f, nil).WithPromoters(promoterFingerprints).Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Discovery says only that the API group is served, so the evidence names
	// the group, once, and never a CRD nobody checked.
	want := []fleet.Provider{{ID: "promotion/Promoter", Name: "Promoter", UsedFor: []string{fleet.UsePromotion},
		Evidence: "promotion owned by Promoter (API group promoter.example.com served)"}}
	if got := promoterRowsOf(col); !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %+v", got)
	}
	if !slices.Contains(f.discovered, "promoter.example.com") {
		t.Errorf("discovery asked for %v", f.discovered)
	}
	// A failed discovery is a partial row that says Pacto could not check,
	// never a missing row that reads as no promoter.
	f = &workloadFake{versions: map[string]string{"promoter.example.com": "v1"}, discErr: errors.New("down")}
	col, _ = NewWorkloadSource("w", nil, f, nil).WithPromoters(promoterFingerprints).Collect(t.Context())
	want = []fleet.Provider{{ID: "promotion/Promoter", Name: "Promoter", UsedFor: []string{fleet.UsePromotion}, Status: fleet.SourcePartial,
		Evidence: "Pacto could not check whether Promoter promotes here", Limitations: []fleet.Limitation{
			{Code: fleet.LimitationSourcePartial, Source: "w", Message: "Pacto could not discover whether CRD stages.promoter.example.com is served"},
			{Code: fleet.LimitationSourcePartial, Source: "w", Message: "Pacto could not discover whether CRD warehouses.promoter.example.com is served"}}}}
	if got := promoterRowsOf(col); !reflect.DeepEqual(got, want) {
		t.Errorf("discovery failed: %+v", got)
	}
	if strings.HasPrefix(want[0].Evidence, fleet.PromotionOwnedBy) {
		t.Error("an unchecked promoter claims promotion")
	}
	// A product found another way keeps its evidence and says what it could not check.
	both := []workloadmanager.Fingerprint{{Product: "P", CRD: "stages.promoter.example.com"}, {Product: "P", Label: "portal.example.com/promoted"}}
	f = &workloadFake{discErr: errors.New("down"), pages: map[string][2]string{"deployments||": {`{"metadata":{"name":"cart","namespace":"shop","labels":{"portal.example.com/promoted":"yes"}}}`}}}
	col, _ = NewWorkloadSource("w", nil, f, nil).WithPromoters(both).Collect(t.Context())
	if got := promoterRowsOf(col); len(got) != 1 || got[0].Evidence != "promotion owned by P (label portal.example.com/promoted on workloads)" || got[0].Status != fleet.SourcePartial || len(got[0].Limitations) != 1 {
		t.Errorf("found and unchecked: %+v", got)
	}
	f.discErr, f.versions = nil, map[string]string{"promoter.example.com": "v1"}
	col, _ = NewWorkloadSource("w", nil, f, nil).WithPromoters(both).Collect(t.Context())
	if got := promoterRowsOf(col); len(got) != 1 || got[0].Evidence != "promotion owned by P (API group promoter.example.com served, label portal.example.com/promoted on workloads)" || len(got[0].Objects) != 1 {
		t.Errorf("found twice: %+v", got)
	}
	if col, _ := NewWorkloadSource("w", nil, &workloadFake{versions: map[string]string{"promoter.example.com": "v1"}}, nil).Collect(t.Context()); len(promoterRowsOf(col)) != 0 {
		t.Errorf("no fingerprints: %+v", promoterRowsOf(col))
	}
}

func TestPromoterRowFromAnnotation(t *testing.T) {
	annotated := `{"metadata":{"name":"cart","namespace":"shop","annotations":{"updater.example.com/image-list":"cart=img"},"labels":{"portal.example.com/promoted":"yes"}}}`
	other := `{"metadata":{"name":"db","namespace":"shop","annotations":{"portal.example.com/promoted":"in annotations, not labels"}}}`
	f := &workloadFake{pages: map[string][2]string{"deployments||": {annotated + "," + other}}}
	col, err := NewWorkloadSource("w", nil, f, nil).WithPromoters(promoterFingerprints).Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	cart := []fleet.ProviderObject{{Kind: "Deployment", Namespace: "shop", Name: "cart"}}
	want := []fleet.Provider{
		{ID: "promotion/Updater", Name: "Updater", UsedFor: []string{fleet.UsePromotion}, Evidence: "promotion owned by Updater (annotation updater.example.com/image-list on workloads)", Objects: cart},
		{ID: "promotion/Portal", Name: "Portal", UsedFor: []string{fleet.UsePromotion}, Evidence: "promotion owned by Portal (label portal.example.com/promoted on workloads)", Objects: cart},
	}
	if got := promoterRowsOf(col); !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %+v", got)
	}
}

var promoterOffer = workloadmanager.Offer{ID: "promoter-gate", Text: "Gate every promotion with one template", Doc: "https://pacto.run/latest/x/#y"}

func TestPromoterOfferFromFingerprint(t *testing.T) {
	fps := []workloadmanager.Fingerprint{
		{Product: "Promoter", CRD: "stages.promoter.example.com", Offer: &promoterOffer},
		{Product: "Plain", CRD: "stages.plain.example.com"},
		{Product: "Unserved", CRD: "stages.unserved.example.com", Offer: &promoterOffer},
	}
	f := &workloadFake{versions: map[string]string{"promoter.example.com": "v1", "plain.example.com": "v1"}}
	col, err := NewWorkloadSource("w", nil, f, nil).WithPromoters(fps).Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rows := promoterRowsOf(col)
	if len(rows) != 2 || !reflect.DeepEqual(rows[0].Offers, []fleet.ProviderOffer{fleet.ProviderOffer(promoterOffer)}) || rows[1].Offers != nil {
		t.Fatalf("rows = %+v", rows)
	}
	// A promoter discovery could not check is partial and offers nothing.
	f.discErr = errors.New("down")
	col, _ = NewWorkloadSource("w", nil, f, nil).WithPromoters(fps[:1]).Collect(t.Context())
	if rows := promoterRowsOf(col); len(rows) != 1 || rows[0].Offers != nil {
		t.Fatalf("unchecked rows = %+v", rows)
	}
}

func TestKargoFingerprintsGiveOneOffer(t *testing.T) {
	values, err := os.ReadFile("../../integrations/kubernetes/charts/pacto-operator/values.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var chart struct {
		Fingerprints yaml.Node `yaml:"fingerprints"`
	}
	if err := yaml.Unmarshal(values, &chart); err != nil {
		t.Fatal(err)
	}
	raw, _ := yaml.Marshal(&chart.Fingerprints)
	fp, err := workloadmanager.ParseFingerprints(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	f := &workloadFake{versions: map[string]string{"kargo.akuity.io": "v1alpha1"}}
	snap := buildOne(t, NewWorkloadSource("w", nil, f, nil).WithPromoters(fp.Promoters))
	var kargo []fleet.Provider
	for _, p := range snap.Providers {
		if p.ID == "promotion/Kargo" {
			kargo = append(kargo, p)
		}
	}
	want := []fleet.ProviderOffer{{ID: "kargo-analysis-template", Text: "Gate every Kargo promotion with one shared AnalysisTemplate", Doc: "https://pacto.run/latest/integrations/kubernetes/gitops-kargo/#kargo"}}
	if len(kargo) != 1 || !reflect.DeepEqual(kargo[0].Offers, want) {
		t.Fatalf("Kargo rows = %+v", kargo)
	}
}
