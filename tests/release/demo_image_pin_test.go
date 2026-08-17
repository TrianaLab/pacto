package release

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The demo artifact is published once, immutably, and pulled by digest. That is
// worth nothing if the compose file inside it names its images by tag: the tag
// moves, and one unchanged artifact digest starts executing different bytes,
// with no record anywhere that it did. The projection refuses a tag-only
// reference (scenario.checkPinnedImage), so this file gates the other half — the
// release path has to be ABLE to give it a digest, and has to refuse to publish
// when it cannot.
//
// Static, because the alternative is finding out during a release.

// pactoImageArgRE captures the value of each -pacto-image argument.
var pactoImageArgRE = regexp.MustCompile(`-pacto-image\s+"?([^"\s]+)"?`)

// unpinnedImageArgs returns every -pacto-image argument in text that a tag could
// move. A reference is pinned when it carries an @digest; the projection checks
// the digest's spelling, so this only asks whether one is there at all.
func unpinnedImageArgs(text string) []string {
	var out []string
	for _, m := range pactoImageArgRE.FindAllStringSubmatch(text, -1) {
		if ref := m[1]; !strings.Contains(ref, "@") {
			out = append(out, ref)
		}
	}
	return out
}

// demoComposeJob returns what the release job that publishes the demo artifact
// waits for, and every shell body it runs.
func demoComposeJob(t *testing.T, root string) (needs []string, runs string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("read release.yml: %v", err)
	}
	// `needs:` is a scalar or a sequence depending on the job, so it is decoded
	// loosely and normalised here.
	var doc struct {
		Jobs map[string]struct {
			Needs any `yaml:"needs"`
			Steps []struct {
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse release.yml: %v", err)
	}
	job, ok := doc.Jobs["demo-compose"]
	if !ok {
		t.Fatal("release.yml has no demo-compose job — the demo artifact's publisher was renamed; move this gate with it")
	}
	switch n := job.Needs.(type) {
	case string:
		needs = []string{n}
	case []any:
		for _, v := range n {
			needs = append(needs, v.(string))
		}
	}
	var bodies []string
	for _, s := range job.Steps {
		bodies = append(bodies, s.Run)
	}
	return needs, strings.Join(bodies, "\n")
}

// TestDemoComposePinsTheImageTheTransactionPublished: the released demo runs the
// dashboard image THIS transaction built, addressed by the digest the ledger
// recorded for it — not `dashboard:$VER`, which is a tag and which the release
// itself moves.
func TestDemoComposePinsTheImageTheTransactionPublished(t *testing.T) {
	root := repoRoot(t)
	needs, runs := demoComposeJob(t, root)

	// The ledger is where publish-oci-unit.sh recorded what dashboard-image
	// actually pushed. Reading it there rather than from a job output is what
	// makes a single-unit recovery dispatch resolve the same image, and is why
	// this needs no second store and no second adapter.
	if !strings.Contains(runs, "ledger.sh digest") || !strings.Contains(runs, "dashboard-image") {
		t.Error("demo-compose does not read the dashboard-image digest from the release ledger — a tag or a job output would let one demo artifact run different bytes on different days")
	}
	if unpinned := unpinnedImageArgs(runs); len(unpinned) > 0 {
		t.Errorf("demo-compose projects -pacto-image %q, which a tag could move; it must be repo@sha256:...", unpinned)
	}
	// Ordering: the digest cannot exist before the image is published.
	if !contains(needs, "dashboard-image") {
		t.Errorf("demo-compose needs %v, which does not include dashboard-image — nothing orders the demo after the image it pins", needs)
	}
}

// TestDemoComposeRefusesToPublishWithoutTheDigest: a narrowed recovery — a
// dispatch of demo-compose alone, where dashboard-image was skipped — must not
// publish an artifact pinned to an image nothing in this transaction verified.
// The digest lookup fails closed instead.
func TestDemoComposeRefusesToPublishWithoutTheDigest(t *testing.T) {
	root := repoRoot(t)
	_, runs := demoComposeJob(t, root)

	// The guard has to live in the same shell that reads the digest: a later
	// check would run after `oras push` in the recovery orderings that matter.
	var guarded bool
	for _, step := range strings.Split(runs, "ledger.sh digest")[1:] {
		body := step
		if i := strings.Index(body, "\n\n"); i >= 0 { // conservative: same step body
			body = body[:i]
		}
		if strings.Contains(body, "exit 1") && strings.Contains(body, "sha256:") {
			guarded = true
		}
	}
	if !guarded {
		t.Error("the step that reads the dashboard-image digest does not `exit 1` when the digest is missing or malformed — demo-compose would publish an artifact pinning an unverified image")
	}
}

// TestDryRunPinsTheDemoImageToo: the dry run is the behavioural proof of the
// production path, so it has to exercise the same shape. A dry run that projected
// a tag would pass while production could not, which is the failure mode a dry
// run exists to prevent.
func TestDryRunPinsTheDemoImageToo(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "release", "orchestrator", "dry-run.sh"))
	if err != nil {
		t.Fatalf("read dry-run.sh: %v", err)
	}
	if unpinned := unpinnedImageArgs(string(b)); len(unpinned) > 0 {
		t.Errorf("dry-run.sh projects -pacto-image %q by tag, so it does not exercise what production does", unpinned)
	}
	if !strings.Contains(string(b), "-pacto-image") {
		t.Error("dry-run.sh no longer projects a demo artifact — the production path lost its behavioural proof")
	}
}

// TestUnpinnedImageArgsHasTeeth: the gate above is a text scan, so prove the scan
// can tell the two references apart.
func TestUnpinnedImageArgsHasTeeth(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want []string
	}{
		{"a tag", `-pacto-image "ghcr.io/trianalab/pacto/dashboard:$VER" \`, []string{"ghcr.io/trianalab/pacto/dashboard:$VER"}},
		{"a bare name", `-pacto-image pacto-demo:acceptance`, []string{"pacto-demo:acceptance"}},
		{"a shell variable, which hides whatever it holds", `-pacto-image "$IMAGE"`, []string{"$IMAGE"}},
		{"a digest", `-pacto-image "ghcr.io/x/y@sha256:` + strings.Repeat("a", 64) + `"`, nil},
		{"a digest from an expansion", `-pacto-image "ghcr.io/x/y@${{ steps.image.outputs.digest }}"`, nil},
		{"nothing at all", `go run ./x demo -dir "$D"`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := unpinnedImageArgs(tc.text)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("unpinnedImageArgs(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}
