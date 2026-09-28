package architecture

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// Collector-round item 9: documentation-consistency gates. These keep the public
// narrative honest about the collector/evidence architecture — the concepts must not
// drift back into "the engine has a collector interface", "a plugin is a collector",
// or "we ship an ECS/cloud collector".

func docsRoot(t *testing.T) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(self), "..", ".."))
}

// TestPublicCollectorInterfaceIsAbsentOrImplemented: the speculative
// pkg/collector.Collector interface must be ABSENT (Evidence is the boundary) — or,
// if it is ever reintroduced, this gate must be updated to prove the first-party
// collector implements it. Today: absent.
func TestPublicCollectorInterfaceIsAbsentOrImplemented(t *testing.T) {
	root := docsRoot(t)
	if _, err := os.Stat(filepath.Join(root, "pkg", "collector")); !os.IsNotExist(err) {
		t.Errorf("pkg/collector exists again — a public collector interface must be genuinely implemented by the Kubernetes collector (different Collect signature today), not speculative. Remove it or prove conformance and update this gate.")
	}
}

// TestCollectorsDocIsCanonical: docs/model.md exists and carries the canonical
// diagrams + the collector-vs-plugin distinction, so collectors are first-class in
// the public docs.
func TestCollectorsDocIsCanonical(t *testing.T) {
	root := docsRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "docs", "model.md"))
	if err != nil {
		t.Fatalf("docs/model.md missing: %v", err)
	}
	doc := string(b)
	mermaid := strings.Count(doc, "```mermaid")
	if mermaid < 2 {
		t.Errorf("docs/model.md must carry the canonical diagrams (declaration-vs-observation + collector-system); found %d mermaid blocks", mermaid)
	}
	if !strings.Contains(doc, "Evaluate") || !strings.Contains(doc, "EvidenceSet") {
		t.Error("docs/model.md must center the Contract + Evidence -> Evaluate model")
	}
	// collector != plugin must be explicit.
	if !regexp.MustCompile(`(?i)collector.{0,40}plugin|plugin.{0,40}collector`).MatchString(doc) {
		t.Error("docs/model.md must explicitly distinguish a collector from a plugin")
	}
	// The custom-collector Go example must be real (mirrors the compiled ExampleEvaluate
	// in pkg/validation/collector_example_test.go), not ellipsis-based pseudocode.
	if !strings.Contains(doc, "validation.Evaluate(c, ev)") || !strings.Contains(doc, "evidence.EvidenceSet{") {
		t.Error("docs/model.md must show the real Evaluate call over an EvidenceSet (see ExampleEvaluate)")
	}
	for _, pseudo := range []string{"customCollector.Observe", "loadContract(...)"} {
		if strings.Contains(doc, pseudo) {
			t.Errorf("docs/model.md presents pseudocode %q as Go — use the compilable example", pseudo)
		}
	}
}

// TestDocsDoNotConflateOrOverclaimCollectors scans the public docs for two errors:
// calling the Kubernetes collector "the engine", and presenting an unimplemented
// collector (ECS/Nomad/Terraform/cloud) as shipped/supported/implemented.
func TestDocsDoNotConflateOrOverclaimCollectors(t *testing.T) {
	root := docsRoot(t)
	// The list tracks wherever the collector narrative lives, which moves as the
	// documentation is reorganised. A deleted entry must be REPLACED by the page its
	// prose moved to, never just dropped: the read below skips a file it cannot find
	// rather than failing, so a stale list silently covers less prose than it used to.
	// Entries for pages that do not exist yet are deliberate — they start working the
	// moment the page lands.
	files := []string{
		"README.md", "MANIFEST.md", "CONTRIBUTING.md", "ARCHITECTURE.md",
		"docs/index.md", "docs/model.md", "docs/platform-engineers.md",
		"docs/operational-graph.md", "docs/impact.md", "docs/evidence.md",
		"docs/quickstart.md", "docs/developers.md", "docs/fleet-tools.md",
		"docs/mcp-integration.md", "docs/patterns/index.md",
	}
	engineConflation := regexp.MustCompile(`(?i)kubernetes (collector|operator)[^.\n]{0,30}\bis the engine\b`)
	overclaim := regexp.MustCompile(`(?i)\b(ECS|Nomad|Terraform)\b[^.\n]{0,40}\b(collector|shipped|supported|implemented)\b`)
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			continue // not all files must exist
		}
		s := string(b)
		if engineConflation.MatchString(s) {
			t.Errorf("%s calls the Kubernetes collector/operator 'the engine' — the engine is the pure Evaluate function", f)
		}
		if overclaim.MatchString(s) {
			t.Errorf("%s presents an unimplemented collector (ECS/Nomad/Terraform) as shipped/supported — only the Kubernetes collector is implemented", f)
		}
	}
}
