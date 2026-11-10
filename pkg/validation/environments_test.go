package validation

import (
	"strings"
	"testing"
	"testing/fstest"
	"time"

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
		!strings.Contains(st.Warnings[0].Message, "configuration platform: its schema comes from the contract oci://ghcr.io/acme/platform:1.0.0, which this validation does not resolve") {
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
	if last := stages[len(stages)-1]; len(stages) != 5 || stages[3].Name != "configuration-refs" || last.Name != "environments" || last.Status != StageFail {
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

// TestEnvironmentStage_NullDeletesAKey: a null in an environment's file
// removes that key from the effective values, at any depth.
func TestEnvironmentStage_NullDeletesAKey(t *testing.T) {
	c := envContract("staging.yaml")
	c.Configurations[1].Values = map[string]any{"replicaCount": 2, "ingress": map[string]any{"host": "h"}}
	strict := `{"type":"object","properties":{"replicaCount":{"type":"integer"},
"ingress":{"type":"object","required":["host"],"additionalProperties":false,"properties":{"host":{"type":"string"}}}}}`
	for file, want := range map[string]string{
		"chart:\n  replicaCount: null\n":                        "",
		"chart:\n  ingress: {host: null}\n":                     "missing property 'host'",
		"chart:\n  ingress: {host: h2, tls: null}\n":            "",
		"chart:\n  replicaCount: 3\n  ingress: {tls: {a: 1}}\n": "additional properties 'tls' not allowed",
		"chart:\n  ingress: {tls: {a: null}}\n":                 "additional properties 'tls' not allowed",
	} {
		fsys := envFS(map[string]string{"staging.yaml": file})
		fsys["chart.json"].Data = []byte(strict)
		st := EnvironmentStage(c, fsys)
		got := ""
		if len(st.Errors) > 0 {
			got = st.Errors[0].Message
		}
		if (want == "") != (got == "") || !strings.Contains(got, want) {
			t.Errorf("%q: got %q, want %q", file, got, want)
		}
	}
	if c.Configurations[1].Values["replicaCount"] != 2 {
		t.Fatal("the contract's defaults were changed")
	}
}

// TestEnvironmentStage_DottedKey: a key holding a dot or a slash is one
// segment: the path escapes it as a JSON Pointer and the line is its own.
func TestEnvironmentStage_DottedKey(t *testing.T) {
	fsys := envFS(map[string]string{"staging.yaml": "chart:\n  ingress: {host: h}\n  podAnnotations:\n    prometheus.io/scrape: true\n"})
	fsys["chart.json"].Data = []byte(`{"type":"object","properties":{"podAnnotations":{"additionalProperties":{"type":"string"}}}}`)
	st := EnvironmentStage(envContract("staging.yaml"), fsys)
	if len(st.Errors) != 1 || !strings.Contains(st.Errors[0].Message, "at /chart/podAnnotations/prometheus.io~1scrape: ") ||
		st.Errors[0].Line != 4 || st.Errors[0].Column != 5 {
		t.Fatalf("got %+v", st.Errors)
	}
}

// TestEnvironmentStage_SyntaxErrorLine: a file that does not parse is
// located at the line the parser names.
func TestEnvironmentStage_SyntaxErrorLine(t *testing.T) {
	st := EnvironmentStage(envContract("staging.yaml"), envFS(map[string]string{"staging.yaml": "chart:\n  - [\n"}))
	if len(st.Errors) != 1 || st.Errors[0].Line != 2 {
		t.Fatalf("got %+v", st.Errors)
	}
}

func TestExpiredEnvironmentWarnings(t *testing.T) {
	now := time.Date(2026, time.November, 20, 0, 0, 0, 0, time.UTC)
	c := &contract.Contract{Environments: []contract.Environment{
		{Name: "past", File: "p.yaml", Lifetime: "temporary", Expires: "2026-11-19T00:00:00Z"},
		{Name: "now", File: "n.yaml", Lifetime: "temporary", Expires: "2026-11-20T00:00:00Z"},
		{Name: "future", File: "f.yaml", Lifetime: "temporary", Expires: "2026-11-21T00:00:00Z"},
		{Name: "none", File: "x.yaml"},
		{Name: "bad", File: "b.yaml", Lifetime: "temporary", Expires: "next week"},
	}}
	got := ExpiredEnvironmentWarnings(c, now)
	if len(got) != 2 || got[0].Path != "environments[0].expires" || got[1].Path != "environments[1].expires" {
		t.Fatalf("got %+v", got)
	}
	w := got[0]
	if w.Code != "ENVIRONMENT_EXPIRED" || w.Message != "environment past expired at 2026-11-19T00:00:00Z; nothing was deleted: remove it with pacto_edit remove_environments or move expires" {
		t.Fatalf("got %+v", w)
	}
}

func TestEnvironmentStageHasNoExpiry(t *testing.T) {
	c := envContract("staging.yaml")
	c.Environments[0].Lifetime, c.Environments[0].Expires = "temporary", "2000-01-01T00:00:00Z"
	st := EnvironmentStageWith(c, envFS(map[string]string{"staging.yaml": "chart: {ingress: {host: s}}\n"}), nil)
	if strings.Contains(codes(st), "ENVIRONMENT_EXPIRED") {
		t.Fatalf("got %+v", st)
	}
}

func diffContract() *contract.Contract {
	return &contract.Contract{
		PactoVersion: "2.3",
		Configurations: []contract.Configuration{
			{Name: "app", Values: map[string]any{"LOG_LEVEL": "info", "db": map[string]any{"host": "db", "port": 5432}, "tags": []any{"a"}, "gone": 1}},
			{Name: "chart"},
		},
		Environments: []contract.Environment{{Name: "dev", File: "dev.yaml"}, {Name: "test", File: "test.yaml"}, {Name: "prod", File: "prod.yaml"}},
	}
}

var diffFiles = fstest.MapFS{
	"dev.yaml":  {Data: []byte("app:\n  LOG_LEVEL: debug\n  tags: [a, b]\n  gone: null\nchart:\n  only: dev\n")},
	"test.yaml": {Data: []byte("app:\n  db: {port: 6543}\n  tags: [a, b]\n")},
	"prod.yaml": {Data: []byte("{}\n")},
}

// diffRows is each difference as configuration.path.
func diffRows(ds []EnvironmentDifference) string {
	var out []string
	for _, d := range ds {
		out = append(out, d.Configuration+"."+d.Path)
	}
	return strings.Join(out, ",")
}

func TestEnvironmentDifferences(t *testing.T) {
	got, err := EnvironmentDifferences(diffContract(), diffFiles, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rows := diffRows(got); rows != "app.LOG_LEVEL,app.db.port,app.gone,app.tags,chart.only" {
		t.Fatalf("rows = %s", rows)
	}
	if v := got[0].Values; v["dev"] != "debug" || v["test"] != "info" || v["prod"] != "info" {
		t.Errorf("scalar values = %v", v)
	}
	if _, set := got[2].Values["dev"]; set || got[2].Values["prod"] != float64(1) {
		t.Errorf("null deletes a default key, so dev does not set it: %v", got[2].Values)
	}
	if _, set := got[4].Values["prod"]; set || got[4].Values["dev"] != "dev" {
		t.Errorf("a key set in one environment only: %v", got[4].Values)
	}
}

func TestEnvironmentDifferencesNamedSubset(t *testing.T) {
	got, err := EnvironmentDifferences(diffContract(), diffFiles, []string{"test", "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if rows := diffRows(got); rows != "app.LOG_LEVEL,app.db.port,app.gone,chart.only" {
		t.Fatalf("rows = %s (arrays compare whole and equal ones give no row)", rows)
	}
	if _, ok := got[0].Values["prod"]; ok {
		t.Errorf("an environment that was not chosen is listed: %v", got[0].Values)
	}
}

func TestEnvironmentDifferencesNone(t *testing.T) {
	if got, err := EnvironmentDifferences(diffContract(), fstest.MapFS{"dev.yaml": {}, "test.yaml": {}, "prod.yaml": {}}, nil); err != nil || len(got) != 0 {
		t.Fatalf("defaults equal everywhere: %v, %v", got, err)
	}
	if got, err := EnvironmentDifferences(diffContract(), diffFiles, []string{"dev"}); err != nil || got != nil {
		t.Fatalf("one environment: %v, %v", got, err)
	}
}

func TestEnvironmentDifferencesErrors(t *testing.T) {
	_, err := EnvironmentDifferences(diffContract(), diffFiles, []string{"dev", "nope"})
	if err == nil || !strings.Contains(err.Error(), `"nope"`) || !strings.Contains(err.Error(), "declared: dev, test, prod") {
		t.Errorf("undeclared name: %v", err)
	}
	_, err = EnvironmentDifferences(diffContract(), fstest.MapFS{"dev.yaml": {}, "test.yaml": {Data: []byte("[1]")}}, nil)
	if err == nil || !strings.Contains(err.Error(), "environment test: cannot read test.yaml") {
		t.Errorf("unreadable file: %v", err)
	}
	if _, err := EnvironmentDifferences(diffContract(), nil, nil); err == nil {
		t.Error("no bundle files: want an error")
	}
}

func TestEnvironmentDifferencesLeaves(t *testing.T) {
	c := diffContract()
	c.Configurations = []contract.Configuration{{Name: "x"}}
	got, err := EnvironmentDifferences(c, fstest.MapFS{"dev.yaml": {Data: []byte("x: 3\n")}, "test.yaml": {Data: []byte("x: {a: {}}\n")}, "prod.yaml": {Data: []byte("x: {}\n")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if diffRows(got) != "x.,x.a" || got[0].Values["dev"] != float64(3) {
		t.Fatalf("values that are not an object and a nested empty object are leaves: %+v", got)
	}
}
