package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func TestInstallationConfigNilWithoutRef(t *testing.T) {
	t.Parallel()
	if f := detectedService(t).InstallationConfig(""); f != nil {
		t.Error("an empty ref reads a detection")
	}
}

func TestInstallationConfigReadsDetectionFile(t *testing.T) {
	t.Parallel()
	b, err := json.Marshal(detect.Result{SchemaVersion: detect.SchemaVersion, AsOf: detectedNow, Cluster: "uid-1",
		Installation: &detect.Installation{Profile: []string{"hardened"}}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "detected.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	d, st := detectedService(t).InstallationConfig(path)(context.Background())
	if st.Status != fleet.SourceAvailable || d == nil || !slices.Equal(d.Profile, []string{"hardened"}) || d.ClusterID != "uid-1" || !d.AsOf.Equal(detectedNow) {
		t.Errorf("declared %+v state %+v", d, st)
	}
	// The host path never reaches a caller.
	if strings.Contains(d.Source, path) || d.Source != "the operator's installation block in a detection file" {
		t.Errorf("source = %q", d.Source)
	}
}

func TestInstallationConfigUnavailableState(t *testing.T) {
	t.Parallel()
	d, st := detectedService(t).InstallationConfig(filepath.Join(t.TempDir(), "missing.json"))(context.Background())
	if d != nil || st.Status != fleet.SourceUnavailable || st.Error == nil {
		t.Errorf("declared %+v state %+v", d, st)
	}
}

// A k8s: ref names no host path, so the answer may name it.
func TestInstallationConfigNamesK8sRef(t *testing.T) {
	t.Parallel()
	b, err := json.Marshal(detect.Result{SchemaVersion: detect.SchemaVersion, AsOf: detectedNow, Cluster: "uid-1",
		Installation: &detect.Installation{Profile: []string{"idp"}}})
	if err != nil {
		t.Fatal(err)
	}
	cm := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "pacto-operator-detected", "namespace": "pacto-system", "labels": map[string]any{detect.ManagedByLabel: detect.ManagedBy}},
		"data":     map[string]any{detect.ConfigMapKey: string(b)}}}
	cm.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: detect.FieldManager, FieldsV1: metav1.NewFieldsV1(`{"f:data":{"f:` + detect.ConfigMapKey + `":{}}}`)}})
	svc := detectedService(t)
	svc.sys.newDynamic = func() (dynamic.Interface, error) {
		return dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), cm), nil
	}
	d, st := svc.InstallationConfig("k8s:pacto-system/pacto-operator-detected")(context.Background())
	if st.Status != fleet.SourceAvailable || d == nil || d.Source != "the operator's installation block in k8s:pacto-system/pacto-operator-detected" {
		t.Errorf("declared %+v state %+v", d, st)
	}
}
