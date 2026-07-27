package release

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Release-safety item 1: on a workflow_dispatch RECOVERY run, main may have advanced
// past the transaction's source commit. Every publisher/tagger job must therefore
// build the EXACT transaction source SHA (needs.detect.outputs.source_sha), NOT the
// dispatch-time branch HEAD (github.sha / GITHUB_SHA). This gate parses release.yml
// and fails if any release job checks out the wrong commit or stamps artifact
// metadata (tags, image labels, CLI build, GitHub Release target) from github.sha.
//
// Simulated scenario the gate encodes: transaction created at commit A, main
// advances to B, workflow_dispatch runs from B with source_sha=A — with this gate
// green, every job's checkout ref + every commit-bearing metadata value is A.

// jobsOf splits a workflow into top-level job blocks: name -> block text. A job
// header is two-space-indented `  <name>:`; its block runs until the next header.
func jobsOf(t *testing.T, text string) map[string]string {
	t.Helper()
	lines := strings.Split(text, "\n")
	header := regexp.MustCompile(`^  ([a-zA-Z0-9_-]+):\s*$`)
	out := map[string]string{}
	var cur string
	var buf []string
	flush := func() {
		if cur != "" {
			out[cur] = strings.Join(buf, "\n")
		}
	}
	inJobs := false
	for _, l := range lines {
		if l == "jobs:" {
			inJobs = true
			continue
		}
		if !inJobs {
			continue
		}
		if m := header.FindStringSubmatch(l); m != nil {
			flush()
			cur = m[1]
			buf = []string{l}
			continue
		}
		if cur != "" {
			buf = append(buf, l)
		}
	}
	flush()
	return out
}

func TestReleaseJobsBuildTransactionSourceSha(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("read release.yml: %v", err)
	}
	text := string(b)
	jobs := jobsOf(t, text)
	if len(jobs) < 10 {
		t.Fatalf("parsed only %d jobs from release.yml — parser or file changed", len(jobs))
	}

	const pin = "ref: ${{ needs.detect.outputs.source_sha }}"

	// detect pins via the dispatch INPUT (it validates HEAD==source_sha itself);
	// changesets is push-only, opens the Version PR and publishes nothing.
	exemptCheckout := map[string]bool{"detect": true, "changesets": true}
	// github.sha is legitimate ONLY in detect (it derives source_sha from it on push)
	// and in non-commit contexts (server_url/repository). Everywhere else it is the
	// dispatch commit and must not stamp released-artifact metadata.
	exemptSha := map[string]bool{"detect": true}

	for name, block := range jobs {
		checksOut := strings.Contains(block, "actions/checkout@")
		if checksOut && !exemptCheckout[name] {
			if !strings.Contains(block, pin) {
				t.Errorf("job %q checks out but is not pinned to the transaction source SHA (missing %q) — a recovery run would build the dispatch commit", name, pin)
			}
		}
		if exemptSha[name] {
			continue
		}
		for _, tok := range []string{"github.sha", "GITHUB_SHA"} {
			if strings.Contains(block, tok) {
				t.Errorf("job %q references %q — released-artifact metadata must use needs.detect.outputs.source_sha, not the dispatch commit", name, tok)
			}
		}
	}

	// The single source of truth every job consumes must exist.
	if !strings.Contains(jobs["detect"], "source_sha") {
		t.Error("detect job does not expose a source_sha output")
	}
}
