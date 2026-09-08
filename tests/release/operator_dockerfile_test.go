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
