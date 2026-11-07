package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/pactify"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// fakeInit records what pacto_create from hands the command layer.
type fakeInit struct {
	opts pactify.Options
	req  FromRequest
	r    *pactify.Result
	err  error
}

func (f *fakeInit) init(_ context.Context, opts pactify.Options, req FromRequest) (*pactify.Result, error) {
	f.opts, f.req = opts, req
	return f.r, f.err
}

func callFrom(t *testing.T, init Initializer, args map[string]any) (string, bool) {
	t.Helper()
	res := callToolOn(t, NewServer(Authoring{Init: init}, "test"), "pacto_create", args)
	return resultText(t, res), res.IsError
}

func TestCreateFromPassesInputs(t *testing.T) {
	f := &fakeInit{r: &pactify.Result{Service: "gift-cards"}}
	_, isErr := callFrom(t, f.init, map[string]any{
		"from": "oci://ghcr.io/acme/paths/http-service:1.0.0", "name": "gift-cards", "owner": "checkout",
		"domain": "commerce", "path": "svc/gift-cards", "registry": "ghcr.io/acme", "describe_only": true,
		"no_transparency_log": true, "dry_run": true,
		"values": `{"service":{"summary":"Gift card balances"}}`, "set": `["service.summary=x"]`, "run_plugins": `["dockerfile"]`,
	})
	if isErr {
		t.Fatal("expected success")
	}
	want := pactify.Options{Dir: "svc/gift-cards", Name: "gift-cards", Owner: "checkout", Domain: "commerce",
		Registry: "ghcr.io/acme", DescribeOnly: true, NoTransparencyLog: true, Plan: true}
	if !reflect.DeepEqual(f.opts, want) {
		t.Errorf("opts = %+v, want %+v", f.opts, want)
	}
	wantReq := FromRequest{Ref: "oci://ghcr.io/acme/paths/http-service:1.0.0",
		Values: map[string]any{"service": map[string]any{"summary": "Gift card balances"}},
		Set:    []string{"service.summary=x"}, RunPlugins: []string{"dockerfile"}}
	if !reflect.DeepEqual(f.req, wantReq) {
		t.Errorf("req = %+v, want %+v", f.req, wantReq)
	}
	// Without dry_run the plan is applied.
	if _, isErr := callFrom(t, f.init, map[string]any{"from": "./paths/http", "name": "a", "owner": "o"}); isErr || f.opts.Plan {
		t.Errorf("no dry_run: plan=%v isErr=%v", f.opts.Plan, isErr)
	}
}

func TestCreateFromRefusesIntentArgs(t *testing.T) {
	f := &fakeInit{r: &pactify.Result{}}
	for _, arg := range []map[string]any{
		{"description": "an API"}, {"version": "1.0.0"}, {"interfaces": "[]"}, {"dependencies": "[]"},
		{"workload": "service"}, {"stores_data": true}, {"data_survives_restart": true},
		{"data_shared_across_instances": true}, {"data_loss_impact": "low"}, {"config_properties": "[]"},
		{"metadata": "{}"}, {"pactify": true}, {"paths": "[]"}, {"cluster": true}, {"namespaces": "[]"},
		{"delivery": true}, {"environments": "[]"}, {"fleet_roots": "[]"}, {"insecure_registry": true},
		{"registry_secret": "s"},
	} {
		args := map[string]any{"from": "./path", "name": "a", "owner": "o"}
		var name string
		for k, v := range arg {
			args[k], name = v, k
		}
		f.req = FromRequest{}
		text, isErr := callFrom(t, f.init, args)
		if !isErr || !strings.Contains(text, "does not take "+name) || f.req.Ref != "" {
			t.Errorf("%s: isErr=%v text=%q called=%v", name, isErr, text, f.req.Ref != "")
		}
	}
	// values, set and run_plugins need from.
	for _, k := range []string{"values", "set", "run_plugins"} {
		text, isErr := callFrom(t, f.init, map[string]any{"name": "a", k: "[]"})
		if !isErr || !strings.Contains(text, "go with from") {
			t.Errorf("%s without from: %q", k, text)
		}
	}
	// Malformed JSON is refused before the path is read.
	text, isErr := callFrom(t, f.init, map[string]any{"from": "./path", "name": "a", "owner": "o", "values": "{"})
	if !isErr || !strings.Contains(text, "invalid values JSON") {
		t.Errorf("bad values: %q", text)
	}
	// The command layer's error is the tool error.
	f.err = errors.New("not a golden path")
	if text, isErr := callFrom(t, f.init, map[string]any{"from": "./path", "name": "a", "owner": "o"}); !isErr || !strings.Contains(text, "not a golden path") {
		t.Errorf("init error: %q", text)
	}
}

func TestCreateFromWithoutInitializer(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "svc")
	text, isErr := callFrom(t, nil, map[string]any{"from": "./path", "name": "a", "owner": "o", "path": dir})
	if !isErr || !strings.Contains(text, "creating from a golden path serves stdio only") {
		t.Fatalf("got %q", text)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("nothing is written without an Initializer")
	}
}

func TestCreateFromReturnsSteps(t *testing.T) {
	steps := []fleet.CreationStep{
		{Step: fleet.StepSignature, Status: fleet.StepStatusUnknown, Detail: "a directory carries no signature"},
		{Step: fleet.StepPlugin, Name: "dockerfile", Status: fleet.StepStatusSkipped},
	}
	f := &fakeInit{r: &pactify.Result{Service: "gift-cards", Mode: "template", Steps: steps}}
	text, isErr := callFrom(t, f.init, map[string]any{"from": "./path", "name": "gift-cards", "owner": "o"})
	if isErr {
		t.Fatal(text)
	}
	var got pactify.Result
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Steps, steps) || got.Mode != "template" {
		t.Errorf("steps = %+v", got.Steps)
	}
}

func TestEditTemplate(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("pactoVersion: \"2.1\"\nservice:\n  name: http-service\n  version: 1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plugins := []contract.TemplatePlugin{{Name: "dockerfile", Description: "A Dockerfile", Options: map[string]string{"go": "1.27"}}}
	r, err := Edit(context.Background(), nil, EditInput{Path: dir, Template: &TemplateInput{Plugins: plugins}})
	if err != nil {
		t.Fatal(err)
	}
	c := readContract(t, dir)
	if c.PactoVersion != "2.4" || c.Service.Template == nil || !reflect.DeepEqual(c.Service.Template.Plugins, plugins) || !r.Summary.Template {
		t.Fatalf("set: %+v %+v", c.PactoVersion, c.Service.Template)
	}
	// No plugins writes template: {}.
	if _, err := Edit(context.Background(), nil, EditInput{Path: dir, Template: &TemplateInput{}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "pacto.yaml"))
	if !strings.Contains(string(raw), "template: {}") {
		t.Errorf("empty template:\n%s", raw)
	}
	r, err = Edit(context.Background(), nil, EditInput{Path: dir, Template: &TemplateInput{Clear: true}})
	if err != nil {
		t.Fatal(err)
	}
	if c := readContract(t, dir); c.Service.Template != nil || r.Summary.Template || !strings.Contains(strings.Join(r.Changes, ";"), "removed service template") {
		t.Errorf("clear: %+v", c.Service.Template)
	}
}

// TestEditTemplateTool: the tool takes template as JSON; origin has no setter.
func TestEditTemplateTool(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("pactoVersion: \"2.3\"\nservice:\n  name: http-service\n  version: 1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := callToolOn(t, NewServer(Authoring{}, "test"), "pacto_edit", map[string]any{"path": dir, "template": `{"plugins":[{"name":"ci"}]}`})
	if c := readContract(t, dir); res.IsError || c.Service.Template == nil || c.PactoVersion != "2.4" {
		t.Errorf("tool: %s", resultText(t, res))
	}
	if res := callToolOn(t, NewServer(Authoring{}, "test"), "pacto_edit", map[string]any{"path": dir, "origin": "{}"}); !res.IsError {
		t.Error("origin is not settable")
	}
}
