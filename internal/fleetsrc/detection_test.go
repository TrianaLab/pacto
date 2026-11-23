package fleetsrc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

var detectionNow = time.Date(2026, 11, 16, 9, 5, 0, 0, time.UTC)

func clockAt(t time.Time) func() time.Time { return func() time.Time { return t } }

// detectionJSON is a Result dated asOf whose every entry has completeness c.
func detectionJSON(t *testing.T, asOf time.Time, c string) []byte {
	t.Helper()
	b, err := json.Marshal(detect.Result{SchemaVersion: detect.SchemaVersion, AsOf: asOf, Entries: []detect.Entry{
		{Kind: "cloud", Value: "GKE", Completeness: c, Scope: fleet.ScopeCluster},
		{Kind: "nodes", Value: "kernel 6.1", Completeness: c},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeDetection(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "detected.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func collectDetection(t *testing.T, s *DetectionSource) *fleet.Collection {
	t.Helper()
	col, err := s.Collect(context.Background())
	if err != nil || col == nil || !col.Advisory || col.State == nil {
		t.Fatalf("Collect = %+v, %v; want an advisory collection with a state", col, err)
	}
	return col
}

func assertUnavailable(t *testing.T, col *fleet.Collection, code string) {
	t.Helper()
	if col.State.Status != fleet.SourceUnavailable || col.State.Error == nil || col.State.Error.Code != code || len(col.Providers) != 0 {
		t.Errorf("state = %+v error %+v rows %d, want unavailable %s", col.State, col.State.Error, len(col.Providers), code)
	}
}

func TestDetectionFileSource(t *testing.T) {
	asOf := detectionNow.Add(-time.Minute)
	s := NewDetectionFileSource("detection", writeDetection(t, detectionJSON(t, asOf, detect.Complete)), clockAt(detectionNow))
	if s.ID() != "detection" || s.Kind() != fleet.SourceKindDetection {
		t.Errorf("id %q kind %q", s.ID(), s.Kind())
	}
	col := collectDetection(t, s)
	if col.State.Status != fleet.SourceAvailable || !col.State.ObservedAt.Equal(asOf) || len(col.Providers) != 2 || col.Providers[0].Provenance != fleet.ProvenanceInferred {
		t.Errorf("collection = %+v", col)
	}
}

func TestDetectionStaleAfter(t *testing.T) {
	s := NewDetectionFileSource("detection", writeDetection(t, detectionJSON(t, detectionNow.Add(-detect.StaleAfter-time.Second), detect.Complete)), clockAt(detectionNow))
	if col := collectDetection(t, s); col.State.Status != fleet.SourceStale {
		t.Errorf("status = %s, want stale", col.State.Status)
	}
}

func TestDetectionFileMissingIsUnavailable(t *testing.T) {
	col := collectDetection(t, NewDetectionFileSource("detection", filepath.Join(t.TempDir(), "missing.json"), clockAt(detectionNow)))
	assertUnavailable(t, col, "NOT_FOUND")
}

func TestDetectionFileInvalidIsUnavailable(t *testing.T) {
	assertUnavailable(t, collectDetection(t, NewDetectionFileSource("detection", writeDetection(t, []byte("{")), clockAt(detectionNow))), "INVALID")
	// A directory reads as an error that is neither missing nor forbidden.
	assertUnavailable(t, collectDetection(t, NewDetectionFileSource("detection", t.TempDir(), clockAt(detectionNow))), "UNAVAILABLE")
}

func TestDetectionFutureAsOfIsInvalid(t *testing.T) {
	future := NewDetectionFileSource("detection", writeDetection(t, detectionJSON(t, detectionNow.Add(detect.MaxFutureSkew+time.Second), detect.Complete)), clockAt(detectionNow))
	assertUnavailable(t, collectDetection(t, future), "INVALID")
	skewed := NewDetectionFileSource("detection", writeDetection(t, detectionJSON(t, detectionNow.Add(detect.MaxFutureSkew), detect.Complete)), clockAt(detectionNow))
	if col := collectDetection(t, skewed); col.State.Status != fleet.SourceAvailable {
		t.Errorf("an asOf within the skew must read: %+v", col.State)
	}
}

// detectionConfigMap is the ConfigMap the operator writes, with the label
// and the managed field when trusted.
func detectionConfigMap(data []byte, labelled bool, manager string) *unstructured.Unstructured {
	cm := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "pacto-operator-detected", "namespace": "pacto-system"},
		"data":     map[string]any{detect.ConfigMapKey: string(data)}}}
	if labelled {
		cm.SetLabels(map[string]string{detect.ManagedByLabel: detect.ManagedBy})
	}
	cm.SetManagedFields([]metav1.ManagedFieldsEntry{
		{Manager: "kubectl", FieldsV1: metav1.NewFieldsV1(`{"f:metadata":{}}`)},
		{Manager: manager},
		{Manager: manager, FieldsV1: metav1.NewFieldsV1(`{"f:data":{"f:detected.json":{}}}`)},
	})
	return cm
}

func k8sDetection(objs ...runtime.Object) *DetectionSource {
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{{Version: "v1", Resource: "configmaps"}: "ConfigMapList"}, objs...)
	return NewDetectionK8sSource("detection", dyn, "pacto-system", "pacto-operator-detected", clockAt(detectionNow))
}

func TestDetectionK8sConfigMap(t *testing.T) {
	col := collectDetection(t, k8sDetection(detectionConfigMap(detectionJSON(t, detectionNow, detect.Complete), true, detect.FieldManager)))
	if col.State.Status != fleet.SourceAvailable || len(col.Providers) != 2 {
		t.Errorf("collection = %+v", col)
	}
}

func TestDetectionK8sForeignWriterIsUnavailable(t *testing.T) {
	data := detectionJSON(t, detectionNow, detect.Complete)
	assertUnavailable(t, collectDetection(t, k8sDetection(detectionConfigMap(data, false, detect.FieldManager))), "UNTRUSTED")
	assertUnavailable(t, collectDetection(t, k8sDetection(detectionConfigMap(data, true, "kubectl-edit"))), "UNTRUSTED")
}

func TestDetectionK8sForbiddenIsUnavailable(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	dyn.PrependReactor("get", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "configmaps"}, "pacto-operator-detected", errors.New("no"))
	})
	col := collectDetection(t, NewDetectionK8sSource("detection", dyn, "pacto-system", "pacto-operator-detected", clockAt(detectionNow)))
	assertUnavailable(t, col, "AUTH_FAILED")
}

func TestDetectionK8sNotFoundIsUnavailable(t *testing.T) {
	assertUnavailable(t, collectDetection(t, k8sDetection()), "NOT_FOUND")
}

func TestDetectionFailedSource(t *testing.T) {
	s := NewDetectionFailedSource("detection", errors.New("loading kubeconfig: no configuration"))
	assertUnavailable(t, collectDetection(t, s), "UNAVAILABLE")
	noCluster := NewDetectionFailedSource("detection", fmt.Errorf("%w: loading kubeconfig", ErrDetectionNoCluster))
	assertUnavailable(t, collectDetection(t, noCluster), "NO_CLUSTER")
}

func TestDetectionFilePermissionIsForbidden(t *testing.T) {
	s := NewDetectionFailedSource("detection", os.ErrPermission)
	assertUnavailable(t, collectDetection(t, s), "AUTH_FAILED")
}

// TestDetectionCollectIsAlwaysAdvisory: every outcome, good or bad, is an
// advisory collection and never an error.
func TestDetectionCollectIsAlwaysAdvisory(t *testing.T) {
	for _, s := range []*DetectionSource{
		NewDetectionFileSource("d", writeDetection(t, detectionJSON(t, detectionNow, detect.Complete)), clockAt(detectionNow)),
		NewDetectionFileSource("d", "/nonexistent/detected.json", clockAt(detectionNow)),
		NewDetectionFailedSource("d", errors.New("x")),
		k8sDetection(),
	} {
		collectDetection(t, s)
	}
}

// TestDetectionSourceStatusIgnoresRowCompleteness: every row partial, the
// source still available and the snapshot complete.
func TestDetectionSourceStatusIgnoresRowCompleteness(t *testing.T) {
	s := NewDetectionFileSource("detection", writeDetection(t, detectionJSON(t, detectionNow, detect.Partial)), clockAt(detectionNow))
	col := collectDetection(t, s)
	if col.State.Status != fleet.SourceAvailable {
		t.Fatalf("status = %s", col.State.Status)
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: clockAt(detectionNow)},
		fleet.NewMemorySource("local", "local", &fleet.Collection{Targets: []fleet.RawTarget{{Scope: "shop", Kind: "kubernetes", Name: "web", Service: "web"}}}), s)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Completeness != fleet.CompletenessComplete {
		t.Errorf("completeness = %s", snap.Completeness)
	}
	for _, p := range snap.Providers {
		if p.Status != fleet.SourcePartial {
			t.Errorf("row %s = %s, want partial", p.ID, p.Status)
		}
	}
}

// TestDetectionResultSourceMatchesCollect: a Result handed to the source
// reports what reading the same Result does, fresh or stale.
func TestDetectionResultSourceMatchesCollect(t *testing.T) {
	for _, asOf := range []time.Time{detectionNow.Add(-time.Minute), detectionNow.Add(-detect.StaleAfter - time.Minute)} {
		data := detectionJSON(t, asOf, detect.Partial)
		read := collectDetection(t, NewDetectionFileSource("detection", writeDetection(t, data), clockAt(detectionNow)))
		res, err := detect.Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		fixed := collectDetection(t, NewDetectionResultSource("detection", res, clockAt(detectionNow)))
		if !reflect.DeepEqual(read, fixed) {
			t.Fatalf("result source = %+v, want %+v", fixed, read)
		}
	}
}
