package cli

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"gopkg.in/yaml.v3"
)

// explainCompletion explains a copy of discover's completion fixture under a
// 2.5 pacto.yaml that declares only the service.
func explainCompletion(t *testing.T) *app.ExplainResult {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("..", "discover", "testdata", "complete"))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("pactoVersion: \"2.5\"\nservice: {name: app, version: 1.0.0}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := app.NewService(nil, nil).Explain(context.Background(), app.ExplainOptions{Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// section is the indented block of out under heading, dedented by two.
func section(out, heading string) string {
	_, rest, ok := strings.Cut(out, heading+"\n")
	if !ok {
		return ""
	}
	var lines []string
	for _, l := range strings.Split(rest, "\n") {
		if !strings.HasPrefix(l, "  ") {
			break
		}
		lines = append(lines, l[2:])
	}
	return strings.Join(lines, "\n")
}

func TestExplainTextInferredAndDeclare(t *testing.T) {
	cmd, buf := testCmd()
	if err := printExplainResult(cmd, explainCompletion(t), "text"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"\nInferred (not part of the contract):\n",
		"  interfaces[api]  inferred  openapi api/openapi.yaml  from api/openapi.yaml\n",
		"  workload  inferred  service (Deployment app)  from deploy/app.yaml:2\n",
		"  dependencies[billing]  inferred  billing  from deploy/app.yaml:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if !regexp.MustCompile(`(?m)^  configuration\.DB_PASSWORD  inferred  string, writeOnly  from deploy/app\.yaml:\d+ \(value not read\)$`).MatchString(out) {
		t.Errorf("DB_PASSWORD line wrong:\n%s", out)
	}
	if regexp.MustCompile(`configuration\.PORT .*value not read`).MatchString(out) {
		t.Errorf("PORT reads as not read:\n%s", out)
	}
	var declare map[string]any
	if err := yaml.Unmarshal([]byte(section(out, "To declare in pacto.yaml:")), &declare); err != nil {
		t.Fatalf("To declare does not parse: %v\n%s", err, out)
	}
	for _, key := range []string{"workload", "service", "interfaces", "capabilities", "dependencies"} {
		if declare[key] == nil {
			t.Errorf("To declare lacks %s: %v", key, declare)
		}
	}
	if deps, _ := declare["dependencies"].([]any); len(deps) < 2 {
		t.Errorf("dependencies not grouped under one key: %v", declare["dependencies"])
	}
	var props map[string]map[string]any
	if err := yaml.Unmarshal([]byte(section(out, "To declare in the configuration schema, under properties:")), &props); err != nil || props["DB_PASSWORD"]["writeOnly"] != true || props["PORT"]["type"] == nil {
		t.Fatalf("configuration properties %v %v\n%s", props, err, out)
	}
}

func TestExplainNeverPrintsValues(t *testing.T) {
	result := explainCompletion(t)
	for _, format := range []string{"text", "json"} {
		cmd, buf := testCmd()
		if err := printExplainResult(cmd, result, format); err != nil {
			t.Fatal(err)
		}
		for _, leak := range []string{"hunter2", "u4l-s3cret", "v4lues-s3cret", "c0nfigmap-s3cret"} {
			if strings.Contains(buf.String(), leak) {
				t.Fatalf("%s output holds %q", format, leak)
			}
		}
	}
}

func TestExplainUnreadableRecord(t *testing.T) {
	cmd, buf := testCmd()
	if err := printExplainResult(cmd, &app.ExplainResult{Name: "svc", InferredUnreadable: true}, "text"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Inferred (not part of the contract):\n  Pacto could not read the inferred record\n") || strings.Contains(buf.String(), "To declare") {
		t.Fatalf("unreadable record:\n%s", buf.String())
	}
}

// A registry record may carry fields with no declare snippet: they are
// listed, and nothing is offered to declare.
func TestExplainInferredWithoutDeclare(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.ExplainResult{Name: "svc", Inferred: []contract.Inferred{{Field: "workload", Value: "service", Source: "a.yaml:1", Confidence: "likely"}}}
	if err := printExplainResult(cmd, result, "text"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "  workload  inferred  service  from a.yaml:1\n") || strings.Contains(buf.String(), "To declare") {
		t.Fatalf("output:\n%s", buf.String())
	}
}
