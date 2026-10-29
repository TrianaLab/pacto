package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestDocsPublishGate runs release/scripts/docs_publish_gate.sh in a throwaway
// repository against fixture versions.json files. The gate is the only thing
// standing between a main push and a gh-pages write, and it only runs after
// merge, so this test is where a broken gate shows up on a PR.
func TestDocsPublishGate(t *testing.T) {
	gate := filepath.Join(repoRoot(t), "release/scripts/docs_publish_gate.sh")
	const holdsLatest = `[{"version":"1.2.0","aliases":["latest"]},{"version":"1.1.0","aliases":[]}]`

	cases := []struct {
		name      string
		versions  string
		after     map[string]string // files committed after the release commit
		atRelease bool              // HEAD is the release commit itself
		want      string
	}{
		{name: "docs fix over the release", versions: holdsLatest,
			after: map[string]string{"docs/page.md": "fix"}, want: "publish=true"},
		{name: "version present without latest", versions: `[{"version":"1.2.0","aliases":[]}]`,
			after: map[string]string{"docs/page.md": "fix"}, want: "publish=false"},
		{name: "version absent", versions: `[{"version":"1.1.0","aliases":["latest"]}]`,
			after: map[string]string{"docs/page.md": "fix"}, want: "publish=false"},
		{name: "pending changeset", versions: holdsLatest,
			after: map[string]string{"docs/page.md": "fix", ".changeset/x.md": "---\n---\n"}, want: "publish=false"},
		{name: "code changed since the release", versions: holdsLatest,
			after: map[string]string{"docs/page.md": "fix", "cmd/gendocs/main.go": "package main"}, want: "publish=false"},
		{name: "HEAD is the release commit", versions: holdsLatest, atRelease: true, want: "publish=false"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			git := func(args ...string) {
				t.Helper()
				cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
				cmd.Dir = dir
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
			}
			write := func(files map[string]string) {
				for p, body := range files {
					full := filepath.Join(dir, p)
					if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			git("init", "-q")
			write(map[string]string{".changeset/README.md": "x", "docs/page.md": "v1"})
			git("add", "-A")
			git("commit", "-qm", "base")
			write(map[string]string{
				"release/release-manifest.json":    `{"units":{"core":{"version":"1.2.0"}}}`,
				"release/release-transaction.json": `{}`,
			})
			git("add", "-A")
			git("commit", "-qm", "release")
			if !tc.atRelease {
				write(tc.after)
				git("add", "-A")
				git("commit", "-qm", "after")
			}

			versions := filepath.Join(t.TempDir(), "versions.json")
			output := filepath.Join(t.TempDir(), "output")
			if err := os.WriteFile(versions, []byte(tc.versions), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", gate, versions)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GITHUB_OUTPUT="+output, "GITHUB_STEP_SUMMARY="+os.DevNull)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("gate failed: %v\n%s", err, out)
			}
			got, _ := os.ReadFile(output)
			if !strings.Contains(string(got), tc.want) {
				t.Errorf("want %s, got:\n%s", tc.want, got)
			}
			if tc.want == "publish=true" && !strings.Contains(string(got), "version=1.2.0") {
				t.Errorf("publish must pin the checked version, got:\n%s", got)
			}
		})
	}
}
