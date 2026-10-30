package validation

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const chartSchema = `{"type":"object","properties":{"replicaCount":{"type":"integer"},
"ingress":{"type":"object","required":["host"],"properties":{"host":{"type":"string"}}}},"required":["ingress"]}`

func envContract(values ...string) *contract.Contract {
	c := &contract.Contract{
		PactoVersion: "2.3",
		Service:      contract.Service{Name: "svc", Version: "1.0.0"},
		Configurations: []contract.Configuration{
			{Name: "app", Schema: "app.json", Values: map[string]any{"LOG_LEVEL": "info"}},
			{Name: "chart", Schema: "chart.json", Required: true},
		},
	}
	for i, v := range values {
		c.Environments = append(c.Environments, contract.Environment{Name: []string{"staging", "production"}[i], File: v})
	}
	return c
}

func envFS(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{
		"app.json":   {Data: []byte(`{"type":"object","properties":{"LOG_LEVEL":{"enum":["debug","info","warn"]}},"required":["LOG_LEVEL"]}`)},
		"chart.json": {Data: []byte(chartSchema)},
	}
	for k, v := range files {
		fsys[k] = &fstest.MapFile{Data: []byte(v)}
	}
	return fsys
}

func codes(st Stage) string {
	var out []string
	for _, e := range st.Errors {
		out = append(out, e.Code)
	}
	for _, w := range st.Warnings {
		out = append(out, w.Code)
	}
	return strings.Join(out, ",")
}

func TestEnvironmentStage_NoEnvironments(t *testing.T) {
	st := EnvironmentStage(envContract(), nil)
	if st.Status != StageSkip || st.Reason != "no environments declared" {
		t.Fatalf("got %+v", st)
	}
}

func TestEnvironmentStage_NoBundleFiles(t *testing.T) {
	st := EnvironmentStage(envContract("staging.yaml"), nil)
	if st.Status != StageSkip || st.Reason != "could not verify: staging" || codes(st) != "ENVIRONMENT_VALUES_UNVERIFIED" {
		t.Fatalf("got %+v", st)
	}
}

func TestEnvironmentStage_Pass(t *testing.T) {
	// app inherits LOG_LEVEL from its defaults; chart is fully given.
	st := EnvironmentStage(envContract("staging.yaml"), envFS(map[string]string{
		"staging.yaml": "chart:\n  replicaCount: 2\n  ingress: {host: staging.example.com}\n",
	}))
	if st.Status != StagePass || codes(st) != "" {
		t.Fatalf("got %+v", st)
	}
}

func TestEnvironmentStage_InvalidValueIsLocatedInTheValuesFile(t *testing.T) {
	st := EnvironmentStage(envContract("staging.yaml", "prod.yaml"), envFS(map[string]string{
		"staging.yaml": "chart:\n  ingress: {host: s.example.com}\n",
		"prod.yaml":    "app:\n  LOG_LEVEL: loud\nchart:\n  replicaCount: 5\n  ingress: {}\n",
	}))
	if st.Status != StageFail || codes(st) != "ENVIRONMENT_VALUES_INVALID,ENVIRONMENT_VALUES_INVALID" {
		t.Fatalf("got %+v", st)
	}
	byMsg := map[string]contract.ValidationError{}
	for _, e := range st.Errors {
		if e.File != "prod.yaml" || e.Path != "environments[1].file" {
			t.Fatalf("file/path: %+v", e)
		}
		byMsg[e.Message] = e
	}
	host := byMsg["environment production: configuration chart at /chart/ingress: missing property 'host'"]
	if host.Line != 5 || host.Column != 3 {
		t.Fatalf("host error at %d:%d, want 5:3; errors %+v", host.Line, host.Column, st.Errors)
	}
	level := byMsg["environment production: configuration app at /app/LOG_LEVEL: value must be one of 'debug', 'info', 'warn'"]
	if level.Line != 2 {
		t.Fatalf("LOG_LEVEL error: %+v", st.Errors)
	}
}

func TestEnvironmentStage_UnknownConfiguration(t *testing.T) {
	st := EnvironmentStage(envContract("staging.yaml"), envFS(map[string]string{
		"staging.yaml": "chart:\n  ingress: {host: h}\ncharts:\n  replicaCount: 1\n",
	}))
	if codes(st) != "ENVIRONMENT_UNKNOWN_CONFIGURATION" || !strings.Contains(st.Errors[0].Message, `did you mean "chart"?`) || st.Errors[0].Line != 3 {
		t.Fatalf("got %+v", st)
	}
}

func TestEnvironmentStage_RequiredConfigurationWithoutValues(t *testing.T) {
	st := EnvironmentStage(envContract("staging.yaml"), envFS(map[string]string{"staging.yaml": ""}))
	if codes(st) != "ENVIRONMENT_VALUES_MISSING" || !strings.Contains(st.Errors[0].Message, "required configuration chart has no values here") ||
		st.Errors[0].Line != 0 {
		t.Fatalf("got %+v", st)
	}
	// Defaults that satisfy the schema are enough.
	c := envContract("staging.yaml")
	c.Configurations[1].Values = map[string]any{"ingress": map[string]any{"host": "h"}}
	if st := EnvironmentStage(c, envFS(map[string]string{"staging.yaml": "app: {LOG_LEVEL: warn}\n"})); st.Status != StagePass {
		t.Fatalf("got %+v", st)
	}
}

func TestEnvironmentStage_ValuesThatAreNotAnObject(t *testing.T) {
	st := EnvironmentStage(envContract("staging.yaml"), envFS(map[string]string{"staging.yaml": "chart: 3\napp:\n"}))
	// chart: 3 replaces the defaults and fails type object; app: null keeps the defaults.
	if codes(st) != "ENVIRONMENT_VALUES_INVALID" || !strings.Contains(st.Errors[0].Message, "configuration chart at /chart:") {
		t.Fatalf("got %+v", st)
	}
}

func TestEnvironmentStage_UnreadableFile(t *testing.T) {
	for name, fsys := range map[string]fstest.MapFS{
		"missing":    envFS(nil),
		"not a list": envFS(map[string]string{"staging.yaml": "- chart\n"}),
	} {
		st := EnvironmentStage(envContract("staging.yaml"), fsys)
		if codes(st) != "ENVIRONMENT_VALUES_INVALID" || !strings.Contains(st.Errors[0].Message, "cannot read staging.yaml") {
			t.Fatalf("%s: got %+v", name, st)
		}
	}
}

func TestEnvironmentStage_Unverified(t *testing.T) {
	c := envContract("staging.yaml")
	c.Configurations = append(c.Configurations, contract.Configuration{Name: "platform", Ref: "oci://ghcr.io/acme/platform:1.0.0"})
	fsys := envFS(map[string]string{"staging.yaml": "chart:\n  ingress: {host: h}\nplatform: {region: eu}\n"})
	st := EnvironmentStage(c, fsys)
	if st.Status != StageSkip || st.Reason != "could not verify: staging/platform" || codes(st) != "ENVIRONMENT_VALUES_UNVERIFIED" ||
		!strings.Contains(st.Warnings[0].Message, "oci://ghcr.io/acme/platform:1.0.0") {
		t.Fatalf("got %+v", st)
	}
	// An error still fails the stage.
	fsys["staging.yaml"] = &fstest.MapFile{Data: []byte("chart: {}\nplatform: {region: eu}\n")}
	if st := EnvironmentStage(c, fsys); st.Status != StageFail {
		t.Fatalf("got %+v", st)
	}
	// A schema file that cannot be read is unverified too.
	delete(fsys, "chart.json")
	fsys["staging.yaml"] = &fstest.MapFile{Data: []byte("chart: {}\n")}
	if st := EnvironmentStage(envContract("staging.yaml"), fsys); st.Reason != "could not verify: staging/chart" {
		t.Fatalf("got %+v", st)
	}
}

// TestValidateRunsTheEnvironmentsStage: every entry point runs the
// environments stage after policy, so pack, push, publish, the operator and
// the dashboard refuse what validate refuses.
func TestValidateRunsTheEnvironmentsStage(t *testing.T) {
	raw := []byte(`pactoVersion: "2.3"
service: {name: svc, version: 1.0.0}
configurations:
  - {name: app, schema: app.json, required: false, values: {LOG_LEVEL: info}}
  - {name: chart, schema: chart.json, required: true}
environments:
  - {name: staging, file: staging.yaml}
`)
	c, err := contract.Parse(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	fsys := envFS(map[string]string{"staging.yaml": "chart: {}\n"})
	if r := Validate(c, raw, fsys); r.IsValid() || r.Errors[0].Code != "ENVIRONMENT_VALUES_INVALID" {
		t.Fatalf("Validate: %+v", r)
	}
	stages := ValidateStagesWithResolver(t.Context(), c, raw, fsys, nil)
	if last := stages[len(stages)-1]; len(stages) != 4 || last.Name != "environments" || last.Status != StageFail {
		t.Fatalf("stages: %+v", stages)
	}
	// An earlier failure skips it.
	stages = ValidateStagesWithResolver(t.Context(), c, []byte("pactoVersion: \"2.3\"\n"), fsys, nil)
	if last := stages[len(stages)-1]; last.Name != "environments" || last.Status != StageSkip || last.Reason != "structural failed" {
		t.Fatalf("stages: %+v", stages)
	}
	delete(fsys, "chart.json")
	stages = ValidateStagesWithResolver(t.Context(), c, raw, fsys, nil)
	if last := stages[len(stages)-1]; last.Name != "environments" || last.Reason != "cross-field failed" {
		t.Fatalf("stages: %+v", stages)
	}
}
