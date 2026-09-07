package release

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// docs_check.py builds ./cmd/pacto and validates every fenced Pacto contract example
// in the docs. If the docs-check workflow's path filter omits the validation code, a
// change to the validator/schema/CLI can invalidate a doc example without triggering
// the gate. Assert the filter includes the validation inputs.
func TestDocsCheckPathsCoverValidationCode(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "docs-check.yml"))
	if err != nil {
		t.Fatalf("read docs-check.yml: %v", err)
	}
	s := string(b)
	for _, p := range []string{"cmd/pacto/**", "pkg/validation/**", "pkg/contract/**", "schema/**"} {
		if !strings.Contains(s, p) {
			t.Errorf("docs-check.yml path filter is missing %q; a change there could break a fenced-contract example without running docs-check", p)
		}
	}
}

// TestDocsCheckPathsCoverDemoTranscripts: the demo tour transcripts are generated
// from real output and compared by docs-check. If the filter omits the subsystems
// that produce that output, a change can drift the committed transcripts without
// triggering the drift gate.
func TestDocsCheckPathsCoverDemoTranscripts(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "docs-check.yml"))
	if err != nil {
		t.Fatalf("read docs-check.yml: %v", err)
	}
	s := string(b)
	for _, p := range []string{
		"examples/demo/**",
		"release/scripts/gen_demo_transcripts.sh",
		"pkg/fleet/**",
		"internal/fleetsrc/**",
		"pkg/readiness/**",
		"pkg/diff/**",
		"pkg/impact/**",
		"internal/mcp/**",
		"pkg/sbom/**",
		"pkg/graph/**",
	} {
		if !strings.Contains(s, p) {
			t.Errorf("docs-check.yml path filter is missing %q; a change there could drift the demo transcripts without running docs-check", p)
		}
	}
}

// TestTheDemoTourTeachesTheTwelveBeatJourney: the page a user actually follows.
// It has to name all twelve beats the acceptance script asserts, link to
// compose-demo.md rather than restating the run command, and must not claim a
// beat the acceptance script does not execute.
func TestTheDemoTourTeachesTheTwelveBeatJourney(t *testing.T) {
	root := repoRoot(t)
	tour, err := os.ReadFile(filepath.Join(root, "docs", "examples", "demo-tour.md"))
	if err != nil {
		t.Fatalf("read demo-tour.md: %v", err)
	}
	doc := string(tour)

	// All twelve beats must be named in the document
	beatPattern := regexp.MustCompile(`(?i)## Beat (\d+)`)
	beatsFound := make(map[int]bool)
	for _, match := range beatPattern.FindAllStringSubmatch(doc, -1) {
		if n, err := strconv.Atoi(match[1]); err == nil {
			beatsFound[n] = true
		}
	}
	for i := 1; i <= 12; i++ {
		if !beatsFound[i] {
			t.Errorf("demo-tour.md never mentions Beat %d; the acceptance script asserts it", i)
		}
	}

	// Must link to compose-demo.md rather than restating the run command
	if !strings.Contains(doc, "compose-demo.md") {
		t.Errorf("demo-tour.md never links compose-demo.md; it should point there for the real-stack journey")
	}

	// Must not claim a beat the acceptance script does not assert. The script
	// drives beats 1 through 12, plus beat 3-readiness and beat 11-writes. If the
	// tour starts mentioning Beat 13 or higher without the acceptance script
	// executing it, this gate catches it.
	excessiveBeat := regexp.MustCompile(`(?i)## Beat (1[3-9]|[2-9][0-9])`)
	if matches := excessiveBeat.FindAllString(doc, -1); len(matches) > 0 {
		t.Errorf("demo-tour.md claims beats the acceptance script does not execute: %v", matches)
	}
}
