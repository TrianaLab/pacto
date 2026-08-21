package architecture

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The knowledge vocabulary is the words Pacto uses to say how much of the world an
// answer actually saw. It exists in two layers, and the public concepts page is the
// only place a reader learns either of them:
//
//   - the WIRE layer, pkg/fleet.Completeness, carried in every answer's
//     `meta.completeness`;
//   - the DERIVED layer, the dashboard's CompletenessLevel, which adds the levels a
//     consumer works out for itself from per-source health and from an envelope that
//     never arrived.
//
// A reader who is told a short closed list writes a branch for a value the wire can
// never send and no branch for the levels it will actually meet. So the documented
// vocabulary must equal the derived vocabulary exactly — the superset — and the page
// must say which of them the wire carries.

var (
	// `complete` / `partial` / `empty` as declared in pkg/fleet/fleet.go.
	goCompletenessRe = regexp.MustCompile(`\bCompleteness[A-Z]\w*\s+Completeness\s*=\s*"([a-z]+)"`)
	// The `'a' | 'b' | ...` union declared in knowledgeState.ts.
	tsLevelUnionRe = regexp.MustCompile(`export type CompletenessLevel\s*=\s*([^;]+);`)
	tsMemberRe     = regexp.MustCompile(`'([a-z]+)'`)
	// The first column of a `| **word** | claim |` table row.
	docWordRe = regexp.MustCompile(`(?m)^\|\s*\*\*([a-z]+)\*\*\s*\|`)
	// A spelled-out count in the sentence introducing the table.
	countWordRe = regexp.MustCompile(`(?i)\b(two|three|four|five|six|seven|eight)\s+(words|levels|values|states)\b`)
)

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func readDoc(t *testing.T, parts ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(docsRoot(t), filepath.Join(parts...)))
	if err != nil {
		t.Fatalf("read %v: %v", parts, err)
	}
	return string(b)
}

// knowledgeSection returns the "## Knowledge" section of the concepts page, so the
// vocabulary table is compared against and not against some other table on the page
// (the bounded-total table below it is a different four-state vocabulary).
func knowledgeSection(t *testing.T, doc string) string {
	t.Helper()
	const heading = "\n## Knowledge\n"
	i := strings.Index(doc, heading)
	if i < 0 {
		t.Fatal("docs/concepts.md has no `## Knowledge` section — the knowledge vocabulary is public API for anyone branching on meta.completeness")
	}
	rest := doc[i+len(heading):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// TestKnowledgeVocabularyIsDocumentedInFull: the words in the concepts page's
// knowledge table are exactly the dashboard's CompletenessLevel union, and the page
// says which of them `meta.completeness` actually carries.
func TestKnowledgeVocabularyIsDocumentedInFull(t *testing.T) {
	root := docsRoot(t)

	wire := map[string]bool{}
	for _, m := range goCompletenessRe.FindAllStringSubmatch(readDoc(t, "pkg", "fleet", "fleet.go"), -1) {
		wire[m[1]] = true
	}
	if len(wire) == 0 {
		t.Fatal("no Completeness constants found in pkg/fleet/fleet.go — this gate lost its anchor")
	}

	ts := readDoc(t, "pkg", "dashboard", "frontend", "src", "lib", "knowledgeState.ts")
	union := tsLevelUnionRe.FindStringSubmatch(ts)
	if union == nil {
		t.Fatal("no `export type CompletenessLevel` union found in knowledgeState.ts — this gate lost its anchor")
	}
	derived := map[string]bool{}
	for _, m := range tsMemberRe.FindAllStringSubmatch(union[1], -1) {
		derived[m[1]] = true
	}

	// The derived layer must contain the wire layer: a value the backend can send
	// that no consumer level covers would render as "unknown" and lose its meaning.
	for w := range wire {
		if !derived[w] {
			t.Errorf("wire completeness %q has no CompletenessLevel — the dashboard would classify a real backend answer as unknown", w)
		}
	}

	section := knowledgeSection(t, readDoc(t, "docs", "concepts.md"))
	documented := map[string]bool{}
	for _, m := range docWordRe.FindAllStringSubmatch(section, -1) {
		documented[m[1]] = true
	}

	if got, want := sortedKeys(documented), sortedKeys(derived); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("docs/concepts.md documents the knowledge vocabulary as %v, the code's is %v\n"+
			"An undocumented level is one a reader meets with no branch for it; a documented level the code cannot produce is a dead branch.\n"+
			"Fix the table in the `## Knowledge` section, or the CompletenessLevel union if the level should not exist.", got, want)
	}

	// Which layer each word belongs to has to be on the page, or the reader cannot
	// tell a value they can switch on from one they must compute.
	if !strings.Contains(section, "meta.completeness") {
		t.Error("the `## Knowledge` section must name `meta.completeness` — otherwise the reader cannot tell which of these words arrives on the wire")
	}
	for w := range wire {
		if !strings.Contains(section, "`"+w+"`") {
			t.Errorf("the `## Knowledge` section must name the wire value `%s` in code font where it says what meta.completeness carries", w)
		}
	}

	// No spelled-out count in the sentence introducing the table: it is a second copy
	// of the vocabulary's size that this gate cannot keep true, and it was wrong
	// before this gate existed. Scoped to the intro, because the bounded-total table
	// further down the same section counts its own (different, genuinely four) states.
	intro := section
	if i := strings.Index(intro, "| Word | Claim |"); i >= 0 {
		intro = intro[:i]
	}
	if m := countWordRe.FindString(intro); m != "" {
		t.Errorf("the knowledge vocabulary is introduced with a hardcoded count (%q). Drop the number; the table is the list.", m)
	}

	_ = root
}
