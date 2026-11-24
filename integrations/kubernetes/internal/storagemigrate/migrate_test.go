/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package storagemigrate

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

const (
	pactosCRD = "pactos.pacto.trianalab.io"
	// pactoV1Hash is what envtest discovery reports for pactos stored as v1
	// (TestStorageMigrationEnvtest checks it against the live apiserver).
	pactoV1Hash = "djl5OLh5PS4="
)

var pactosGVR = PactoTargets()[0].Resource

// cluster is a fake apiserver for one target: successive CRD reads, paged
// objects and the outcome of every write, in the order they happened.
type cluster struct {
	*dynamicfake.FakeDynamicClient
	mu        sync.Mutex
	crds      []*unstructured.Unstructured // successive CRD reads, the last repeats; nil reads NotFound
	crdGetErr error
	crdPatch  []error // successive CRD status patch outcomes, then nil
	pages     [][]unstructured.Unstructured
	listErr   error
	lists     int
	patch     func(clienttesting.PatchAction) error // an object write outcome; nil writes
	get       func(name string) (*unstructured.Unstructured, error)
	discoRead *int // discovery reads so far, when the test watches it
	firstAt   int  // discovery reads at the first object write
}

func newCluster(t *testing.T, crds ...*unstructured.Unstructured) *cluster {
	t.Helper()
	listKinds := map[schema.GroupVersionResource]string{}
	for _, tg := range append(PactoTargets(), DeliveryTargets()...) {
		listKinds[tg.Resource] = tg.Kind + "List"
	}
	c := &cluster{FakeDynamicClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds), crds: crds, firstAt: -1}
	c.PrependReactor("get", "customresourcedefinitions", func(clienttesting.Action) (bool, runtime.Object, error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.crdGetErr != nil {
			return true, nil, c.crdGetErr
		}
		crd := c.crds[0]
		if len(c.crds) > 1 {
			c.crds = c.crds[1:]
		}
		if crd == nil {
			return true, nil, apierrors.NewNotFound(crdGVR.GroupResource(), pactosCRD)
		}
		return true, crd.DeepCopy(), nil
	})
	c.PrependReactor("patch", "customresourcedefinitions", func(clienttesting.Action) (bool, runtime.Object, error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if len(c.crdPatch) > 0 {
			err := c.crdPatch[0]
			c.crdPatch = c.crdPatch[1:]
			return true, nil, err
		}
		return true, &unstructured.Unstructured{Object: map[string]any{}}, nil
	})
	c.PrependReactor("list", "*", func(clienttesting.Action) (bool, runtime.Object, error) {
		if c.listErr != nil {
			return true, nil, c.listErr
		}
		// The fake drops ListOptions when it copies the action, so the
		// pages are served in turn and a pass starts over at the first.
		list := &unstructured.UnstructuredList{Object: map[string]any{}}
		if len(c.pages) > 0 {
			i := c.lists % len(c.pages)
			list.Items = c.pages[i]
			if i+1 < len(c.pages) {
				list.SetContinue(strconv.Itoa(i + 1))
			}
		}
		c.lists++
		return true, list, nil
	})
	c.PrependReactor("patch", "*", func(a clienttesting.Action) (bool, runtime.Object, error) {
		if a.GetResource() == crdGVR {
			return false, nil, nil
		}
		if c.firstAt < 0 && c.discoRead != nil {
			c.firstAt = *c.discoRead
		}
		if c.patch != nil {
			if err := c.patch(a.(clienttesting.PatchAction)); err != nil {
				return true, nil, err
			}
		}
		return true, &unstructured.Unstructured{Object: map[string]any{}}, nil
	})
	c.PrependReactor("get", "*", func(a clienttesting.Action) (bool, runtime.Object, error) {
		if a.GetResource() == crdGVR {
			return false, nil, nil
		}
		return true, nilSafe(c.get(a.(clienttesting.GetAction).GetName())), nil
	})
	return c
}

func nilSafe(o *unstructured.Unstructured, err error) runtime.Object {
	if err != nil {
		panic(err)
	}
	return o
}

// crd is pactos.pacto.trianalab.io with storage as its storage version.
func crd(storage string, stored ...string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{"versions": []any{
			map[string]any{"name": "v1", "storage": storage == "v1"},
			map[string]any{"name": "v1alpha1", "storage": storage == "v1alpha1"},
		}},
	}}
	u.SetName(pactosCRD)
	u.SetResourceVersion("7")
	_ = unstructured.SetNestedStringSlice(u.Object, stored, "status", "storedVersions")
	return u
}

func obj(ns, name, rv string) unstructured.Unstructured {
	u := unstructured.Unstructured{Object: map[string]any{}}
	u.SetNamespace(ns)
	u.SetName(name)
	u.SetResourceVersion(rv)
	return u
}

// disco reports the storage hash hash(read) on each discovery read of the
// pacto group, counting reads.
func disco(hash func(read int) string) (*fakediscovery.FakeDiscovery, *int) {
	reads := 0
	d := &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}
	d.PrependReactor("get", "resource", func(clienttesting.Action) (bool, runtime.Object, error) {
		reads++
		d.Resources = []*metav1.APIResourceList{{GroupVersion: "pacto.trianalab.io/v1",
			APIResources: []metav1.APIResource{{Name: "other"}, {Name: "pactos", StorageVersionHash: hash(reads)}}}}
		return false, nil, nil
	})
	return d, &reads
}

func v1Hash(int) string { return storageHash("pacto.trianalab.io", "v1", "Pacto") }

func migrator(c *cluster, targets ...Target) *Migrator {
	d, reads := disco(v1Hash)
	c.discoRead = reads
	if len(targets) == 0 {
		targets = PactoTargets()[:1]
	}
	m := &Migrator{Client: c, Discovery: d, Targets: targets, From: "v1alpha1", To: "v1",
		Backoff: wait.Backoff{Duration: time.Millisecond}, HashPoll: time.Millisecond, HashTimeout: time.Second}
	m.defaults()
	return m
}

// capture logs every line m writes.
func capture(m *Migrator) *[]string {
	var mu sync.Mutex
	lines := &[]string{}
	m.Log = funcr.New(func(_, args string) {
		mu.Lock()
		defer mu.Unlock()
		*lines = append(*lines, args)
	}, funcr.Options{})
	return lines
}

func logged(lines *[]string, want string) bool {
	return slices.ContainsFunc(*lines, func(l string) bool { return strings.Contains(l, want) })
}

// writes are the patches in order as "resource ns/name sub body".
func writes(c *cluster) []string {
	var out []string
	for _, a := range c.Actions() {
		if p, ok := a.(clienttesting.PatchAction); ok {
			out = append(out, p.GetResource().Resource+" "+p.GetNamespace()+"/"+p.GetName()+" "+p.GetSubresource()+" "+string(p.GetPatch()))
		}
	}
	return out
}

func storedPatches(c *cluster) []string {
	var out []string
	for _, w := range writes(c) {
		if strings.HasPrefix(w, "customresourcedefinitions") {
			out = append(out, w)
		}
	}
	return out
}

func TestMigrateRewritesThenPatchesStoredVersions(t *testing.T) {
	c := newCluster(t, crd("v1", "v1alpha1", "v1"))
	c.pages = [][]unstructured.Unstructured{{obj("a", "one", "11"), obj("b", "two", "12")}, {obj("a", "three", "13")}}
	if err := migrator(c).Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`pactos a/one status {"metadata":{"resourceVersion":"11"}}`,
		`pactos b/two status {"metadata":{"resourceVersion":"12"}}`,
		`pactos a/three status {"metadata":{"resourceVersion":"13"}}`,
		`customresourcedefinitions /` + pactosCRD + ` status {"metadata":{"resourceVersion":"7"},"status":{"storedVersions":["v1"]}}`,
	}
	if got := writes(c); !slices.Equal(got, want) {
		t.Fatalf("writes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestMigrateUsesStatusSubresource(t *testing.T) {
	for _, tg := range append(PactoTargets(), DeliveryTargets()[:2]...) {
		if tg.Subresource != "status" {
			t.Errorf("%s writes through %q, want status", tg.CRD, tg.Subresource)
		}
	}
}

func TestMigrateApprovalsUseMainResource(t *testing.T) {
	approvals := DeliveryTargets()[2]
	c := newCluster(t, crd("v1", "v1alpha1", "v1"))
	c.pages = [][]unstructured.Unstructured{{obj("a", "one", "11")}}
	m := migrator(c, approvals)
	m.Discovery = &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{Resources: []*metav1.APIResourceList{{GroupVersion: "delivery.pacto.trianalab.io/v1",
		APIResources: []metav1.APIResource{{Name: "approvals", StorageVersionHash: storageHash("delivery.pacto.trianalab.io", "v1", "Approval")}}}}}}
	if !m.migrateCRD(context.Background(), approvals) {
		t.Fatal("approvals pending")
	}
	if got := writes(c)[0]; got != `approvals a/one  {"metadata":{"resourceVersion":"11"}}` {
		t.Fatalf("approval write %q", got)
	}
}

func TestMigrateConflictRetries(t *testing.T) {
	c := newCluster(t, crd("v1", "v1alpha1", "v1"))
	c.pages = [][]unstructured.Unstructured{{obj("a", "one", "11")}}
	calls := 0
	c.patch = func(clienttesting.PatchAction) error {
		if calls++; calls == 1 {
			return apierrors.NewConflict(pactosGVR.GroupResource(), "one", errors.New("stale"))
		}
		return nil
	}
	c.get = func(string) (*unstructured.Unstructured, error) { u := obj("a", "one", "12"); return &u, nil }
	if !migrator(c).migrateCRD(context.Background(), PactoTargets()[0]) {
		t.Fatal("pending after a conflict")
	}
	got := writes(c)
	if got[0] != `pactos a/one status {"metadata":{"resourceVersion":"11"}}` || got[1] != `pactos a/one status {"metadata":{"resourceVersion":"12"}}` {
		t.Fatalf("writes %q", got)
	}
}

func TestMigrateSkipsDeletedObject(t *testing.T) {
	c := newCluster(t, crd("v1", "v1alpha1", "v1"))
	c.pages = [][]unstructured.Unstructured{{obj("a", "gone", "11"), obj("a", "raced", "12"), obj("a", "kept", "13")}}
	c.patch = func(p clienttesting.PatchAction) error {
		switch p.GetName() {
		case "gone":
			return apierrors.NewNotFound(pactosGVR.GroupResource(), "gone")
		case "raced":
			return apierrors.NewConflict(pactosGVR.GroupResource(), "raced", errors.New("stale"))
		}
		return nil
	}
	c.PrependReactor("get", "pactos", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewNotFound(pactosGVR.GroupResource(), "raced")
	})
	m := migrator(c)
	lines := capture(m)
	if !m.migrateCRD(context.Background(), PactoTargets()[0]) {
		t.Fatal("a deleted object kept the target pending")
	}
	if !logged(lines, "rewrote 1 objects as v1") {
		t.Fatalf("log %q", *lines)
	}
}

func TestMigrateListErrorLeavesStoredVersions(t *testing.T) {
	c := newCluster(t, crd("v1", "v1alpha1", "v1"))
	c.listErr = apierrors.NewResourceExpired("continue token expired")
	m := migrator(c)
	lines := capture(m)
	if m.migrateCRD(context.Background(), PactoTargets()[0]) {
		t.Fatal("a list error marked the target done")
	}
	if len(storedPatches(c)) != 0 {
		t.Fatalf("storedVersions patched: %q", storedPatches(c))
	}
	if !logged(lines, "storage migration: "+pactosCRD+" incomplete after 0 objects: continue token expired; storedVersions unchanged") {
		t.Fatalf("log %q", *lines)
	}
}

func TestMigratePatchErrorLeavesStoredVersions(t *testing.T) {
	c := newCluster(t, crd("v1", "v1alpha1", "v1"))
	c.pages = [][]unstructured.Unstructured{{obj("a", "one", "11")}}
	c.patch = func(clienttesting.PatchAction) error {
		return apierrors.NewForbidden(pactosGVR.GroupResource(), "one", errors.New("no"))
	}
	if migrator(c).migrateCRD(context.Background(), PactoTargets()[0]) || len(storedPatches(c)) != 0 {
		t.Fatalf("a failed write marked the target done: %q", storedPatches(c))
	}
}

func TestMigrateInvalidObjectContinuesAndLeavesStoredVersions(t *testing.T) {
	c := newCluster(t, crd("v1", "v1alpha1", "v1"))
	c.pages = [][]unstructured.Unstructured{{obj("a", "one", "11"), obj("a", "legacy", "12"), obj("b", "three", "13")}}
	c.patch = func(p clienttesting.PatchAction) error {
		if p.GetName() == "legacy" {
			return apierrors.NewInvalid(schema.GroupKind{Group: "pacto.trianalab.io", Kind: "Pacto"}, "legacy", nil)
		}
		return nil
	}
	m := migrator(c)
	lines := capture(m)
	if m.migrateCRD(context.Background(), PactoTargets()[0]) {
		t.Fatal("an invalid object marked the target done")
	}
	if got := writes(c); len(got) != 3 || !strings.HasPrefix(got[2], "pactos b/three") {
		t.Fatalf("writes %q", got)
	}
	if !logged(lines, "storage migration: "+pactosCRD+" could not rewrite 1 objects ([a/legacy]); see MIGRATION.md") {
		t.Fatalf("log %q", *lines)
	}
}

func TestMigrateNamesAtMostTwentyFailures(t *testing.T) {
	c := newCluster(t, crd("v1", "v1alpha1", "v1"))
	page := make([]unstructured.Unstructured, 0, 25)
	for i := range 25 {
		page = append(page, obj("a", "o"+strconv.Itoa(i), "1"))
	}
	c.pages = [][]unstructured.Unstructured{page}
	c.patch = func(clienttesting.PatchAction) error {
		return apierrors.NewForbidden(pactosGVR.GroupResource(), "", errors.New("no"))
	}
	m := migrator(c)
	lines := capture(m)
	m.migrateCRD(context.Background(), PactoTargets()[0])
	if !logged(lines, "could not rewrite 25 objects") || logged(lines, "a/o20") || !logged(lines, "a/o19]") {
		t.Fatalf("log %q", *lines)
	}
}

func TestMigrateMissingCRDSkipped(t *testing.T) {
	c := newCluster(t, nil)
	if !migrator(c).migrateCRD(context.Background(), PactoTargets()[0]) || len(c.Actions()) != 1 {
		t.Fatalf("a missing CRD: %v", c.Actions())
	}
}

func TestMigrateCRDReadErrorRetries(t *testing.T) {
	c := newCluster(t, crd("v1", "v1alpha1", "v1"))
	c.crdGetErr = apierrors.NewServiceUnavailable("down")
	m := migrator(c)
	lines := capture(m)
	if m.migrateCRD(context.Background(), PactoTargets()[0]) || !logged(lines, pactosCRD+" could not be read: down; will retry") {
		t.Fatalf("log %q", *lines)
	}
}

func TestMigrateNothingStoredAsV1alpha1IsNoop(t *testing.T) {
	c := newCluster(t, crd("v1", "v1"))
	if !migrator(c).migrateCRD(context.Background(), PactoTargets()[0]) || len(c.Actions()) != 1 {
		t.Fatalf("no-op ran %v", c.Actions())
	}
}

func TestMigrateCRDNotYetV1Skipped(t *testing.T) {
	for _, old := range []*unstructured.Unstructured{crd("v1alpha1", "v1alpha1"), crd("none", "v1alpha1")} {
		c := newCluster(t, old)
		m := migrator(c)
		lines := capture(m)
		if m.migrateCRD(context.Background(), PactoTargets()[0]) || len(c.Actions()) != 1 {
			t.Fatalf("an old CRD: %v", c.Actions())
		}
		if !logged(lines, "storage migration: "+pactosCRD+" is not served with v1 as storage yet; will retry") {
			t.Fatalf("log %q", *lines)
		}
	}
}

func TestMigrateStoredVersionsConflictRetries(t *testing.T) {
	c := newCluster(t, crd("v1", "v1alpha1", "v1"))
	c.crdPatch = []error{apierrors.NewConflict(crdGVR.GroupResource(), pactosCRD, errors.New("stale"))}
	if !migrator(c).migrateCRD(context.Background(), PactoTargets()[0]) || len(storedPatches(c)) != 2 {
		t.Fatalf("stored patches %q", storedPatches(c))
	}
}

func TestMigrateStoredVersionsPatchErrorRetries(t *testing.T) {
	c := newCluster(t, crd("v1", "v1alpha1", "v1"))
	c.crdPatch = []error{apierrors.NewForbidden(crdGVR.GroupResource(), pactosCRD, errors.New("no"))}
	m := migrator(c)
	lines := capture(m)
	if m.migrateCRD(context.Background(), PactoTargets()[0]) || !logged(lines, "storedVersions could not be patched") {
		t.Fatalf("log %q", *lines)
	}
}

func TestNextStored(t *testing.T) {
	for _, tc := range []struct{ old, want []string }{
		{[]string{"v1alpha1", "v1"}, []string{"v1"}},
		{[]string{"v1alpha1"}, []string{"v1"}},
		{[]string{"v1"}, []string{"v1"}},
		{[]string{"v0", "v1alpha1", "v1"}, []string{"v0", "v1"}},
	} {
		if got := nextStored(tc.old, "v1alpha1", "v1"); !slices.Equal(got, tc.want) {
			t.Errorf("nextStored(%v) = %v, want %v", tc.old, got, tc.want)
		}
	}
}

func TestMigrateLogLine(t *testing.T) {
	c := newCluster(t, crd("v1", "v1alpha1", "v1"))
	c.pages = [][]unstructured.Unstructured{{obj("a", "one", "1"), obj("a", "two", "2")}}
	m := migrator(c)
	lines := capture(m)
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := `"msg"="storage migration: pactos.pacto.trianalab.io rewrote 2 objects as v1; storedVersions [v1alpha1 v1] -> [v1]"`
	if len(*lines) != 1 || !strings.Contains((*lines)[0], want) {
		t.Fatalf("log %q, want %s", *lines, want)
	}
}

func TestMigrateRetriesUntilCRDServesV1(t *testing.T) {
	c := newCluster(t, crd("v1alpha1", "v1alpha1"), crd("v1", "v1alpha1", "v1"))
	m := migrator(c)
	lines := capture(m)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if len(storedPatches(c)) != 1 || !logged(lines, "not served with v1 as storage yet") || !logged(lines, "storedVersions [v1alpha1 v1] -> [v1]") {
		t.Fatalf("log %q", *lines)
	}
}

func TestMigrateStopsOnContextCancel(t *testing.T) {
	c := newCluster(t, crd("v1alpha1", "v1alpha1"))
	m := migrator(c)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if len(storedPatches(c)) != 0 {
		t.Fatal("a cancelled migration patched storedVersions")
	}
}

func TestMigrateWaitsForStorageVersionHash(t *testing.T) {
	c := newCluster(t, crd("v1", "v1alpha1", "v1"))
	c.pages = [][]unstructured.Unstructured{{obj("a", "one", "1")}}
	m := migrator(c)
	old := storageHash("pacto.trianalab.io", "v1alpha1", "Pacto")
	var reads *int
	m.Discovery, reads = disco(func(read int) string {
		if read <= 2 || read == 4 {
			return old
		}
		return v1Hash(read)
	})
	c.discoRead = reads
	if !m.migrateCRD(context.Background(), PactoTargets()[0]) {
		t.Fatal("pending after the hash matched")
	}
	if c.firstAt != 7 {
		t.Fatalf("first object write after %d discovery reads, want 7 (3 consecutive v1 reads)", c.firstAt)
	}
}

func TestMigrateHashNeverMatchesLeavesStoredVersions(t *testing.T) {
	c := newCluster(t, crd("v1", "v1alpha1", "v1"))
	c.pages = [][]unstructured.Unstructured{{obj("a", "one", "1")}}
	m := migrator(c)
	m.HashTimeout = 20 * time.Millisecond
	d := &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}
	d.PrependReactor("get", "resource", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("discovery down")
	})
	m.Discovery = d
	lines := capture(m)
	if m.migrateCRD(context.Background(), PactoTargets()[0]) || len(writes(c)) != 0 {
		t.Fatalf("writes before the hash matched: %q", writes(c))
	}
	if !logged(lines, "discovery did not report v1 as the storage version") {
		t.Fatalf("log %q", *lines)
	}
	// A list without the resource never matches either.
	if hasHash(&metav1.APIResourceList{}, "pactos", v1Hash(0)) {
		t.Fatal("a missing resource matched")
	}
}

// TestStorageHash pins the hash a real apiserver (envtest, Kubernetes 1.37)
// reports in discovery for pactos stored as v1.
func TestStorageHash(t *testing.T) {
	if got := storageHash("pacto.trianalab.io", "v1", "Pacto"); got != pactoV1Hash {
		t.Fatalf("storageHash = %s, want %s", got, pactoV1Hash)
	}
}

func TestDefaults(t *testing.T) {
	m := &Migrator{Log: logr.Discard()}
	m.defaults()
	if m.Backoff.Duration != time.Minute || m.Backoff.Cap != 10*time.Minute ||
		m.HashPoll != 5*time.Second || m.HashReads != 3 || m.HashTimeout != 2*time.Minute {
		t.Fatalf("defaults %+v", m)
	}
}

func TestMigrateCRDGoneBeforeStoredPatchRetries(t *testing.T) {
	c := newCluster(t, crd("v1", "v1alpha1", "v1"), nil)
	if migrator(c).migrateCRD(context.Background(), PactoTargets()[0]) || len(storedPatches(c)) != 0 {
		t.Fatalf("stored patches %q", storedPatches(c))
	}
}
