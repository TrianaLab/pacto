package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/pactify"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

func runInit(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRootCommand(app.NewService(nil, nil), VersionInfo{Version: "test"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"init"}, args...))
	err := root.Execute()
	return out.String(), err
}

func serviceFolder(t *testing.T, name, image string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: " + name + "}\nspec:\n  template:\n    spec:\n      containers: [{name: " + name + ", image: " + image + "}]\n"
	if err := os.WriteFile(filepath.Join(dir, "deploy.yaml"), []byte(m), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestInitSeveralFolders: each folder is planned, then a summary; a folder
// that fails is named and fails the command after the rest.
func TestInitSeveralFolders(t *testing.T) {
	a, b := serviceFolder(t, "checkout", "reg.io/acme/checkout:1.0.0"), serviceFolder(t, "payments", "reg.io/acme/payments:2.0.0")
	out, err := runInit(t, "--plan", a, b)
	if err != nil || !strings.Contains(out, "SERVICE") || !strings.Contains(out, "checkout") || !strings.Contains(out, "payments") {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = runInit(t, "--plan", a, filepath.Join(b, "deploy.yaml"))
	if err == nil || !strings.Contains(err.Error(), "is a file, not a folder") || !strings.Contains(out, "checkout") {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = runInit(t, "--plan", "--output-format", "json", a, b)
	var bulk pactify.Bulk
	if err != nil || json.Unmarshal([]byte(out), &bulk) != nil || len(bulk.Results) != 2 {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := runInit(t, "--name", "x", a, b); err == nil {
		t.Fatal("one name for two folders")
	}
}

// TestInitCluster: --cluster drafts every workload no Pacto describes, and
// the flags that only go with it, or without folders, are refused.
func TestInitCluster(t *testing.T) {
	for _, args := range [][]string{{"--cluster", "x"}, {"--out", "x"}, {"--namespace", "shop"}} {
		if _, err := runInit(t, args...); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
	defer func(old func() (dynamic.Interface, error)) { initDynamic = old }(initDynamic)
	initDynamic = func() (dynamic.Interface, error) { return nil, errors.New("no kubeconfig") }
	if _, err := runInit(t, "--cluster", "--out", t.TempDir()); err == nil || err.Error() != "no kubeconfig" {
		t.Fatalf("client: %v", err)
	}
	gvr := func(g, v, r string) schema.GroupVersionResource {
		return schema.GroupVersionResource{Group: g, Version: v, Resource: r}
	}
	lists := map[schema.GroupVersionResource]string{
		gvr("apps", "v1", "deployments"): "DeploymentList", gvr("apps", "v1", "statefulsets"): "StatefulSetList", gvr("apps", "v1", "daemonsets"): "DaemonSetList",
		gvr("batch", "v1", "cronjobs"): "CronJobList", gvr("", "v1", "services"): "ServiceList", gvr("", "v1", "pods"): "PodList",
		gvr("apps", "v1", "replicasets"): "ReplicaSetList", gvr("batch", "v1", "jobs"): "JobList", gvr("discovery.k8s.io", "v1", "endpointslices"): "EndpointSliceList",
		gvr("pacto.trianalab.io", "v1alpha1", "pactos"): "PactoList", gvr("pacto.trianalab.io", "v1alpha1", "noderuntimeobservations"): "NodeRuntimeObservationList",
	}
	deploy := func(name string) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"name": name, "namespace": "shop"},
			"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"containers": []any{map[string]any{"name": name, "image": "reg.io/acme/" + name + ":1.0.0"}}}}}}}
	}
	pacto := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "pacto.trianalab.io/v1alpha1", "kind": "Pacto", "metadata": map[string]any{"name": "payments", "namespace": "shop"},
		"spec": map[string]any{"target": map[string]any{"workloadRef": map[string]any{"name": "payments", "kind": "Deployment"}}}}}
	initDynamic = func() (dynamic.Interface, error) {
		return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), lists, deploy("checkout"), deploy("payments"), pacto), nil
	}
	drafts := t.TempDir()
	out, err := runInit(t, "--cluster", "--namespace", "shop", "--out", drafts)
	if err != nil || !strings.Contains(out, "shop/Deployment/payments") || !strings.Contains(out, "skipped: a Pacto describes it") {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(drafts, "shop", "checkout", "pacto.yaml")); err != nil {
		t.Fatal(err)
	}
}

// TestPrintBulk: nothing to print returns the error; a JSON write that fails
// is reported.
func TestPrintBulk(t *testing.T) {
	cmd, _ := testCmd()
	boom := errors.New("boom")
	if err := printBulk(cmd, nil, boom, "text"); err != boom {
		t.Fatal(err)
	}
	cmd.SetOut(errWriter{})
	if err := printBulk(cmd, &pactify.Bulk{}, nil, "json"); err == nil {
		t.Fatal("the failed write was not reported")
	}
	cmd, buf := testCmd()
	if err := printBulk(cmd, &pactify.Bulk{Results: []*pactify.Result{nil}}, boom, "json"); err != boom || !strings.Contains(buf.String(), "results") {
		t.Fatal(err)
	}
}

// TestPrintBulkFailedFolder: a folder that planned nothing leaves no row.
func TestPrintBulkFailedFolder(t *testing.T) {
	cmd, buf := testCmd()
	r := &pactify.Result{Service: "a", Dir: "a", Files: []pactify.File{{Path: "pacto.yaml", Action: pactify.Keep}}, Dependencies: []pactify.Finding{{Name: "b"}, {Name: "c"}}}
	if err := printBulk(cmd, &pactify.Bulk{Results: []*pactify.Result{nil, r}}, nil, "text"); err != nil || !strings.Contains(buf.String(), "b,c") {
		t.Fatalf("%v\n%s", err, buf)
	}
}

// TestInitAcceptsTheCommandDeliveryPrints: a hold tells the person to run
// delivery.InitClusterCommand; pacto init must parse exactly those flags.
func TestInitAcceptsTheCommandDeliveryPrints(t *testing.T) {
	defer func(old func() (dynamic.Interface, error)) { initDynamic = old }(initDynamic)
	initDynamic = func() (dynamic.Interface, error) { return nil, errors.New("no kubeconfig") }
	args := strings.Fields(strings.Replace(delivery.InitClusterCommand("prod"), "<dir>", t.TempDir(), 1))
	if args[0] != "pacto" || args[1] != "init" {
		t.Fatalf("not a pacto init command: %v", args)
	}
	if _, err := runInit(t, args[2:]...); err == nil || err.Error() != "no kubeconfig" {
		t.Fatalf("pacto init refused %v: %v", args, err)
	}
}
