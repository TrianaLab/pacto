package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// publish-oci-unit.sh and dry-run.sh each kept their own digest(), and the
// publisher's sent crane's stderr to /dev/null: a denied or broken registry read
// after a push died with no message at all. Both now read through
// `verify-oci.sh --digest`, the one strict reader: a real 404 is "", anything
// else fails loudly with the client's own error.
func TestVerifyOCIDigestModeIsTheOneStrictReader(t *testing.T) {
	root := repoRoot(t)
	script := filepath.Join(root, "release", "orchestrator", "verify-oci.sh")
	cases := []struct {
		name, stdout, stderr string
		rc                   int
		want                 string
		wantFail             bool
	}{
		{"present", "sha256:abc", "", 0, "sha256:abc", false},
		{"a registry 404 is empty", "", "Error: GET https://ghcr.io/v2/x/manifests/1: MANIFEST_UNKNOWN: manifest unknown", 1, "", false},
		{"a denied read fails with the error", "", "denied: requested access to the resource is denied", 1, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			fake := "#!/bin/sh\nprintf '%s' '" + tc.stdout + "'\nprintf '%s\\n' '" + tc.stderr + "' >&2\nexit " + string(rune('0'+tc.rc)) + "\n"
			if err := os.WriteFile(filepath.Join(bin, "crane"), []byte(fake), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", script, "--digest", "ghcr.io/trianalab/x:1")
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
			var out, errb strings.Builder
			cmd.Stdout, cmd.Stderr = &out, &errb
			err := cmd.Run()
			if tc.wantFail {
				if err == nil || !strings.Contains(errb.String(), "denied") {
					t.Fatalf("want a failure carrying the registry's error, got err=%v stderr=%q", err, errb.String())
				}
				return
			}
			if err != nil || out.String() != tc.want {
				t.Fatalf("got %q (err %v, stderr %q), want %q", out.String(), err, errb.String(), tc.want)
			}
		})
	}

	for _, rel := range []string{"publish-oci-unit.sh", "dry-run.sh"} {
		src := readFile(t, root, "release", "orchestrator", rel)
		if strings.Contains(src, "crane digest") || !strings.Contains(src, "verify-oci.sh\" --digest") {
			t.Errorf("%s reads digests with its own crane call instead of `verify-oci.sh --digest`", rel)
		}
	}
}
