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
		"  interfaces[api]  inferred  openapi api/openapi.yaml  from api/openapi.yaml:1\n",
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

func TestPrintExplainDeclarationLines(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.ExplainResult{Name: "invoices", Inferred: []contract.Inferred{
		{Field: "dependencies[ledger].compatibility", Value: "compatibility unknown", Source: "cluster:fleet reported versions 2.3.1",
			AsOf: "2026-11-14T09:58:12Z", Completeness: contract.CoveragePartial, Proposed: "^2.3.0"},
		{Field: "workload", Value: "service", Source: "cluster:manifest billing/Deployment/invoices deploy.yaml", AsOf: "2026-11-14T09:58:12Z"},
	},
		InferredCoverage: []contract.InferredCoverage{{Source: "pods", State: contract.CoverageComplete}, {Source: "calls", State: contract.CoveragePartial, Reason: "forbidden"}},
		InferredHidden:   &contract.InferredHidden{Services: 2, Callers: 1},
		Declaration: &contract.InferredDeclaration{State: contract.DeclarationDeclared, InferredFrom: "k8s:billing/Deployment/invoices",
			Asker: contract.InferredSigner{Subject: "ana@acme.com", Issuer: "https://accounts.google.com", Class: contract.SignerClassHuman}}}
	if err := printExplainResult(cmd, result, "text"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"  dependencies[ledger].compatibility  inferred  compatibility unknown  from cluster:fleet reported versions 2.3.1 (read from the cluster 2026-11-14T09:58:12Z, partial); proposed ^2.3.0\n",
		"  workload  inferred  service  from cluster:manifest billing/Deployment/invoices deploy.yaml (read from the cluster 2026-11-14T09:58:12Z)\n",
		"\nDeclared by ana@acme.com (https://accounts.google.com) from k8s:billing/Deployment/invoices; signer not checked: run pacto pull --verify-identity\n",
		"Cluster read: 1 of 2 sources complete\n",
		"Hidden from the cluster read: 2 services, 1 callers\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	result.Declaration.InferredFrom = ""
	buf.Reset()
	if err := printExplainResult(cmd, result, "text"); err != nil || !strings.Contains(buf.String(), " from a folder; signer") {
		t.Fatalf("folder declaration:\n%s", buf.String())
	}
}

// Explain names where the declared service's source lives and how Pacto
// found it, for a declaration and a draft alike.
func TestExplainPrintsRepository(t *testing.T) {
	repo := &contract.InferredSourceRepository{URL: "https://github.com/acme/invoices", Rung: contract.RepositoryRungGitOps, Folder: "deploy", FolderRung: contract.RepositoryRungGitOps}
	for _, state := range []string{contract.DeclarationDeclared, contract.DeclarationDraft} {
		cmd, buf := testCmd()
		result := &app.ExplainResult{Name: "invoices", Declaration: &contract.InferredDeclaration{State: state, Repository: repo,
			Asker: contract.InferredSigner{Subject: "ana@acme.com", Issuer: "https://accounts.google.com"}}}
		if err := printExplainResult(cmd, result, "text"); err != nil ||
			!strings.Contains(buf.String(), "\nRepository (the record claims): github.com/acme/invoices (GitOps source), folder: deploy/ (GitOps source)\n") {
			t.Fatalf("%s: %v\n%s", state, err, buf.String())
		}
	}
}

// A draft reads as a proposal awaiting a person, never as declared, and a
// declaration with no field still says who declared it.
func TestPrintExplainDraftAndFieldlessDeclaration(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.ExplainResult{Name: "invoices", Declaration: &contract.InferredDeclaration{State: contract.DeclarationDraft, OnBehalfOf: "ana",
		InferredFrom: "k8s:billing/Deployment/invoices", Asker: contract.InferredSigner{Issuer: contract.SignerUnsigned, Class: contract.SignerClassAgent},
		Requested: []contract.InferredAcceptance{{Field: "dependencies[ledger].compatibility", Range: "^2.3.0"}}}}
	if err := printExplainResult(cmd, result, "text"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"\nDraft by nobody (unsigned) for ana (asserted) from k8s:billing/Deployment/invoices, awaiting a person: accept it with pacto declare invoices --from-draft <tag>; signer not checked",
		"  requests dependencies[ledger].compatibility ^2.3.0\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Declared by") {
		t.Errorf("a draft reads as declared:\n%s", out)
	}
	result.Declaration = &contract.InferredDeclaration{State: contract.DeclarationDeclared, Asker: contract.InferredSigner{Subject: "ana@acme.com", Issuer: "https://accounts.google.com"}}
	buf.Reset()
	if err := printExplainResult(cmd, result, "text"); err != nil || !strings.Contains(buf.String(), "Declared by ana@acme.com") {
		t.Fatalf("a declaration with no field:\n%s", buf.String())
	}
}
