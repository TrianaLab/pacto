package fleetsrc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/trianalab/pacto/v4/pkg/detect"
)

// clustersJSON is a clusters.json dated asOf with one candidate.
func clustersJSON(t *testing.T, asOf time.Time) string {
	t.Helper()
	b, err := detect.EncodeClusters(detect.Clusters{AsOf: asOf, Candidates: []detect.Candidate{{Source: detect.SourceCAPI, Kind: "Cluster", Name: "eu"}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// clustersReader reads the detection ConfigMap holding data under the
// clusters key, owned by manager.
func clustersReader(t *testing.T, data map[string]string, manager string) func() (detect.Clusters, error) {
	cm := detectionConfigMap(nil, true, detect.FieldManager)
	d := map[string]any{}
	for k, v := range data {
		d[k] = v
	}
	cm.Object["data"] = d
	cm.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: manager, FieldsV1: metav1.NewFieldsV1(`{"f:data":{"f:clusters.json":{}}}`)}})
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{{Version: "v1", Resource: "configmaps"}: "ConfigMapList"}, cm)
	read := ClustersReader(dyn, "k8s:pacto-system/pacto-operator-detected", clockAt(detectionNow))
	return func() (detect.Clusters, error) { return read(t.Context()) }
}

func TestClustersReaderK8s(t *testing.T) {
	c, err := clustersReader(t, map[string]string{detect.ClustersConfigMapKey: clustersJSON(t, detectionNow)}, detect.FieldManager)()
	if err != nil || len(c.Candidates) != 1 || c.Candidates[0].Name != "eu" {
		t.Errorf("clusters = %+v %v", c, err)
	}
	if _, err := clustersReader(t, map[string]string{detect.ClustersConfigMapKey: clustersJSON(t, detectionNow)}, "kubectl-edit")(); err == nil ||
		err.Error() != "not written by the Pacto operator" {
		t.Errorf("foreign = %v", err)
	}
	future := clustersJSON(t, detectionNow.Add(detect.MaxFutureSkew+time.Second))
	if _, err := clustersReader(t, map[string]string{detect.ClustersConfigMapKey: future}, detect.FieldManager)(); err == nil || !strings.HasPrefix(err.Error(), "invalid") {
		t.Errorf("future = %v", err)
	}
	if _, err := clustersReader(t, map[string]string{detect.ClustersConfigMapKey: "{"}, detect.FieldManager)(); err == nil || !strings.HasPrefix(err.Error(), "invalid") {
		t.Errorf("unreadable = %v", err)
	}
}

// TestNoClustersKeyReadsAsNone: an operator that predates candidates, or
// runs no inventory, writes no clusters.json: no candidates and no error.
func TestNoClustersKeyReadsAsNone(t *testing.T) {
	for name, manager := range map[string]string{"never written": "other", "written before": detect.FieldManager} {
		if c, err := clustersReader(t, map[string]string{detect.ConfigMapKey: "{}"}, manager)(); err != nil || !c.AsOf.IsZero() {
			t.Errorf("%s: %+v %v", name, c, err)
		}
	}
	dir := t.TempDir()
	read := ClustersReader(nil, filepath.Join(dir, "detected.json"), clockAt(detectionNow))
	if c, err := read(t.Context()); err != nil || !c.AsOf.IsZero() {
		t.Errorf("no file beside: %+v %v", c, err)
	}
	if err := os.WriteFile(filepath.Join(dir, detect.ClustersConfigMapKey), []byte(clustersJSON(t, detectionNow)), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := read(t.Context()); err != nil || len(c.Candidates) != 1 {
		t.Errorf("file beside: %+v %v", c, err)
	}
}
