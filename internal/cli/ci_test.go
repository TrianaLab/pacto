package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ciEnvOff empties every variable the CI context reads, so a test running on
// a CI runner never plans with the runner's own context. Every pacto ci test
// calls it first; a sink test then sets only what it needs.
func ciEnvOff(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"GITHUB_ACTIONS", "GITHUB_REPOSITORY_OWNER", "GITHUB_SERVER_URL", "GITHUB_BASE_REF", "GITHUB_STEP_SUMMARY",
		"ACTIONS_ID_TOKEN_REQUEST_URL", "GITLAB_CI", "CI_REGISTRY_IMAGE", "CI_MERGE_REQUEST_DIFF_BASE_SHA", "CI_PROJECT_DIR",
	} {
		t.Setenv(k, "")
	}
}

// ciRepo writes a repository with two services and a folder the root
// .pactoignore hides.
func ciRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for rel, data := range map[string]string{
		"services/payments/pacto.yaml": "service:\n  name: payments\n",
		"services/ledger/pacto.yaml":   "service:\n  name: ledger\n",
		"services/legacy/pacto.yaml":   "service:\n  name: legacy\n",
		".pactoignore":                 "services/legacy/\n",
	} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestCISkeletonListsServices(t *testing.T) {
	ciEnvOff(t)
	statsOff(t)
	stdout, _, err := runRoot(t, defaultDeps(), nil, "ci", ciRepo(t), "--registry", "127.0.0.1:1/acme")
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != 2 {
		t.Fatalf("err = %v, want exit 2", err)
	}
	for _, want := range []string{"Registry: 127.0.0.1:1/acme (from --registry)\n", "\nledger ", "\npayments ", "skipped: services/legacy (from .pactoignore:1)\n"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("missing %q in:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "\nlegacy ") {
		t.Errorf("the hidden folder is planned:\n%s", stdout)
	}
}

func TestCISkeletonRootMissing(t *testing.T) {
	ciEnvOff(t)
	statsOff(t)
	stdout, _, err := runRoot(t, defaultDeps(), nil, "ci", filepath.Join(t.TempDir(), "nope"))
	var exit *ExitError
	if err == nil || errors.As(err, &exit) || stdout != "" {
		t.Fatalf("err = %v, stdout %q", err, stdout)
	}
}

func TestCISkeletonDefaultRootClear(t *testing.T) {
	ciEnvOff(t)
	statsOff(t)
	t.Chdir(t.TempDir())
	_, _, err := runRoot(t, defaultDeps(), nil, "ci")
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != 2 {
		t.Fatalf("an empty repository: err = %v, want exit 2", err)
	}
}

func TestExitStatus(t *testing.T) {
	var exit *ExitError
	if exitStatus(0, "") != nil || !errors.As(exitStatus(1, "blocked"), &exit) || exit.Code != 1 || exit.Reason != "blocked" {
		t.Fatal("exitStatus maps 0 to nil and any other code to an ExitError")
	}
}
