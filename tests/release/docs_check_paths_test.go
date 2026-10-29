package release

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// TestTheDemoTourTeachesEveryRecordedCommand: the page a user actually follows.
//
// The tour presents six user stories; the generator, the acceptance script and
// beats.sh work in "beats", where one beat is one recorded command. A story
// groups two to four of them, so the two vocabularies are deliberate and the
// mapping is many-to-one. That means counting headings proves nothing about
// coverage — what has to hold is that the recorded output and the page cover each
// other exactly:
//
//   - every generated transcript is snippet-included, or a command runs in CI and
//     is taught nowhere;
//   - every snippet the page includes exists, or the page renders a broken include;
//   - the two beats no generator can produce — the MCP stdin handshake and the
//     live dashboard — are still on the page, since the acceptance script asserts
//     them and only prose carries them here.
func TestTheDemoTourTeachesEveryRecordedCommand(t *testing.T) {
	root := repoRoot(t)
	// The tour is one page covering all six stories.
	page, err := os.ReadFile(filepath.Join(root, "docs", "examples", "demo-tour.md"))
	if err != nil {
		t.Fatalf("read demo-tour.md: %v", err)
	}
	doc := string(page)

	generated, err := filepath.Glob(filepath.Join(root, "examples", "demo", "generated", "_beat-*.md"))
	if err != nil {
		t.Fatalf("glob generated transcripts: %v", err)
	}
	if len(generated) == 0 {
		t.Fatal("no generated transcripts found; run `make gen-demo-transcripts`")
	}

	included := map[string]bool{}
	for _, m := range regexp.MustCompile(`--8<-- "(examples/demo/generated/[^"]+)"`).FindAllStringSubmatch(doc, -1) {
		included[m[1]] = true
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(m[1]))); err != nil {
			t.Errorf("the tour includes %s, which does not exist: the page renders a broken snippet", m[1])
		}
	}
	for _, g := range generated {
		rel := filepath.ToSlash(strings.TrimPrefix(g, root+string(filepath.Separator)))
		if !included[rel] {
			t.Errorf("%s is generated and asserted in CI but the tour never includes it; a command runs and is taught nowhere", rel)
		}
	}

	// The two beats no generator covers. Their text is the only thing carrying
	// them, so assert the commands themselves rather than a heading.
	for _, cmd := range []string{
		"pacto mcp examples/demo/bundles/payments-service/v2.1.0",
		"pacto dashboard examples/demo/bundles",
	} {
		if !strings.Contains(doc, cmd) {
			t.Errorf("the tour never shows %q; the acceptance script asserts it and no transcript covers it", cmd)
		}
	}

	// Must link to compose-demo.md rather than restating the run command
	if !strings.Contains(doc, "compose-demo.md") {
		t.Errorf("the tour never links compose-demo.md; it should point there for the real-stack journey")
	}
}

// TestDocsCheckComparisonReview: the comparison page grades other projects, so
// it goes stale with the calendar. docs_check.py fails when the review date is
// missing or unparseable and only warns when it is old, so a build nobody
// changed never turns red on a date.
func TestDocsCheckComparisonReview(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command("python3", "-I", "-c", `
import datetime, importlib.util, json, sys
spec = importlib.util.spec_from_file_location("dc", sys.argv[1])
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)
today = datetime.date(2026, 10, 29)
cases = {
    "fresh": "# T\n\nReviewed on 2026-10-01. More.\n",
    "stale": "# T\n\nReviewed on 2026-01-01. More.\n",
    "missing": "# T\n\nNo date here.\n",
    "garbled": "# T\n\nReviewed on 2026-13-40. More.\n",
}
out = {k: m.comparison_review(v, today) for k, v in cases.items()}
out["page"] = m.comparison_review(open(sys.argv[2]).read(), datetime.date.today())
print(json.dumps(out))
`, filepath.Join(root, "release", "scripts", "docs_check.py"), filepath.Join(root, "docs", "comparison.md"))
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docs_check.py: %v\n%s", err, out)
	}
	var got map[string][2]string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("parse %s: %v", out, err)
	}
	for name, want := range map[string][2]bool{
		"fresh":   {false, false},
		"stale":   {false, true},
		"missing": {true, false},
		"garbled": {true, false},
	} {
		if (got[name][0] != "") != want[0] || (got[name][1] != "") != want[1] {
			t.Errorf("%s: (error, warning) = %q, want error=%v warning=%v", name, got[name], want[0], want[1])
		}
	}
	if got["page"][0] != "" {
		t.Errorf("docs/comparison.md: %s", got["page"][0])
	}
}
