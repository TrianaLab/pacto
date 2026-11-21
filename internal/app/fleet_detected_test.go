package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

var detectedNow = time.Date(2026, 11, 16, 9, 1, 0, 0, time.UTC)

func detectedJSON(t *testing.T) []byte {
	t.Helper()
	b, err := json.Marshal(detect.Result{SchemaVersion: detect.SchemaVersion, AsOf: detectedNow, Entries: []detect.Entry{{Kind: "cloud", Value: "kind", Completeness: detect.Complete}}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func detectedService(t *testing.T) *Service {
	t.Helper()
	svc := NewService(nil, nil)
	svc.sys.now = func() time.Time { return detectedNow }
	return svc
}

// detectionState is the detection source of snap and its rows.
func detectionState(t *testing.T, snap *fleet.FleetSnapshot) (fleet.SourceState, []fleet.Provider) {
	t.Helper()
	i := slices.IndexFunc(snap.Sources, func(s fleet.SourceState) bool { return s.Kind == fleet.SourceKindDetection })
	if i < 0 {
		t.Fatalf("no detection source in %+v", snap.Sources)
	}
	return snap.Sources[i], snap.Providers
}

func TestFleetOptionsDetectedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "detected.json")
	if err := os.WriteFile(path, detectedJSON(t), 0o600); err != nil {
		t.Fatal(err)
	}
	snap, err := detectedService(t).Fleet(context.Background(), FleetOptions{Detected: path, Now: func() time.Time { return detectedNow }})
	if err != nil {
		t.Fatal(err)
	}
	st, rows := detectionState(t, snap)
	if st.ID != "detection" || st.Status != fleet.SourceAvailable || !st.Advisory || len(rows) != 1 || rows[0].Provenance != fleet.ProvenanceInferred {
		t.Errorf("state %+v rows %+v", st, rows)
	}
}

func TestFleetOptionsDetectedK8s(t *testing.T) {
	cm := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "pacto-operator-detected", "namespace": "pacto-system", "labels": map[string]any{detect.ManagedByLabel: detect.ManagedBy}},
		"data":     map[string]any{detect.ConfigMapKey: string(detectedJSON(t))}}}
	cm.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: detect.FieldManager, FieldsV1: metav1.NewFieldsV1(`{"f:data":{}}`)}})
	svc := detectedService(t)
	svc.sys.newDynamic = func() (dynamic.Interface, error) {
		return dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), cm), nil
	}
	snap, err := svc.Fleet(context.Background(), FleetOptions{Detected: "k8s:pacto-system/pacto-operator-detected"})
	if err != nil {
		t.Fatal(err)
	}
	if st, rows := detectionState(t, snap); st.Status != fleet.SourceAvailable || len(rows) != 1 {
		t.Errorf("state %+v rows %+v", st, rows)
	}
}

func TestFleetOptionsDetectedK8sClientError(t *testing.T) {
	svc := detectedService(t)
	svc.sys.newDynamic = func() (dynamic.Interface, error) { return nil, errors.New("loading kubeconfig: none") }
	snap, err := svc.Fleet(context.Background(), FleetOptions{Detected: "k8s:pacto-system/pacto-operator-detected"})
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := detectionState(t, snap); st.Status != fleet.SourceUnavailable || !st.Advisory || st.Error == nil || st.Error.Code != "NO_CLUSTER" ||
		snap.Completeness == fleet.CompletenessPartial {
		t.Errorf("state %+v completeness %s", st, snap.Completeness)
	}
}

// TestCheckDetectedRef: a k8s: ref that names no single ConfigMap is a usage
// error, never an unavailable source; a file path passes.
func TestCheckDetectedRef(t *testing.T) {
	for _, ref := range []string{"k8s:pacto-system", "k8s:/name", "k8s:ns/", "k8s:ns/a/b"} {
		if err := CheckDetectedRef(ref); err == nil || !strings.Contains(err.Error(), "k8s:<namespace>/<name>") {
			t.Errorf("%s: err = %v", ref, err)
		}
		if _, err := detectedService(t).Fleet(context.Background(), FleetOptions{Detected: ref}); err == nil {
			t.Errorf("%s: Fleet accepted it", ref)
		}
	}
	for _, ref := range []string{"", "detected.json", "k8s:pacto-system/pacto-operator-detected"} {
		if err := CheckDetectedRef(ref); err != nil {
			t.Errorf("%s: err = %v", ref, err)
		}
	}
}

func TestFleetOptionsNoDetectedNoSource(t *testing.T) {
	svc := detectedService(t)
	svc.sys.newK8s = func(string) (k8sclient.K8sClient, error) { return nil, errors.New("no cluster") }
	svc.sys.kubeContext = func() string { return "" }
	snap, err := svc.Fleet(context.Background(), FleetOptions{IncludeK8s: true})
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(snap.Sources, func(s fleet.SourceState) bool { return s.Kind == fleet.SourceKindDetection }) {
		t.Errorf("--k8s alone added a detection source: %+v", snap.Sources)
	}
}
