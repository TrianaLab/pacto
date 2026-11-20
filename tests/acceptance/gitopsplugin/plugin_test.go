// Package gitopsplugin runs the Argo CD Config Management Plugin that the
// GitOps page publishes, exactly as the page includes it, against stub pacto,
// helm and kustomize binaries. The plugin is the sync gate: every case here is
// a way it could render something it should have refused.
package gitopsplugin

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	pinnedA = "oci://r/acme/pacto-targets:cluster-a"
	pinnedB = "oci://r/acme/pacto-targets:cluster-b"

	contract20 = "pactoVersion: '2.5'\nservice:\n  name: payments\n  version: 2.0.0\n"
	rendered   = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: rendered\n"
	impactOut  = "IMPACT-REPORT\n"
)

// plugin is the slice of the ConfigMap fixture the tests run.
type plugin struct {
	generate string
	discover []string
}

func loadPlugin(t *testing.T) plugin {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(file), "..", "kind", "fixtures", "gitops", "argocd-cmp-pacto.yaml")
	raw, err := os.ReadFile(path) //nolint:gosec // a fixture under the repository root
	if err != nil {
		t.Fatal(err)
	}
	var cm struct {
		Data map[string]string `yaml:"data"`
	}
	if err := yaml.Unmarshal(raw, &cm); err != nil {
		t.Fatalf("parse the plugin ConfigMap: %v", err)
	}
	var spec struct {
		Spec struct {
			Discover struct {
				Find struct {
					Command []string `yaml:"command"`
				} `yaml:"find"`
			} `yaml:"discover"`
			Generate struct {
				Command []string `yaml:"command"`
			} `yaml:"generate"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal([]byte(cm.Data["plugin.yaml"]), &spec); err != nil {
		t.Fatalf("parse plugin.yaml: %v", err)
	}
	gen := spec.Spec.Generate.Command
	if len(gen) != 2 || gen[0] != "sh" || filepath.Base(gen[1]) != "generate.sh" {
		t.Fatalf("plugin.yaml generate.command = %v, want [sh .../generate.sh]", gen)
	}
	if cm.Data["generate.sh"] == "" || len(spec.Spec.Discover.Find.Command) == 0 {
		t.Fatal("the plugin ConfigMap has no generate.sh or no discovery command")
	}
	return plugin{generate: cm.Data["generate.sh"], discover: spec.Spec.Discover.Find.Command}
}

func needSh(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on PATH; the plugin is a POSIX sh script")
	}
}

const toolStub = `#!/bin/sh
printf '%s\n' "$@" > "$STUB_LOG/TOOL.args"
env | grep '^PACTO_REGISTRY_' > "$STUB_LOG/TOOL.env"
if [ "$1" = dependency ]; then touch "$STUB_LOG/TOOL.dep"; exit 0; fi
cat "$STUB_LOG/manifests"
`

const pactoStub = `#!/bin/sh
printf '%s\n' "$@" > "$STUB_LOG/pacto.args"
printf '%s' "$STUB_PACTO_OUT"
printf '%s' "$STUB_PACTO_ERR" >&2
exit "$STUB_PACTO_RC"
`

// pluginCase is one render. Zero values mean a kustomize folder holding the
// 2.0.0 contract, one pinned snapshot and a clear impact.
type pluginCase struct {
	files     map[string]string
	manifests string
	env       map[string]string
	rc        string
	out, err  string
	noPacto   bool
}

type pluginRun struct {
	code           int
	stdout, stderr string
	logDir         string
}

func (r pluginRun) args(tool string) []string {
	raw, err := os.ReadFile(filepath.Join(r.logDir, tool+".args")) //nolint:gosec // test temp dir
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
}

func (r pluginRun) called(tool string) bool { return r.args(tool) != nil }

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func runPlugin(t *testing.T, c pluginCase) pluginRun {
	t.Helper()
	needSh(t)
	p := loadPlugin(t)
	root := t.TempDir()
	bin, app, logDir := filepath.Join(root, "bin"), filepath.Join(root, "app"), filepath.Join(root, "log")
	for _, d := range []string{bin, app, logDir} {
		if err := os.Mkdir(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	for _, tool := range []string{"helm", "kustomize"} {
		writeFile(t, filepath.Join(bin, tool), strings.ReplaceAll(toolStub, "TOOL", tool), 0o700) //nolint:gosec // an executable stub
	}
	if !c.noPacto {
		writeFile(t, filepath.Join(bin, "pacto"), pactoStub, 0o700) //nolint:gosec // an executable stub
	}
	files := c.files
	if files == nil {
		files = map[string]string{"kustomization.yaml": "resources: []\n", "pacto.yaml": contract20}
	}
	for name, content := range files {
		if strings.HasSuffix(name, "/") {
			if err := os.Mkdir(filepath.Join(app, name), 0o750); err != nil {
				t.Fatal(err)
			}
			continue
		}
		writeFile(t, filepath.Join(app, name), content, 0o600)
	}
	if c.manifests == "" {
		c.manifests = rendered
	}
	writeFile(t, filepath.Join(logDir, "manifests"), c.manifests, 0o600)
	script := filepath.Join(root, "generate.sh")
	writeFile(t, script, p.generate, 0o600)

	env := map[string]string{
		"PATH":                  bin + ":/usr/bin:/bin",
		"STUB_LOG":              logDir,
		"STUB_PACTO_OUT":        impactOut,
		"STUB_PACTO_ERR":        c.err,
		"STUB_PACTO_RC":         "0",
		"PACTO_PLUGIN_LOG":      filepath.Join(logDir, "sidecar.log"),
		"PACTO_TARGET_SNAPSHOT": pinnedA,
		"ARGOCD_APP_NAME":       "payments",
		"ARGOCD_APP_NAMESPACE":  "apps",
	}
	if c.rc != "" {
		env["STUB_PACTO_RC"] = c.rc
		env["STUB_PACTO_OUT"] = c.out
	}
	for k, v := range c.env {
		env[k] = v
	}
	cmd := exec.Command("sh", script) //nolint:gosec // the fixture under test
	cmd.Dir = app
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return pluginRun{code: code, stdout: stdout.String(), stderr: stderr.String(), logDir: logDir}
}

func wantExit(t *testing.T, r pluginRun, code int, stderrHas string) {
	t.Helper()
	if r.code != code {
		t.Fatalf("exit %d, want %d (stderr %q)", r.code, code, r.stderr)
	}
	if !strings.Contains(r.stderr, stderrHas) {
		t.Fatalf("stderr %q does not contain %q", r.stderr, stderrHas)
	}
	if code != 0 && r.stdout != "" {
		t.Fatalf("a refused render wrote to stdout, which Argo CD reads as manifests: %q", r.stdout)
	}
}

func wantRendered(t *testing.T, r pluginRun) {
	t.Helper()
	if r.code != 0 || r.stdout != rendered {
		t.Fatalf("exit %d stdout %q stderr %q; want exit 0 and only the rendered manifests", r.code, r.stdout, r.stderr)
	}
}

func hasPair(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func TestArgoPluginDiscoversContractsOnly(t *testing.T) {
	needSh(t)
	p := loadPlugin(t)
	for name, c := range map[string]struct {
		content string
		want    bool
	}{
		"contract":  {contract20, true},
		"pacto CR":  {"apiVersion: pacto.trianalab.io/v1alpha1\nkind: Pacto\nmetadata:\n  name: x\n", false},
		"no file":   {"", false},
		"no header": {"service:\n  name: x\n", true},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if c.content != "" {
				writeFile(t, filepath.Join(dir, "pacto.yaml"), c.content, 0o600)
			}
			cmd := exec.Command(p.discover[0], p.discover[1:]...) //nolint:gosec // the fixture under test
			cmd.Dir = dir
			out, _ := cmd.Output()
			if got := strings.TrimSpace(string(out)) != ""; got != c.want {
				t.Fatalf("discovered = %v, want %v (Argo CD discovers on non-empty stdout)", got, c.want)
			}
		})
	}
}

func TestArgoPluginClearDelegatesToKustomize(t *testing.T) {
	r := runPlugin(t, pluginCase{})
	wantRendered(t, r)
	if got := r.args("kustomize"); !slices.Equal(got, []string{"build", "."}) {
		t.Fatalf("kustomize args %v, want [build .]", got)
	}
	if r.called("helm") {
		t.Fatal("helm ran on a kustomize folder")
	}
}

func TestArgoPluginClearDelegatesToHelm(t *testing.T) {
	r := runPlugin(t, pluginCase{
		files: map[string]string{"Chart.yaml": "name: payments\n", "pacto.yaml": contract20},
		env:   map[string]string{"KUBE_VERSION": "1.33.0"},
	})
	wantRendered(t, r)
	args := r.args("helm")
	if len(args) < 3 || args[0] != "template" || args[1] != "payments" || args[2] != "." {
		t.Fatalf("helm args %v, want template payments . ...", args)
	}
	if !hasPair(args, "--namespace", "apps") || !hasPair(args, "--kube-version", "1.33.0") || !slices.Contains(args, "--include-crds") {
		t.Fatalf("helm args %v miss the namespace, kube version or --include-crds", args)
	}
}

func TestArgoPluginHelmPassesApiVersions(t *testing.T) {
	r := runPlugin(t, pluginCase{
		files: map[string]string{"Chart.yaml": "name: payments\n", "pacto.yaml": contract20},
		env:   map[string]string{"KUBE_API_VERSIONS": "v1,apps/v1"},
	})
	wantRendered(t, r)
	args := r.args("helm")
	if !hasPair(args, "--api-versions", "v1") || !hasPair(args, "--api-versions", "apps/v1") {
		t.Fatalf("helm args %v, want one --api-versions per KUBE_API_VERSIONS entry", args)
	}
}

func TestArgoPluginKustomizeBuildOptions(t *testing.T) {
	r := runPlugin(t, pluginCase{env: map[string]string{"PACTO_KUSTOMIZE_BUILD_OPTIONS": "--enable-helm --load-restrictor LoadRestrictionsNone"}})
	wantRendered(t, r)
	want := []string{"build", "--enable-helm", "--load-restrictor", "LoadRestrictionsNone", "."}
	if got := r.args("kustomize"); !slices.Equal(got, want) {
		t.Fatalf("kustomize args %v, want %v", got, want)
	}
}

func TestArgoPluginHelmDependencyBuild(t *testing.T) {
	chart := map[string]string{"Chart.yaml": "name: payments\n", "Chart.lock": "dependencies: []\n", "pacto.yaml": contract20}
	r := runPlugin(t, pluginCase{files: chart})
	wantRendered(t, r)
	if _, err := os.Stat(filepath.Join(r.logDir, "helm.dep")); err != nil {
		t.Fatal("Chart.lock without charts/ did not run helm dependency build")
	}
	chart["charts/"] = ""
	r = runPlugin(t, pluginCase{files: chart})
	if _, err := os.Stat(filepath.Join(r.logDir, "helm.dep")); err == nil {
		t.Fatal("helm dependency build ran although charts/ is vendored")
	}
}

func TestArgoPluginNoToolFails(t *testing.T) {
	r := runPlugin(t, pluginCase{files: map[string]string{"pacto.yaml": contract20}})
	wantExit(t, r, 1, "has no Chart.yaml or kustomization.yaml")
	if r.called("pacto") {
		t.Fatal("pacto ran on a folder that renders nothing")
	}
}

func TestArgoPluginBlockedPrintsBlocked(t *testing.T) {
	r := runPlugin(t, pluginCase{rc: "1", out: impactOut, err: "breaking changes affect active consumers: ledger\n"})
	wantExit(t, r, 1, "pacto impact: BLOCKED breaking changes affect active consumers: ledger, exit 1")
}

func TestArgoPluginExitTwoFailsByDefault(t *testing.T) {
	r := runPlugin(t, pluginCase{rc: "2", out: impactOut, err: "unverified: the target snapshot is partial\n"})
	wantExit(t, r, 2, "pacto impact: UNVERIFIED the target snapshot is partial, exit 2")
}

func TestArgoPluginExitTwoPassesWithOptIn(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"installation":    {"PACTO_ALLOW_UNVERIFIED": "true"},
		"per-application": {"PACTO_ALLOW_UNVERIFIED": "per-application", "ARGOCD_ENV_PACTO_ALLOW_UNVERIFIED": "true"},
	} {
		t.Run(name, func(t *testing.T) {
			wantRendered(t, runPlugin(t, pluginCase{rc: "2", out: impactOut, err: "unverified: partial\n", env: env}))
		})
	}
}

func TestArgoPluginAppOptInIgnoredUnlessInstallationAllows(t *testing.T) {
	for _, installation := range []string{"", "false", "yes"} {
		t.Run("installation="+installation, func(t *testing.T) {
			env := map[string]string{"ARGOCD_ENV_PACTO_ALLOW_UNVERIFIED": "true"}
			if installation != "" {
				env["PACTO_ALLOW_UNVERIFIED"] = installation
			}
			r := runPlugin(t, pluginCase{rc: "2", out: impactOut, err: "unverified: partial\n", env: env})
			wantExit(t, r, 2, "UNVERIFIED")
		})
	}
	r := runPlugin(t, pluginCase{rc: "2", out: impactOut, err: "unverified: partial\n",
		env: map[string]string{"PACTO_ALLOW_UNVERIFIED": "per-application"}})
	wantExit(t, r, 2, "UNVERIFIED")
}

func TestArgoPluginRefusesUnpinnedSnapshot(t *testing.T) {
	unpinned := map[string]string{"PACTO_TARGET_SNAPSHOT": pinnedA + " oci://r/acme/pacto-targets"}
	r := runPlugin(t, pluginCase{env: unpinned})
	wantExit(t, r, 1, "pacto plugin: pin each destination cluster (oci://<registry>/pacto-targets:cluster-<uid>)")
	if r.called("pacto") || r.called("kustomize") {
		t.Fatal("an unpinned snapshot reached pacto or the render")
	}
	unpinned["PACTO_ALLOW_CLUSTER_DISCOVERY"] = "true"
	wantRendered(t, runPlugin(t, pluginCase{env: unpinned}))
}

func pactoObject(ref string) string {
	return "---\napiVersion: pacto.trianalab.io/v1alpha1\nkind: Pacto\nmetadata:\n  name: payments\nspec:\n  contractRef:\n    oci: " + ref + "\n"
}

func TestArgoPluginJudgesRenderedContractRef(t *testing.T) {
	for _, ref := range []string{"oci://r/acme/payments:2.0.0", "r/acme/payments:2.0.0", `"r/acme/payments:2.0.0"`} {
		t.Run(ref, func(t *testing.T) {
			r := runPlugin(t, pluginCase{manifests: rendered + pactoObject(ref)})
			if r.code != 0 {
				t.Fatalf("exit %d: %s", r.code, r.stderr)
			}
			if got := r.args("pacto"); got[len(got)-1] != "oci://r/acme/payments:2.0.0" {
				t.Fatalf("pacto args %v, want the rendered contractRef judged", got)
			}
		})
	}
	// A ConfigMap that happens to hold an oci: key is no Pacto object.
	r := runPlugin(t, pluginCase{manifests: rendered + "data:\n  oci: r/acme/other:9.9.9\n"})
	if got := r.args("pacto"); got[len(got)-1] != "." {
		t.Fatalf("pacto args %v, want the folder contract judged when no Pacto object renders", got)
	}
}

func TestArgoPluginContractRefMismatchFails(t *testing.T) {
	older := map[string]string{"kustomization.yaml": "resources: []\n", "pacto.yaml": "service:\n  name: payments\n  version: 1.1.0\n"}
	r := runPlugin(t, pluginCase{files: older, manifests: pactoObject("oci://r/acme/payments:2.0.0")})
	wantExit(t, r, 1, "pacto plugin: ERROR rendered contractRef oci://r/acme/payments:2.0.0 does not match pacto.yaml 1.1.0")

	flow := map[string]string{"kustomization.yaml": "resources: []\n", "pacto.yaml": "service: {name: payments, version: 1.1.0}\n"}
	wantExit(t, runPlugin(t, pluginCase{files: flow, manifests: pactoObject("r/acme/payments:2.0.0")}), 1, "ERROR rendered contractRef")

	two := pactoObject("r/acme/payments:2.0.0") + pactoObject("r/acme/payments:2.1.0")
	r = runPlugin(t, pluginCase{manifests: two})
	wantExit(t, r, 1, "ERROR rendered contractRef")
	if r.called("pacto") {
		t.Fatal("pacto judged one of two disagreeing rendered contracts")
	}
}

func TestArgoPluginMessageIsBounded(t *testing.T) {
	long := strings.Repeat("ledger, ", 512) + "\nsecond line\n" + strings.Repeat("x", 2048)
	r := runPlugin(t, pluginCase{rc: "1", out: impactOut, err: long})
	if r.code != 1 {
		t.Fatalf("exit %d", r.code)
	}
	lines := strings.Split(strings.TrimSuffix(r.stderr, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("stderr has %d lines, want one (Argo CD shows it to every Application reader)", len(lines))
	}
	reason := strings.TrimSuffix(strings.TrimPrefix(lines[0], "pacto impact: BLOCKED "), ", exit 1")
	if len(reason) > 512 {
		t.Fatalf("reason is %d bytes, want at most 512", len(reason))
	}
	logged, _ := os.ReadFile(filepath.Join(r.logDir, "sidecar.log")) //nolint:gosec // test temp dir
	if string(logged) != long {
		t.Fatal("the full impact stderr did not reach the sidecar log")
	}
}

func TestArgoPluginUnsetsRegistryCredentials(t *testing.T) {
	creds := map[string]string{"PACTO_REGISTRY_USERNAME": "u", "PACTO_REGISTRY_PASSWORD": "p", "PACTO_REGISTRY_TOKEN": "t"}
	for name, files := range map[string]map[string]string{
		"kustomize": nil,
		"helm":      {"Chart.yaml": "name: payments\n", "pacto.yaml": contract20},
	} {
		t.Run(name, func(t *testing.T) {
			r := runPlugin(t, pluginCase{files: files, env: creds})
			wantRendered(t, r)
			env, err := os.ReadFile(filepath.Join(r.logDir, name+".env")) //nolint:gosec // test temp dir
			if err != nil || len(env) != 0 {
				t.Fatalf("%s saw registry credentials: %q", name, env)
			}
		})
	}
}

func TestArgoPluginCommandNotFoundIsError(t *testing.T) {
	r := runPlugin(t, pluginCase{noPacto: true})
	wantExit(t, r, 1, "pacto impact: ERROR")
	if !strings.Contains(r.stderr, "exit 127") {
		t.Fatalf("stderr %q does not name exit 127", r.stderr)
	}
}

func TestArgoPluginRunFailureIsError(t *testing.T) {
	r := runPlugin(t, pluginCase{rc: "1", err: "cannot read the target snapshot\n"})
	wantExit(t, r, 1, "pacto impact: ERROR cannot read the target snapshot, exit 1")
}

func TestArgoPluginStdoutIsManifestsOnly(t *testing.T) {
	r := runPlugin(t, pluginCase{})
	wantRendered(t, r)
	if strings.Contains(r.stdout, "IMPACT-REPORT") {
		t.Fatal("impact's report reached the manifest stream")
	}
	r = runPlugin(t, pluginCase{rc: "1", out: impactOut, err: "blocked\n"})
	wantExit(t, r, 1, "BLOCKED")
}

func TestArgoPluginPassesEverySnapshot(t *testing.T) {
	r := runPlugin(t, pluginCase{env: map[string]string{"PACTO_TARGET_SNAPSHOT": pinnedA + " " + pinnedB}})
	wantRendered(t, r)
	args := r.args("pacto")
	if !hasPair(args, "--target-snapshot", pinnedA) || !hasPair(args, "--target-snapshot", pinnedB) {
		t.Fatalf("pacto args %v, want one --target-snapshot per pinned word", args)
	}
	if !hasPair(args, "--local", "") || args[0] != "impact" || !slices.Contains(args, "--running") {
		t.Fatalf("pacto args %v, want impact --running with --local \"\"", args)
	}
}
