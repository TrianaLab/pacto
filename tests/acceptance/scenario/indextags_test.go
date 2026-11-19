package scenario

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Every `pacto push` of a version writes one entry to the demo's organization
// index, so the index holds one tag per distinct service and version the seed
// pushes. compose-demo.sh asserts that count against the live registry, and this
// test keeps its constant equal to the plan Compose projects: the bash harness
// never parses the plan.
func TestDemoIndexTagsMatchPlan(t *testing.T) {
	plan, err := OperationalGraph.Plan(ComposeArtifactMount)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	pairs := map[string]bool{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(plan)), "\n") {
		if f := strings.Split(line, "\t"); f[0] == RecordPush {
			pairs[f[1]] = true // DigestKey: service@version
		}
	}
	if len(pairs) == 0 {
		t.Fatal("the plan pushes nothing, so this proves nothing")
	}
	script, err := os.ReadFile(filepath.Join("..", "local", "compose-demo.sh"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^EXPECTED_INDEX_TAGS=(\d+)$`).FindSubmatch(script)
	if m == nil {
		t.Fatal("compose-demo.sh declares no EXPECTED_INDEX_TAGS")
	}
	if got := string(m[1]); got != strconv.Itoa(len(pairs)) {
		t.Errorf("compose-demo.sh expects %s index tags, the plan pushes %d service versions", got, len(pairs))
	}
}
