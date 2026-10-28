package mcp

import (
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

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func bulkFolder(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: " + name + "}\nspec: {template: {spec: {containers: [{name: " + name + ", image: 'reg.io/acme/" + name + ":1.0.0'}]}}}\n"
	if err := os.WriteFile(filepath.Join(dir, "d.yaml"), []byte(m), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

type bulkResult struct {
	Results []struct {
		Service string
		Applied bool
	}
	Skipped []string
}

// TestCreateTool_PactifyPaths: several folders plan by default, ignore the
// required name, and are written only with dry_run false.
func TestCreateTool_PactifyPaths(t *testing.T) {
	a, b := bulkFolder(t, "checkout"), bulkFolder(t, "payments")
	paths, _ := json.Marshal([]string{a, b})
	args := map[string]any{"name": "ignored", "pactify": true, "paths": string(paths)}
	var got bulkResult
	res := callTool(t, "pacto_create", args)
	if res.IsError || json.Unmarshal([]byte(resultText(t, res)), &got) != nil || len(got.Results) != 2 || got.Results[0].Applied {
		t.Fatalf("plan: %s", resultText(t, res))
	}
	if _, err := os.Stat(filepath.Join(a, "pacto.yaml")); err == nil {
		t.Fatal("paths wrote without dry_run false")
	}
	args["dry_run"] = false
	if res = callTool(t, "pacto_create", args); res.IsError {
		t.Fatalf("apply: %s", resultText(t, res))
	}
	if _, err := os.Stat(filepath.Join(b, "pacto.yaml")); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(a, "d.yaml") // a file, not a folder
	paths, _ = json.Marshal([]string{bulkFolder(t, "orders"), missing})
	args["paths"] = string(paths)
	res = callTool(t, "pacto_create", args)
	if !res.IsError || len(res.Content) != 2 || json.Unmarshal([]byte(resultText(t, res)), &got) != nil || len(got.Results) != 2 || !got.Results[0].Applied {
		t.Fatalf("a failed folder hid the written one: %v %s", res.IsError, resultText(t, res))
	}
	if txt := res.Content[1].(*mcpsdk.TextContent).Text; !strings.Contains(txt, missing) {
		t.Fatalf("the error does not name the failed folder: %s", txt)
	}
	args["paths"] = "not json"
	if res = callTool(t, "pacto_create", args); !res.IsError {
		t.Fatal("bad paths JSON was accepted")
	}
}

// TestCreateTool_PactifyCluster: cluster plans by default into path, skips
// what a Pacto describes, and reports a cluster it cannot reach.
func TestCreateTool_PactifyCluster(t *testing.T) {
	defer func(old func() (dynamic.Interface, error)) { newDynamic = old }(newDynamic)
	newDynamic = func() (dynamic.Interface, error) { return nil, errors.New("no kubeconfig") }
	out := filepath.Join(t.TempDir(), "drafts")
	args := map[string]any{"name": "drafts", "path": out, "pactify": true, "cluster": true, "namespaces": `["shop"]`}
	if res := callTool(t, "pacto_create", args); !res.IsError || !strings.Contains(resultText(t, res), "no kubeconfig") {
		t.Fatalf("unreachable: %s", resultText(t, res))
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
	deploy := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"name": "checkout", "namespace": "shop"},
		"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"containers": []any{map[string]any{"name": "checkout", "image": "reg.io/acme/checkout:1.0.0"}}}}}}}
	newDynamic = func() (dynamic.Interface, error) {
		return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), lists, deploy), nil
	}
	var got bulkResult
	res := callTool(t, "pacto_create", args)
	if res.IsError || json.Unmarshal([]byte(resultText(t, res)), &got) != nil || len(got.Results) != 1 || got.Results[0].Service != "checkout" || got.Results[0].Applied {
		t.Fatalf("plan: %s", resultText(t, res))
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("cluster wrote without dry_run false")
	}
	args["namespaces"] = "not json"
	if res = callTool(t, "pacto_create", args); !res.IsError {
		t.Fatal("bad namespaces JSON was accepted")
	}
}
