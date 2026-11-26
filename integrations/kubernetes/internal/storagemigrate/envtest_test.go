/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package storagemigrate

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"
)

// TestStorageMigrationEnvtest stores an object as v1alpha1 on a real
// apiserver, upgrades its CRD to the generated v1-storage one, migrates it and
// then drops v1alpha1 from the CRD, which the apiserver refuses before the
// migration and accepts after it.
func TestStorageMigrationEnvtest(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS is not set: run make -C integrations/kubernetes ci-test")
	}
	te := &envtest.Environment{}
	cfg, err := te.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = te.Stop() })
	dyn := dynamic.NewForConfigOrDie(cfg)
	disc := discovery.NewDiscoveryClientForConfigOrDie(cfg)

	targets := map[string]Target{}
	for _, tg := range append(PactoTargets(), DeliveryTargets()...) {
		targets[tg.Resource.Resource] = tg
	}
	for _, tc := range []struct {
		file, resource string
		spec, status   map[string]any
	}{
		{"pacto.trianalab.io_pactos.yaml", "pactos",
			map[string]any{"contractRef": map[string]any{"oci": "ghcr.io/acme/checkout:1.0.0"}},
			map[string]any{"contractVersion": "1.0.0"}},
		{"pacto.trianalab.io_noderuntimeobservations.yaml", "noderuntimeobservations",
			nil, map[string]any{"nodeName": "node-a", "agentVersion": "1.0.0"}},
		{"delivery.pacto.trianalab.io_approvals.yaml", "approvals",
			map[string]any{"by": "dana@example.com", "decision": "approve", "intentDigest": "sha256:abc",
				"operation": "checkout-1", "operationUID": "uid-1"}, nil},
	} {
		t.Run(tc.resource, func(t *testing.T) {
			envtestMigrate(t, dyn, disc, targets[tc.resource], tc.file, tc.spec, tc.status)
		})
	}

	// The pinned hash is the one the apiserver reports.
	list, err := disc.ServerResourcesForGroupVersion("pacto.trianalab.io/v1")
	if err != nil || !hasHash(list, "pactos", pactoV1Hash) {
		t.Fatalf("discovery does not report %s for pactos: %v", pactoV1Hash, err)
	}
}

func envtestMigrate(t *testing.T, dyn dynamic.Interface, disc discovery.DiscoveryInterface, tg Target, file string, spec, status map[string]any) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	crds := dyn.Resource(crdGVR)
	generated := readCRD(t, file)

	// (2) A v1alpha1-only CRD, as chart 6 shipped it.
	old := generated.DeepCopy()
	setVersions(t, old, func(v map[string]any) bool {
		v["storage"] = true
		delete(v, "deprecated")
		delete(v, "deprecationWarning")
		return v["name"] == "v1alpha1"
	})
	must(t, ignore(crds.Create(ctx, old, metav1.CreateOptions{})))
	waitEstablished(ctx, t, dyn, tg.CRD)

	// (3) An object stored as v1alpha1, with its status where it has one.
	alpha := tg.Resource
	alpha.Version = "v1alpha1"
	o := &unstructured.Unstructured{Object: map[string]any{"apiVersion": alpha.GroupVersion().String(), "kind": tg.Kind,
		"metadata": map[string]any{"namespace": "default", "name": "one"}}}
	if spec != nil {
		o.Object["spec"] = spec
	}
	created, err := dyn.Resource(alpha).Namespace("default").Create(ctx, o, metav1.CreateOptions{})
	must(t, err)
	if status != nil {
		created.Object["status"] = status
		created, err = dyn.Resource(alpha).Namespace("default").UpdateStatus(ctx, created, metav1.UpdateOptions{})
		must(t, err)
	}

	// (4) and (5)
	assertStored(ctx, t, dyn, tg.CRD, "v1alpha1")
	live, err := crds.Get(ctx, tg.CRD, metav1.GetOptions{})
	must(t, err)
	live.Object["spec"] = generated.Object["spec"]
	must(t, ignore(crds.Update(ctx, live, metav1.UpdateOptions{})))
	assertStored(ctx, t, dyn, tg.CRD, "v1alpha1", "v1")

	// (6) Dropping v1alpha1 before the migration strands its objects: refused.
	v1Only := func() error {
		live, err := crds.Get(ctx, tg.CRD, metav1.GetOptions{})
		must(t, err)
		setVersions(t, live, func(v map[string]any) bool { return v["name"] == "v1" })
		_, err = crds.Update(ctx, live, metav1.UpdateOptions{})
		return err
	}
	if err := v1Only(); err == nil {
		t.Fatal("step 6: the apiserver accepted a CRD that drops a stored version")
	} else {
		t.Logf("step 6: dropping v1alpha1 before the migration is refused: %v", err)
	}

	// (7) Migrate against the real apiserver and discovery.
	m := &Migrator{Client: dyn, Discovery: disc, Targets: []Target{tg}, From: "v1alpha1", To: "v1",
		Backoff: wait.Backoff{Duration: 100 * time.Millisecond}, HashPoll: 100 * time.Millisecond, HashTimeout: 30 * time.Second}
	must(t, m.Start(ctx))
	assertStored(ctx, t, dyn, tg.CRD, "v1")
	migrated, err := dyn.Resource(tg.Resource).Namespace("default").Get(ctx, "one", metav1.GetOptions{})
	must(t, err)
	if migrated.GetResourceVersion() == created.GetResourceVersion() {
		t.Fatal("step 7: the object was not rewritten")
	}
	for _, f := range []string{"spec", "status"} {
		if !reflect.DeepEqual(migrated.Object[f], created.Object[f]) {
			t.Fatalf("step 7: %s changed: %v -> %v", f, created.Object[f], migrated.Object[f])
		}
	}

	// (8) Now v1alpha1 can go, and the object still decodes as v1.
	must(t, v1Only())
	t.Logf("step 8: dropping v1alpha1 after the migration is accepted")
	got, err := dyn.Resource(tg.Resource).Namespace("default").Get(ctx, "one", metav1.GetOptions{})
	must(t, err)
	if got.GetAPIVersion() != tg.Resource.GroupVersion().String() || !reflect.DeepEqual(got.Object["spec"], created.Object["spec"]) {
		t.Fatalf("step 8: read back %v", got.Object)
	}

	// (9) A second run is a no-op.
	must(t, m.Start(ctx))
	again, err := dyn.Resource(tg.Resource).Namespace("default").Get(ctx, "one", metav1.GetOptions{})
	must(t, err)
	if again.GetResourceVersion() != got.GetResourceVersion() {
		t.Fatal("step 9: a second run rewrote the object")
	}
}

func readCRD(t *testing.T, file string) *unstructured.Unstructured {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "bases", file))
	must(t, err)
	u := &unstructured.Unstructured{}
	must(t, yaml.Unmarshal(b, &u.Object))
	return u
}

// setVersions keeps the versions keep returns true for.
func setVersions(t *testing.T, crd *unstructured.Unstructured, keep func(map[string]any) bool) {
	t.Helper()
	versions, _, err := unstructured.NestedSlice(crd.Object, "spec", "versions")
	must(t, err)
	versions = slices.DeleteFunc(versions, func(v any) bool { return !keep(v.(map[string]any)) })
	must(t, unstructured.SetNestedSlice(crd.Object, versions, "spec", "versions"))
}

func waitEstablished(ctx context.Context, t *testing.T, dyn dynamic.Interface, name string) {
	t.Helper()
	must(t, wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		crd, err := dyn.Resource(crdGVR).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		conds, _, _ := unstructured.NestedSlice(crd.Object, "status", "conditions")
		return slices.ContainsFunc(conds, func(c any) bool {
			c0 := c.(map[string]any)
			return c0["type"] == "Established" && c0["status"] == "True"
		}), nil
	}))
}

func assertStored(ctx context.Context, t *testing.T, dyn dynamic.Interface, name string, want ...string) {
	t.Helper()
	crd, err := dyn.Resource(crdGVR).Get(ctx, name, metav1.GetOptions{})
	must(t, err)
	got, _, _ := unstructured.NestedStringSlice(crd.Object, "status", "storedVersions")
	if !slices.Equal(got, want) {
		t.Fatalf("storedVersions %v, want %v", got, want)
	}
}

func ignore(_ *unstructured.Unstructured, err error) error { return err }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
