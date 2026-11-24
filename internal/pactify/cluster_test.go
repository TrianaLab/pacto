package pactify

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
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
	"github.com/trianalab/pacto/v4/internal/k8sclient"
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
	k8sclient.WorkloadGVRs["Deployment"]: "DeploymentList", k8sclient.WorkloadGVRs["StatefulSet"]: "StatefulSetList", k8sclient.WorkloadGVRs["DaemonSet"]: "DaemonSetList",
	k8sclient.WorkloadGVRs["CronJob"]: "CronJobList", gvrServices: "ServiceList", gvrPods: "PodList", gvrReplicaSets: "ReplicaSetList", gvrJobs: "JobList",
	gvrEndpointSlices: "EndpointSliceList", k8sclient.PactosGVR: "PactoList", kubesink.GVR: "NodeRuntimeObservationList",
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

// fakeCluster serves objs. Touching a Secret or a ConfigMap panics, so no
// test of the cluster read can pass while it reads one.
func fakeCluster(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	cl := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), clusterLists, objs...)
	for _, r := range []string{"secrets", "configmaps"} {
		cl.PrependReactor("*", r, func(a k8stesting.Action) (bool, runtime.Object, error) {
			panic("the cluster read touched " + a.GetResource().Resource)
		})
	}
	return cl
}

// paged makes cl serve the lists of resource in one namespace page by page,
// by Limit and Continue as an API server does, and returns the Limits asked
// for. The fake drops both from a list of every namespace, which then gets
// every item.
func paged(cl *dynamicfake.FakeDynamicClient, resource string) *[]int64 {
	limits := &[]int64{}
	cl.PrependReactor("list", resource, func(a k8stesting.Action) (bool, runtime.Object, error) {
		la := a.(k8stesting.ListActionImpl)
		o, err := cl.Tracker().List(la.Resource, la.Kind, la.Namespace)
		if err != nil {
			return true, nil, err
		}
		l := o.(*unstructured.UnstructuredList)
		slices.SortFunc(l.Items, func(a, b unstructured.Unstructured) int { return strings.Compare(a.GetName(), b.GetName()) })
		from, _ := strconv.Atoi(la.ListOptions.Continue)
		to := len(l.Items)
		if n := int(la.ListOptions.Limit); n > 0 && from+n < to {
			to = from + n
			l.SetContinue(strconv.Itoa(to))
		}
		*limits = append(*limits, la.ListOptions.Limit)
		l.Items = l.Items[from:to]
		return true, l, nil
	})
	return limits
}

// failing makes every list of resource fail with err.
func failing(cl *dynamicfake.FakeDynamicClient, resource string, err error) *dynamicfake.FakeDynamicClient {
	cl.PrependReactor("list", resource, func(k8stesting.Action) (bool, runtime.Object, error) { return true, nil, err })
	return cl
}

// coverage is the row of source in b.
func coverage(t *testing.T, b *Bulk, source string) contract.InferredCoverage {
	t.Helper()
	i := slices.IndexFunc(b.Coverage, func(c contract.InferredCoverage) bool { return c.Source == source })
	if i < 0 {
		t.Fatalf("no coverage row for %s: %+v", source, b.Coverage)
	}
	return b.Coverage[i]
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

// checkCheckoutDraft checks the checkout draft: payments with compatibility
// unknown and a range proposed from the version its Pacto reports, and its
// manifests with no cluster-only fields.
func checkCheckoutDraft(t *testing.T, checkout *Result, out string) {
	t.Helper()
	text := file(t, checkout, "pacto.yaml").Content
	proposed := "dependencies:\n  # inferred from runtime calls (calls seen to the Service shop/payments)\n" +
		"  - name: payments\n    ref: oci://reg.io/acme/contracts/payments\n    required: true\n" +
		"    compatibility: unknown   # proposed ^2.3.0, from the Pactos describing the Service shop/payments at 2.3.0\n"
	if !strings.Contains(text, proposed) || strings.Contains(text, "'*'") || strings.Contains(text, `"*"`) || !strings.Contains(text, "  # from calls seen to the Service shop/fraud\n  # no contract for fraud was read") ||
		!strings.Contains(text, "  - name: fraud\n    ref: oci://reg.io/acme/contracts/fraud\n    required: true\n    compatibility: unknown   # no range proposed: no contract or running version of fraud was read\n") {
		t.Fatalf("checkout's dependencies:\n%s", text)
	}
	if checkout.Dir != filepath.Join(out, "shop", "checkout") || file(t, checkout, "manifests.yaml").Action != Add ||
		!hasLine(checkout.Next, "declare the versions of payments that checkout accepts: replace compatibility: unknown with a range, such as the proposed ^2.3.0") ||
		!hasLine(checkout.Found, "checkout calls the Service shop/payments, described by payments 2.3.0") {
		t.Fatalf("checkout: %+v", checkout)
	}
	m := file(t, checkout, "manifests.yaml").Content
	if !strings.Contains(m, "kind: Deployment") || !strings.Contains(m, "kind: Service") || strings.Contains(m, "clusterIP") || strings.Contains(m, "namespace") {
		t.Fatalf("manifests.yaml:\n%s", m)
	}
}

// TestClusterDrafts: every workload no Pacto describes gets a draft, its
// dependencies read from the calls its pods made, each with compatibility
// unknown. A call to a provider whose Pacto reports a version gets a range
// proposed from it in a comment, never "*"; one with no contract version
// gets a comment saying no range is proposed. A plan writes nothing.
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
	checkCheckoutDraft(t, result(t, b, "checkout"), out)
	// ledger calls payments and legacy, whose Pacto names no contract yet: no range proposed.
	if l := file(t, result(t, b, "ledger"), "pacto.yaml").Content; !strings.Contains(l, "compatibility: unknown   # proposed ^2.3.0, from the Pactos describing the Service shop/payments at 2.3.0\n") ||
		!strings.Contains(l, "  - name: legacy\n    ref: oci://reg.io/acme/contracts/legacy\n    required: true\n    compatibility: unknown   # no range proposed: no reconciled Pacto describing the Service shop/legacy reports a contract version\n") ||
		!hasLine(result(t, b, "ledger").Unknown, "the version of shop/legacy that Pacto shop/ghost describes: its status and spec name no usable contract ref, so no range is proposed from it") {
		t.Fatalf("ledger:\n%s", l)
	}
	if n := result(t, b, "nightly"); strings.Contains(file(t, n, "pacto.yaml").Content, "dependencies") || len(n.Dependencies) != 0 {
		t.Fatalf("nightly calls nothing: %+v", n)
	}

}

// TestClusterDraftsApply: applying writes the drafts; every one is valid,
// and a second run keeps what is there.
func TestClusterDraftsApply(t *testing.T) {
	now := time.Now()
	out := filepath.Join(t.TempDir(), "drafts")
	c := ClusterOptions{Out: out, Now: func() time.Time { return now }}
	b, err := Cluster(context.Background(), fakeCluster(shopCluster(now)...), Options{}, c, os.WriteFile)
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
	checkProposedReadsUnknown(t, out)
}

// checkProposedReadsUnknown: the draft consumer leaves its range to payments
// unknown, with only a proposal in a comment, so payments 2.4.0 reads
// unknown for it: no guessed range decides who the change breaks.
func checkProposedReadsUnknown(t *testing.T, out string) {
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
	if i < 0 || res.Consumers[i].CompatibilityVerdict != impact.CompatibilityUnknown || res.Consumers[i].Compatibility != contract.CompatibilityUnknown {
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

// TestClusterStaleObservations: a node whose observation is stale, that
// has none or whose collection failed cannot rule a call out, nor can a
// fresh one that has not mapped the pod or shares it with the host, so each
// draft with such a pod says so; a pod mapped on a node observed now adds no
// such line.
func TestClusterStaleObservations(t *testing.T) {
	now := time.Now()
	pod := func(name, node string) runtime.Object {
		return controlled(obj("v1", "Pod", "shop", name+"-0", map[string]any{"spec": map[string]any{"nodeName": node},
			"status": map[string]any{"startTime": now.Add(-time.Hour).UTC().Format(time.RFC3339)}}), "StatefulSet", name, "u-"+name)
	}
	nro := func(node string, published time.Time, state string, pods ...any) runtime.Object {
		return obj("pacto.trianalab.io/v1alpha1", "NodeRuntimeObservation", "pacto-system", node, map[string]any{"status": map[string]any{
			"nodeName": node, "publishIntervalSeconds": int64(30), "lastPublished": published.UTC().Format(time.RFC3339),
			"collection": map[string]any{"state": state}, "pods": pods}})
	}
	set := func(name string) runtime.Object {
		return obj("apps/v1", "StatefulSet", "shop", name, map[string]any{"spec": map[string]any{"template": map[string]any{
			"spec": map[string]any{"containers": []any{map[string]any{"name": name, "image": "reg.io/acme/" + name + ":1.0.0"}}}}}})
	}
	fresh := now.Add(-10 * time.Second)
	cl := fakeCluster(set("checkout"), set("fraud"), set("ledger"), set("audit"), set("cart"), set("edge"),
		pod("checkout", "node-1"), pod("fraud", "node-2"), pod("ledger", "node-3"), pod("audit", "node-4"), pod("cart", "node-1"), pod("edge", "node-1"),
		obj("v1", "Pod", "shop", "pending", nil),
		nro("node-1", fresh, "Running", map[string]any{"uid": "u-checkout"}, map[string]any{"uid": "u-edge", "netnsShared": true}),
		nro("node-2", now.Add(-3*time.Hour), "Running"), nro("node-4", fresh, "Failed"))
	b, err := Cluster(context.Background(), cl, Options{Plan: true}, ClusterOptions{Out: t.TempDir(), Now: func() time.Time { return now }}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"checkout": "",
		"fraud":    "node node-2 has no current observation",
		"ledger":   "node node-3 has no current observation",
		"audit":    "node node-4 failed to collect",
		"cart":     "pod cart-0 on node node-1 is not mapped in its observation",
		"edge":     "pod edge-0 on node node-1 is not mapped in its observation or shares the host network",
	} {
		u := result(t, b, name).Unknown
		if want == "" && hasLine(u, "could not be seen") || want != "" && !hasLine(u, want) {
			t.Errorf("%s: %q", name, u)
		}
	}
}

// TestClusterCrossNamespaceCall: reading one namespace still maps a call to
// a Service in another, so the dependency is drafted, not dropped.
func TestClusterCrossNamespaceCall(t *testing.T) {
	now := time.Now()
	objs := []runtime.Object{
		deployment("demo", "checkout", "reg.io/acme/checkout:1.4.0"),
		controlled(obj("apps/v1", "ReplicaSet", "demo", "checkout-7f", nil), "Deployment", "checkout", ""),
		controlled(obj("v1", "Pod", "demo", "checkout-7f-a", map[string]any{"metadata": map[string]any{"labels": map[string]any{"app": "checkout"}}}), "ReplicaSet", "checkout-7f", "u-checkout"),
		service("shop", "payments", "10.0.0.20"),
		obj("pacto.trianalab.io/v1alpha1", "NodeRuntimeObservation", "pacto-system", "node-1", map[string]any{"status": map[string]any{
			"nodeName": "node-1", "pods": []any{map[string]any{"uid": "u-checkout", "connections": []any{
				map[string]any{"address": "10.0.0.20", "port": int64(80), "successes": int64(1), "lastSuccessAt": now.UTC().Format(time.RFC3339)}}}}}}),
	}
	c := ClusterOptions{Namespaces: []string{"demo"}, Out: t.TempDir(), Now: func() time.Time { return now }}
	b, err := Cluster(context.Background(), fakeCluster(objs...), Options{Plan: true}, c, nil)
	if err != nil || len(b.Results) != 1 || !slices.ContainsFunc(b.Results[0].Dependencies, func(d Finding) bool { return d.Where == "calls seen to the Service shop/payments" }) {
		t.Fatalf("cross-namespace call: %+v %v", b, err)
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
		{Options{Plan: true, InsecureRegistry: true}, c, "shop/checkout: an insecure registry"},
	} {
		if _, err := Cluster(ctx, fakeCluster(shopCluster(now)...), tc.opts, tc.c, nil); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: %v", tc.opts, err)
		}
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

// env is an env list of literal name=value pairs.
func env(pairs ...string) []any {
	out := []any{}
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, map[string]any{"name": pairs[i], "value": pairs[i+1]})
	}
	return out
}

// secretShop is checkout, whose container and initContainer set sensitive
// literals, and nightly, a CronJob that sets one.
func secretShop() []runtime.Object {
	appEnv := append(env("DB_PASSWORD", "hunter2", "REDIS_URL", "redis://:r3dis-pw@cache:6379", "LOG_LEVEL", "info"),
		map[string]any{"name": "CACHE_DSN", "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": "cache", "key": "dsn"}}})
	checkout := obj("apps/v1", "Deployment", "shop", "checkout", map[string]any{"spec": map[string]any{"template": map[string]any{
		"metadata": map[string]any{"labels": map[string]any{"app": "checkout"}},
		"spec": map[string]any{
			"containers":     []any{map[string]any{"name": "app", "image": "reg.io/acme/checkout:1.4.0", "env": appEnv}},
			"initContainers": []any{map[string]any{"name": "migrate", "image": "reg.io/acme/migrate:1.0.0", "env": env("MIGRATE_TOKEN", "t0ken-123")}},
		}}}})
	nightly := obj("batch/v1", "CronJob", "shop", "nightly", map[string]any{"spec": map[string]any{"jobTemplate": map[string]any{"spec": map[string]any{"template": map[string]any{
		"metadata": map[string]any{"labels": map[string]any{"app": "nightly"}},
		"spec":     map[string]any{"containers": []any{map[string]any{"name": "nightly", "image": "reg.io/acme/nightly:1.0.0", "env": env("BATCH_PASSWORD", "cr0n-s3cret")}}}}}}}})
	return []runtime.Object{checkout, nightly}
}

var secretLiterals = []string{"hunter2", "r3dis-pw", "t0ken-123", "cr0n-s3cret"}

// secretRef matches a secretKeyRef to key in secret at any indentation.
func secretRef(secret, key string) *regexp.Regexp {
	return regexp.MustCompile(`secretKeyRef:\s+key: ` + regexp.QuoteMeta(key) + `\s+name: ` + regexp.QuoteMeta(secret) + `\n`)
}

// checkSecretSchema checks that a draft's schema reads each key from secret.
func checkSecretSchema(t *testing.T, r *Result, secret string, keys ...string) {
	t.Helper()
	var s struct {
		Properties map[string]map[string]any `json:"properties"`
	}
	if err := json.Unmarshal([]byte(file(t, r, configSchema).Content), &s); err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		p := s.Properties[k]
		if p["writeOnly"] != true || !strings.Contains(fmt.Sprint(p["description"]), "Read from the Secret "+secret) {
			t.Fatalf("%s in %s: %+v", k, r.Service, s.Properties)
		}
	}
}

// checkSecretDrafts checks the checkout and nightly drafts of secretShop:
// secretKeyRefs in place of the literals, none of which appears anywhere, the
// Secret shape in the schema and a next step with no value on it.
func checkSecretDrafts(t *testing.T, b *Bulk, checkout, nightly *Result) {
	t.Helper()
	m := file(t, checkout, "manifests.yaml").Content + file(t, nightly, "manifests.yaml").Content
	for _, want := range []*regexp.Regexp{secretRef("checkout-env", "DB_PASSWORD"), secretRef("checkout-env", "REDIS_URL"), secretRef("checkout-env", "MIGRATE_TOKEN"),
		secretRef("nightly-env", "BATCH_PASSWORD"), regexp.MustCompile(`name: LOG_LEVEL\s+value: info\n`), secretRef("cache", "dsn")} {
		if !want.MatchString(m) {
			t.Fatalf("manifests.yaml lacks %q:\n%s", want, m)
		}
	}
	data, _ := json.Marshal(b)
	for _, lit := range secretLiterals {
		if strings.Contains(m, lit) || strings.Contains(string(data), lit) {
			t.Fatalf("%s leaked:\n%s", lit, m)
		}
	}
	checkSecretSchema(t, checkout, "checkout-env", "DB_PASSWORD", "REDIS_URL")
	checkSecretSchema(t, nightly, "nightly-env", "BATCH_PASSWORD")
	if !hasLine(checkout.Next, "manifests.yaml: the cluster sets DB_PASSWORD, MIGRATE_TOKEN, REDIS_URL to literal values") ||
		!hasLine(checkout.Next, "kubectl -n shop create secret generic checkout-env --from-env-file=") || hasLine(checkout.Next, "--from-literal") ||
		!hasLine(nightly.Next, "kubectl -n shop create secret generic nightly-env --from-env-file=") {
		t.Fatalf("next steps: %q %q", checkout.Next, nightly.Next)
	}
	if !hasLine(checkout.Next, "manifests.yaml: REDIS_URL (cache) held a URL") || hasLine(checkout.Next, "DB_PASSWORD held") || hasLine(nightly.Next, "held a URL") {
		t.Fatalf("URL next steps: %q %q", checkout.Next, nightly.Next)
	}
}

// TestClusterDraftReadsSensitiveLiteralsFromSecret: a sensitive literal in a
// container or initContainer, by name or by URL credentials, never reaches the
// manifests.yaml a draft writes: the draft reads it from <workload>-env and
// says how to create that Secret, with no value on the command line. The live
// object is untouched.
func TestClusterDraftReadsSensitiveLiteralsFromSecret(t *testing.T) {
	for _, plan := range []bool{true, false} {
		objs := secretShop()
		out := filepath.Join(t.TempDir(), "drafts")
		b, err := Cluster(context.Background(), fakeCluster(objs...), Options{Plan: plan}, ClusterOptions{Out: out}, os.WriteFile)
		if err != nil {
			t.Fatal(err)
		}
		checkout, nightly := result(t, b, "checkout"), result(t, b, "nightly")
		checkSecretDrafts(t, b, checkout, nightly)
		live := objs[0].(*unstructured.Unstructured)
		if v, _, _ := unstructured.NestedSlice(live.Object, "spec", "template", "spec", "containers"); !strings.Contains(fmt.Sprint(v), "hunter2") {
			t.Fatalf("the live object changed: %v", v)
		}
		if !plan {
			disk, err := os.ReadFile(filepath.Join(out, "shop", "checkout", "manifests.yaml"))
			if err != nil || strings.Contains(string(disk), "hunter2") || !strings.Contains(string(disk), "checkout-env") {
				t.Fatalf("manifests.yaml on disk: %s %v", disk, err)
			}
		}
	}
}

// TestClusterDraftSplitsDifferingLiterals: one name set to different literals
// in two containers reads two Secret keys, so they never share one value; the
// same literal keeps one key.
func TestClusterDraftSplitsDifferingLiterals(t *testing.T) {
	workload := func(migrate string) runtime.Object {
		return obj("apps/v1", "Deployment", "shop", "checkout", map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
			"containers":     []any{map[string]any{"name": "app", "image": "reg.io/acme/checkout:1.4.0", "env": env("DB_PASSWORD", "app-pw")}},
			"initContainers": []any{map[string]any{"name": "migrate", "image": "reg.io/acme/migrate:1.0.0", "env": env("DB_PASSWORD", migrate)}},
		}}}})
	}
	b, err := Cluster(context.Background(), fakeCluster(workload("migrate-pw")), Options{Plan: true}, ClusterOptions{Out: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := result(t, b, "checkout")
	m := file(t, r, "manifests.yaml").Content
	if !strings.Contains(m, "key: app.DB_PASSWORD") || !strings.Contains(m, "key: migrate.DB_PASSWORD") || strings.Contains(m, "app-pw") || strings.Contains(m, "migrate-pw") ||
		!hasLine(r.Next, "sets app.DB_PASSWORD, migrate.DB_PASSWORD to literal values") {
		t.Fatalf("differing literals:\n%s\n%q", m, r.Next)
	}
	b, err = Cluster(context.Background(), fakeCluster(workload("app-pw")), Options{Plan: true}, ClusterOptions{Out: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r = result(t, b, "checkout")
	if m := file(t, r, "manifests.yaml").Content; strings.Count(m, "key: DB_PASSWORD") != 2 || strings.Contains(m, ".DB_PASSWORD") || !hasLine(r.Next, "sets DB_PASSWORD to a literal value; the draft reads it from") {
		t.Fatalf("equal literals:\n%s\n%q", m, r.Next)
	}
}

// TestClusterDraftKeptManifestsNoSecretNote: a manifests.yaml already in the
// draft folder is the user's, so the draft says nothing about its Secret.
func TestClusterDraftKeptManifestsNoSecretNote(t *testing.T) {
	out := t.TempDir()
	writeTree(t, out, map[string]string{"shop/checkout/manifests.yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: checkout}\n"})
	b, err := Cluster(context.Background(), fakeCluster(secretShop()...), Options{Plan: true}, ClusterOptions{Out: out}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r := result(t, b, "checkout"); file(t, r, "manifests.yaml").Action != Keep || hasLine(r.Next, "manifests.yaml: the cluster sets") {
		t.Fatalf("kept manifests: %+v", r)
	}
}

// TestSecretEnvNoPodSpec: an object with no containers reads no Secret.
func TestSecretEnvNoPodSpec(t *testing.T) {
	o := obj("apps/v1", "Deployment", "shop", "empty", map[string]any{"spec": map[string]any{}})
	before := fmt.Sprint(o.Object)
	if keys, urls := secretEnv(o, "empty-env", nil); keys != nil || urls != nil || fmt.Sprint(o.Object) != before {
		t.Fatalf("keys %v, object %v", keys, o.Object)
	}
}

// TestClusterDraftURLLiterals: a $(NAME) reference is no literal, so a URL
// whose password is one stays in the draft; a malformed credential URL is
// rewritten; only a rewritten URL the scan reads a dependency from is named
// as a lost dependency, never an outside host.
func TestClusterDraftURLLiterals(t *testing.T) {
	appEnv := append(env("DATABASE_URL", "postgres://app:$(DB_PASSWORD)@db:5432/x", "ORDERS_URL", "postgres://app:pa/ss@orders-db:5432/orders",
		"DB_DSN", "postgres://app:p%zz@ledger:5432/x", "STRIPE_URL", "https://api.stripe.com/v1?api_key=sk_live_x", "ALIAS_PASSWORD", "$(OTHER_PASSWORD)"),
		map[string]any{"name": "DB_PASSWORD", "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": "db", "key": "password"}}})
	checkout := obj("apps/v1", "Deployment", "shop", "checkout", map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
		"containers":     []any{map[string]any{"name": "app", "image": "reg.io/acme/checkout:1.4.0", "env": appEnv}},
		"initContainers": []any{map[string]any{"name": "migrate", "image": "reg.io/acme/migrate:1.0.0", "env": env("MIGRATE_DB_URL", "postgres://u:p@db/x")}},
	}}}})
	b, err := Cluster(context.Background(), fakeCluster(checkout), Options{Plan: true}, ClusterOptions{Out: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := result(t, b, "checkout")
	m := file(t, r, "manifests.yaml").Content
	for _, want := range []*regexp.Regexp{regexp.MustCompile(`value: postgres://app:\$\(DB_PASSWORD\)@db:5432/x\n`), regexp.MustCompile(`value: \$\(OTHER_PASSWORD\)\n`),
		secretRef("checkout-env", "ORDERS_URL"), secretRef("checkout-env", "DB_DSN"), secretRef("checkout-env", "STRIPE_URL"), secretRef("checkout-env", "MIGRATE_DB_URL")} {
		if !want.MatchString(m) {
			t.Fatalf("manifests.yaml lacks %q:\n%s", want, m)
		}
	}
	for _, lit := range []string{"pa/ss", "p%zz", "sk_live_x", "u:p@"} {
		if strings.Contains(m, lit) {
			t.Fatalf("%s leaked:\n%s", lit, m)
		}
	}
	// ORDERS_URL's / in the password is one the scan's host reader never got past.
	// MIGRATE_DB_URL names db, which DATABASE_URL still lists as a dependency.
	if !hasLine(r.Next, "manifests.yaml: DB_DSN (ledger) held a URL") || hasLine(r.Next, "MIGRATE_DB_URL (") || hasLine(r.Next, "ORDERS_URL (") || hasLine(r.Next, "STRIPE_URL (") ||
		hasLine(r.Next, "DATABASE_URL") {
		t.Fatalf("next steps: %q", r.Next)
	}
	if !hasDependency(r, "db") {
		t.Fatalf("db is not a dependency: %+v", r.Dependencies)
	}
}

// pactoCR is a Pacto in ns describing the Service svc with the contract
// name at ref and version, at metadata.generation gen and
// status.observedGeneration observed.
func pactoCR(ns, name, svc, contractName, ref, version string, gen, observed int64) *unstructured.Unstructured {
	return obj("pacto.trianalab.io/v1alpha1", "Pacto", ns, name, map[string]any{
		"metadata": map[string]any{"generation": gen},
		"spec":     map[string]any{"contractRef": map[string]any{"oci": ref + ":" + version}, "target": map[string]any{"serviceName": svc}},
		"status": map[string]any{"observedGeneration": observed,
			"contract": map[string]any{"serviceName": contractName, "version": version, "resolvedRef": ref + "@sha256:aa"}}})
}

const paymentsRef = "oci://reg.io/acme/contracts/payments"

// TestClusterProviderLowestVersion: the dependency a call to payments is on
// carries every version the reconciled Pactos describing it report,
// ascending and distinct, whatever order they are listed in.
func TestClusterProviderLowestVersion(t *testing.T) {
	p := func(name, version string) *unstructured.Unstructured {
		return pactoCR("shop", name, "payments", "payments", paymentsRef, version, 0, 0)
	}
	stale := pactoCR("shop", "payments-next", "payments", "payments", paymentsRef, "2.4.0", 3, 2)
	other := pactoCR("other", "payments", "payments", "payments", paymentsRef, "1.0.0", 0, 0)
	for _, tt := range []struct {
		name     string
		pactos   []*unstructured.Unstructured
		versions []string
		unknown  []string
	}{
		{"one", []*unstructured.Unstructured{p("a", "2.3.1")}, []string{"2.3.1"}, nil},
		{"higher first", []*unstructured.Unstructured{p("a", "2.4.0"), p("b", "2.3.1")}, []string{"2.3.1", "2.4.0"}, nil},
		{"lower first", []*unstructured.Unstructured{p("a", "2.3.1"), p("b", "2.4.0")}, []string{"2.3.1", "2.4.0"}, nil},
		{"same version", []*unstructured.Unstructured{p("a", "2.3.1"), p("b", "2.3.1")}, []string{"2.3.1"}, nil},
		{"non-strict ignored", []*unstructured.Unstructured{p("a", "v2.2"), p("b", "2.3.1")}, []string{"2.3.1"},
			[]string{"the version of shop/payments that Pacto shop/a describes: its status reports no contract version, so no range is proposed from it"}},
		{"other namespace ignored", []*unstructured.Unstructured{other, p("b", "2.3.1")}, []string{"2.3.1"}, nil},
		{"stale generation ignored", []*unstructured.Unstructured{stale, p("b", "2.4.1")}, []string{"2.4.1"},
			[]string{"the version of shop/payments that Pacto shop/payments-next describes: its status is not reconciled with its spec, so no range is proposed from it"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cl := &cluster{}
			for _, o := range tt.pactos {
				cl.pactos = append(cl.pactos, *o)
			}
			d := cl.provider(*service("shop", "payments", "10.0.0.20"))
			if !slices.Equal(d.Versions, tt.versions) || d.Ref != paymentsRef || d.Name != "payments" || !slices.Equal(d.Unknown, tt.unknown) {
				t.Fatalf("provider: %+v", d)
			}
		})
	}
}

// TestProviderConflictingContractsProposeNothing: two reconciled Pactos
// describing one Service name different contracts, so no versions are kept,
// the identity is the first by ref and one note names both.
func TestProviderConflictingContractsProposeNothing(t *testing.T) {
	cl := &cluster{pactos: []unstructured.Unstructured{
		*pactoCR("shop", "payments", "payments", "payments", paymentsRef, "2.4.0", 1, 1),
		*pactoCR("shop", "legacy", "payments", "payments-old", paymentsRef+"-old", "2.3.1", 1, 1),
	}}
	d := cl.provider(*service("shop", "payments", "10.0.0.20"))
	want := "the version of shop/payments to propose a range from: the Pactos describing it name different contracts (" +
		"payments at oci://reg.io/acme/contracts/payments in Pacto shop/payments and payments-old at oci://reg.io/acme/contracts/payments-old in Pacto shop/legacy), so no range is proposed"
	if len(d.Versions) != 0 || d.Ref != paymentsRef || d.Name != "payments" || !slices.Equal(d.Unknown, []string{want}) {
		t.Fatalf("provider: %+v", d)
	}
}

// twoPaymentServices is checkout calling the Services payments and
// payments-canary, each described by a Pacto naming the contract payments,
// at canaryRef for the canary. Pod u-a calls both; u-b calls payments, or
// both when bothPods.
func twoPaymentServices(now time.Time, canaryRef string, bothPods bool) []runtime.Object {
	call := func(ip string) map[string]any {
		return map[string]any{"address": ip, "port": int64(80), "successes": int64(3), "lastSuccessAt": now.UTC().Format(time.RFC3339)}
	}
	callsB := []any{call("10.0.0.20")}
	if bothPods {
		callsB = append(callsB, call("10.0.0.21"))
	}
	return []runtime.Object{
		deployment("shop", "checkout", "reg.io/acme/checkout:1.4.0"),
		service("shop", "payments", "10.0.0.20"), service("shop", "payments-canary", "10.0.0.21"),
		controlled(obj("apps/v1", "ReplicaSet", "shop", "checkout-7f", nil), "Deployment", "checkout", ""),
		controlled(obj("v1", "Pod", "shop", "checkout-7f-a", map[string]any{"metadata": map[string]any{"labels": map[string]any{"app": "checkout"}}}), "ReplicaSet", "checkout-7f", "u-a"),
		controlled(obj("v1", "Pod", "shop", "checkout-7f-b", map[string]any{"metadata": map[string]any{"labels": map[string]any{"app": "checkout"}}}), "ReplicaSet", "checkout-7f", "u-b"),
		pactoCR("shop", "payments", "payments", "payments", paymentsRef, "2.4.0", 1, 1),
		pactoCR("shop", "payments-canary", "payments-canary", "payments", canaryRef, "2.3.1", 1, 1),
		obj("pacto.trianalab.io/v1alpha1", "NodeRuntimeObservation", "pacto-system", "node-1", map[string]any{"status": map[string]any{
			"nodeName": "node-1",
			"pods": []any{
				map[string]any{"uid": "u-a", "connections": []any{call("10.0.0.20"), call("10.0.0.21")}},
				map[string]any{"uid": "u-b", "connections": callsB},
			}}}),
	}
}

// checkoutInferred is checkout's one dependency, the same whether one or
// both pods call both Services.
func checkoutInferred(t *testing.T, canaryRef string) Inferred {
	t.Helper()
	one := checkoutInferredPods(t, canaryRef, false)
	if both := checkoutInferredPods(t, canaryRef, true); !reflect.DeepEqual(one, both) {
		t.Fatalf("both pods calling both Services: %+v, want %+v", both, one)
	}
	return one
}

func checkoutInferredPods(t *testing.T, canaryRef string, bothPods bool) Inferred {
	t.Helper()
	now := time.Now()
	cl, err := readCluster(context.Background(), fakeCluster(twoPaymentServices(now, canaryRef, bothPods)...),
		ClusterOptions{Now: func() time.Time { return now }, MaxItems: DefaultMaxItems}, []string{"shop"}, cursor{})
	if err != nil {
		t.Fatal(err)
	}
	deps := cl.inferred(now.Add(-time.Hour))["shop/Deployment/checkout"]
	if len(deps) != 1 || deps[0].Where != "calls seen to the Services shop/payments and shop/payments-canary" {
		t.Fatalf("checkout's dependencies: %+v", deps)
	}
	return deps[0]
}

// TestInferredMergesVersionsAcrossServices: two Services whose Pactos name
// one contract are one dependency with the versions of both.
func TestInferredMergesVersionsAcrossServices(t *testing.T) {
	d := checkoutInferred(t, paymentsRef)
	if !slices.Equal(d.Versions, []string{"2.3.1", "2.4.0"}) || d.Ref != paymentsRef || len(d.Unknown) != 0 {
		t.Fatalf("merged: %+v", d)
	}
}

// TestInferredMergeConflictingRefsProposeNothing: the same, with the canary
// naming another ref: no versions, the lower ref and a note naming both.
func TestInferredMergeConflictingRefsProposeNothing(t *testing.T) {
	d := checkoutInferred(t, "oci://reg.io/acme/contracts/canary")
	if len(d.Versions) != 0 || d.Ref != "oci://reg.io/acme/contracts/canary" || len(d.Unknown) != 1 ||
		!strings.Contains(d.Unknown[0], "the Services shop/payments and shop/payments-canary name different contracts (oci://reg.io/acme/contracts/canary and oci://reg.io/acme/contracts/payments)") {
		t.Fatalf("merged: %+v", d)
	}
}

// mergeAll merges ds as inferred does, then settles the result.
func mergeAll(ds ...Inferred) []Inferred {
	var list []Inferred
	for _, d := range ds {
		list = mergeInferred(list, d)
	}
	for i := range list {
		finishInferred(&list[i])
	}
	return list
}

// described is a Service shop/<svc> whose Pactos name ref at versions.
func described(svc, ref string, versions ...string) Inferred {
	d := Inferred{Name: "payments", services: []string{"shop/" + svc}, Ref: ref, refs: []string{ref}, withRef: []string{"shop/" + svc}, Versions: versions}
	if len(versions) > 0 {
		d.described = d.services
	}
	return d
}

// TestMergeInferredKeepsConflict: once the Pactos disagree, a later
// agreeing Service adds no versions, a repeated note is kept once and a new
// one is added.
func TestMergeInferredKeepsConflict(t *testing.T) {
	note := "conflict"
	a := described("a", paymentsRef)
	a.Unknown, a.conflict = []string{note}, true
	b := described("b", paymentsRef, "2.3.1")
	b.Unknown = []string{note}
	c := Inferred{Name: "payments", services: []string{"shop/c"}, Unknown: []string{"other"}}
	list := mergeAll(a, b, c)
	if len(list) != 1 || len(list[0].Versions) != 0 || !slices.Equal(list[0].Unknown, []string{note, "other"}) || list[0].Ref != paymentsRef ||
		list[0].Where != "calls seen to the Services shop/a, shop/b and shop/c" {
		t.Fatalf("merged: %+v", list)
	}
}

// TestMergeUndescribedServiceNamesOnlyDescribed: a Service no Pacto
// describes merged with one described at 2.0.0 keeps the version, credits
// it only to the described Service and says the other has no Pacto.
func TestMergeUndescribedServiceNamesOnlyDescribed(t *testing.T) {
	other := Inferred{Name: "payments", services: []string{"other/payments"}, Where: "calls seen to the Service other/payments"}
	for _, order := range [][]Inferred{{other, described("payments", paymentsRef, "2.0.0")}, {described("payments", paymentsRef, "2.0.0"), other}} {
		d := mergeAll(order...)[0]
		want := "the version of other/payments: no reconciled Pacto describing it names a contract, so the proposal comes only from the Pactos describing the Service shop/payments"
		if d.Ref != paymentsRef || !slices.Equal(d.Versions, []string{"2.0.0"}) || !slices.Equal(d.described, []string{"shop/payments"}) || !slices.Equal(d.Unknown, []string{want}) {
			t.Fatalf("merged: %+v", d)
		}
	}
}

// TestMergeConflictNotedOnce: a third Service naming a ref already seen adds
// no second conflict note; the one note names every Service and ref.
func TestMergeConflictNotedOnce(t *testing.T) {
	x, y := paymentsRef, "oci://reg.io/acme/contracts/canary"
	d := mergeAll(described("a", x, "2.0.0"), described("b", y, "2.1.0"), described("c", x, "2.2.0"))[0]
	want := "the version of payments to propose a range from: the Services shop/a, shop/b and shop/c name different contracts (" + y + " and " + x + "), so no range is proposed"
	if len(d.Versions) != 0 || d.Ref != y || !slices.Equal(d.Unknown, []string{want}) {
		t.Fatalf("merged: %+v", d)
	}
}

// TestProviderUnreadPactoJoinsAgreement: a reconciled Pacto whose contract
// did not load still names its spec ref, so a different ref is a conflict;
// one with no usable ref adds a note.
func TestProviderUnreadPactoJoinsAgreement(t *testing.T) {
	failed := func(name, ref string) unstructured.Unstructured {
		return *obj("pacto.trianalab.io/v1alpha1", "Pacto", "shop", name, map[string]any{
			"metadata": map[string]any{"generation": int64(1)},
			"spec":     map[string]any{"contractRef": map[string]any{"oci": ref}, "target": map[string]any{"serviceName": "payments"}},
			"status":   map[string]any{"observedGeneration": int64(1)}})
	}
	cl := &cluster{pactos: []unstructured.Unstructured{*pactoCR("shop", "payments", "payments", "payments", paymentsRef, "2.4.0", 1, 1), failed("payments-v3", "reg.io/acme/contracts/payments-v3:3.0.0")}}
	d := cl.provider(*service("shop", "payments", "10.0.0.20"))
	if len(d.Versions) != 0 || d.Ref != paymentsRef || !d.conflict || len(d.Unknown) != 2 ||
		d.Unknown[0] != "the version of shop/payments that Pacto shop/payments-v3 describes: its status reports no contract version, so no range is proposed from it" ||
		!strings.Contains(d.Unknown[1], "an unnamed contract at oci://reg.io/acme/contracts/payments-v3 in Pacto shop/payments-v3") {
		t.Fatalf("provider: %+v", d)
	}
	cl.pactos[1] = failed("payments-v3", "")
	d = cl.provider(*service("shop", "payments", "10.0.0.20"))
	if !slices.Equal(d.Versions, []string{"2.4.0"}) || !slices.Equal(d.Unknown, []string{"the version of shop/payments that Pacto shop/payments-v3 describes: its status and spec name no usable contract ref, so no range is proposed from it"}) {
		t.Fatalf("provider: %+v", d)
	}
}

// manyPods are n pods of checkout in shop.
func manyPods(n int) []runtime.Object {
	out := []runtime.Object{deployment("shop", "checkout", "reg.io/acme/checkout:1.4.0")}
	for i := range n {
		out = append(out, obj("v1", "Pod", "shop", fmt.Sprintf("checkout-%04d", i), nil))
	}
	return out
}

// TestReadClusterPagesWithLimitAndContinue: a source longer than a page is
// read page by page, each asking for at most 500 and no more than the bound
// leaves, until every item is read.
func TestReadClusterPagesWithLimitAndContinue(t *testing.T) {
	cl := fakeCluster(manyPods(1200)...)
	limits := paged(cl, "pods")
	b, err := Cluster(context.Background(), cl, Options{Plan: true}, ClusterOptions{Namespaces: []string{"shop"}, Out: t.TempDir(), MaxItems: 1200}, nil)
	if err != nil || !slices.Equal(*limits, []int64{500, 500, 200}) || coverage(t, b, "pods").State != contract.CoverageComplete {
		t.Fatalf("limits %v, coverage %+v: %v", *limits, b.Coverage, err)
	}
	read, err := readCluster(context.Background(), cl, ClusterOptions{Now: time.Now, MaxItems: 1200}, []string{"shop"}, cursor{})
	if err != nil || len(read.pods) != 1200 {
		t.Fatalf("read %d pods: %v", len(read.pods), err)
	}
}

// TestReadClusterBoundStopsSourcePartial: a source with more items than the
// bound keeps the bound's worth and reads partial, saying why.
func TestReadClusterBoundStopsSourcePartial(t *testing.T) {
	cl := fakeCluster(manyPods(400)...)
	paged(cl, "pods")
	b, err := Cluster(context.Background(), cl, Options{Plan: true}, ClusterOptions{Namespaces: []string{"shop"}, Out: t.TempDir(), MaxItems: 300}, nil)
	want := contract.InferredCoverage{Source: "pods", State: contract.CoveragePartial, Reason: "stopped at the item bound (300)"}
	if err != nil || coverage(t, b, "pods") != want || coverage(t, b, "workloads").State != contract.CoverageComplete {
		t.Fatalf("coverage %+v: %v", b.Coverage, err)
	}
}

// TestReadClusterServerIgnoringLimitSkipsNothing: a server that sends more
// than Limit asks for has the whole page kept, so the token resumes after
// what was read and no workload is skipped.
func TestReadClusterServerIgnoringLimitSkipsNothing(t *testing.T) {
	cl := fakeCluster(deployment("a", "a1", "x:1"), deployment("a", "a2", "x:1"), deployment("a", "a3", "x:1"))
	cl.PrependReactor("list", "deployments", func(a k8stesting.Action) (bool, runtime.Object, error) {
		la := a.(k8stesting.ListActionImpl)
		o, _ := cl.Tracker().List(la.Resource, la.Kind, la.Namespace)
		l := o.(*unstructured.UnstructuredList)
		slices.SortFunc(l.Items, func(a, b unstructured.Unstructured) int { return strings.Compare(a.GetName(), b.GetName()) })
		from, _ := strconv.Atoi(la.ListOptions.Continue)
		to := min(from+2, len(l.Items)) // two a page, whatever Limit says
		if to < len(l.Items) {
			l.SetContinue(strconv.Itoa(to))
		}
		l.Items = l.Items[from:to]
		return true, l, nil
	})
	c := ClusterOptions{Namespaces: []string{"a"}, Out: t.TempDir(), MaxItems: 1}
	var got []string
	for range 3 {
		b, err := Cluster(context.Background(), cl, Options{Plan: true}, c, nil)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, drafted(b)...)
		if c.Continue = b.Continue; c.Continue == "" {
			break
		}
	}
	if !slices.Equal(got, []string{"a1", "a2", "a3"}) {
		t.Fatalf("drafted %v", got)
	}
}

// TestReadClusterKeepsGoingAfterListError: a source the reader may not list
// fails with a reason and the read goes on, drafting every workload.
func TestReadClusterKeepsGoingAfterListError(t *testing.T) {
	now := time.Now()
	cl := failing(fakeCluster(shopCluster(now)...), "endpointslices", apierrors.NewForbidden(schema.GroupResource{Resource: "endpointslices"}, "", errors.New("no")))
	b, err := Cluster(context.Background(), cl, Options{Plan: true}, ClusterOptions{Out: t.TempDir(), Now: func() time.Time { return now }}, nil)
	want := contract.InferredCoverage{Source: "endpointslices", State: contract.CoverageFailed, Reason: "forbidden"}
	if err != nil || len(b.Results) != 4 || coverage(t, b, "endpointslices") != want {
		t.Fatalf("drafts %d, coverage %+v: %v", len(b.Results), b.Coverage, err)
	}
}

// TestReadClusterMissingCRDComplete: a Pacto CRD that is not installed is a
// complete source with nothing in it, never a failure.
func TestReadClusterMissingCRDComplete(t *testing.T) {
	cl := failing(fakeCluster(deployment("shop", "checkout", "reg.io/acme/checkout:1.4.0")), "pactos", errNotFound("pactos"))
	b, err := Cluster(context.Background(), cl, Options{Plan: true}, ClusterOptions{Out: t.TempDir()}, nil)
	if err != nil || coverage(t, b, "pactos").State != contract.CoverageComplete ||
		coverage(t, b, "runtime-calls") != (contract.InferredCoverage{Source: "runtime-calls", State: contract.CoverageFailed, Reason: "no node runtime observations"}) {
		t.Fatalf("coverage %+v: %v", b.Coverage, err)
	}
}

// listErrors are list errors and the coverage reason each gives.
var listErrors = []struct {
	err  error
	want string
}{
	{apierrors.NewForbidden(schema.GroupResource{Resource: "services"}, "", errors.New("no")), "forbidden"},
	{apierrors.NewNotFound(schema.GroupResource{Resource: "services"}, ""), "not found"},
	{apierrors.NewTimeoutError("slow", 1), "timed out"},
	{apierrors.NewServerTimeout(schema.GroupResource{Resource: "services"}, "list", 1), "timed out"},
	{apierrors.NewResourceExpired("too old"), "the continue token expired"},
	{apierrors.NewInternalError(errors.New("shop/vault is down")), "listing failed (500)"},
	{apierrors.NewTooManyRequests("slow down", 1), "listing failed (429)"},
	{errors.New("dial tcp 10.0.0.1:443: connection refused"), "listing failed (no response)"},
}

// TestReadClusterTimeoutReason: a list that times out, on either side,
// fails its source as timed out; every other error has its own reason.
func TestReadClusterTimeoutReason(t *testing.T) {
	now := time.Now()
	for _, tc := range listErrors {
		cl := failing(fakeCluster(shopCluster(now)...), "services", tc.err)
		b, err := Cluster(context.Background(), cl, Options{Plan: true}, ClusterOptions{Out: t.TempDir(), Now: func() time.Time { return now }}, nil)
		if err != nil || coverage(t, b, "services") != (contract.InferredCoverage{Source: "services", State: contract.CoverageFailed, Reason: tc.want}) {
			t.Errorf("%v: %+v %v", tc.err, b.Coverage, err)
		}
	}
}

// TestCoverageReasonsNameNothing: every reason is one of the fixed
// templates, so no API server error text, and no name in it, reaches a row.
func TestCoverageReasonsNameNothing(t *testing.T) {
	template := regexp.MustCompile(`^(stopped at the item bound \([0-9]+\)|forbidden|not found|timed out|the continue token expired|` +
		`listing failed \(([0-9]{3}|malformed observation|no response)\)|no node runtime observations)$`)
	for _, tc := range listErrors {
		if got := failure(tc.err); !template.MatchString(got) || strings.Contains(got, "shop") || strings.Contains(got, "10.0.0.1") {
			t.Errorf("%v: %q", tc.err, got)
		}
	}
	rec := contract.InferredRecord{Format: contract.InferredRecordFormat, Fields: []contract.Inferred{},
		Inputs:   contract.InferredInputs{Cluster: &contract.InferredClusterInput{AsOf: "2026-11-14T10:02:00Z", Object: "shop/Deployment/checkout", CallWindow: "24h"}},
		Coverage: []contract.InferredCoverage{{Source: "services", State: contract.CoverageFailed, Reason: failure(errors.New("x"))}}}
	if _, err := contract.ParseInferredRecord(rec.Marshal()); err != nil {
		t.Fatalf("the record refuses the reason: %v", err)
	}
}

// TestReadClusterMalformedObservation: an observation that cannot be read
// fails the runtime calls and is skipped; the others are still read.
func TestReadClusterMalformedObservation(t *testing.T) {
	now := time.Now()
	bad := obj("pacto.trianalab.io/v1alpha1", "NodeRuntimeObservation", "pacto-system", "node-2", map[string]any{"status": map[string]any{"surprise": true}})
	b, err := Cluster(context.Background(), fakeCluster(append(shopCluster(now), bad)...), Options{Plan: true},
		ClusterOptions{Namespaces: []string{"shop"}, Out: t.TempDir(), Now: func() time.Time { return now }}, nil)
	want := contract.InferredCoverage{Source: "runtime-calls", State: contract.CoverageFailed, Reason: "listing failed (malformed observation)"}
	if err != nil || coverage(t, b, "runtime-calls") != want || len(result(t, b, "checkout").Dependencies) == 0 {
		t.Fatalf("coverage %+v: %v", b.Coverage, err)
	}
}

// TestClusterAllWorkloadsFailedErrors: with no workload read at all the read
// fails, and still says how far it got.
func TestClusterAllWorkloadsFailedErrors(t *testing.T) {
	cl := fakeCluster(shopCluster(time.Now())...)
	for _, r := range []string{"deployments", "statefulsets", "daemonsets", "cronjobs"} {
		failing(cl, r, errors.New("boom"))
	}
	b, err := Cluster(context.Background(), cl, Options{Plan: true}, ClusterOptions{Out: t.TempDir()}, nil)
	if err == nil || !strings.Contains(err.Error(), "listing deployments: boom") || b == nil || len(b.Results) != 0 ||
		coverage(t, b, "workloads").State != contract.CoverageFailed || coverage(t, b, "services").State != contract.CoverageComplete {
		t.Fatalf("%+v: %v", b, err)
	}
}

// fiveDeployments are a1, a2 and a3 in a and b1 and b2 in b, served page by
// page.
func fiveDeployments() *dynamicfake.FakeDynamicClient {
	cl := fakeCluster(deployment("a", "a1", "x:1"), deployment("a", "a2", "x:1"), deployment("a", "a3", "x:1"),
		deployment("b", "b1", "x:1"), deployment("b", "b2", "x:1"))
	paged(cl, "deployments")
	return cl
}

// drafted are the services b drafted.
func drafted(b *Bulk) []string {
	var out []string
	for _, r := range b.Results {
		out = append(out, r.Service)
	}
	return out
}

// TestClusterContinueResumes: a read the workload bound stopped drafts what
// it read and returns a token; each run with the token drafts the next
// workloads, and the last returns none.
func TestClusterContinueResumes(t *testing.T) {
	c := ClusterOptions{Namespaces: []string{"b", "a"}, Out: t.TempDir(), MaxItems: 2}
	for _, want := range [][]string{{"a1", "a2"}, {"a3", "b1"}, {"b2"}} {
		b, err := Cluster(context.Background(), fiveDeployments(), Options{Plan: true}, c, nil)
		if err != nil || !slices.Equal(drafted(b), want) {
			t.Fatalf("from %q: %v, want %v: %v %+v", c.Continue, drafted(b), want, err, b.Coverage)
		}
		last := slices.Equal(want, []string{"b2"})
		if state := coverage(t, b, "workloads"); last != (b.Continue == "") ||
			!last && state != (contract.InferredCoverage{Source: "workloads", State: contract.CoveragePartial, Reason: "stopped at the item bound (2)"}) {
			t.Fatalf("after %v: token %q, %+v", want, b.Continue, state)
		}
		c.Continue = b.Continue
	}
}

// TestClusterContinueOtherNamespacesRefused: a token is refused, before any
// read, for another namespace list than the one it was made for.
func TestClusterContinueOtherNamespacesRefused(t *testing.T) {
	b, err := Cluster(context.Background(), fiveDeployments(), Options{Plan: true}, ClusterOptions{Namespaces: []string{"a", "b"}, Out: t.TempDir(), MaxItems: 2}, nil)
	if err != nil || b.Continue == "" {
		t.Fatalf("%+v: %v", b, err)
	}
	cl := fiveDeployments()
	_, err = Cluster(context.Background(), cl, Options{Plan: true}, ClusterOptions{Namespaces: []string{"a"}, Out: t.TempDir(), Continue: b.Continue}, nil)
	if err == nil || err.Error() != "the continue token was made for other namespaces: run again without the token" || len(cl.Actions()) != 0 {
		t.Fatalf("%v, %d reads", err, len(cl.Actions()))
	}
}

// TestClusterContinueExpired: an API continue the server no longer holds
// fails the workloads, saying so.
func TestClusterContinueExpired(t *testing.T) {
	c := ClusterOptions{Namespaces: []string{"a", "b"}, Out: t.TempDir(), MaxItems: 2}
	b, err := Cluster(context.Background(), fiveDeployments(), Options{Plan: true}, c, nil)
	if err != nil {
		t.Fatal(err)
	}
	cl := fiveDeployments()
	cl.PrependReactor("list", "deployments", func(a k8stesting.Action) (bool, runtime.Object, error) {
		return a.(k8stesting.ListActionImpl).ListOptions.Continue != "", nil, apierrors.NewResourceExpired("too old")
	})
	c.Continue = b.Continue
	b, _ = Cluster(context.Background(), cl, Options{Plan: true}, c, nil)
	if got := coverage(t, b, "workloads"); got != (contract.InferredCoverage{Source: "workloads", State: contract.CoverageFailed, Reason: "the continue token expired"}) {
		t.Fatalf("%+v", got)
	}
}

// TestClusterContinueMalformed: a token that is not one, or points past the
// namespaces or kinds, is refused.
func TestClusterContinueMalformed(t *testing.T) {
	sum := sha256.Sum256([]byte("a"))
	for _, token := range []string{"!!", base64.RawURLEncoding.EncodeToString([]byte("{")),
		encodeContinue(cursor{N: hex.EncodeToString(sum[:]), I: 1}), encodeContinue(cursor{N: hex.EncodeToString(sum[:]), K: 4})} {
		_, err := Cluster(context.Background(), fiveDeployments(), Options{Plan: true}, ClusterOptions{Namespaces: []string{"a"}, Out: t.TempDir(), Continue: token}, nil)
		if err == nil || !strings.Contains(err.Error(), "the continue token is malformed") {
			t.Errorf("%q: %v", token, err)
		}
	}
}

// TestClusterCallWindowConfigurable: a call older than the default window
// counts once the window is wide enough.
func TestClusterCallWindowConfigurable(t *testing.T) {
	now := time.Now()
	objs := []runtime.Object{
		deployment("shop", "checkout", "reg.io/acme/checkout:1.4.0"), service("shop", "payments", "10.0.0.20"),
		controlled(obj("v1", "Pod", "shop", "checkout-0", nil), "Deployment", "checkout", "u-checkout"),
		obj("pacto.trianalab.io/v1alpha1", "NodeRuntimeObservation", "pacto-system", "node-1", map[string]any{"status": map[string]any{
			"nodeName": "node-1", "pods": []any{map[string]any{"uid": "u-checkout", "connections": []any{map[string]any{
				"address": "10.0.0.20", "port": int64(80), "successes": int64(1), "lastSuccessAt": now.Add(-30 * time.Hour).UTC().Format(time.RFC3339)}}}}}}),
	}
	for window, want := range map[time.Duration]int{0: 0, 48 * time.Hour: 1} {
		c := ClusterOptions{Out: t.TempDir(), Now: func() time.Time { return now }, CallWindow: window}
		b, err := Cluster(context.Background(), fakeCluster(objs...), Options{Plan: true}, c, nil)
		if err != nil || len(result(t, b, "checkout").Dependencies) != want {
			t.Errorf("window %v: %+v %v", window, b, err)
		}
	}
}

// TestClusterOptionRefusals: a negative bound and a window under a minute
// are refused.
func TestClusterOptionRefusals(t *testing.T) {
	for _, tc := range []struct {
		c    ClusterOptions
		want string
	}{
		{ClusterOptions{MaxItems: -1}, "--max-items"},
		{ClusterOptions{CallWindow: 10 * time.Second}, "--call-window"},
		{ClusterOptions{CallWindow: -time.Hour}, "--call-window"},
	} {
		tc.c.Out = t.TempDir()
		if _, err := Cluster(context.Background(), fakeCluster(), Options{Plan: true}, tc.c, nil); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: %v", tc.c, err)
		}
	}
}

// TestClusterReadsNoSecrets: the cluster read lists and gets no Secret and no
// ConfigMap, over every fixture of this file's reads.
func TestClusterReadsNoSecrets(t *testing.T) {
	now := time.Now()
	for _, objs := range [][]runtime.Object{shopCluster(now), secretShop(), manyPods(3)} {
		cl := fakeCluster(objs...)
		if _, err := Cluster(context.Background(), cl, Options{Plan: true}, ClusterOptions{Out: t.TempDir(), Now: func() time.Time { return now }}, nil); err != nil {
			t.Fatal(err)
		}
		for _, a := range cl.Actions() {
			if r := a.GetResource().Resource; r == "secrets" || r == "configmaps" || a.GetVerb() != "list" {
				t.Errorf("the read did %s %s", a.GetVerb(), r)
			}
		}
	}
}

// TestManyHasNoCoverageKey: several folders read no cluster, so their JSON
// carries no coverage and no continue token.
func TestManyHasNoCoverageKey(t *testing.T) {
	a, b := fraudDir(t), writeTree(t, t.TempDir(), map[string]string{"x/Dockerfile": "FROM scratch\n"})
	bulk, err := Many(context.Background(), Options{Plan: true}, []string{a, b}, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(bulk)
	if strings.Contains(string(raw), `"coverage"`) || strings.Contains(string(raw), `"continue"`) {
		t.Fatalf("%s", raw)
	}
}

// viewerShop is shop/invoices calling vault/kms, which a Pacto describes,
// while two vault pods call invoices now and a third did 30 hours ago.
func viewerShop(now time.Time) []runtime.Object {
	call := func(ip string, at time.Time) []any {
		return []any{map[string]any{"address": ip, "port": int64(80), "successes": int64(1), "lastSuccessAt": at.UTC().Format(time.RFC3339)}}
	}
	pod := func(ns, name, owner string) *unstructured.Unstructured {
		return controlled(obj("v1", "Pod", ns, name, map[string]any{"metadata": map[string]any{"labels": map[string]any{"app": owner}}}), "Deployment", owner, "u-"+name)
	}
	return []runtime.Object{
		deployment("shop", "invoices", "reg.io/acme/invoices:1.0.0"), service("shop", "invoices", "10.0.0.30"), pod("shop", "invoices-0", "invoices"),
		deployment("vault", "kms", "reg.io/acme/kms:1.0.0"), service("vault", "kms", "10.0.0.50"),
		pod("vault", "agent-a", "kms"), pod("vault", "agent-b", "kms"), pod("vault", "agent-old", "kms"),
		pactoCR("vault", "kms", "kms", "kms", "oci://reg.io/acme/contracts/kms", "1.2.0", 1, 1),
		obj("pacto.trianalab.io/v1alpha1", "NodeRuntimeObservation", "pacto-system", "node-1", map[string]any{"status": map[string]any{
			"nodeName": "node-1", "pods": []any{
				map[string]any{"uid": "u-invoices-0", "connections": call("10.0.0.50", now)},
				map[string]any{"uid": "u-agent-a", "connections": call("10.0.0.30", now)},
				map[string]any{"uid": "u-agent-b", "connections": call("10.0.0.30", now)},
				map[string]any{"uid": "u-agent-old", "connections": call("10.0.0.30", now.Add(-30*time.Hour))},
			}}}),
	}
}

func onlyShop(ns string) bool { return ns == "shop" }

// TestViewerDropsBeforeCompletion: a Service the viewer may not read is
// dropped before the draft is made, so it is no dependency and no line
// names it; without the hook the same read lists it.
func TestViewerDropsBeforeCompletion(t *testing.T) {
	now := time.Now()
	for _, visible := range []func(string) bool{nil, onlyShop} {
		c := ClusterOptions{Out: t.TempDir(), Now: func() time.Time { return now }, Visible: visible}
		b, err := Cluster(context.Background(), fakeCluster(viewerShop(now)...), Options{Plan: true}, c, nil)
		if err != nil {
			t.Fatal(err)
		}
		r := result(t, b, "invoices")
		raw, _ := json.Marshal(r)
		if named := strings.Contains(string(raw), "kms"); named != (visible == nil) || len(r.Dependencies) != map[bool]int{true: 1}[visible == nil] {
			t.Errorf("visible %v: %s", visible != nil, raw)
		}
	}
}

// TestViewerHiddenCounts: per workload, the hidden Services its pods called
// and the hidden pods calling its Services within the call window.
func TestViewerHiddenCounts(t *testing.T) {
	now := time.Now()
	c := ClusterOptions{MaxItems: DefaultMaxItems, Now: func() time.Time { return now }}
	nss := []string{""}
	cur, _ := decodeContinue("", nss)
	cl, err := readCluster(context.Background(), fakeCluster(viewerShop(now)...), c, nss, cur)
	if err != nil {
		t.Fatal(err)
	}
	cl.restrict(onlyShop, now.Add(-DefaultCallWindow))
	want := map[string]int{"shop/Deployment/invoices": 1}
	if !reflect.DeepEqual(cl.hiddenServices, want) || !reflect.DeepEqual(cl.hiddenCallers, map[string]int{"shop/Deployment/invoices": 2}) {
		t.Fatalf("services %v, callers %v", cl.hiddenServices, cl.hiddenCallers)
	}
	for _, l := range [][]unstructured.Unstructured{cl.workloads, cl.pods, cl.services, cl.pactos} {
		for _, o := range l {
			if o.GetNamespace() != "shop" {
				t.Errorf("kept %s", key(o))
			}
		}
	}
}

// tokenRE matches strings that may be base64 of something.
var tokenRE = regexp.MustCompile(`[A-Za-z0-9+/_=-]{8,}`)

// decoded is s with every token-shaped string in it base64-decoded, in the
// standard and URL alphabets, twice.
func decoded(s string) string {
	out := s
	for range 2 {
		for _, tok := range tokenRE.FindAllString(out, -1) {
			for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
				if raw, err := enc.DecodeString(tok); err == nil {
					out += "\n" + string(raw)
				}
			}
		}
	}
	return out
}

// TestViewerHiddenObjectsNeverNamed: nothing a viewer gets names a hidden
// namespace, Service or pod, even decoded.
func TestViewerHiddenObjectsNeverNamed(t *testing.T) {
	now := time.Now()
	c := ClusterOptions{Out: t.TempDir(), Now: func() time.Time { return now }, Visible: onlyShop}
	b, err := Cluster(context.Background(), fakeCluster(viewerShop(now)...), Options{Plan: true}, c, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(b)
	text := decoded(string(raw))
	for _, name := range []string{"vault", "kms", "agent-a", "agent-b", "agent-old"} {
		if strings.Contains(text, name) {
			t.Errorf("%q named in %s", name, raw)
		}
	}
}

// TestViewerNoContinueToken: a viewer read the bound stopped says so, and
// gets no token.
func TestViewerNoContinueToken(t *testing.T) {
	c := ClusterOptions{Namespaces: []string{"a", "b"}, Out: t.TempDir(), MaxItems: 2, Visible: func(string) bool { return true }}
	b, err := Cluster(context.Background(), fiveDeployments(), Options{Plan: true}, c, nil)
	if err != nil || b.Continue != "" || coverage(t, b, "workloads") != (contract.InferredCoverage{Source: "workloads", State: contract.CoveragePartial, Reason: "stopped at the item bound (2)"}) {
		t.Fatalf("%+v: %v", b, err)
	}
}

// TestViewerContinueRefused: a viewer read refuses a token before any read.
func TestViewerContinueRefused(t *testing.T) {
	cl := fiveDeployments()
	c := ClusterOptions{Out: t.TempDir(), Continue: "x", Visible: onlyShop}
	if _, err := Cluster(context.Background(), cl, Options{Plan: true}, c, nil); err == nil ||
		err.Error() != "a continue token is not available when reading on behalf of another person" || len(cl.Actions()) != 0 {
		t.Fatalf("%v, %d reads", err, len(cl.Actions()))
	}
}

// TestViewerNilChangesNothing: a viewer who may read everything gets what a
// read with no hook gets.
func TestViewerNilChangesNothing(t *testing.T) {
	now := time.Now()
	var got []string
	for _, visible := range []func(string) bool{nil, func(string) bool { return true }} {
		c := ClusterOptions{Out: "drafts", Now: func() time.Time { return now }, Visible: visible}
		b, err := Cluster(context.Background(), fakeCluster(shopCluster(now)...), Options{Plan: true}, c, nil)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(b)
		got = append(got, string(raw))
	}
	if got[0] != got[1] {
		t.Fatalf("without the hook:\n%s\nwith it:\n%s", got[0], got[1])
	}
}

// TestViewerRefusesHiddenNamespace: a namespace the viewer may not read is
// refused before any read, with the same words whether it exists or not, so
// no bound row counts what it holds; a visible one is read as asked.
func TestViewerRefusesHiddenNamespace(t *testing.T) {
	now := time.Now()
	for _, objs := range [][]runtime.Object{viewerShop(now), nil} {
		cl := fakeCluster(objs...)
		c := ClusterOptions{Namespaces: []string{"vault", "shop"}, Out: t.TempDir(), MaxItems: 1, Now: func() time.Time { return now }, Visible: onlyShop}
		if b, err := Cluster(context.Background(), cl, Options{Plan: true}, c, nil); b != nil || err == nil ||
			err.Error() != "the namespace vault is not one you may read" || len(cl.Actions()) != 0 {
			t.Fatalf("%+v: %v, %d reads", b, err, len(cl.Actions()))
		}
	}
	c := ClusterOptions{Namespaces: []string{"shop"}, Out: t.TempDir(), Now: func() time.Time { return now }, Visible: onlyShop}
	b, err := Cluster(context.Background(), fakeCluster(viewerShop(now)...), Options{Plan: true}, c, nil)
	if err != nil || !slices.Equal(drafted(b), []string{"invoices"}) || len(b.Skipped) != 0 {
		t.Fatalf("%v %+v: %v", drafted(b), b, err)
	}
}

// argsWorkload is checkout with the containers and initContainers given, each
// a name and its command and args.
func argsWorkload(containers, inits []any) *unstructured.Unstructured {
	spec := map[string]any{"containers": containers}
	if inits != nil {
		spec["initContainers"] = inits
	}
	return obj("apps/v1", "Deployment", "shop", "checkout", map[string]any{"spec": map[string]any{"template": map[string]any{"spec": spec}}})
}

// ctr is a container named name with command and args (nil leaves one out)
// and env.
func ctr(name string, command, args []any, env []any) map[string]any {
	c := map[string]any{"name": name, "image": "reg.io/acme/" + name + ":1.0.0"}
	for k, v := range map[string][]any{"command": command, "args": args, "env": env} {
		if v != nil {
			c[k] = v
		}
	}
	return c
}

// rewriteArgs runs secretEnv over w and returns its containers, its
// initContainers and the Secret's keys.
func rewriteArgs(t *testing.T, w *unstructured.Unstructured) (containers, inits []any, keys []string) {
	t.Helper()
	keys, _ = secretEnv(w, "checkout-env", map[string]bool{"checkout": true})
	containers, _, _ = unstructured.NestedSlice(w.Object, "spec", "template", "spec", "containers")
	inits, _, _ = unstructured.NestedSlice(w.Object, "spec", "template", "spec", "initContainers")
	return containers, inits, keys
}

// field is the string list key of the container c.
func field(c any, key string) []string {
	l, _ := c.(map[string]any)[key].([]any)
	out := make([]string, len(l))
	for i, v := range l {
		out[i] = v.(string)
	}
	return out
}

// envRef reports whether the container c has the env entry name reading key
// from checkout-env.
func envRef(c any, name, key string) bool {
	envs, _ := c.(map[string]any)["env"].([]any)
	return slices.ContainsFunc(envs, func(e any) bool {
		em := e.(map[string]any)
		return em["name"] == name && fmt.Sprint(em["valueFrom"]) == fmt.Sprint(map[string]any{"secretKeyRef": map[string]any{"name": "checkout-env", "key": key}})
	})
}

func TestSecretEnvMovesSensitiveArgs(t *testing.T) {
	cs, _, keys := rewriteArgs(t, argsWorkload([]any{ctr("app", nil, []any{"serve", "--db-password=hunter2", "--port=8080"}, nil)}, nil))
	if got := field(cs[0], "args"); !slices.Equal(got, []string{"serve", "--db-password=$(DB_PASSWORD)", "--port=8080"}) ||
		!envRef(cs[0], "DB_PASSWORD", "DB_PASSWORD") || !slices.Equal(keys, []string{"DB_PASSWORD"}) || strings.Contains(fmt.Sprint(cs), "hunter2") {
		t.Fatalf("args %v keys %v containers %v", got, keys, cs)
	}
}

func TestSecretEnvArgsKeyEqualsForm(t *testing.T) {
	cs, _, keys := rewriteArgs(t, argsWorkload([]any{ctr("app", []any{"env", "TOKEN=t0ken-arg", "app"}, nil, nil)}, nil))
	if got := field(cs[0], "command"); !slices.Equal(got, []string{"env", "TOKEN=$(TOKEN)", "app"}) || !envRef(cs[0], "TOKEN", "TOKEN") || !slices.Equal(keys, []string{"TOKEN"}) {
		t.Fatalf("command %v keys %v", got, keys)
	}
	if _, ok := cs[0].(map[string]any)["args"]; ok {
		t.Fatalf("args appeared: %v", cs[0])
	}
}

// TestSecretEnvArgsSplitForm: the value after a bare sensitive flag moves;
// a short flag moves when its value is sensitive by itself.
func TestSecretEnvArgsSplitForm(t *testing.T) {
	cs, _, keys := rewriteArgs(t, argsWorkload([]any{ctr("app", []any{"app"}, []any{"--db-password", "hunter2", "-p", "postgres://u:hunter2@db/x"}, nil)}, nil))
	if got := slices.Concat(field(cs[0], "command"), field(cs[0], "args")); !slices.Equal(got, []string{"app", "--db-password", "$(DB_PASSWORD)", "-p", "$(P)"}) ||
		!envRef(cs[0], "P", "P") || !slices.Equal(keys, []string{"DB_PASSWORD", "P"}) || strings.Contains(fmt.Sprint(cs), "hunter2") {
		t.Fatalf("argv %v keys %v", got, keys)
	}
}

func TestSecretEnvArgsShellString(t *testing.T) {
	cs, _, keys := rewriteArgs(t, argsWorkload([]any{ctr("app", []any{"/bin/sh", "-c"}, []any{"app --token=t0ken-arg  --port=8080"}, nil)}, nil))
	if got := field(cs[0], "args"); !slices.Equal(got, []string{"app --token=$(TOKEN)  --port=8080"}) || !slices.Equal(keys, []string{"TOKEN"}) {
		t.Fatalf("args %v keys %v", got, keys)
	}
	cs, _, _ = rewriteArgs(t, argsWorkload([]any{ctr("app", nil, []any{"bash", "-c", "x"}, nil)}, nil))
	if got := field(cs[0], "args"); !slices.Equal(got, []string{"bash", "-c", "x"}) {
		t.Fatalf("plain script %v", got)
	}
}

// TestSecretEnvArgsEnvInSameContainer: the env entry lands in the container
// or init container that holds the arg, and in no other.
func TestSecretEnvArgsEnvInSameContainer(t *testing.T) {
	cs, is, keys := rewriteArgs(t, argsWorkload(
		[]any{ctr("app", nil, []any{"serve"}, nil), ctr("sidecar", nil, []any{"--api-key=k3y"}, nil)},
		[]any{ctr("migrate", nil, []any{"--db-password=hunter2"}, nil)}))
	if !envRef(cs[1], "API_KEY", "API_KEY") || envRef(cs[0], "API_KEY", "API_KEY") || cs[0].(map[string]any)["env"] != nil ||
		!envRef(is[0], "DB_PASSWORD", "DB_PASSWORD") || envRef(cs[1], "DB_PASSWORD", "DB_PASSWORD") || !slices.Equal(keys, []string{"API_KEY", "DB_PASSWORD"}) {
		t.Fatalf("containers %v inits %v keys %v", cs, is, keys)
	}
}

func TestSecretEnvArgsURLWithUserinfo(t *testing.T) {
	cs, _, keys := rewriteArgs(t, argsWorkload([]any{ctr("app", nil, []any{"connect", "postgres://u:s3cret@db/x", "postgres://v:other@db/y"}, nil)}, nil))
	if got := field(cs[0], "args"); !slices.Equal(got, []string{"connect", "$(URL)", "$(URL_2)"}) || !envRef(cs[0], "URL_2", "URL_2") ||
		!slices.Equal(keys, []string{"URL", "URL_2"}) {
		t.Fatalf("args %v keys %v", got, keys)
	}
}

// TestSecretEnvArgsNameClash: a name the container's env already sets reads
// ARG_<name>; a Secret key another container's env holds with a different
// literal reads <container>.<name>; the same literal twice shares one entry.
func TestSecretEnvArgsNameClash(t *testing.T) {
	cs, is, keys := rewriteArgs(t, argsWorkload(
		[]any{ctr("app", nil, []any{"--db-password=hunter2", "--db-password=hunter2"}, env("DB_PASSWORD", "env-pw"))},
		[]any{ctr("migrate", nil, []any{"--db-password=m1grate"}, nil)}))
	if got := field(cs[0], "args"); !slices.Equal(got, []string{"--db-password=$(ARG_DB_PASSWORD)", "--db-password=$(ARG_DB_PASSWORD)"}) ||
		!envRef(cs[0], "ARG_DB_PASSWORD", "ARG_DB_PASSWORD") || len(cs[0].(map[string]any)["env"].([]any)) != 2 ||
		!envRef(is[0], "DB_PASSWORD", "migrate.DB_PASSWORD") || !slices.Equal(keys, []string{"ARG_DB_PASSWORD", "DB_PASSWORD", "migrate.DB_PASSWORD"}) {
		t.Fatalf("containers %v inits %v keys %v", cs, is, keys)
	}
}

func TestSecretEnvLeavesPlainArgs(t *testing.T) {
	probe := "while true; do nc -z -w 2 wallet 80; sleep 2; done"
	w := argsWorkload([]any{ctr("app", []any{"sh", "-c", probe}, []any{"--port=8080", "--port", "8080", "--token", "--verbose", "-", int64(7)}, nil)}, nil)
	before := fmt.Sprint(w.Object)
	if _, _, keys := rewriteArgs(t, w); keys != nil || fmt.Sprint(w.Object) != before {
		t.Fatalf("plain args changed: %v %v", keys, w.Object)
	}
}

// TestSecretEnvInitContainerURLNamesDependency: a URL an init container's
// env sets to a dependency is read by the scan, so moving it into the Secret
// names the dependency the draft no longer lists.
func TestSecretEnvInitContainerURLNamesDependency(t *testing.T) {
	w := argsWorkload([]any{ctr("app", nil, nil, nil)}, []any{ctr("migrate", nil, nil, env("LEDGER_URL", "postgres://u:p@ledger:5432/x"))})
	if keys, urls := secretEnv(w, "checkout-env", map[string]bool{"checkout": true}); !slices.Equal(keys, []string{"LEDGER_URL"}) || !slices.Equal(urls, []string{"LEDGER_URL (ledger)"}) {
		t.Fatalf("keys %v urls %v", keys, urls)
	}
}

// TestClusterWorkloadSelects: --workload drafts only the workload it names,
// by name, ns/name or ns/Kind/name; none, several or one a Pacto describes
// is refused with a message saying what to pass.
func TestClusterWorkloadSelects(t *testing.T) {
	now := time.Now()
	objs := append(shopCluster(now), deployment("other", "checkout", "reg.io/acme/checkout:1.4.0"))
	draft := func(w string) (*Bulk, error) {
		c := ClusterOptions{Out: t.TempDir(), Now: func() time.Time { return now }, Workload: w}
		return Cluster(context.Background(), fakeCluster(objs...), Options{Plan: true}, c, nil)
	}
	for _, w := range []string{"fraud", "shop/checkout", "shop/Deployment/checkout"} {
		if b, err := draft(w); err != nil || len(b.Results) != 1 || !strings.HasSuffix(w, b.Results[0].Service) {
			t.Fatalf("%s: %+v %v", w, b, err)
		}
	}
	for w, want := range map[string]error{"checkout": ErrWorkloadAmbiguous, "missing": ErrWorkloadNotFound, "shop/payments": ErrAlreadyDeclared, "shop/StatefulSet/checkout": ErrWorkloadNotFound} {
		_, err := draft(w)
		if !errors.Is(err, want) {
			t.Fatalf("%s: %v", w, err)
		}
	}
	_, err := draft("checkout")
	_, gone := draft("missing")
	_, declared := draft("shop/payments")
	if err.Error() != "2 workloads are named checkout: pass ns/Kind/name" || gone.Error() != "no workload named missing that no Pacto describes" ||
		declared.Error() != "shop/Deployment/payments already has a contract: change it with pacto publish" {
		t.Fatalf("%v | %v | %v", err, gone, declared)
	}
}

// TestClusterWorkloadHiddenNotCounted: a workload of the same name in a
// namespace the viewer may not read is never counted, so the name is not
// ambiguous and nothing names the hidden one.
func TestClusterWorkloadHiddenNotCounted(t *testing.T) {
	now := time.Now()
	objs := append(shopCluster(now), deployment("other", "checkout", "reg.io/acme/checkout:1.4.0"))
	c := ClusterOptions{Out: t.TempDir(), Now: func() time.Time { return now }, Workload: "checkout", Visible: func(ns string) bool { return ns == "shop" }}
	b, err := Cluster(context.Background(), fakeCluster(objs...), Options{Plan: true}, c, nil)
	if err != nil || len(b.Results) != 1 || b.Results[0].Dir != filepath.Join(c.Out, "shop", "checkout") {
		t.Fatalf("%+v %v", b, err)
	}
	c.Workload = "other/checkout"
	if _, err := Cluster(context.Background(), fakeCluster(objs...), Options{Plan: true}, c, nil); !errors.Is(err, ErrWorkloadNotFound) || strings.Contains(err.Error(), "2 workloads") {
		t.Fatal(err)
	}
}
