package app

import (
	"context"
	"io/fs"
	"os"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/internal/pactify"
	"github.com/trianalab/pacto/v4/internal/runtimewatch/kubesink"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/override"
)

// TestPublishClusterDraftHoldsNoLiteral: the bundle pacto publish packs from
// a cluster draft holds no literal of the workload's env or args, in the =,
// split and sh -c forms or in an init container.
func TestPublishClusterDraftHoldsNoLiteral(t *testing.T) {
	ctr := func(name string, command, args []any, env ...any) map[string]any {
		c := map[string]any{"name": name, "image": "reg.io/acme/" + name + ":1.0.0", "args": args, "env": env}
		if command != nil {
			c["command"] = command
		}
		return c
	}
	w := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"name": "checkout", "namespace": "shop", "labels": map[string]any{"team": "shop"}},
		"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
			"containers": []any{
				ctr("checkout", []any{"/bin/sh", "-c"}, []any{"serve --client-secret=v4lues-s3cret"}, map[string]any{"name": "DB_PASSWORD", "value": "hunter2"}),
				ctr("audit", nil, []any{"--api-key", "l0g-l1teral"}),
			},
			"initContainers": []any{ctr("migrate", nil, []any{"--token=t0ken-arg"})},
		}}}}}
	lists := map[schema.GroupVersionResource]string{k8sclient.PactosGVR: "PactoList", kubesink.GVR: "NodeRuntimeObservationList"}
	for kind, r := range k8sclient.WorkloadGVRs {
		lists[r] = kind + "List"
	}
	for _, r := range []string{"services", "pods", "replicasets", "jobs", "endpointslices"} {
		lists[schema.GroupVersionResource{Group: map[string]string{"replicasets": "apps", "jobs": "batch", "endpointslices": "discovery.k8s.io"}[r], Version: "v1", Resource: r}] = r + "List"
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), lists, w)
	b, err := pactify.Cluster(context.Background(), dyn, pactify.Options{}, pactify.ClusterOptions{Out: t.TempDir()}, os.WriteFile)
	if err != nil || len(b.Results) != 1 {
		t.Fatalf("%+v %v", b, err)
	}
	dir := b.Results[0].Dir
	if _, err := os.Stat(dir + "/" + contract.InferredRecordFile); err != nil {
		t.Fatalf("the draft holds no record: %v", err)
	}
	bundle, err := loadLocalBundleNamed(dir, dirBase(dir), override.Overrides{}, contract.InferredInputs{}, nil)
	if err == nil {
		bundle, err = publishable(bundle, true)
	}
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	err = fs.WalkDir(bundle.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(bundle.FS, path)
		n++
		for _, l := range []string{"hunter2", "v4lues-s3cret", "l0g-l1teral", "t0ken-arg"} {
			if strings.Contains(string(data), l) {
				t.Errorf("%q in %s:\n%s", l, path, data)
			}
		}
		return err
	})
	if err != nil || n < 3 {
		t.Fatalf("%d files: %v", n, err)
	}
}
