package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/internal/ci"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// advertisement writes a detection file dated asOf whose installation names
// registry (none when empty) and returns its path.
func advertisement(t *testing.T, asOf time.Time, registry string) string {
	t.Helper()
	res := detect.Result{SchemaVersion: detect.SchemaVersion, AsOf: asOf}
	if registry != "" {
		res.Installation = &detect.Installation{Registry: registry}
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "detected.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// advertisedPlan plans one service with reg and the --detected ref.
func advertisedPlan(t *testing.T, reg publish.Registry, detected string) *ci.Plan {
	t.Helper()
	root := t.TempDir()
	writeCIFile(t, root, "reports/pacto.yaml", reportsV1)
	svc := &Service{sys: sysDeps{publishRegistry: func() publish.Registry { return reg }}}
	p, _, err := svc.CIPlan(context.Background(), CIOptions{Root: root, Fleet: FleetOptions{Detected: detected}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCIPrintsAdvertisedSource(t *testing.T) {
	path := advertisement(t, time.Now(), "oci://europe-docker.pkg.dev/acme/pacto/")
	p := advertisedPlan(t, &fakeCIRegistry{tags: noTags}, path)
	if want := "europe-docker.pkg.dev/acme/pacto (from the installation advertised at " + path + ")"; p.Registry.String() != want {
		t.Fatalf("registry = %q, want %q", p.Registry, want)
	}
	var out strings.Builder
	ci.Text(&out, p)
	if !strings.Contains(out.String(), "(from the installation advertised at ") {
		t.Errorf("text lacks the advertised source:\n%s", out.String())
	}
}

func TestCIContactsAdvertisedRegistry(t *testing.T) {
	reg := &fakeCIRegistry{tags: noTags}
	p := advertisedPlan(t, reg, advertisement(t, time.Now(), "r.example/acme"))
	if allowedHost(p.Registry) != "r.example" || !slices.Contains(reg.calls, "tags r.example/acme/reports") {
		t.Fatalf("registry %+v, calls %v", p.Registry, reg.calls)
	}
}

func TestCIIgnoresStaleAdvertisement(t *testing.T) {
	reg := &fakeCIRegistry{tags: noTags}
	p := advertisedPlan(t, reg, advertisement(t, time.Now().Add(-time.Hour), "r.example/acme"))
	if p.Registry.Value != "" || allowedHost(p.Registry) != "" || len(reg.calls) != 0 || !slices.Contains(p.Notes, "installation advertisement stale: not used") {
		t.Fatalf("registry %+v, calls %v, notes %q", p.Registry, reg.calls, p.Notes)
	}
}

func TestCIIgnoresUnavailableAdvertisement(t *testing.T) {
	for path, note := range map[string]string{
		filepath.Join(t.TempDir(), "missing.json"):      "installation advertisement unavailable: not used",
		advertisement(t, time.Now(), ""):                "installation advertisement absent: not used",
		advertisement(t, time.Now(), "https://r.x/a?b"): "installation advertisement invalid: not used",
	} {
		reg := &fakeCIRegistry{tags: noTags}
		p := advertisedPlan(t, reg, path)
		if p.Registry.Value != "" || allowedHost(p.Registry) != "" || len(reg.calls) != 0 || !slices.Contains(p.Notes, note) {
			t.Errorf("%s: registry %+v, calls %v, notes %q", path, p.Registry, reg.calls, p.Notes)
		}
	}
}
