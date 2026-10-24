package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// verify-public.sh only runs inside a real release, so a regression in how it
// picks repositories (a renamed .pacto.kind, .pacto.coordinate or
// .pacto.releaseUnit in release/units/*/package.json) would first show up as a
// release that silently checks nothing. Its list-only mode is offline, so PR CI
// asserts the selection here.

func verifyPublic(t *testing.T, unitsDir, units string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", filepath.Join(repoRoot(t), "release", "orchestrator", "verify-public.sh"), unitsDir)
	cmd.Env = append(os.Environ(), "UNITS="+units, "PACTO_VERIFY_PUBLIC_LIST_ONLY=1")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestVerifyPublicSelectsEveryGHCRRepository(t *testing.T) {
	// Every release unit, so the selection is decided by kind alone.
	all := `["dashboard-contract-bundle","delivery-chart","delivery-image","demo-bundles",` +
		`"demo-compose","k8s-docs","k8s-module","operator-chart","operator-image","cli","core","dashboard-image"]`
	out, err := verifyPublic(t, filepath.Join(repoRoot(t), "release", "units"), all)
	if err != nil {
		t.Fatalf("verify-public.sh list mode: %v\n%s", err, out)
	}
	// ponytail: hard-coded on purpose. Deriving it from the same package.json
	// fields would agree with a rename instead of catching it. A new oci-image
	// or helm-chart unit belongs here too.
	want := strings.Join([]string{
		"dashboard-contract-bundle trianalab/pacto/dashboard-contract",
		"delivery-chart trianalab/pacto/charts/pacto-delivery",
		"delivery-image trianalab/pacto/delivery",
		"demo-compose trianalab/pacto/demo",
		"operator-chart trianalab/pacto/charts/pacto-operator",
		"operator-image trianalab/pacto/operator",
		"dashboard-image trianalab/pacto/dashboard",
	}, "\n")
	if got := strings.TrimSpace(out); got != want {
		t.Fatalf("verify-public.sh would query:\n%s\nwant:\n%s", got, want)
	}
}

func TestVerifyPublicRefusesAnEmptyCoordinate(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	pkg := `{"pacto":{"releaseUnit":"x","kind":"oci-image","coordinate":""}}`
	if err := os.WriteFile(filepath.Join(dir, "x", "package.json"), []byte(pkg), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := verifyPublic(t, dir, `["x"]`)
	if err == nil || !strings.Contains(out, "has no .pacto.coordinate") {
		t.Fatalf("want a failure naming the empty coordinate, got err=%v\n%s", err, out)
	}
}
