package ci

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

func TestCallerGitHubWithinBudget(t *testing.T) {
	path, content := Caller("github")
	if path != ".github/workflows/pacto.yml" || Lines(content) != 5 {
		t.Fatalf("path %q, %d lines:\n%s", path, Lines(content), content)
	}
	if !strings.Contains(content, "permissions: {contents: read, packages: write, id-token: write, pull-requests: write}\n") {
		t.Fatalf("the caller grants another map:\n%s", content)
	}
}

func TestCallerGitLabWithinBudget(t *testing.T) {
	path, content := Caller("gitlab")
	if path != ".gitlab-ci.yml" || Lines(content) != 2 || content != "include:\n  - remote: "+GitLabIncludeURL()+"\n" {
		t.Fatalf("path %q, %d lines:\n%s", path, Lines(content), content)
	}
}

func TestCallerUnknownPlatform(t *testing.T) {
	if path, content := Caller("jenkins"); path != "" || content != "" {
		t.Fatalf("path %q, content %q", path, content)
	}
}

func TestCallerRefIsFullSHA(t *testing.T) {
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(ActionsSHA) {
		t.Fatalf("ActionsSHA %q is not a commit SHA", ActionsSHA)
	}
	_, content := Caller("github")
	if !strings.HasSuffix(content, "/.github/workflows/ci.yml@"+ActionsSHA+" # "+ActionsTag+"\n") {
		t.Fatalf("the uses: line does not pin the SHA with the tag comment:\n%s", content)
	}
	if !strings.Contains(GitLabIncludeURL(), "/"+ActionsSHA+"/templates/pacto.yml") {
		t.Fatalf("the include URL does not pin the SHA: %s", GitLabIncludeURL())
	}
}

func TestLinesCountsNonBlank(t *testing.T) {
	if n := Lines("a\n\n  \nb\n"); n != 2 {
		t.Fatalf("Lines = %d", n)
	}
}

// TestCallerExamplesAreCurrent: the callers the guide includes are what
// pacto ci init writes. PACTO_UPDATE_CI_EXAMPLES=1 rewrites them.
func TestCallerExamplesAreCurrent(t *testing.T) {
	for platform, file := range map[string]string{"github": "github/pacto.yml", "gitlab": "gitlab/.gitlab-ci.yml"} {
		_, content := Caller(platform)
		golden := filepath.Join("..", "..", "examples", "ci", file)
		if os.Getenv("PACTO_UPDATE_CI_EXAMPLES") == "1" {
			if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(golden, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		got, err := os.ReadFile(golden)
		if err != nil || string(got) != content {
			t.Errorf("%s is stale (err %v); run PACTO_UPDATE_CI_EXAMPLES=1 go test ./internal/ci -run TestCallerExamplesAreCurrent", golden, err)
		}
	}
}

func TestDetectPlatform(t *testing.T) {
	for name, tc := range map[string]struct {
		fs   fstest.MapFS
		want string
		err  string
	}{
		"github":  {fs: fstest.MapFS{".github/dependabot.yml": {}}, want: "github"},
		"gitlab":  {fs: fstest.MapFS{".gitlab-ci.yml": {}}, want: "gitlab"},
		"both":    {fs: fstest.MapFS{".github/dependabot.yml": {}, ".gitlab-ci.yml": {}}, err: "both .github/ and .gitlab-ci.yml"},
		"neither": {fs: fstest.MapFS{"README.md": {}}, err: "neither .github/ nor .gitlab-ci.yml"},
		// A file named .github is no GitHub folder.
		"github file": {fs: fstest.MapFS{".github": {}}, err: "neither"},
	} {
		got, err := DetectPlatform(tc.fs)
		if got != tc.want || (err == nil) != (tc.err == "") || err != nil && (!strings.Contains(err.Error(), tc.err) || !strings.Contains(err.Error(), "--platform github|gitlab")) {
			t.Errorf("%s: got %q, err %v", name, got, err)
		}
	}
}
