package pactify

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/plugin"
)

// twoPlugins is a golden path that names dockerfile and github-actions.
var twoPlugins = strings.Replace(pathContract, "        description: A Dockerfile for a Go HTTP service\n",
	"        description: A Dockerfile for a Go HTTP service\n      - name: github-actions\n        options:\n          go-version: \"1.27\"\n", 1)

func notInstalled(name string) (string, error) {
	return "", errors.New("plugin " + name + " not found")
}

func installed(name string) (string, error) { return "/plugins/pacto-plugin-" + name, nil }

// runFunc is a plugin runner that answers with files.
func runFunc(files ...plugin.GeneratedFile) func(context.Context, string, plugin.GenerateRequest) (*plugin.GenerateResponse, error) {
	return func(context.Context, string, plugin.GenerateRequest) (*plugin.GenerateResponse, error) {
		return &plugin.GenerateResponse{Files: files}, nil
	}
}

// createWithPlugins creates gift-cards from the two-plugin path with t, in a
// new folder unless dir is given. Plan only plans.
func createWithPlugins(tb testing.TB, dir string, tpl *Template, plan bool) (*Result, error) {
	tb.Helper()
	if dir == "" {
		dir = filepath.Join(tb.TempDir(), "gift-cards")
	}
	tpl.FS, tpl.Ref, tpl.Digest = goldenPath(twoPlugins), "oci://ghcr.io/platform/http-service:1.0.0", pathDigest
	if tpl.Find == nil {
		tpl.Find = installed
	}
	return Init(context.Background(), Options{Dir: dir, Owner: "checkout", Plan: plan, Template: tpl}, os.WriteFile)
}

func pluginSteps(r *Result) []fleet.CreationStep {
	return slices.DeleteFunc(slices.Clone(r.Steps), func(s fleet.CreationStep) bool { return s.Step != fleet.StepPlugin })
}

func TestTemplatePluginsListedWithBinary(t *testing.T) {
	find := func(name string) (string, error) {
		if name == "dockerfile" {
			return installed(name)
		}
		return notInstalled(name)
	}
	r, err := createWithPlugins(t, "", &Template{Find: find}, true)
	if err != nil {
		t.Fatal(err)
	}
	want := []fleet.CreationStep{
		{Step: fleet.StepPlugin, Name: "dockerfile", Status: fleet.StepStatusSkipped, Detail: "pass --run-plugins to run /plugins/pacto-plugin-dockerfile"},
		{Step: fleet.StepPlugin, Name: "github-actions", Status: fleet.StepStatusSkipped, Detail: "not installed: install pacto-plugin-github-actions"},
	}
	if got := pluginSteps(r); !reflect.DeepEqual(got, want) {
		t.Errorf("steps: %+v", got)
	}
	if r.Steps[2].Step != fleet.StepValidation || r.Steps[3].Step != fleet.StepPlugin || r.Steps[5].Step != fleet.StepPublished {
		t.Errorf("plugin steps follow validation: %+v", r.Steps)
	}
}

func TestTemplatePluginsSkippedWithoutFlag(t *testing.T) {
	r, err := createWithPlugins(t, "", &Template{Run: func(context.Context, string, plugin.GenerateRequest) (*plugin.GenerateResponse, error) {
		t.Fatal("ran a plugin nobody named")
		return nil, nil
	}}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range pluginSteps(r) {
		if s.Status != fleet.StepStatusSkipped {
			t.Errorf("step: %+v", s)
		}
	}
}

func TestTemplatePluginsNeverRunInPlan(t *testing.T) {
	r, err := createWithPlugins(t, "", &Template{RunPlugins: []string{"dockerfile"}, Run: func(context.Context, string, plugin.GenerateRequest) (*plugin.GenerateResponse, error) {
		t.Fatal("ran a plugin under --plan")
		return nil, nil
	}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if s := pluginSteps(r)[0]; s.Status != fleet.StepStatusPlanned || s.Detail != "would run /plugins/pacto-plugin-dockerfile" {
		t.Errorf("step: %+v", s)
	}
}

func TestTemplatePluginsAddOnly(t *testing.T) {
	dir := writeTree(t, filepath.Join(t.TempDir(), "gift-cards"), map[string]string{"Dockerfile": "FROM mine\n"})
	run := runFunc(
		plugin.GeneratedFile{Path: "pacto.yaml", Content: "pactoVersion: \"2.0\"\n"},
		plugin.GeneratedFile{Path: "interfaces/openapi.yaml", Content: "plugin\n"},
		plugin.GeneratedFile{Path: "Dockerfile", Content: "FROM plugin\n"},
		plugin.GeneratedFile{Path: "ci/./build.yaml", Content: "build\n"},
		plugin.GeneratedFile{Path: ".pactoignore", Content: "dist/\n"},
	)
	r, err := createWithPlugins(t, dir, &Template{RunPlugins: []string{"dockerfile"}, Run: run}, false)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, f := range r.Files {
		seen[f.Path]++
	}
	for p, n := range seen {
		if n != 1 {
			t.Errorf("%s planned %d times", p, n)
		}
	}
	read := func(p string) string {
		data, _ := os.ReadFile(filepath.Join(dir, p))
		return string(data)
	}
	if !strings.Contains(read("pacto.yaml"), "name: gift-cards") || read("interfaces/openapi.yaml") != pathOpenAPI || read("Dockerfile") != "FROM mine\n" ||
		read("ci/build.yaml") != "build\n" || read(".pactoignore") != "dist/\ncosign.key\n" {
		t.Errorf("files: %+v", r.Files)
	}
	if s := pluginSteps(r)[0]; s.Status != fleet.StepStatusDone || s.Detail != "ran /plugins/pacto-plugin-dockerfile: added 2 files, kept 3" {
		t.Errorf("step: %+v", s)
	}

	// A returned .pactoignore that already ignores the key is kept as it came.
	r, err = createWithPlugins(t, "", &Template{RunPlugins: []string{"dockerfile"}, Run: runFunc(plugin.GeneratedFile{Path: ".pactoignore", Content: "cosign.key\n"})}, true)
	if err != nil || file(t, r, ".pactoignore").Content != "cosign.key\n" {
		t.Errorf("kept ignore: %v %+v", err, r)
	}
}

func TestTemplatePluginsRejectEscapingPath(t *testing.T) {
	for _, p := range []string{"../outside", "/etc/passwd", ""} {
		dir := filepath.Join(t.TempDir(), "gift-cards")
		_, err := createWithPlugins(t, dir, &Template{RunPlugins: []string{"dockerfile"}, Run: runFunc(plugin.GeneratedFile{Path: p})}, false)
		if err == nil || !strings.Contains(err.Error(), "a path outside the folder") {
			t.Errorf("%q: %v", p, err)
		}
		if _, err := os.Stat(dir); err == nil {
			t.Errorf("%q: the folder was written", p)
		}
	}
}

func TestTemplatePluginsFailureRefuses(t *testing.T) {
	for name, c := range map[string]struct {
		tpl  *Template
		want string
	}{
		"missing binary": {&Template{RunPlugins: []string{"dockerfile"}, Find: notInstalled}, "the plugin dockerfile is not installed: install pacto-plugin-dockerfile"},
		"run error": {&Template{RunPlugins: []string{"dockerfile"}, Run: func(context.Context, string, plugin.GenerateRequest) (*plugin.GenerateResponse, error) {
			return nil, errors.New("boom")
		}}, "running the plugin dockerfile (/plugins/pacto-plugin-dockerfile): boom"},
		"no runner": {&Template{RunPlugins: []string{"dockerfile"}}, "no plugin runner is configured"},
	} {
		dir := filepath.Join(t.TempDir(), "gift-cards")
		if _, err := createWithPlugins(t, dir, c.tpl, false); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
		if _, err := os.Stat(dir); err == nil {
			t.Errorf("%s: the folder was written", name)
		}
	}
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	if _, err := createWithPlugins(t, "", &Template{RunPlugins: []string{"dockerfile"}, Run: runFunc()}, false); err == nil || !strings.Contains(err.Error(), "running the plugin dockerfile") {
		t.Errorf("no temp folder: %v", err)
	}
}

func TestTemplatePluginsCopyFailureRefuses(t *testing.T) {
	p := &planner{r: &Result{Dir: t.TempDir()}, opts: Options{Template: &Template{Run: runFunc()}}}
	planned := fstest.MapFS{"dev": {Mode: fs.ModeDevice}}
	if _, err := p.runPlugin(context.Background(), contract.TemplatePlugin{Name: "dockerfile"}, "bin", &contract.Contract{}, planned); err == nil {
		t.Error("copied a file that is not regular")
	}
}

func TestTemplatePluginsOnlyNamed(t *testing.T) {
	var ran []string
	run := func(_ context.Context, name string, _ plugin.GenerateRequest) (*plugin.GenerateResponse, error) {
		ran = append(ran, name)
		return &plugin.GenerateResponse{}, nil
	}
	r, err := createWithPlugins(t, "", &Template{RunPlugins: []string{"github-actions"}, Run: run}, false)
	if err != nil {
		t.Fatal(err)
	}
	if s := pluginSteps(r); s[0].Status != fleet.StepStatusSkipped || s[1].Status != fleet.StepStatusDone || !slices.Equal(ran, []string{"github-actions"}) {
		t.Errorf("steps: %+v, ran %v", s, ran)
	}
	_, err = createWithPlugins(t, "", &Template{RunPlugins: []string{"helm"}, Run: run}, true)
	if err == nil || err.Error() != "the golden path names no plugin helm; it names dockerfile, github-actions" {
		t.Errorf("unlisted: %v", err)
	}
	p := &planner{r: &Result{}, opts: Options{Template: &Template{RunPlugins: []string{"helm"}}}}
	if _, err := p.plugins(context.Background(), nil, nil, nil); err == nil || !strings.HasSuffix(err.Error(), "it names none") {
		t.Errorf("no plugins: %v", err)
	}
}

func TestTemplatePluginsRequest(t *testing.T) {
	target := filepath.Join(t.TempDir(), "gift-cards")
	var got plugin.GenerateRequest
	run := func(_ context.Context, _ string, req plugin.GenerateRequest) (*plugin.GenerateResponse, error) {
		got = req
		entries, err := os.ReadDir(req.OutputDir)
		if err != nil || len(entries) != 0 {
			t.Errorf("output folder: %v %v", entries, err)
		}
		if data, err := os.ReadFile(filepath.Join(req.BundleDir, "pacto.yaml")); err != nil || !strings.Contains(string(data), "name: gift-cards") {
			t.Errorf("bundle copy: %v %s", err, data)
		}
		if _, err := os.Stat(filepath.Join(req.BundleDir, "interfaces", "openapi.yaml")); err != nil {
			t.Errorf("bundle copy: %v", err)
		}
		_ = os.WriteFile(filepath.Join(req.OutputDir, "leak.txt"), []byte("x"), 0o644)
		return &plugin.GenerateResponse{}, nil
	}
	if _, err := createWithPlugins(t, target, &Template{RunPlugins: []string{"github-actions"}, Run: run}, false); err != nil {
		t.Fatal(err)
	}
	if got.ProtocolVersion != plugin.ProtocolVersion || !reflect.DeepEqual(got.Options, map[string]any{"go-version": "1.27"}) ||
		got.Contract.Service.Name != "gift-cards" || strings.HasPrefix(got.OutputDir, target) || strings.HasPrefix(got.BundleDir, target) {
		t.Errorf("request: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(target, "leak.txt")); err == nil {
		t.Error("a file the plugin did not return reached the folder")
	}
	if _, err := os.Stat(got.OutputDir); err == nil {
		t.Error("the output folder was left behind")
	}
}

func TestTemplatePluginsRefusedUnverifiedUnderTrust(t *testing.T) {
	_, err := createWithPlugins(t, "", &Template{RunPlugins: []string{"dockerfile"}, TrustConfigured: true, Run: runFunc()}, false)
	if err == nil || !strings.Contains(err.Error(), "signature a template key verified") {
		t.Errorf("unverified: %v", err)
	}
	signed := fleet.CreationStep{Status: fleet.StepStatusDone, Detail: "signed by platform"}
	if _, err := createWithPlugins(t, "", &Template{RunPlugins: []string{"dockerfile"}, TrustConfigured: true, Signature: signed, Run: runFunc()}, false); err != nil {
		t.Errorf("verified: %v", err)
	}
}
