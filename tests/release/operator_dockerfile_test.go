package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The operator image release build (RELEASE_BUILD=1) resolves the freshly-published,
// pinned core module, which proxy.golang.org has not indexed yet — so the core must
// come straight from VCS. GOPRIVATE='github.com/trianalab/*' is what buys that: it
// feeds GONOPROXY, so trianalab modules bypass the proxy and everything else keeps
// using it.
//
// This gate used to require GOPROXY=direct instead, which turns the same knob far too
// far: it routes the ENTIRE dependency graph to VCS, making the build depend on every
// upstream repository still existing at its pinned revision. github.com/google/cel-go
// (an indirect dependency) was deleted from GitHub and the 5.3.0 operator image stopped
// building, even though the proxy still served the exact version go.sum pins. So the
// invariant is now stated as the two things that actually have to hold, and
// GOPROXY=direct is banned rather than required.
func TestOperatorDockerfileReleaseBuildResolvesFreshCoreWithoutDisablingTheProxy(t *testing.T) {
	root := repoRoot(t)

	// Every place that builds the operator module standalone against a published core
	// shares the failure mode, so they share the gate.
	for _, rel := range []string{
		filepath.Join("integrations", "kubernetes", "Dockerfile"),
		filepath.Join("release", "scripts", "verify-standalone.sh"),
		filepath.Join("release", "orchestrator", "verify-k8s-standalone.sh"),
	} {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		body := string(b)

		// Only the assignment is banned; prose explaining why may name it.
		for _, line := range strings.Split(body, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				continue
			}
			if strings.Contains(line, "GOPROXY=direct") {
				t.Errorf("%s sets GOPROXY=direct, which sends every dependency to VCS and breaks the build when any upstream repo disappears (github.com/google/cel-go did). Use the proxy chain and let GOPRIVATE route trianalab direct.", rel)
			}
		}
		if !strings.Contains(body, "GOPROXY=https://proxy.golang.org,direct") {
			t.Errorf("%s must set GOPROXY=https://proxy.golang.org,direct so third-party modules resolve from the durable proxy copy", rel)
		}
		if !strings.Contains(body, "GOPRIVATE='github.com/trianalab/*'") {
			t.Errorf("%s must set GOPRIVATE='github.com/trianalab/*' — it feeds GONOPROXY, which is what makes a just-published core tag resolve before the proxy indexes it", rel)
		}
		if !strings.Contains(body, "GONOSUMDB='github.com/trianalab/*'") {
			t.Errorf("%s must set GONOSUMDB for github.com/trianalab/* so the checksum database is not consulted for the in-flight module", rel)
		}
	}
}

// TestNoDockerBuildSyncsTheWorkspace bans `go work sync` inside an image build.
//
// It rewrites the member go.mod files to match the packages it can see, and what
// a Dockerfile can see is a COPY list, never the whole repository. The operator
// image copies pkg/, internal/ and integrations/kubernetes/ — not cmd/, tests/ or
// examples/ — so a sync there drops every require those directories are the sole
// importers of. Run against this tree it removes go-md2man and blackfriday; run
// against the full tree it removes huma. The `go build` that follows runs under
// the default -mod=readonly and cannot put anything back, so the manifests are
// rewritten wrong and the build dies with "no required module provides package".
//
// Nothing needs it: the committed go.mod files are already correct, which is what
// the standalone-verify and drift gates exist to keep true, and `go mod download`
// resolves fine without a sync. It is a rewrite of checked-in state performed
// from a deliberately partial view of the repository — the only question is when
// it bites, not whether.
func TestNoDockerBuildSyncsTheWorkspace(t *testing.T) {
	root := repoRoot(t)

	for _, rel := range []string{
		"Dockerfile",
		filepath.Join("integrations", "kubernetes", "Dockerfile"),
	} {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			// Prose explaining the ban may name it; a command line may not.
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if strings.Contains(line, "go work sync") {
				t.Errorf("%s:%d runs `go work sync` — it rewrites go.mod from the COPY list, which is a partial source tree, and the -mod=readonly build that follows cannot re-add what it dropped. The committed manifests are already correct; delete the sync.", rel, i+1)
			}
		}
	}
}
