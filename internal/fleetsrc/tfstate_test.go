package fleetsrc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func TestTerraformStateSource(t *testing.T) {
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	src := NewTerraformStateSource("tf-state", "../../pkg/managedres/testdata/state-v4.json", externalcatalog.Default(), clock)
	if src.ID() != "tf-state" || src.Kind() != "terraform" {
		t.Fatalf("id %q kind %q", src.ID(), src.Kind())
	}
	col, err := src.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(col.ManagedResources) != 4 {
		t.Fatalf("resources %+v", col.ManagedResources)
	}
	r := col.ManagedResources[0]
	if r.Identity != "aws/rds/123456789012/eu-west-1/orders-db" || r.Location != "state-v4.json" || r.Cluster != "" || !r.ObservedAt.Equal(now) {
		t.Errorf("resource %+v", r)
	}

	snap, err := fleet.Build(t.Context(), fleet.BuildOptions{Now: clock}, src)
	if err != nil {
		t.Fatal(err)
	}
	if rec := snap.Externals[fleet.ExternalKey(r.Identity)]; rec == nil || len(rec.Resources) != 1 {
		t.Fatalf("external record %+v", rec)
	}

}

func TestTerraformStateSourceErrors(t *testing.T) {
	clock := func() time.Time { return time.Time{} }
	for name, data := range map[string]string{"missing": "", "invalid": `{"version":3}`} {
		path := filepath.Join(t.TempDir(), "s.tfstate")
		if data != "" {
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := NewTerraformStateSource("x", path, externalcatalog.Default(), clock).Collect(t.Context()); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := NewTerraformStateSource("x", "../../pkg/managedres/testdata/state-v4.json", externalcatalog.Default(), clock).Collect(ctx); err == nil {
		t.Error("a cancelled collect read")
	}
}
