package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	ggcrremote "github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/trianalab/pacto/v4/internal/ci"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// writeCIFile writes data to root/rel, creating its folders.
func writeCIFile(t *testing.T, root, rel, data string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ciSkeletonPlan plans a repository with a service whose compose file names
// an image, one before pactoVersion 2.5, one unloadable folder and one the
// root .pactoignore hides.
func ciSkeletonPlan(t *testing.T) (*ci.Plan, map[string]*impact.Result) {
	t.Helper()
	root := t.TempDir()
	writeCIFile(t, root, "services/payments/pacto.yaml", "service:\n  name: payments\n")
	writeCIFile(t, root, "services/payments/compose.yaml", "services:\n  payments:\n    image: ghcr.io/acme/payments:1.0\n")
	writeCIFile(t, root, "services/old/pacto.yaml", "pactoVersion: \"2.0\"\nservice:\n  name: old\n  version: 1.0.0\n")
	writeCIFile(t, root, "services/broken/pacto.yaml", "service: [\n")
	writeCIFile(t, root, "services/legacy/pacto.yaml", "service:\n  name: legacy\n")
	writeCIFile(t, root, ".pactoignore", "services/legacy/\n")
	p, results, err := NewService(nil, nil).CIPlan(context.Background(), CIOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	return p, results
}

func TestCIPlanSkeleton(t *testing.T) {
	t.Parallel()
	p, results := ciSkeletonPlan(t)
	if results != nil {
		t.Fatalf("results = %v, want nil until impact runs", results)
	}
	if len(p.Rows) != 3 || p.Rows[0].Dir != "services/broken" || p.Rows[1].Service != "old" || p.Rows[2].Service != "payments" {
		t.Fatalf("rows = %+v", p.Rows)
	}
	if len(p.Rows[0].Errors) == 0 || p.Rows[0].Service != "" {
		t.Fatalf("broken row = %+v", p.Rows[0])
	}
	if p.Rows[1].Inferred != 0 || p.Rows[2].Inferred == 0 {
		t.Fatalf("inferred counts = %d, %d", p.Rows[1].Inferred, p.Rows[2].Inferred)
	}
	if p.Exit != 1 || p.Rows[2].Impact.Text != "unverified (registry inferred from images; pass --registry)" {
		t.Fatalf("exit %d, impact %+v", p.Exit, p.Rows[2].Impact)
	}
}

func TestCISkippedFromPactoignore(t *testing.T) {
	t.Parallel()
	p, _ := ciSkeletonPlan(t)
	if len(p.Skipped) != 1 || p.Skipped[0].Rel != "services/legacy" || p.Skipped[0].Rule != ".pactoignore:1" {
		t.Fatalf("skipped = %+v", p.Skipped)
	}
}

func TestCISkippedPastDepthLimit(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeCIFile(t, root, "a/pacto.yaml", "service:\n  name: a\n")
	writeCIFile(t, root, "deep/1/2/3/4/5/6/7/8/pacto.yaml", "service:\n  name: deep\n")
	p, _, err := NewService(nil, nil).CIPlan(context.Background(), CIOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	ci.Text(&out, p)
	if len(p.Rows) != 1 || !strings.Contains(out.String(), "skipped: deep/1/2/3/4/5/6/7/8 (from depth limit 8)\n") {
		t.Fatalf("rows %+v\n%s", p.Rows, out.String())
	}
}

func TestCIPlanSkeletonRegistryFromImages(t *testing.T) {
	t.Parallel()
	p, _ := ciSkeletonPlan(t)
	if p.Registry.Value != "ghcr.io/acme" || p.Registry.Source != "image references in services/payments/compose.yaml:3" {
		t.Fatalf("registry = %+v", p.Registry)
	}
}

func TestCIPlanRegistryFlagAndContext(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeCIFile(t, root, "pacto.yaml", "service:\n  name: root\n")
	env := map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_BASE_REF": "main"}
	p, _, err := NewService(nil, nil).CIPlan(context.Background(), CIOptions{Root: root, Registry: "r.example/acme", Getenv: func(k string) string { return env[k] }})
	if err != nil {
		t.Fatal(err)
	}
	if p.Platform != "github" || p.Base.Value != "main" || p.Registry.Source != "--registry" || len(p.Rows) != 1 || p.Rows[0].Dir != "." {
		t.Fatalf("plan = %+v", p)
	}
}

func TestCIPlanRootMissing(t *testing.T) {
	t.Parallel()
	if _, _, err := NewService(nil, nil).CIPlan(context.Background(), CIOptions{Root: filepath.Join(t.TempDir(), "nope")}); err == nil {
		t.Fatal("a missing root must fail the run")
	}
}

// TestCIUnchangedAcrossTypedPush: pacto ci compares content, so a baseline an
// older Pacto pushed untyped still reads unchanged against the same folder,
// even though a push would now write another manifest digest.
func TestCIUnchangedAcrossTypedPush(t *testing.T) {
	t.Parallel()
	ir := newIndexReg(t)
	svc, dir := pushIndexService(ir), writeTestBundle(t)
	local, err := svc.ResolveBundle(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	img, _ := oci.LegacyBundleImage(local)
	ref, _ := name.ParseReference(ir.host+"/acme/svc:1.0.0", name.Insecure)
	if err := ggcrremote.Write(ref, img); err != nil {
		t.Fatal(err)
	}
	base, err := svc.BundleStore.Pull(t.Context(), ir.host+"/acme/svc:1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	ds, _ := oci.BundleDigests(local)
	if ds.Typed == ds.Legacy {
		t.Fatal("the typed and legacy digests are equal")
	}
	if same, why := Unchanged(base, local); !same {
		t.Fatalf("a legacy baseline of the same content reads changed: %s", why)
	}
}
