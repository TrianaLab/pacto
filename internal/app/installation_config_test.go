package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func TestInstallationConfigNilWithoutRef(t *testing.T) {
	if f := detectedService(t).InstallationConfig(""); f != nil {
		t.Error("an empty ref reads a detection")
	}
}

func TestInstallationConfigReadsDetectionFile(t *testing.T) {
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
}

func TestInstallationConfigUnavailableState(t *testing.T) {
	d, st := detectedService(t).InstallationConfig(filepath.Join(t.TempDir(), "missing.json"))(context.Background())
	if d != nil || st.Status != fleet.SourceUnavailable || st.Error == nil {
		t.Errorf("declared %+v state %+v", d, st)
	}
}
