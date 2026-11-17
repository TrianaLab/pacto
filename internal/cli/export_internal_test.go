package cli

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
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

// TestExportBackstageDemoFleet exports the demo fleet: one Component per
// non-template service, one System per known domain, and the excerpt in
// docs/backstage.md is cut from this output (as-of normalized on both sides).
func TestExportBackstageDemoFleet(t *testing.T) {
	bundles := filepath.Join("..", "..", "examples", "demo", "bundles")
	targets := filepath.Join("..", "..", "examples", "demo", "fleet-targets.yaml")
	out, _, err := execExport(t, "--local", bundles, "--target-state", targets)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := app.NewService(nil, nil).Fleet(t.Context(), app.FleetOptions{LocalRoots: []string{bundles}, TargetStateFiles: []string{targets}})
	if err != nil {
		t.Fatal(err)
	}
	services, domains := 0, map[string]bool{}
	for _, s := range snap.Services {
		if s.Template {
			continue
		}
		services++
		if s.BusinessDomain != "" && !s.BusinessDomainUnknown {
			domains[s.BusinessDomain] = true
		}
	}
	if got := strings.Count(out, "\nkind: Component\n"); got != services {
		t.Errorf("Components = %d, want %d", got, services)
	}
	if got := strings.Count(out, "\nkind: System\n"); got != len(domains) {
		t.Errorf("Systems = %d, want %d", got, len(domains))
	}
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "backstage.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), "## Who sees the file") {
		t.Error(`docs/backstage.md lacks the "Who sees the file" section`)
	}
	_, block, ok := strings.Cut(string(doc), "## An exported Component")
	if ok {
		_, block, ok = strings.Cut(block, "```yaml\n")
	}
	if ok {
		block, _, ok = strings.Cut(block, "```")
	}
	if !ok {
		t.Fatal("docs/backstage.md lacks the exported Component YAML block")
	}
	asOf := regexp.MustCompile(`(pacto.trianalab.io/as-of: ).*`)
	if norm := asOf.ReplaceAllString(block, "${1}T"); !strings.Contains(asOf.ReplaceAllString(out, "${1}T"), norm) {
		t.Errorf("the docs excerpt is not in the demo export:\n%s", norm)
	}
}

// A --namespace scope whose namespaces were all read is no outage; an
// unreadable namespace, any other degraded source or an empty fleet is.
func TestExportBackstageWritable(t *testing.T) {
	svc := map[fleet.ServiceKey]*fleet.ServiceRecord{"pay": {Key: "pay"}}
	k8s := func(st fleet.SourceStatus) fleet.SourceState {
		return fleet.SourceState{ID: "k8s", Kind: fleet.SourceKindKubernetes, Status: st}
	}
	scope := fleet.NamespaceScopeLimitation("k8s", []string{"payments"}, 0)
	derived := fleet.Limitation{Code: fleet.LimitationSourcePartial, Source: "k8s"}
	for _, tc := range []struct {
		name string
		snap fleet.FleetSnapshot
		want bool
	}{
		{"complete", fleet.FleetSnapshot{Completeness: fleet.CompletenessComplete}, true},
		{"scoped, every namespace read", fleet.FleetSnapshot{
			Completeness: fleet.CompletenessPartial, Services: svc,
			Sources:     []fleet.SourceState{k8s(fleet.SourcePartial), {ID: "oci", Status: fleet.SourceAvailable}},
			Limitations: []fleet.Limitation{scope, derived},
		}, true},
		{"scoped, a namespace unreadable", fleet.FleetSnapshot{
			Completeness: fleet.CompletenessPartial, Services: svc,
			Sources:     []fleet.SourceState{k8s(fleet.SourcePartial)},
			Limitations: []fleet.Limitation{scope, derived, fleet.NamespaceUnreadableLimitation("k8s", "ledger", "forbidden")},
		}, false},
		{"scoped, another source unavailable", fleet.FleetSnapshot{
			Completeness: fleet.CompletenessPartial, Services: svc,
			Sources:     []fleet.SourceState{k8s(fleet.SourcePartial), {ID: "oci", Status: fleet.SourceUnavailable}},
			Limitations: []fleet.Limitation{scope, derived},
		}, false},
		{"scoped, empty", fleet.FleetSnapshot{
			Completeness: fleet.CompletenessPartial,
			Sources:      []fleet.SourceState{k8s(fleet.SourcePartial)},
			Limitations:  []fleet.Limitation{scope, derived},
		}, false},
		{"partial without scope", fleet.FleetSnapshot{
			Completeness: fleet.CompletenessPartial, Services: svc,
			Sources:     []fleet.SourceState{k8s(fleet.SourcePartial)},
			Limitations: []fleet.Limitation{derived},
		}, false},
		{"scoped, external catalog unavailable", fleet.FleetSnapshot{
			Completeness: fleet.CompletenessPartial, Services: svc,
			Sources:     []fleet.SourceState{k8s(fleet.SourcePartial)},
			Limitations: []fleet.Limitation{scope, derived, {Code: fleet.LimitationExternalCatalogUnavailable}},
		}, false},
	} {
		if got := writable(&tc.snap); got != tc.want {
			t.Errorf("%s: writable = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A write that fails leaves the existing file as it was and no temp file.
func TestExportBackstageWriteKeepsFileOnFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "entities.yaml")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "keep"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(target, []byte("new")); err == nil {
		t.Fatal("rename over a non-empty directory succeeded")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("left %v behind", entries)
	}
	file := filepath.Join(dir, "ok.yaml")
	if err := os.WriteFile(file, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(file, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(file); string(b) != "new" {
		t.Errorf("file = %q", b)
	}
	if fi, _ := os.Stat(file); fi.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v", fi.Mode())
	}
	if err := writeFileAtomic(filepath.Join(dir, "missing", "f.yaml"), nil); err == nil {
		t.Error("write into a missing directory succeeded")
	}
}

// The help is all an agent reads: it names every kind the file holds and every
// spec field an inferred value never fills.
func TestExportBackstageHelpNamesEveryKind(t *testing.T) {
	long := newExportBackstageCommand(app.NewService(nil, nil)).Long
	for _, want := range []string{"API per interface", "Resource per external system", "pacto-observed", "spec.dependsOn", "spec.consumesApis", "fleet is empty"} {
		if !strings.Contains(long, want) {
			t.Errorf("help does not say %q", want)
		}
	}
}

// TestExportBackstageMatchesDashboardRoute: the file pacto export backstage
// writes and the dashboard's GET /api/backstage/entities are the same bytes
// over the same sources. Only as-of differs, because each side builds its own
// snapshot at its own time.
func TestExportBackstageMatchesDashboardRoute(t *testing.T) {
	bundles := filepath.Join("..", "..", "examples", "demo", "bundles")
	targets := filepath.Join("..", "..", "examples", "demo", "fleet-targets.yaml")
	cli, _, err := execExport(t, "--local", bundles, "--target-state", targets)
	if err != nil {
		t.Fatal(err)
	}
	svc := app.NewService(nil, nil)
	srv := dashboard.NewServer(dashboard.Options{Fleet: func(ctx context.Context) (*fleet.Query, error) {
		snap, err := svc.Fleet(ctx, app.FleetOptions{LocalRoots: []string{bundles}, TargetStateFiles: []string{targets},
			Provenance: app.NewProvenanceChecker()})
		if err != nil {
			return nil, err
		}
		return fleet.NewQuery(snap), nil
	}})
	mux := http.NewServeMux()
	srv.RegisterRawRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/backstage/entities", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("route = %d: %s", rec.Code, rec.Body)
	}
	route := rec.Body.String()
	asOf := regexp.MustCompile(`(?m)^\s*pacto\.trianalab\.io/as-of: .*\n`)
	entities := regexp.MustCompile(`(?m)^kind: `)
	for name, out := range map[string]string{"cli": cli, "route": route} {
		if n, a := len(entities.FindAllString(out, -1)), len(asOf.FindAllString(out, -1)); n == 0 || n != a {
			t.Errorf("%s: %d entities, %d as-of lines", name, n, a)
		}
	}
	if c, r := asOf.ReplaceAllString(cli, ""), asOf.ReplaceAllString(route, ""); c != r {
		t.Errorf("the CLI and the route differ:\n--- cli\n%s\n--- route\n%s", c, r)
	}
}
