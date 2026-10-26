package pactify

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/internal/runtimewatch/kubesink"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

func obj(apiVersion, kind, ns, name string, fields map[string]any) *unstructured.Unstructured {
	o := map[string]any{"apiVersion": apiVersion, "kind": kind, "metadata": map[string]any{"name": name, "namespace": ns}}
	for k, v := range fields {
		if k == "metadata" {
			for mk, mv := range v.(map[string]any) {
				o["metadata"].(map[string]any)[mk] = mv
			}
			continue
		}
		o[k] = v
	}
	return &unstructured.Unstructured{Object: o}
}

func deployment(ns, name, image string) *unstructured.Unstructured {
	return obj("apps/v1", "Deployment", ns, name, map[string]any{
		"metadata": map[string]any{"labels": map[string]any{"team": "shop"}},
		"spec": map[string]any{"selector": map[string]any{"matchLabels": map[string]any{"app": name}}, "template": map[string]any{
			"metadata": map[string]any{"labels": map[string]any{"app": name}},
			"spec":     map[string]any{"containers": []any{map[string]any{"name": name, "image": image, "ports": []any{map[string]any{"containerPort": int64(8080)}}}}}}}})
}

func service(ns, name, ip string) *unstructured.Unstructured {
	return obj("v1", "Service", ns, name, map[string]any{"spec": map[string]any{
		"clusterIP": ip, "clusterIPs": []any{ip}, "selector": map[string]any{"app": name},
		"ports": []any{map[string]any{"port": int64(80), "targetPort": int64(8080)}}}})
}

func controlled(o *unstructured.Unstructured, kind, name, uid string) *unstructured.Unstructured {
	o.Object["metadata"].(map[string]any)["ownerReferences"] = []any{map[string]any{"apiVersion": "apps/v1", "kind": kind, "name": name, "uid": "x", "controller": true}}
	if uid != "" {
		o.Object["metadata"].(map[string]any)["uid"] = uid
	}
	return o
}

var clusterLists = map[schema.GroupVersionResource]string{
	workloadGVRs["Deployment"]: "DeploymentList", workloadGVRs["StatefulSet"]: "StatefulSetList", workloadGVRs["DaemonSet"]: "DaemonSetList",
	workloadGVRs["CronJob"]: "CronJobList", gvrServices: "ServiceList", gvrPods: "PodList", gvrReplicaSets: "ReplicaSetList", gvrJobs: "JobList",
	gvrEndpointSlices: "EndpointSliceList", gvrPactos: "PactoList", kubesink.GVR: "NodeRuntimeObservationList",
}

// shopCluster is a namespace with checkout calling payments, which a Pacto
// describes at 2.3.0, and fraud, which none does; a nightly CronJob calls
// nothing; payments calls itself.
func shopCluster(now time.Time) []runtime.Object {
	call := func(ip string, port int64, at time.Time) map[string]any {
		return map[string]any{"address": ip, "port": port, "successes": int64(3), "lastSuccessAt": at.UTC().Format(time.RFC3339)}
	}
	return []runtime.Object{
		deployment("shop", "checkout", "reg.io/acme/checkout:1.4.0"), service("shop", "checkout", "10.0.0.10"),
		deployment("shop", "payments", "reg.io/acme/payments:2.3.0"), service("shop", "payments", "10.0.0.20"),
		deployment("shop", "fraud", "reg.io/acme/fraud:0.9.0"), service("shop", "fraud", "None"),
		obj("batch/v1", "CronJob", "shop", "nightly", map[string]any{"spec": map[string]any{"jobTemplate": map[string]any{"spec": map[string]any{"template": map[string]any{
			"metadata": map[string]any{"labels": map[string]any{"app": "nightly"}},
			"spec":     map[string]any{"containers": []any{map[string]any{"name": "nightly", "image": "reg.io/acme/nightly:1.0.0"}}}}}}}}),
		controlled(obj("apps/v1", "ReplicaSet", "shop", "checkout-7f", nil), "Deployment", "checkout", ""),
		controlled(obj("v1", "Pod", "shop", "checkout-7f-a", map[string]any{"metadata": map[string]any{"labels": map[string]any{"app": "checkout"}}}), "ReplicaSet", "checkout-7f", "u-checkout"),
		obj("v1", "Pod", "shop", "payments-0", map[string]any{"metadata": map[string]any{"uid": "u-payments", "labels": map[string]any{"app": "payments"}}}),
		controlled(obj("batch/v1", "Job", "shop", "nightly-1", nil), "CronJob", "nightly", ""),
		controlled(obj("v1", "Pod", "shop", "nightly-1-a", nil), "Job", "nightly-1", "u-nightly"),
		obj("apps/v1", "StatefulSet", "shop", "ledger", map[string]any{"spec": map[string]any{"template": map[string]any{
			"metadata": map[string]any{"labels": map[string]any{"app": "ledger"}},
			"spec":     map[string]any{"containers": []any{map[string]any{"name": "ledger", "image": "reg.io/acme/ledger:3.0.0"}}}}}}),
		controlled(obj("v1", "Pod", "shop", "ledger-0", nil), "StatefulSet", "ledger", "u-ledger"),
		obj("v1", "Service", "shop", "legacy", map[string]any{"spec": map[string]any{"clusterIP": "10.0.0.40", "ports": []any{map[string]any{"port": int64(80)}}}}),
		obj("pacto.trianalab.io/v1alpha1", "Pacto", "shop", "ghost", map[string]any{"spec": map[string]any{"target": map[string]any{"serviceName": "legacy"}}}),
		obj("discovery.k8s.io/v1", "EndpointSlice", "shop", "fraud-x", map[string]any{
			"metadata":  map[string]any{"labels": map[string]any{"kubernetes.io/service-name": "fraud"}},
			"endpoints": []any{map[string]any{"addresses": []any{"10.1.0.31"}}}, "ports": []any{map[string]any{"port": int64(8080)}}}),
		obj("discovery.k8s.io/v1", "EndpointSlice", "shop", "other", map[string]any{"metadata": map[string]any{"labels": map[string]any{"kubernetes.io/service-name": "checkout"}}}),
		obj("pacto.trianalab.io/v1alpha1", "Pacto", "shop", "payments", map[string]any{
			"spec":   map[string]any{"contractRef": map[string]any{"oci": "oci://reg.io/acme/contracts/payments:2.3.0"}, "target": map[string]any{"serviceName": "payments", "workloadRef": map[string]any{"name": "payments", "kind": "Deployment"}}},
			"status": map[string]any{"contract": map[string]any{"serviceName": "payments", "version": "2.3.0", "resolvedRef": "oci://reg.io/acme/contracts/payments@sha256:aa"}}}),
		obj("pacto.trianalab.io/v1alpha1", "NodeRuntimeObservation", "pacto-system", "node-1", map[string]any{"status": map[string]any{
			"nodeName": "node-1",
			"pods": []any{
				map[string]any{"uid": "u-checkout", "connections": []any{call("10.0.0.20", 80, now.Add(-time.Minute)), call("10.1.0.31", 8080, now.Add(-time.Minute)), call("10.0.0.99", 80, now)}},
				map[string]any{"uid": "u-payments", "connections": []any{call("10.0.0.20", 80, now), call("10.0.0.10", 80, now.Add(-48*time.Hour))}},
				map[string]any{"uid": "u-gone", "connections": []any{call("10.0.0.20", 80, now)}},
				map[string]any{"uid": "u-ledger", "connections": []any{call("10.0.0.40", 80, now), call("10.0.0.20", 80, now)}},
			}}}),
	}
}

func fakeCluster(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), clusterLists, objs...)
}

func result(t *testing.T, b *Bulk, name string) *Result {
	t.Helper()
	for _, r := range b.Results {
		if r.Service == name {
			return r
		}
	}
	t.Fatalf("no draft for %s: %+v", name, b)
	return nil
}

// TestClusterDrafts: every workload no Pacto describes gets a draft, its
// dependencies read from the calls its pods made. A call to a provider whose
// Pacto runs a version is pinned to exactly that version, never "*"; one
// with no contract version is only a comment. A plan writes nothing.
func TestClusterDrafts(t *testing.T) {
	now := time.Now()
	out := filepath.Join(t.TempDir(), "drafts")
	c := ClusterOptions{Namespaces: []string{"shop"}, Out: out, Now: func() time.Time { return now }}
	b, err := Cluster(context.Background(), fakeCluster(shopCluster(now)...), Options{Plan: true}, c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(b.Skipped, []string{"shop/Deployment/payments"}) || len(b.Results) != 4 {
		t.Fatalf("drafts: %+v", b)
	}
	if _, err := os.Stat(out); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("a plan wrote under --out")
	}
	checkout := result(t, b, "checkout")
	text := file(t, checkout, "pacto.yaml").Content
	pin := "dependencies:\n  # inferred from runtime calls: pinned to the version seen running; widen it once you know\n" +
		"  - name: payments\n    ref: oci://reg.io/acme/contracts/payments\n    required: true\n    compatibility: '=2.3.0'\n"
	if !strings.Contains(text, pin) || strings.Contains(text, "'*'") || strings.Contains(text, `"*"`) || !strings.Contains(text, "#   - name: fraud\n") || strings.Contains(text, "# dependencies:") {
		t.Fatalf("checkout's dependencies:\n%s", text)
	}
	if checkout.Dir != filepath.Join(out, "shop", "checkout") || file(t, checkout, "manifests.yaml").Action != Add ||
		!hasLine(checkout.Next, "pacto.yaml declares payments pinned to =2.3.0") || !hasLine(checkout.Found, "checkout calls payments 2.3.0 (calls seen to the Service shop/payments)") {
		t.Fatalf("checkout: %+v", checkout)
	}
	m := file(t, checkout, "manifests.yaml").Content
	if !strings.Contains(m, "kind: Deployment") || !strings.Contains(m, "kind: Service") || strings.Contains(m, "clusterIP") || strings.Contains(m, "namespace") {
		t.Fatalf("manifests.yaml:\n%s", m)
	}
	// ledger calls payments and legacy, whose Pacto runs no version yet: a comment.
	if l := file(t, result(t, b, "ledger"), "pacto.yaml").Content; !strings.Contains(l, "compatibility: '=2.3.0'") || !strings.Contains(l, "#   - name: legacy\n") {
		t.Fatalf("ledger:\n%s", l)
	}
	if n := result(t, b, "nightly"); strings.Contains(file(t, n, "pacto.yaml").Content, "dependencies") || len(n.Dependencies) != 0 {
		t.Fatalf("nightly calls nothing: %+v", n)
	}

	// Applying writes the drafts; every one is valid, and a second run keeps
	// what is there.
	c.Namespaces = nil
	b, err = Cluster(context.Background(), fakeCluster(shopCluster(now)...), Options{}, c, os.WriteFile)
	if err != nil || len(b.Results) != 4 {
		t.Fatalf("apply: %+v %v", b, err)
	}
	for _, r := range b.Results {
		if !r.Applied {
			t.Fatalf("not applied: %+v", r)
		}
	}
	again, err := Cluster(context.Background(), fakeCluster(shopCluster(now)...), Options{Plan: true}, c, nil)
	if err != nil || file(t, result(t, again, "checkout"), "manifests.yaml").Action != Keep || file(t, result(t, again, "checkout"), "pacto.yaml").Action != Keep {
		t.Fatalf("second run: %+v %v", again, err)
	}
	checkPinnedReadsIncompatible(t, out)
}

// checkPinnedReadsIncompatible: the draft consumer, pinned to =2.3.0, reads
// incompatible for payments 2.4.0, so the change asks checkout's owners.
func checkPinnedReadsIncompatible(t *testing.T, out string) {
	t.Helper()
	provider := `pactoVersion: "2.1"
service: {name: payments, version: %s}
publish:
  bundle: oci://reg.io/acme/contracts/payments
  artifacts:
    - {name: api, type: image, ref: oci://reg.io/acme/payments, build: {dockerfile: {context: .}}}
`
	dir := writeTree(t, t.TempDir(), map[string]string{"payments/pacto.yaml": strings.Replace(provider, "%s", "2.3.0", 1)})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleetsrc.NewLocalSource("drafts", out), fleetsrc.NewLocalSource("providers", dir))
	if err != nil {
		t.Fatal(err)
	}
	parse := func(v string) (*contract.Contract, fs.FS) {
		text := strings.Replace(provider, "%s", v, 1)
		c, err := contract.Parse(strings.NewReader(text))
		if err != nil {
			t.Fatal(err)
		}
		return c, fstest.MapFS{"pacto.yaml": {Data: []byte(text)}}
	}
	old, oldFS := parse("2.3.0")
	next, newFS := parse("2.4.0")
	res := impact.Analyze(context.Background(), old, next, oldFS, newFS, snap, impact.Options{})
	i := slices.IndexFunc(res.Consumers, func(c impact.AffectedConsumer) bool { return c.Service == "checkout" })
	if i < 0 || res.Consumers[i].CompatibilityVerdict != impact.CompatibilityIncompatible || res.Consumers[i].Compatibility != "=2.3.0" {
		t.Fatalf("checkout for payments 2.4.0: %+v", res.Consumers)
	}
}

// TestClusterWithoutObservations: with no node agent, and no Pacto CRD, the
// drafts carry no dependencies and say no calls were seen.
func TestClusterWithoutObservations(t *testing.T) {
	cl := fakeCluster(deployment("shop", "checkout", "reg.io/acme/checkout:1.4.0"))
	for _, r := range []string{"pactos", "noderuntimeobservations"} {
		cl.PrependReactor("list", r, func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errNotFound(r)
		})
	}
	b, err := Cluster(context.Background(), cl, Options{Plan: true}, ClusterOptions{Out: t.TempDir()}, nil)
	if err != nil || len(b.Results) != 1 || !hasLine(b.Results[0].Unknown, "the cluster holds no node runtime observations") {
		t.Fatalf("drafts: %+v %v", b, err)
	}
}

// TestClusterStaleObservations: a node whose observation is stale, or that
// has none, cannot rule a call out, so each draft with a pod there says so;
// a pod on a node observed now adds no such line.
func TestClusterStaleObservations(t *testing.T) {
	now := time.Now()
	pod := func(name, node string) runtime.Object {
		return controlled(obj("v1", "Pod", "shop", name+"-0", map[string]any{"spec": map[string]any{"nodeName": node}}), "StatefulSet", name, "u-"+name)
	}
	nro := func(node string, published time.Time) runtime.Object {
		return obj("pacto.trianalab.io/v1alpha1", "NodeRuntimeObservation", "pacto-system", node, map[string]any{"status": map[string]any{
			"nodeName": node, "publishIntervalSeconds": int64(30), "lastPublished": published.UTC().Format(time.RFC3339)}})
	}
	set := func(name string) runtime.Object {
		return obj("apps/v1", "StatefulSet", "shop", name, map[string]any{"spec": map[string]any{"template": map[string]any{
			"spec": map[string]any{"containers": []any{map[string]any{"name": name, "image": "reg.io/acme/" + name + ":1.0.0"}}}}}})
	}
	cl := fakeCluster(set("checkout"), set("fraud"), set("ledger"), pod("checkout", "node-1"), pod("fraud", "node-2"), pod("ledger", "node-3"),
		obj("v1", "Pod", "shop", "pending", nil), nro("node-1", now.Add(-10*time.Second)), nro("node-2", now.Add(-3*time.Hour)))
	b, err := Cluster(context.Background(), cl, Options{Plan: true}, ClusterOptions{Out: t.TempDir(), Now: func() time.Time { return now }}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"checkout": "", "fraud": "node node-2 has no current observation", "ledger": "node node-3 has no current observation"} {
		u := result(t, b, name).Unknown
		if want == "" && hasLine(u, "has no current observation") || want != "" && !hasLine(u, want) {
			t.Errorf("%s: %q", name, u)
		}
	}
}

func errNotFound(resource string) error {
	return apierrors.NewNotFound(schema.GroupResource{Group: "pacto.trianalab.io", Resource: resource}, "")
}

// TestClusterRefusals: what Cluster refuses, and the API and folder errors
// it reports.
func TestClusterRefusals(t *testing.T) {
	ctx, out := context.Background(), t.TempDir()
	now := time.Now()
	c := ClusterOptions{Out: out}
	for _, tc := range []struct {
		opts Options
		c    ClusterOptions
		want string
	}{
		{Options{}, ClusterOptions{}, "--out"},
		{Options{Name: "x"}, c, "takes no name or folder"},
		{Options{Dir: "x"}, c, "takes no name or folder"},
		{Options{Plan: true, Environments: []string{"Bad"}}, c, "shop/checkout: environment"},
	} {
		if _, err := Cluster(ctx, fakeCluster(shopCluster(now)...), tc.opts, tc.c, nil); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: %v", tc.opts, err)
		}
	}
	for _, r := range []string{"deployments", "services", "replicasets", "noderuntimeobservations"} {
		cl := fakeCluster(shopCluster(now)...)
		cl.PrependReactor("list", r, func(k8stesting.Action) (bool, runtime.Object, error) { return true, nil, errors.New("boom") })
		if _, err := Cluster(ctx, cl, Options{Plan: true}, c, nil); err == nil || !strings.Contains(err.Error(), "listing "+r+": boom") {
			t.Errorf("%s: %v", r, err)
		}
	}
	bad := obj("pacto.trianalab.io/v1alpha1", "NodeRuntimeObservation", "pacto-system", "node-2", map[string]any{"status": map[string]any{"surprise": true}})
	if _, err := Cluster(ctx, fakeCluster(append(shopCluster(now), bad)...), Options{Plan: true}, c, nil); err == nil || !strings.Contains(err.Error(), "node-2") {
		t.Errorf("bad observation: %v", err)
	}
	failing := func(string, []byte, os.FileMode) error { return errors.New("disk full") }
	if _, err := Cluster(ctx, fakeCluster(shopCluster(now)...), Options{}, c, failing); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("write: %v", err)
	}
	blocked := filepath.Join(t.TempDir(), "file")
	writeTree(t, filepath.Dir(blocked), map[string]string{"file": "x"})
	if _, err := Cluster(ctx, fakeCluster(shopCluster(now)...), Options{}, ClusterOptions{Out: blocked}, os.WriteFile); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("folder: %v", err)
	}
	t.Setenv("TMPDIR", filepath.Join(blocked, "tmp"))
	if _, err := Cluster(ctx, fakeCluster(shopCluster(now)...), Options{Plan: true}, c, nil); err == nil {
		t.Error("a plan with no scratch folder")
	}
}

// TestMany: several folders, each pactified; one that fails is named and
// does not stop the others, and a name fits only one folder.
func TestMany(t *testing.T) {
	a, b := fraudDir(t), writeTree(t, t.TempDir(), map[string]string{"x/Dockerfile": "FROM scratch\n"})
	bulk, err := Many(context.Background(), Options{Plan: true}, []string{a, filepath.Join(a, "k8s", "deployment.yaml"), b}, nil)
	if err == nil || !strings.Contains(err.Error(), "deployment.yaml: ") || len(bulk.Results) != 3 || bulk.Results[0].Service != "fraud" || bulk.Results[1] != nil || bulk.Results[2] == nil {
		t.Fatalf("many: %+v %v", bulk, err)
	}
	if _, err := Many(context.Background(), Options{Name: "x"}, []string{a, b}, nil); err == nil {
		t.Fatal("one name for two folders")
	}
}
