package fleetsrc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/kubecluster"
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
	if row == nil || row.Evidence != "1 DaemonSet" || row.Status != fleet.SourcePartial || !hasCode(row.Limitations, fleet.LimitationK8sNamespaceUnreadable) {
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
	crd := NewK8sSource("k", nil, &nsFakeClient{found: true, lists: map[string]string{"team-secret-x": pactoIn("team-secret-x", "a")}, errs: map[string]error{"payments-y": errors.New("boom")}}, []string{"team-secret-x", "payments-y"})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, workloads, crd)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Providers) != 2 {
		t.Fatalf("providers = %+v", snap.Providers)
	}
	for _, p := range snap.Providers {
		if strings.Contains(p.Evidence, "team-secret-x") || strings.Contains(p.Evidence, "payments-y") {
			t.Errorf("row %s evidence names a namespace: %q", p.ID, p.Evidence)
		}
	}
}
