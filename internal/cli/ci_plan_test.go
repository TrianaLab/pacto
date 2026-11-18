package cli

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/registry"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/ci"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
)

// ciEnv is a repository root and an in-memory registry pacto ci reads its
// baselines from.
type ciEnv struct{ root, host string }

func newCIEnv(t *testing.T) *ciEnv {
	t.Helper()
	ciEnvOff(t)
	statsOff(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("PACTO_NO_UPDATE_CHECK", "1")
	srv := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	t.Setenv("PACTO_INSECURE_REGISTRIES", host)
	return &ciEnv{root: t.TempDir(), host: host}
}

// write writes files below the root.
func (e *ciEnv) write(t *testing.T, files map[string]string) {
	t.Helper()
	writeFiles(t, e.root, files)
}

// writeFiles writes files below dir, creating their folders.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, data := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// publish pushes the folder rel as it is now to <host>/acme/<rel>:<tag>.
func (e *ciEnv) publish(t *testing.T, rel, tag string) {
	t.Helper()
	svc := app.NewService(oci.NewClient(authn.DefaultKeychain, oci.WithInsecureRegistries(e.host)), nil)
	if _, err := svc.Push(t.Context(), app.PushOptions{Ref: "oci://" + e.host + "/acme/" + rel + ":" + tag, Path: filepath.Join(e.root, rel)}); err != nil {
		t.Fatal(err)
	}
}

// additive publishes name 1.0.0 and changes a file beside its contract.
func (e *ciEnv) additive(t *testing.T, name string) {
	t.Helper()
	e.write(t, map[string]string{name + "/pacto.yaml": "service: {name: " + name + ", version: 1.0.0}\n", name + "/notes.txt": "a\n"})
	e.publish(t, name, "1.0.0")
	e.write(t, map[string]string{name + "/pacto.yaml": "service: {name: " + name + "}\n", name + "/notes.txt": "b\n"})
}

// breaking publishes payments 1.0.0 with an interface, drops the interface
// and adds web, a consumer that accepts ^1.0.0 only.
func (e *ciEnv) breaking(t *testing.T) {
	t.Helper()
	e.write(t, map[string]string{
		"payments/pacto.yaml":   "service: {name: payments, version: 1.0.0}\ninterfaces:\n  - name: api\n    type: openapi\n    ref: openapi.yaml\n",
		"payments/openapi.yaml": "openapi: 3.0.0\ninfo: {title: payments, version: \"1\"}\npaths: {}\n",
	})
	e.publish(t, "payments", "1.0.0")
	e.write(t, map[string]string{"payments/pacto.yaml": "service: {name: payments}\n", "web/pacto.yaml": consumerYAML("web", e.host)})
	if err := os.Remove(filepath.Join(e.root, "payments/openapi.yaml")); err != nil {
		t.Fatal(err)
	}
}

// consumerYAML is a service that depends on host/acme/payments ^1.0.0.
func consumerYAML(name, host string) string {
	return "service: {name: " + name + ", version: 1.0.0}\ndependencies:\n  - ref: oci://" + host + "/acme/payments\n    compatibility: \"^1.0.0\"\n"
}

// targetState writes a target-state file running each service.
func targetState(t *testing.T, services ...string) string {
	t.Helper()
	body := "schemaVersion: pacto.dev/fleet-targets/v1\ntargets:\n"
	for _, s := range services {
		body += "  - scope: prod\n    kind: k8s\n    name: " + s + "\n    service: " + s + "\n    compliance: Compliant\n"
	}
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"targets.yaml": body})
	return filepath.Join(dir, "targets.yaml")
}

// run runs pacto ci over the root against the registry.
func (e *ciEnv) run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	return runRoot(t, defaultDeps(), nil, append([]string{"ci", e.root, "--registry", e.host + "/acme"}, args...)...)
}

// ciExit is err's exit code: 0 for nil, -1 for an error that is no
// ExitError.
func ciExit(err error) int {
	var exit *ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.Code
	}
	return -1
}

// readFile is the content of path.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCIExitCodes(t *testing.T) {
	e := newCIEnv(t)
	e.additive(t, "reports")
	if out, _, err := e.run(t, "--target-state", targetState(t, "other")); err != nil || !strings.Contains(out, "\nreports  yes") || !strings.Contains(out, "clear") {
		t.Fatalf("clear: err %v\n%s", err, out)
	}
	if _, stderr, err := e.run(t); ciExit(err) != 2 || stderr != "unverified: 1 of 1 services\n" {
		t.Fatalf("unverified: err %v, stderr %q", err, stderr)
	}
	e.breaking(t)
	if out, stderr, err := e.run(t, "--target-state", targetState(t, "web")); ciExit(err) != 1 || !strings.Contains(out, "blocked (web)") || stderr != "blocked: 1 of 3 services\n" {
		t.Fatalf("blocked: err %v, stderr %q\n%s", err, stderr, out)
	}
}

func TestCINoServicesExitsTwo(t *testing.T) {
	ciEnvOff(t)
	statsOff(t)
	root := t.TempDir()
	_, stderr, err := runRoot(t, defaultDeps(), nil, "ci", root)
	if ciExit(err) != 2 || stderr != "unverified: no pacto.yaml under "+root+"\n" {
		t.Fatalf("err %v, stderr %q", err, stderr)
	}
}

func TestCIWritesGitHubSummary(t *testing.T) {
	e := newCIEnv(t)
	e.additive(t, "reports")
	summary := filepath.Join(t.TempDir(), "summary.md")
	writeFiles(t, filepath.Dir(summary), map[string]string{"summary.md": "prior\n"})
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_STEP_SUMMARY", summary)
	out, _, err := e.run(t)
	got := readFile(t, summary)
	if ciExit(err) != 2 || !strings.HasPrefix(got, "prior\n### pacto ci plan\n") || !strings.Contains(got, "| `reports` | `yes` |") || strings.Contains(out, "###") {
		t.Fatalf("err %v, summary:\n%s\nstdout:\n%s", err, got, out)
	}
}

func TestCIWritesGitLabArtifact(t *testing.T) {
	e := newCIEnv(t)
	e.additive(t, "reports")
	dir := t.TempDir()
	t.Setenv("GITLAB_CI", "true")
	t.Setenv("CI_PROJECT_DIR", dir)
	out, _, err := e.run(t)
	got := readFile(t, filepath.Join(dir, "pacto-plan.md"))
	if ciExit(err) != 2 || !strings.HasPrefix(got, "### pacto ci plan\n") || !strings.Contains(out, "SERVICE  CHANGED") {
		t.Fatalf("err %v, artifact:\n%s\nstdout:\n%s", err, got, out)
	}
}

func TestCISummaryFailureWarns(t *testing.T) {
	e := newCIEnv(t)
	e.additive(t, "reports")
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_STEP_SUMMARY", t.TempDir()) // a directory takes no append
	_, stderr, err := e.run(t)
	if ciExit(err) != 2 || !strings.Contains(stderr, "pacto: warning: step summary not written: ") {
		t.Fatalf("err %v, stderr %q", err, stderr)
	}
}

func TestCIArtifactFailureWarns(t *testing.T) {
	e := newCIEnv(t)
	e.additive(t, "reports")
	file := filepath.Join(t.TempDir(), "file")
	writeFiles(t, filepath.Dir(file), map[string]string{"file": "x"})
	t.Setenv("GITLAB_CI", "true")
	t.Setenv("CI_PROJECT_DIR", file)
	_, stderr, err := e.run(t)
	if ciExit(err) != 2 || !strings.Contains(stderr, "pacto: warning: pacto-plan.md not written: ") {
		t.Fatalf("err %v, stderr %q", err, stderr)
	}
}

// decode is the one JSON document out holds.
func decode(t *testing.T, out string) map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(out))
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("%v in:\n%s", err, out)
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		t.Fatalf("more than one document: %v\n%s", err, out)
	}
	return doc
}

func TestCIJSONOutput(t *testing.T) {
	e := newCIEnv(t)
	e.additive(t, "reports")
	out, _, err := e.run(t, "--output-format", "json")
	doc := decode(t, out)
	rows, _ := doc["rows"].([]any)
	reg, _ := doc["registry"].(map[string]any)
	if ciExit(err) != 2 || len(rows) != 1 || rows[0].(map[string]any)["service"] != "reports" || reg["source"] != "--registry" || doc["snapshot"] == nil || doc["exit"] != 2.0 {
		t.Fatalf("err %v, doc %v", err, doc)
	}
	summary := filepath.Join(t.TempDir(), "summary.md")
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_STEP_SUMMARY", summary)
	out, _, _ = e.run(t, "--output-format", "json")
	decode(t, out)
	if !strings.HasPrefix(readFile(t, summary), "### pacto ci plan\n") {
		t.Fatal("the markdown was not appended under --output-format json")
	}
}

func TestCIMarkdownOutput(t *testing.T) {
	e := newCIEnv(t)
	e.additive(t, "reports")
	out, _, _ := e.run(t, "--output-format", "markdown")
	if !strings.HasPrefix(out, "### pacto ci plan\n") {
		t.Fatalf("stdout:\n%s", out)
	}
}

func TestCIJSONRowShape(t *testing.T) {
	e := newCIEnv(t)
	e.additive(t, "reports")
	e.write(t, map[string]string{"fresh/pacto.yaml": "service: {}\n"})
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_BASE_REF", "main")
	out, stderr, err := e.run(t, "--output-format", "json", "--target-state", targetState(t, "other"))
	doc := decode(t, out)
	requireValueSource(t, doc, "registry", "base")
	if ciExit(err) != 2 || doc["exit"] != 2.0 || doc["reason"] != "unverified: 1 of 2 services" || stderr != "unverified: 1 of 2 services\n" {
		t.Fatalf("err %v, exit %v, reason %v, stderr %q", err, doc["exit"], doc["reason"], stderr)
	}
	byService := rowsByService(doc)
	for _, k := range []string{"service", "changed", "baseline", "next", "classification", "inferred", "baselineDigest", "impact"} {
		if _, ok := byService["reports"][k]; !ok {
			t.Errorf("the changed row has no %s", k)
		}
	}
	answer := byService["reports"]["impact"].(map[string]any)
	if answer["gate"] != "clear" || answer["text"] != "clear" || answer["digest"] != "" || !slices.Equal(ciStrings(answer["sources"]), []string{"target-state"}) {
		t.Fatalf("reports impact = %v", answer)
	}
	if fresh := byService["fresh"]["impact"].(map[string]any); fresh["gate"] != "unverified" || !slices.Equal(ciStrings(fresh["sources"]), nil) || fresh["sources"] == nil {
		t.Fatalf("fresh impact = %v", fresh)
	}
}

// requireValueSource fails unless each key of doc is a {value, source}.
func requireValueSource(t *testing.T, doc map[string]any, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if v, _ := doc[k].(map[string]any); v["value"] == nil || v["source"] == nil {
			t.Errorf("%s = %v, want {value, source}", k, doc[k])
		}
	}
}

// rowsByService are the plan document's rows by service.
func rowsByService(doc map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, r := range doc["rows"].([]any) {
		row := r.(map[string]any)
		out[row["service"].(string)] = row
	}
	return out
}

// ciStrings is v, a decoded JSON array, as strings.
func ciStrings(v any) []string {
	var out []string
	for _, s := range v.([]any) {
		out = append(out, s.(string))
	}
	return out
}

func TestCIExplain(t *testing.T) {
	e := newCIEnv(t)
	e.breaking(t)
	e.write(t, map[string]string{"payments/compose.yaml": "services:\n  payments:\n    image: ghcr.io/acme/payments:1.0\n    environment:\n      LOG_LEVEL: info\n"})
	e.additive(t, "reports")
	out, stderr, err := e.run(t, "--explain", "payments", "--target-state", targetState(t, "web"))
	for _, want := range []string{"Service: payments\n", "Folder: payments\n", "Repository: " + e.host + "/acme/payments\n", "Baseline: 1.0.0 sha256:",
		"Changed: yes ", "Impact: blocked (web)\n", "Inferred (not part of the contract):", "image  inferred  ghcr.io/acme/payments:1.0  from compose.yaml:3", "To declare in the configuration schema", "Affected consumers ("} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if ciExit(err) != 1 || stderr != "payments: blocked (web)\n" || strings.Contains(out, "SERVICE") {
		t.Fatalf("err %v, stderr %q", err, stderr)
	}
	if _, _, err := e.run(t, "--explain", "nope"); err == nil || !strings.Contains(err.Error(), "services: payments, reports, web") {
		t.Fatalf("unknown service: %v", err)
	}
	if out, _, err := e.run(t, "--explain", "reports"); ciExit(err) != 2 || strings.Contains(out, "Affected consumers") {
		t.Fatalf("an unimpacted row: err %v\n%s", err, out)
	}
}

func TestCIExplainRowLinesAndUnreadableFolder(t *testing.T) {
	svc := app.NewService(nil, nil)
	cmd := newCICommand(svc, nil, defaultDeps(), nil)
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetContext(t.Context())
	row := ci.Row{Service: "reports", Dir: "gone", Errors: []string{"service.version: bad"}, Notes: []string{"migrated in memory"}}
	err := printCIExplain(cmd, svc, row, nil, t.TempDir())
	if err == nil || !strings.Contains(out.String(), "error: service.version: bad\nnote: migrated in memory\n") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
}

func TestCIStatsRegistryIgnoredInPlanMode(t *testing.T) {
	e := newCIEnv(t)
	e.additive(t, "reports")
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { n.Add(1) }))
	t.Cleanup(srv.Close)
	t.Setenv("PACTO_STATS_REGISTRY", strings.TrimPrefix(srv.URL, "http://")+"/acme/pacto-stats")
	_, stderr, _ := e.run(t)
	if !strings.Contains(stderr, "pacto: note: plan mode, run record not published (PACTO_STATS_REGISTRY ignored)\n") || n.Load() != 0 {
		t.Fatalf("stderr %q, %d requests", stderr, n.Load())
	}
	_, stderr, _ = e.run(t, "--stats-registry", strings.TrimPrefix(srv.URL, "http://")+"/acme/pacto-stats")
	if !strings.Contains(stderr, "(--stats-registry ignored)") || n.Load() != 0 {
		t.Fatalf("stderr %q, %d requests", stderr, n.Load())
	}
}

func TestCIStatsFilePerService(t *testing.T) {
	e := newCIEnv(t)
	e.additive(t, "payments")
	e.additive(t, "ledger")
	dir := t.TempDir()
	_, stderr, err := e.run(t, "--stats-file", filepath.Join(dir, "run.json"))
	if ciExit(err) != 2 {
		t.Fatalf("err %v, stderr %q", err, stderr)
	}
	for _, svc := range []string{"payments", "ledger"} {
		r, perr := runrecord.Parse([]byte(readFile(t, filepath.Join(dir, "run."+svc+".json"))))
		if perr != nil || r.Kind != runrecord.KindImpact || r.Service != svc || r.OldRevision == "" || r.NewRevision == "" {
			t.Fatalf("%s: %v, %+v", svc, perr, r)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "run.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("run.json: %v", err)
	}
}

func TestCIRedactsSourceURLs(t *testing.T) {
	e := newCIEnv(t)
	e.additive(t, "reports")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	t.Cleanup(srv.Close)
	url := "http://u:tok@" + strings.TrimPrefix(srv.URL, "http://") + "/?token=s"
	dir := t.TempDir()
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_STEP_SUMMARY", filepath.Join(dir, "summary.md"))
	text, _, _ := e.run(t, "--evidence-url", url, "--stats-file", filepath.Join(dir, "run.json"))
	js, _, _ := e.run(t, "--evidence-url", url, "--output-format", "json")
	for name, s := range map[string]string{"stdout": text, "json": js, "summary": readFile(t, filepath.Join(dir, "summary.md")), "record": readFile(t, filepath.Join(dir, "run.reports.json"))} {
		if strings.Contains(s, "tok") || strings.Contains(s, "token=s") {
			t.Errorf("%s leaks the source credentials:\n%s", name, s)
		}
	}
	if !strings.Contains(js, "(redacted)") {
		t.Fatalf("no redacted source in:\n%s", js)
	}
}

func TestCIOutputHasNoPersonName(t *testing.T) {
	e := newCIEnv(t)
	e.additive(t, "reports")
	dir := t.TempDir()
	t.Setenv("GITHUB_ACTOR", "alice")
	t.Setenv("GITLAB_USER_EMAIL", "a@b")
	t.Setenv("USER", "alice")
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_STEP_SUMMARY", filepath.Join(dir, "summary.md"))
	stdout, stderr, _ := e.run(t, "--stats-file", filepath.Join(dir, "run.json"), "--stats-context", "repository,run-url")
	for name, s := range map[string]string{"stdout": stdout, "stderr": stderr, "summary": readFile(t, filepath.Join(dir, "summary.md")), "record": readFile(t, filepath.Join(dir, "run.reports.json"))} {
		if strings.Contains(s, "alice") || strings.Contains(s, "a@b") {
			t.Errorf("%s names a person:\n%s", name, s)
		}
	}
}

func TestCILocalDefaultsToRoot(t *testing.T) {
	e := newCIEnv(t)
	e.breaking(t)
	other := t.TempDir()
	writeFiles(t, other, map[string]string{"mobile/pacto.yaml": consumerYAML("mobile", e.host)})
	file := targetState(t, "web", "mobile")
	if out, _, _ := e.run(t, "--target-state", file); !strings.Contains(out, "blocked (web)") {
		t.Fatalf("without --local:\n%s", out)
	}
	if out, _, _ := e.run(t, "--target-state", file, "--local", other); !strings.Contains(out, "mobile") {
		t.Fatalf("with --local other:\n%s", out)
	}
}

func TestCIRecordsDropDigestFailure(t *testing.T) {
	statsOff(t)
	file := filepath.Join(t.TempDir(), "run.json")
	cmd := newCICommand(app.NewService(nil, nil), nil, defaultDeps(), nil)
	var stderr strings.Builder
	cmd.SetErr(&stderr)
	if err := cmd.Flags().Set("stats-file", file); err != nil {
		t.Fatal(err)
	}
	ciRecords(cmd, newRunRecorder("dev", defaultDeps()), map[string]*revisions{"payments": {err: errors.New("unreadable")}}, map[string]*impact.Result{"payments": {}})
	if _, err := os.Stat(filepath.Join(filepath.Dir(file), "run.payments.json")); !errors.Is(err, os.ErrNotExist) || !strings.Contains(stderr.String(), "could not compute the bundle digest: unreadable") {
		t.Fatalf("stat %v, stderr %q", err, stderr.String())
	}
}

// failingWriter refuses every write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestCIOutputWriteFails(t *testing.T) {
	e := newCIEnv(t)
	e.additive(t, "reports")
	_, _, err := runRoot(t, defaultDeps(), failingWriter{}, "ci", e.root, "--registry", e.host+"/acme", "--output-format", "markdown")
	if err == nil || ciExit(err) != -1 {
		t.Fatalf("err = %v, want the write error", err)
	}
}
