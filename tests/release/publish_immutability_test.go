package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The demo-bundles byte-exact immutability gate in publish-demo-bundles.sh only runs
// when crane is on PATH. If crane is missing it must NOT silently publish over an
// existing immutable coordinate: fail closed in production mode, and the release job
// must actually install crane so the gate runs.
func TestDemoBundlesImmutabilityGateFailsClosedWithoutCrane(t *testing.T) {
	root := repoRoot(t)

	script, err := os.ReadFile(filepath.Join(root, "release", "scripts", "publish-demo-bundles.sh"))
	if err != nil {
		t.Fatalf("read publish-demo-bundles.sh: %v", err)
	}
	s := string(script)
	// A crane-missing branch must refuse the production publish rather than skip the gate.
	if !strings.Contains(s, `elif [ "$PROD" = "1" ]; then`) {
		t.Error("publish-demo-bundles.sh does not fail closed when crane is missing in production mode")
	}

	wf, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("read release.yml: %v", err)
	}
	demo, ok := jobsOf(t, string(wf))["demo-bundles"]
	if !ok {
		t.Fatal("release.yml has no demo-bundles job")
	}
	// Ask toolInstaller rather than matching a literal, so the day crane's
	// installer changes again this assertion moves with it instead of quietly
	// looking for a string no workflow contains any more.
	if !strings.Contains(demo, toolInstaller["crane"]) {
		t.Errorf("demo-bundles job does not install crane (no %q), so the byte-exact immutability gate never runs", toolInstaller["crane"])
	}
}

// A resumed finalize must overwrite assets a prior interrupted run already uploaded
// (--clobber) instead of failing on the ones that already exist.
func TestFinalizeReleaseResumeUsesClobber(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "release", "orchestrator", "finalize-release.sh"))
	if err != nil {
		t.Fatalf("read finalize-release.sh: %v", err)
	}
	if !strings.Contains(string(b), "gh release upload") || !strings.Contains(string(b), "--clobber") {
		t.Error("finalize-release.sh resume upload does not use --clobber; a partial prior run makes the resume fail")
	}
}

// verify-oci.sh prints `absent` when its registry read comes back empty, and
// `absent` is the one answer that unlocks a push. So the question of which
// failures are allowed to produce an empty read is the whole immutability
// guarantee, compressed into one regex.
//
// It used to be no question at all: the read was `remote="$(digest "$REF" ||
// true)"` and digest() swallowed every stderr, so an expired token, a rate
// limit, a wedged registry or a 60-second timeout all reported the ref as
// missing — and the caller published over it. The regex that replaced that is
// prose-matching against tool output, which rots quietly, so it is checked here
// against real messages rather than trusted.
//
// Every string below was captured from an actual client against ghcr.io.
var registryReadCases = []struct {
	name   string
	msg    string
	absent bool // may this be read as "the ref does not exist"?
}{
	{"crane, missing tag", `Error: GET https://ghcr.io/v2/trianalab/pacto/manifests/0.0.0-nope: MANIFEST_UNKNOWN: manifest unknown`, true},
	{"crane, repo that never existed", `Error: GET https://ghcr.io/v2/trianalab/nope-xyz/manifests/0.0.1: MANIFEST_UNKNOWN: manifest unknown`, true},
	{"oras, missing tag", `Error response from registry: failed to find "ghcr.io/trianalab/pacto:0.0.0-nope": ghcr.io/trianalab/pacto:0.0.0-nope: not found`, true},
	{"docker manifest inspect", `manifest unknown`, true},
	{"a registry that answers 404 with no OCI error body", `Error: GET https://reg.example/v2/x/manifests/1: unexpected status code 404 Not Found`, true},

	// The half of the table that matters. Each of these is a failure to ASK,
	// and reporting any of them as an answer publishes over a live tag.
	{"oras, credential helper unreachable", `Error: failed to find "ghcr.io/trianalab/pacto:1": HEAD "https://ghcr.io/v2/trianalab/pacto/manifests/1": exec: "docker-credential-desktop": executable file not found in $PATH`, false},
	{"the registry denies us", `Error response from registry: failed to find "ghcr.io/trianalab/pacto:1": HEAD "https://ghcr.io/v2/trianalab/pacto/manifests/1": denied: requested access to the resource is denied`, false},
	{"connection refused", `Error: Get "https://localhost:5000/v2/": dial tcp [::1]:5000: connect: connection refused`, false},
	{"dns failure", `Error: Get "https://reg.invalid/v2/": dial tcp: lookup reg.invalid: no such host`, false},
	{"a tool is missing", `jq: command not found`, false},
	{"the registry is broken", `Error: GET https://reg.example/v2/x/manifests/1: unexpected status code 500 Internal Server Error`, false},
	{"the 60s timeout fired", ``, false},
}

func TestVerifyOCIReadsOnlyARegistry404AsAbsent(t *testing.T) {
	root := repoRoot(t)
	src := readFile(t, root, "release", "orchestrator", "verify-oci.sh")

	// The caller must not swallow the exit. `exit` inside a command substitution
	// only kills the subshell, so `set -e` on a bare assignment is the only thing
	// carrying a fatal read out of digest().
	if !strings.Contains(src, `remote="$(digest "$REF")"`) {
		t.Error("verify-oci.sh no longer reads the remote digest as a bare `remote=\"$(digest \"$REF\")\"` assignment — a `|| true` there discards the fail-closed exit, because `exit` inside $(...) only ends the subshell")
	}

	m := regexp.MustCompile(`(?m)^NOT_FOUND_RE='([^']*)'$`).FindStringSubmatch(src)
	if m == nil {
		t.Fatal("verify-oci.sh no longer defines NOT_FOUND_RE — the table below has nothing to check, and this gate is vacuous")
	}

	for _, tc := range registryReadCases {
		t.Run(tc.name, func(t *testing.T) {
			// Run the real regex through the real engine: this is a shell script
			// matching with `grep -qiE`, and Go's RE2 is a different engine.
			err := exec.Command("bash", "-c", `printf '%s' "$1" | grep -qiE "$2"`, "_", tc.msg, m[1]).Run()
			if matched := err == nil; matched != tc.absent {
				verdict := map[bool]string{true: "absent", false: "a hard failure"}
				t.Errorf("verify-oci.sh reads\n  %s\nas %s, but it is %s.\nNOT_FOUND_RE = %s", tc.msg, verdict[matched], verdict[tc.absent], m[1])
			}
		})
	}
}
