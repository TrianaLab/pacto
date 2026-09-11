package release

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// The operator is a nested module that ships STANDALONE: go.work's replace is a
// development convenience and does not travel with the published module, so a
// consumer — or a contributor who just cloned the repo and ran `go build` in
// integrations/kubernetes — resolves the engine core from its committed
// go.mod/go.sum pair alone. Those two files have to agree. When go.mod requires a
// core version whose checksums are absent from go.sum, the default
// `-mod=readonly` build fails outright:
//
//	internal/loader/contract.go:22:2: missing go.sum entry for module providing
//	package github.com/trianalab/pacto/v3/pkg/contract
//
// They disagreed for four days. release/scripts/apply-release-plan.mjs bumps the
// require on every core release and, until this gate existed, moved nothing else:
// on 2026-09-11 go.mod said v3.3.1 while go.sum still said v3.2.7, stale since
// 2026-09-07, and the published integrations/kubernetes/v5.4.0 tag carries the
// same split.
//
// Three checks cover this ground and not one of them could fail on it, because
// they all disable the mechanism that would notice:
//
//	verify-k8s-standalone.sh:65   -mod=mod, and builds a throwaway CONSUMER that
//	                              `go get`s the module — it populates its OWN
//	                              go.sum and never reads the committed one
//	verify-standalone.sh:42       -mod=mod, and rewrites the require to a locally
//	                              staged version, so the committed go.sum could
//	                              not match by construction
//	integrations/kubernetes/      -mod=mod plus GONOSUMDB for trianalab, so the
//	  Dockerfile:73-75            release image build writes the missing entries
//	                              as it goes
//
// `-mod=mod` tells go to write whatever entries are missing rather than complain,
// and GONOSUMDB skips the checksum database. Three green gates over one broken
// invariant is the reason this one is a plain file comparison: no build, no
// network, no module cache, no flags to disagree about. It reads the bytes that
// are committed and asserts they are consistent.
//
// One ceiling. This proves the PINNED core version is covered, which is the entry
// that moves every release and the only one that has ever gone missing. It does
// not prove the rest of go.sum is complete — a transitive dependency added by
// hand could still be absent. The build catches that; nothing but this catches
// the pin.
func TestOperatorGoSumCoversThePinnedCoreVersion(t *testing.T) {
	root := repoRoot(t)
	goMod := readFile(t, root, "integrations", "kubernetes", "go.mod")
	goSum := readFile(t, root, "integrations", "kubernetes", "go.sum")

	// The require line, not the module path alone: the version is the whole point.
	m := regexp.MustCompile(`(?m)^\s*(github\.com/trianalab/pacto/v\d+)\s+(v\S+)`).FindStringSubmatch(goMod)
	if m == nil {
		t.Fatal("integrations/kubernetes/go.mod no longer requires github.com/trianalab/pacto/vN — this gate has nothing to check, and is vacuous")
	}
	module, version := m[1], m[2]

	// Both lines matter. `<mod> <ver> h1:` is the module zip; `<mod> <ver>/go.mod h1:`
	// is its go.mod, which go verifies during graph loading before it ever unpacks
	// the zip — a go.sum carrying only the first still fails the build.
	for _, want := range []string{
		fmt.Sprintf("%s %s h1:", module, version),
		fmt.Sprintf("%s %s/go.mod h1:", module, version),
	} {
		if strings.Contains(goSum, want) {
			continue
		}
		have := "nothing at all"
		if got := regexp.MustCompile(regexp.QuoteMeta(module)+` (v\S+)`).FindAllStringSubmatch(goSum, -1); got != nil {
			seen := map[string]bool{}
			var vs []string
			for _, g := range got {
				if v := strings.TrimSuffix(g[1], "/go.mod"); !seen[v] {
					seen[v], vs = true, append(vs, v)
				}
			}
			have = strings.Join(vs, ", ")
		}
		t.Errorf("integrations/kubernetes/go.mod requires %s %s, but go.sum has no %q line (it covers: %s).\n"+
			"A standalone `go build ./...` in integrations/kubernetes fails with \"missing go.sum entry\".\n"+
			"Fix: (cd integrations/kubernetes && GOWORK=off GOFLAGS= go mod download %s) — never `go mod tidy`, which cannot see go.work's replace.\n"+
			"If this fired on a release Version PR, apply-release-plan.mjs failed to stage the checksums for the version it just pinned.",
			module, version, want, have, module)
	}
}
