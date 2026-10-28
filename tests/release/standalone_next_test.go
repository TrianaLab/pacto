package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// verify-standalone.sh computes the core version the next release publishes
// from the pending changesets. Changesets bumps a `fixed` group as one, so a
// changeset that names only @pacto/cli still moves @pacto/core; the script used
// to look for "@pacto/core" alone and so proved the wrong version (or skipped
// the check entirely) for such a release.
func TestVerifyStandaloneNextFollowsTheFixedGroup(t *testing.T) {
	root := repoRoot(t)
	cases := map[string]struct {
		changesets []string
		want       string
	}{
		"sibling in the fixed group":     {[]string{"---\n\"@pacto/cli\": minor\n---\nx\n"}, "1.3.0"},
		"core itself, single quoted":     {[]string{"---\n'@pacto/core': patch\n---\nx\n"}, "1.2.4"},
		"strongest wins across files":    {[]string{"---\n\"@pacto/core\": patch\n---\n", "---\n\"@pacto/dashboard-image\": major\n---\n"}, "2.0.0"},
		"other group does not move core": {[]string{"---\n\"@pacto/k8s-module\": major\n---\n"}, "1.2.3"},
		"body text is not frontmatter":   {[]string{"---\n\"@pacto/k8s-module\": minor\n---\n\"@pacto/core\": major\n"}, "1.2.3"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			write := func(rel, body string) {
				t.Helper()
				p := filepath.Join(dir, rel)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			write("release/scripts/verify-standalone.sh", readFile(t, root, "release", "scripts", "verify-standalone.sh"))
			write("release/scripts/staging-tags.sh", readFile(t, root, "release", "scripts", "staging-tags.sh"))
			write(".changeset/config.json", readFile(t, root, ".changeset", "config.json"))
			write("release/units/pacto-core/package.json", `{"version":"1.2.3"}`)
			for i, c := range tc.changesets {
				write(filepath.Join(".changeset", "c"+string(rune('a'+i))+".md"), c)
			}
			out, err := exec.Command("bash", filepath.Join(dir, "release/scripts/verify-standalone.sh"), "--next").CombinedOutput()
			if err != nil {
				t.Fatalf("verify-standalone.sh --next: %v\n%s", err, out)
			}
			if got := strings.TrimSpace(string(out)); got != tc.want {
				t.Fatalf("NEXT = %q, want %q", got, tc.want)
			}
		})
	}
}
