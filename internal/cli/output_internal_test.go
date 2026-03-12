package cli

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/trianalab/pacto/internal/app"
	"github.com/trianalab/pacto/internal/diff"
	"github.com/trianalab/pacto/internal/graph"
	"github.com/trianalab/pacto/pkg/contract"
)

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, fmt.Errorf("write error") }

func testCmd() (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{Use: "test"}
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	return cmd, buf
}

func TestPrintInitResult_Text(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.InitResult{Dir: "my-svc", Path: "my-svc/pacto.yaml"}
	if err := printInitResult(cmd, result, "text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Created my-svc/") {
		t.Errorf("expected 'Created my-svc/' in output, got %q", out)
	}
	if !strings.Contains(out, "my-svc/pacto.yaml") {
		t.Errorf("expected 'my-svc/pacto.yaml' in output, got %q", out)
	}
}

func TestPrintInitResult_JSON(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.InitResult{Dir: "my-svc", Path: "my-svc/pacto.yaml"}
	if err := printInitResult(cmd, result, "json"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), `"Dir"`) {
		t.Errorf("expected JSON output, got %q", buf.String())
	}
}

func TestPrintValidateResult_Valid(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.ValidateResult{Path: "pacto.yaml", Valid: true}
	if err := printValidateResult(cmd, result, "text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "is valid") {
		t.Errorf("expected 'is valid', got %q", buf.String())
	}
}

func TestPrintValidateResult_Invalid(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.ValidateResult{
		Path:  "pacto.yaml",
		Valid: false,
		Errors: []contract.ValidationError{
			{Code: "TEST_ERR", Path: "service.name", Message: "bad name"},
		},
		Warnings: []contract.ValidationWarning{
			{Code: "TEST_WARN", Path: "runtime", Message: "check this"},
		},
	}
	if err := printValidateResult(cmd, result, "text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "is invalid") {
		t.Errorf("expected 'is invalid', got %q", out)
	}
	if !strings.Contains(out, "ERROR [TEST_ERR]") {
		t.Errorf("expected error output, got %q", out)
	}
	if !strings.Contains(out, "WARN  [TEST_WARN]") {
		t.Errorf("expected warning output, got %q", out)
	}
}

func TestPrintPackResult_Text(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.PackResult{Output: "svc-1.0.0.tar.gz", Name: "svc", Version: "1.0.0"}
	if err := printPackResult(cmd, result, "text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Packed svc@1.0.0") {
		t.Errorf("expected pack output, got %q", buf.String())
	}
}

func TestPrintPackResult_JSON(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.PackResult{Output: "svc-1.0.0.tar.gz", Name: "svc", Version: "1.0.0"}
	if err := printPackResult(cmd, result, "json"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), `"Output"`) {
		t.Errorf("expected JSON output, got %q", buf.String())
	}
}

func TestPrintPushResult_Text(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.PushResult{Ref: "ghcr.io/acme/svc:1.0.0", Digest: "sha256:abc", Name: "svc", Version: "1.0.0"}
	if err := printPushResult(cmd, result, "text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Pushed svc@1.0.0") {
		t.Errorf("expected push output, got %q", out)
	}
	if !strings.Contains(out, "sha256:abc") {
		t.Errorf("expected digest in output, got %q", out)
	}
}

func TestPrintPullResult_Text(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.PullResult{Ref: "ghcr.io/acme/svc:1.0.0", Output: "svc", Name: "svc", Version: "1.0.0"}
	if err := printPullResult(cmd, result, "text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Pulled svc@1.0.0") {
		t.Errorf("expected pull output, got %q", buf.String())
	}
}

func TestPrintDiffResult_NoChanges(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.DiffResult{OldPath: "a.yaml", NewPath: "b.yaml", Classification: "NON_BREAKING"}
	if err := printDiffResult(cmd, result, "text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "NON_BREAKING") {
		t.Errorf("expected classification, got %q", out)
	}
	if !strings.Contains(out, "No changes detected") {
		t.Errorf("expected 'No changes detected', got %q", out)
	}
}

func TestPrintDiffResult_WithDependencyDiffs(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.DiffResult{
		OldPath:        "a.yaml",
		NewPath:        "b.yaml",
		Classification: "BREAKING",
		DependencyDiffs: []app.DependencyDiff{
			{
				Name:           "my-dep",
				Classification: "BREAKING",
				Changes: []diff.Change{
					{Path: "openapi.paths[/users]", Type: diff.Removed, Classification: diff.Breaking, Reason: "API path /users removed"},
				},
			},
		},
	}
	if err := printDiffResult(cmd, result, "text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Dependency my-dep [BREAKING] (1):") {
		t.Errorf("expected dependency diff header, got %q", out)
	}
	if !strings.Contains(out, "openapi.paths[/users]") {
		t.Errorf("expected change path in output, got %q", out)
	}
	if strings.Contains(out, "No changes detected") {
		t.Errorf("should not show 'No changes detected' when dependency diffs exist, got %q", out)
	}
}

func TestPrintDiffResult_OnlyDependencyDiffs(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.DiffResult{
		OldPath:        "a.yaml",
		NewPath:        "b.yaml",
		Classification: "NON_BREAKING",
		DependencyDiffs: []app.DependencyDiff{
			{
				Name:           "governance",
				Classification: "NON_BREAKING",
				Changes: []diff.Change{
					{Path: "service.version", Type: diff.Modified, Classification: diff.NonBreaking, Reason: "version changed"},
				},
			},
		},
	}
	if err := printDiffResult(cmd, result, "text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "No changes detected") {
		t.Errorf("should not show 'No changes detected' when dependency diffs exist, got %q", out)
	}
	if !strings.Contains(out, "Dependency governance") {
		t.Errorf("expected dependency diff section, got %q", out)
	}
}

func TestPrintDiffResult_WithChanges(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.DiffResult{
		OldPath:        "a.yaml",
		NewPath:        "b.yaml",
		Classification: "BREAKING",
		Changes: []diff.Change{
			{Path: "service.name", Type: diff.Modified, Classification: diff.Breaking, Reason: "name changed"},
		},
	}
	if err := printDiffResult(cmd, result, "text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Changes (1)") {
		t.Errorf("expected 'Changes (1)', got %q", out)
	}
	if !strings.Contains(out, "service.name") {
		t.Errorf("expected change path in output, got %q", out)
	}
}

func TestPrintDiffResult_WithGraphDiff(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.DiffResult{
		OldPath:        "a.yaml",
		NewPath:        "b.yaml",
		Classification: "NON_BREAKING",
		GraphDiff: &graph.GraphDiff{
			Root: graph.DiffNode{
				Name: "svc",
				Children: []graph.DiffNode{
					{
						Name:    "redis",
						Version: "7.2.0",
						Change:  &graph.GraphChange{Name: "redis", ChangeType: graph.AddedNode, NewVersion: "7.2.0"},
					},
				},
			},
			Changes: []graph.GraphChange{
				{Name: "redis", ChangeType: graph.AddedNode, NewVersion: "7.2.0"},
			},
		},
	}
	if err := printDiffResult(cmd, result, "text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Dependency graph changes:") {
		t.Errorf("expected graph diff section, got %q", out)
	}
	if !strings.Contains(out, "redis") {
		t.Errorf("expected redis in graph diff, got %q", out)
	}
	if !strings.Contains(out, "+7.2.0") {
		t.Errorf("expected +7.2.0 in graph diff, got %q", out)
	}
	if strings.Contains(out, "No changes detected") {
		t.Errorf("should not show 'No changes detected' when graph has changes, got %q", out)
	}
}

func TestPrintDiffResult_NoGraphDiffChanges(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.DiffResult{
		OldPath:        "a.yaml",
		NewPath:        "b.yaml",
		Classification: "NON_BREAKING",
		GraphDiff:      &graph.GraphDiff{Root: graph.DiffNode{Name: "svc"}},
	}
	if err := printDiffResult(cmd, result, "text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "Dependency graph changes:") {
		t.Errorf("did not expect graph diff section when no changes, got %q", out)
	}
}

func TestPrintGraphResult_Simple(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.GraphResult{
		Root: &graph.Node{Name: "svc", Version: "1.0.0"},
	}
	if err := printGraphResult(cmd, result, "text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "svc@1.0.0") {
		t.Errorf("expected root name, got %q", buf.String())
	}
}

func TestPrintGraphResult_WithDepsAndCyclesAndConflicts(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.GraphResult{
		Root: &graph.Node{
			Name:    "svc",
			Version: "1.0.0",
			Dependencies: []graph.Edge{
				{Ref: "ghcr.io/dep:1.0.0", Node: &graph.Node{Name: "dep", Version: "1.0.0"}},
				{Ref: "ghcr.io/err:1.0.0", Error: "fetch failed"},
				{Ref: "ghcr.io/bare:1.0.0"},
			},
		},
		Cycles: [][]string{{"svc", "dep", "svc"}},
		Conflicts: []graph.Conflict{
			{Name: "dep", Versions: []string{"1.0.0", "2.0.0"}},
		},
	}
	if err := printGraphResult(cmd, result, "text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "├─ dep@1.0.0") {
		t.Errorf("expected tree-style dep, got %q", out)
	}
	if !strings.Contains(out, "fetch failed") {
		t.Errorf("expected error edge, got %q", out)
	}
	if !strings.Contains(out, "bare:1.0.0") {
		t.Errorf("expected bare ref, got %q", out)
	}
	if !strings.Contains(out, "Cycles (1)") {
		t.Errorf("expected cycles section, got %q", out)
	}
	if !strings.Contains(out, "Conflicts (1)") {
		t.Errorf("expected conflicts section, got %q", out)
	}
}

func TestPrintExplainResult_Full(t *testing.T) {
	cmd, buf := testCmd()
	port := 8080
	result := &app.ExplainResult{
		Name:         "svc",
		Version:      "1.0.0",
		Owner:        "team/platform",
		PactoVersion: "1.0",
		Runtime: app.ExplainRuntime{
			WorkloadType:    "service",
			StateType:       "stateless",
			Scope:           "local",
			Durability:      "ephemeral",
			DataCriticality: "low",
		},
		Interfaces: []app.ExplainInterface{
			{Name: "api", Type: "http", Port: &port, Visibility: "internal"},
			{Name: "events", Type: "grpc"},
		},
		Dependencies: []app.ExplainDependency{
			{Ref: "ghcr.io/dep:1.0.0", Required: true, Compatibility: "^1.0.0"},
			{Ref: "ghcr.io/opt:2.0.0", Required: false, Compatibility: "^2.0.0"},
		},
		Scaling: &contract.Scaling{Min: 1, Max: 3},
	}
	if err := printExplainResult(cmd, result, "text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Service: svc@1.0.0") {
		t.Errorf("expected service header, got %q", out)
	}
	if !strings.Contains(out, "Owner: team/platform") {
		t.Errorf("expected owner, got %q", out)
	}
	if !strings.Contains(out, "port 8080") {
		t.Errorf("expected port, got %q", out)
	}
	if !strings.Contains(out, "internal") {
		t.Errorf("expected visibility, got %q", out)
	}
	if !strings.Contains(out, "events (grpc)") {
		t.Errorf("expected grpc interface without port, got %q", out)
	}
	if !strings.Contains(out, "required") {
		t.Errorf("expected required dep, got %q", out)
	}
	if !strings.Contains(out, "optional") {
		t.Errorf("expected optional dep, got %q", out)
	}
	if !strings.Contains(out, "Scaling: 1-3") {
		t.Errorf("expected scaling, got %q", out)
	}
}

func TestPrintExplainResult_Minimal(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.ExplainResult{
		Name:         "svc",
		Version:      "1.0.0",
		PactoVersion: "1.0",
		Runtime: app.ExplainRuntime{
			WorkloadType: "service",
			StateType:    "stateless",
		},
	}
	if err := printExplainResult(cmd, result, "text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "Owner:") {
		t.Errorf("did not expect Owner section, got %q", out)
	}
	if strings.Contains(out, "Interfaces") {
		t.Errorf("did not expect Interfaces section, got %q", out)
	}
	if strings.Contains(out, "Scaling") {
		t.Errorf("did not expect Scaling section, got %q", out)
	}
}

func TestPrintGenerateResult_Text(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.GenerateResult{Plugin: "k8s", OutputDir: "k8s-output", FilesCount: 3, Message: "generated manifests"}
	if err := printGenerateResult(cmd, result, "text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Generated 3 file(s) using k8s") {
		t.Errorf("expected generate output, got %q", out)
	}
	if !strings.Contains(out, "Message: generated manifests") {
		t.Errorf("expected message, got %q", out)
	}
}

func TestPrintGenerateResult_NoMessage(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.GenerateResult{Plugin: "k8s", OutputDir: "k8s-output", FilesCount: 1}
	if err := printGenerateResult(cmd, result, "text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(buf.String(), "Message:") {
		t.Errorf("did not expect Message line, got %q", buf.String())
	}
}

func TestPrintValidateResult_JSON(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.ValidateResult{Path: "pacto.yaml", Valid: true}
	if err := printValidateResult(cmd, result, "json"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), `"Valid": true`) {
		t.Errorf("expected JSON output, got %q", buf.String())
	}
}

func TestPrintDiffResult_JSON(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.DiffResult{Classification: "NON_BREAKING"}
	if err := printDiffResult(cmd, result, "json"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), `"classification"`) {
		t.Errorf("expected JSON output, got %q", buf.String())
	}
}

func TestPrintGraphResult_JSON(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.GraphResult{Root: &graph.Node{Name: "svc", Version: "1.0.0"}}
	if err := printGraphResult(cmd, result, "json"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), `"name"`) {
		t.Errorf("expected JSON output, got %q", buf.String())
	}
}

func TestPrintExplainResult_JSON(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.ExplainResult{Name: "svc", Version: "1.0.0", PactoVersion: "1.0"}
	if err := printExplainResult(cmd, result, "json"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), `"name"`) {
		t.Errorf("expected JSON output, got %q", buf.String())
	}
}

func TestPrintGenerateResult_JSON(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.GenerateResult{Plugin: "k8s", OutputDir: "out", FilesCount: 1}
	if err := printGenerateResult(cmd, result, "json"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), `"plugin"`) {
		t.Errorf("expected JSON output, got %q", buf.String())
	}
}

func TestPrintDocResult_Text(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.DocResult{
		Markdown: "# my-svc\n\n| Field | Value |\n",
	}
	if err := printDocResult(cmd, result, "text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "# my-svc") {
		t.Errorf("expected markdown output, got %q", out)
	}
}

func TestPrintDocResult_TextWithPath(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.DocResult{
		Markdown: "# my-svc\n",
		Path:     "/tmp/docs/my-svc.md",
	}
	if err := printDocResult(cmd, result, "text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "# my-svc") {
		t.Errorf("expected markdown in output, got %q", out)
	}
	if !strings.Contains(out, "Wrote /tmp/docs/my-svc.md") {
		t.Errorf("expected path notice in output, got %q", out)
	}
}

func TestPrintDocResult_JSON(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.DocResult{
		Markdown: "# svc\n",
		Path:     "/tmp/svc.md",
	}
	if err := printDocResult(cmd, result, "json"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"markdown"`) {
		t.Errorf("expected JSON output with markdown field, got %q", out)
	}
	if !strings.Contains(out, `"path"`) {
		t.Errorf("expected JSON output with path field, got %q", out)
	}
}

func TestPrintDiffResult_Markdown_NoChanges(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.DiffResult{OldPath: "a.yaml", NewPath: "b.yaml", Classification: "NON_BREAKING"}
	if err := printDiffResult(cmd, result, "markdown"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "## Contract Diff") {
		t.Errorf("expected markdown heading, got %q", out)
	}
	if !strings.Contains(out, "`NON_BREAKING`") {
		t.Errorf("expected classification in backticks, got %q", out)
	}
	if !strings.Contains(out, "No changes detected") {
		t.Errorf("expected 'No changes detected', got %q", out)
	}
}

func TestPrintDiffResult_Markdown_WithChanges(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.DiffResult{
		OldPath:        "a.yaml",
		NewPath:        "b.yaml",
		Classification: "BREAKING",
		Changes: []diff.Change{
			{Path: "service.name", Type: diff.Modified, Classification: diff.Breaking, Reason: "name changed"},
		},
	}
	if err := printDiffResult(cmd, result, "markdown"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "### Changes (1)") {
		t.Errorf("expected changes heading, got %q", out)
	}
	if !strings.Contains(out, "| Classification | Path | Type | Reason |") {
		t.Errorf("expected table header, got %q", out)
	}
	if !strings.Contains(out, "`service.name`") {
		t.Errorf("expected path in backticks, got %q", out)
	}
}

func TestPrintDiffResult_Markdown_WithDependencyDiffs(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.DiffResult{
		OldPath:        "a.yaml",
		NewPath:        "b.yaml",
		Classification: "BREAKING",
		DependencyDiffs: []app.DependencyDiff{
			{
				Name:           "my-dep",
				Classification: "BREAKING",
				Changes: []diff.Change{
					{Path: "openapi.paths[/users]", Type: diff.Removed, Classification: diff.Breaking, Reason: "API path /users removed"},
				},
			},
		},
	}
	if err := printDiffResult(cmd, result, "markdown"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "### Dependency: my-dep (`BREAKING`)") {
		t.Errorf("expected dependency heading, got %q", out)
	}
	if !strings.Contains(out, "`openapi.paths[/users]`") {
		t.Errorf("expected change path in backticks, got %q", out)
	}
}

func TestPrintDiffResult_Markdown_WithGraphDiff(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.DiffResult{
		OldPath:        "a.yaml",
		NewPath:        "b.yaml",
		Classification: "NON_BREAKING",
		GraphDiff: &graph.GraphDiff{
			Root: graph.DiffNode{
				Name: "svc",
				Children: []graph.DiffNode{
					{
						Name:    "redis",
						Version: "7.2.0",
						Change:  &graph.GraphChange{Name: "redis", ChangeType: graph.AddedNode, NewVersion: "7.2.0"},
					},
				},
			},
			Changes: []graph.GraphChange{
				{Name: "redis", ChangeType: graph.AddedNode, NewVersion: "7.2.0"},
			},
		},
	}
	if err := printDiffResult(cmd, result, "markdown"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "### Dependency Graph Changes") {
		t.Errorf("expected graph diff heading, got %q", out)
	}
	if !strings.Contains(out, "```") {
		t.Errorf("expected code block, got %q", out)
	}
	if !strings.Contains(out, "redis") {
		t.Errorf("expected redis in output, got %q", out)
	}
}

func TestPrintDiffResult_Markdown_NoGraphDiffChanges(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.DiffResult{
		OldPath:        "a.yaml",
		NewPath:        "b.yaml",
		Classification: "NON_BREAKING",
		GraphDiff:      &graph.GraphDiff{Root: graph.DiffNode{Name: "svc"}},
	}
	if err := printDiffResult(cmd, result, "markdown"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "### Dependency Graph Changes") {
		t.Errorf("did not expect graph diff heading when no changes, got %q", out)
	}
}

func TestFormatResult_MarkdownFallbackToText(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.InitResult{Dir: "my-svc", Path: "my-svc/pacto.yaml"}
	if err := printInitResult(cmd, result, "markdown"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Created my-svc/") {
		t.Errorf("expected text fallback for markdown, got %q", out)
	}
}

func TestFormatChangeValues(t *testing.T) {
	tests := []struct {
		name   string
		change diff.Change
		want   string
	}{
		{
			name:   "modified with values",
			change: diff.Change{Type: diff.Modified, OldValue: "old", NewValue: "new"},
			want:   " [old -> new]",
		},
		{
			name:   "modified nil values",
			change: diff.Change{Type: diff.Modified, OldValue: nil, NewValue: nil},
			want:   "",
		},
		{
			name:   "added with value",
			change: diff.Change{Type: diff.Added, NewValue: "val"},
			want:   " [+ val]",
		},
		{
			name:   "added nil value",
			change: diff.Change{Type: diff.Added},
			want:   "",
		},
		{
			name:   "removed with value",
			change: diff.Change{Type: diff.Removed, OldValue: "val"},
			want:   " [- val]",
		},
		{
			name:   "removed nil value",
			change: diff.Change{Type: diff.Removed},
			want:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatChangeValues(tt.change)
			if got != tt.want {
				t.Errorf("formatChangeValues() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatMDValue(t *testing.T) {
	if got := formatMDValue(nil); got != "" {
		t.Errorf("expected empty for nil, got %q", got)
	}
	if got := formatMDValue("hello"); got != "`hello`" {
		t.Errorf("expected backtick-wrapped, got %q", got)
	}
	if got := formatMDValue(42); got != "`42`" {
		t.Errorf("expected backtick-wrapped int, got %q", got)
	}
}

func TestPrintDiffResult_TextWithValues(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.DiffResult{
		OldPath:        "a.yaml",
		NewPath:        "b.yaml",
		Classification: "BREAKING",
		Changes: []diff.Change{
			{Path: "service.name", Type: diff.Modified, Classification: diff.Breaking, Reason: "name changed", OldValue: "svc-a", NewValue: "svc-b"},
		},
	}
	if err := printDiffResult(cmd, result, "text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "[svc-a -> svc-b]") {
		t.Errorf("expected old/new values in text output, got %q", out)
	}
}

func TestPrintDiffResult_Markdown_WithValues(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.DiffResult{
		OldPath:        "a.yaml",
		NewPath:        "b.yaml",
		Classification: "BREAKING",
		Changes: []diff.Change{
			{Path: "service.name", Type: diff.Modified, Classification: diff.Breaking, Reason: "name changed", OldValue: "svc-a", NewValue: "svc-b"},
		},
	}
	if err := printDiffResult(cmd, result, "markdown"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "| Old | New |") {
		t.Errorf("expected Old/New columns in table header, got %q", out)
	}
	if !strings.Contains(out, "`svc-a`") || !strings.Contains(out, "`svc-b`") {
		t.Errorf("expected old/new values in markdown output, got %q", out)
	}
}

func TestPrintDiffResult_TextDependencyWithValues(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.DiffResult{
		OldPath:        "a.yaml",
		NewPath:        "b.yaml",
		Classification: "BREAKING",
		DependencyDiffs: []app.DependencyDiff{
			{
				Name:           "dep",
				Classification: "BREAKING",
				Changes: []diff.Change{
					{Path: "openapi.paths[/users]", Type: diff.Removed, Classification: diff.Breaking, Reason: "path removed", OldValue: "/users"},
				},
			},
		},
	}
	if err := printDiffResult(cmd, result, "text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "[- /users]") {
		t.Errorf("expected removed value in text output, got %q", out)
	}
}

func TestPrintDiffResult_WriteError(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	cmd.SetOut(errWriter{})
	cmd.SetErr(errWriter{})
	result := &app.DiffResult{Classification: "NON_BREAKING"}
	err := printDiffResult(cmd, result, "json")
	if err == nil {
		t.Error("expected error when output writer fails")
	}
}

func TestPrintValidateResult_WriteError(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	cmd.SetOut(errWriter{})
	cmd.SetErr(errWriter{})
	result := &app.ValidateResult{Path: "pacto.yaml", Valid: true}
	err := printValidateResult(cmd, result, "json")
	if err == nil {
		t.Error("expected error when output writer fails")
	}
}
