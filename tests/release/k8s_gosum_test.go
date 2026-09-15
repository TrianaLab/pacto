package release

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
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

// Presence is not correctness. The gate above proves go.sum carries an h1 for the
// pinned core version; it cannot tell a right hash from a wrong one, and a wrong one
// is the worse failure of the two. A missing entry stops a build with "missing go.sum
// entry"; a wrong entry stops it with a checksum MISMATCH, which reads as a supply
// chain attack:
//
//	verifying github.com/trianalab/pacto/v3@v3.3.2: checksum mismatch
//	SECURITY ERROR
//	This download does NOT match an earlier download recorded in go.sum.
//
// v3.3.2 shipped exactly that. apply-release-plan.mjs stages the not-yet-published
// hash by tagging a local commit, and it tagged HEAD -- the commit the Version PR
// branches FROM, not the one it creates. The release tags the latter, whose tree also
// holds the CHANGELOGs, the bumped unit package.json files, the consumed changeset,
// go.work and the release manifests. Nineteen files inside the root module differed,
// so the staged hash described a tree that was never published, and the gate above
// passed on it because the h1 was present.
//
// This one reads the value. The checksum database is the authority every `go build`
// in the world consults, so agreeing with it is the whole property.
//
// Skips rather than fails when the lookup does not come back 200: before the tag
// exists -- which is every Version PR, the run where the entry is first written --
// sum.golang.org answers 404, and there is nothing to compare against yet. Offline is
// the same shape. So this cannot catch the bad hash at the moment it is minted; it
// catches it on the first CI run after the release, which is the window in which
// someone can still act on it.
func TestOperatorGoSumMatchesThePublishedCoreModule(t *testing.T) {
	root := repoRoot(t)
	goMod := readFile(t, root, "integrations", "kubernetes", "go.mod")
	goSum := readFile(t, root, "integrations", "kubernetes", "go.sum")

	m := regexp.MustCompile(`(?m)^\s*(github\.com/trianalab/pacto/v\d+)\s+(v\S+)`).FindStringSubmatch(goMod)
	if m == nil {
		t.Fatal("integrations/kubernetes/go.mod no longer requires github.com/trianalab/pacto/vN — this gate has nothing to check, and is vacuous")
	}
	module, version := m[1], m[2]

	committed := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(module+" "+version) + ` (h1:\S+)$`).FindStringSubmatch(goSum)
	if committed == nil {
		t.Skipf("go.sum has no %s %s h1: line — TestOperatorGoSumCoversThePinnedCoreVersion owns that failure", module, version)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	url := fmt.Sprintf("https://sum.golang.org/lookup/%s@%s", module, version)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("building the lookup request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Skipf("sum.golang.org unreachable (%v) — this gate needs the network and has nothing offline to fall back on", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Skipf("sum.golang.org answered %s for %s %s — not published yet (a Version PR) or withdrawn; nothing to compare", resp.Status, module, version)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		t.Skipf("reading the lookup response: %v", err)
	}

	// The lookup body is a signed record: a sequence number, then one line per hash,
	// then the tree head. Only the module-zip line matters here.
	want := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(module+" "+version) + ` (h1:\S+)$`).FindStringSubmatch(string(body))
	if want == nil {
		t.Skipf("no %s %s h1: line in the sum.golang.org record — its format changed; this gate needs updating, not the go.sum", module, version)
	}
	if committed[1] != want[1] {
		t.Errorf("integrations/kubernetes/go.sum records the wrong checksum for %s %s.\n"+
			"  committed:  %s\n"+
			"  published:  %s\n"+
			"Every `go build` in integrations/kubernetes fails with a checksum mismatch until this is corrected.\n"+
			"Fix: replace the committed h1 with the published one. It is the published module that is authoritative — the tag is immutable and notarized.\n"+
			"Then find out why apply-release-plan.mjs staged a tree that was not the released one; see its section 8.",
			module, version, committed[1], want[1])
	}
}
