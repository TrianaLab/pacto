package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/internal/app"
)

// exportBundle writes a minimal bundle of service name under dir.
func exportBundle(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "pactoVersion: \"2.0\"\nservice:\n  name: " + name + "\n  version: \"1.0.0\"\n  owner:\n    team: pay\n"
	if err := os.WriteFile(filepath.Join(p, "pacto.yaml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// execExport runs pacto with a pinned build clock and returns stdout, stderr
// and the error.
func execExport(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	return execExportTo(t, &bytes.Buffer{}, args...)
}

func execExportTo(t *testing.T, out interface {
	Write([]byte) (int, error)
	String() string
}, args ...string,
) (string, string, error) {
	t.Helper()
	t.Setenv("PACTO_NO_UPDATE_CHECK", "1")
	prev := fleetNow
	fleetNow = func() time.Time { return time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { fleetNow = prev })
	root := NewRootCommand(app.NewService(nil, nil), VersionInfo{Version: "test"})
	root.SetArgs(append([]string{"export", "backstage"}, args...))
	var errb bytes.Buffer
	root.SetOut(out)
	root.SetErr(&errb)
	err := root.Execute()
	return out.String(), errb.String(), err
}

func exportExitCode(err error) int {
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return -1
}

func TestExportBackstageStdout(t *testing.T) {
	out, _, err := execExport(t, "--local", exportBundle(t, t.TempDir(), "pay"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"kind: Component", "name: pay", "owner: group:default/pay", `pacto.trianalab.io/as-of: "2026-11-16T12:00:00Z"`} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
}

func TestExportBackstageOutputFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "entities.yaml")
	out, _, err := execExport(t, "--local", exportBundle(t, t.TempDir(), "pay"), "-o", file)
	if err != nil || out != "" {
		t.Fatalf("err %v, stdout %q", err, out)
	}
	if b, err := os.ReadFile(file); err != nil || !strings.Contains(string(b), "name: pay") {
		t.Errorf("file %q, %v", b, err)
	}
	if _, _, err := execExport(t, "--local", exportBundle(t, t.TempDir(), "pay"), "-o", t.TempDir()); err == nil {
		t.Error("writing over a directory reported no error")
	}
}

type failingOut struct{ bytes.Buffer }

func (*failingOut) Write([]byte) (int, error) { return 0, errors.New("closed pipe") }

func TestExportBackstageStdoutWriteError(t *testing.T) {
	if _, _, err := execExportTo(t, &failingOut{}, "--local", exportBundle(t, t.TempDir(), "pay")); err == nil {
		t.Error("a failing stdout reported no error")
	}
}

func TestExportBackstageBuildErrorWritesNoFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "entities.yaml")
	if err := os.WriteFile(file, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An external catalog given by tag is refused before any build.
	_, _, err := execExport(t, "--local", exportBundle(t, t.TempDir(), "pay"), "--external-catalog", "oci://ghcr.io/acme/catalog:latest", "-o", file)
	if err == nil {
		t.Fatal("build error not reported")
	}
	if b, _ := os.ReadFile(file); string(b) != "before\n" {
		t.Errorf("file changed to %q", b)
	}
}

func TestExportBackstagePartialFailsByDefault(t *testing.T) {
	file := filepath.Join(t.TempDir(), "entities.yaml")
	if err := os.WriteFile(file, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	for name, args := range map[string][]string{
		"partial": {"--local", exportBundle(t, t.TempDir(), "pay"), "--target-state", missing},
		"empty":   nil,
	} {
		out, errOut, err := execExport(t, append(args, "-o", file)...)
		if exportExitCode(err) != 2 || out != "" {
			t.Errorf("%s: err %v, stdout %q", name, err, out)
		}
		if !strings.Contains(errOut, "warning: answer is "+name) || !strings.Contains(errOut, "--allow-partial") {
			t.Errorf("%s: stderr %q", name, errOut)
		}
		if b, _ := os.ReadFile(file); string(b) != "before\n" {
			t.Errorf("%s: file changed to %q", name, b)
		}
	}
	if _, errOut, _ := execExport(t, "--local", exportBundle(t, t.TempDir(), "pay"), "--target-state", missing); !strings.Contains(errOut, "SOURCE_UNAVAILABLE") {
		t.Errorf("stderr does not name the limitation: %q", errOut)
	}
}

func TestExportBackstageAllowPartial(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	out, _, err := execExport(t, "--local", exportBundle(t, t.TempDir(), "pay"), "--target-state", missing, "--allow-partial")
	if err != nil || !strings.Contains(out, "name: pay") || !strings.Contains(out, "pacto.trianalab.io/completeness: partial") {
		t.Errorf("err %v, stdout:\n%s", err, out)
	}
}

func TestExportBackstageNeedsNoDelivery(t *testing.T) {
	local := exportBundle(t, t.TempDir(), "pay")
	t.Setenv("PACTO_DELIVERY_TRUST", "")
	a, _, err := execExport(t, "--local", local)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PACTO_DELIVERY_TRUST", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "missing"))
	b, _, err := execExport(t, "--local", local)
	if err != nil || a != b {
		t.Errorf("err %v; output changed with delivery and Kubernetes configuration:\n%s\n---\n%s", err, a, b)
	}
}

func TestExportBackstageSharedFlags(t *testing.T) {
	cmd := newExportBackstageCommand(app.NewService(nil, nil))
	for _, name := range append(fleetFlagNames(), "output", "allow-partial") {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flag --%s is not declared", name)
		}
	}
	if def := cmd.Flags().Lookup("local").DefValue; def != "[]" {
		t.Errorf("--local defaults to %s, want nothing", def)
	}
}

func TestExportBackstageNoImplicitLocal(t *testing.T) {
	cwd := t.TempDir()
	exportBundle(t, cwd, "draft")
	t.Chdir(filepath.Join(cwd, "draft"))
	out, _, err := execExport(t, "--root", exportBundle(t, t.TempDir(), "pay"))
	if err != nil || !strings.Contains(out, "name: pay") || strings.Contains(out, "draft") {
		t.Errorf("err %v, stdout:\n%s", err, out)
	}
}

func TestExportBackstageK8sScopeNotice(t *testing.T) {
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "missing"))
	for _, tc := range []struct{ ns, want string }{{"", "all namespaces"}, {"b,a", "namespaces a, b"}} {
		_, errOut, _ := execExport(t, "--k8s", "--namespace", tc.ns)
		if !strings.Contains(errOut, "reading Pacto CRs from "+tc.want) || !strings.Contains(errOut, "everyone who reads the file") {
			t.Errorf("--namespace %q: stderr %q", tc.ns, errOut)
		}
	}
	if _, errOut, _ := execExport(t); strings.Contains(errOut, "Pacto CRs") {
		t.Errorf("notice printed without --k8s: %q", errOut)
	}
}
