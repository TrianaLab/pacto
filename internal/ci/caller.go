package ci

import (
	"errors"
	"io/fs"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// ponytail: GitHub and GitLab only; other CI systems use examples/ci/any-ci.sh.

// The pacto-actions release every caller pacto ci init writes pins: the
// repository, the commit and the tag that names it. Dependabot bumps the
// dogfood job's pin in .github/workflows/pacto.yml, and a test keeps these
// equal to it.
const (
	ActionsRepo = "TrianaLab/pacto-actions"
	ActionsSHA  = "8b64cfe46f01f82a1b990afc5b64a01a73d0f4d0"
	ActionsTag  = "v2.0.0"
)

// Caller is the file pacto ci init writes for platform ("github" or
// "gitlab") and its content: the GitHub workflow that calls the pacto-actions
// reusable workflow, or the GitLab include entry. Both are empty for any other
// platform.
func Caller(platform string) (path, content string) {
	switch platform {
	case "github":
		// The permissions map is the union of the reusable workflow's job maps:
		// GitHub refuses to start a called workflow whose jobs ask for more
		// than the caller grants, skipped jobs included.
		return ".github/workflows/pacto.yml", "on: [pull_request, push]\n" +
			"permissions: {contents: read, packages: write, id-token: write, pull-requests: write}\n" +
			"jobs:\n" +
			"  pacto:\n" +
			"    uses: " + delivery.PactoActionsWorkflow + "@" + ActionsSHA + " # " + ActionsTag + "\n"
	case "gitlab":
		return ".gitlab-ci.yml", "include:\n  - remote: " + GitLabIncludeURL() + "\n"
	}
	return "", ""
}

// GitLabIncludeURL is the pacto-actions GitLab component at the pinned
// commit: a commit URL never moves.
func GitLabIncludeURL() string {
	return "https://raw.githubusercontent.com/" + ActionsRepo + "/" + ActionsSHA + "/templates/pacto.yml"
}

// DetectPlatform is the CI platform a repository uses: "github" when it has a
// .github folder, "gitlab" when it has .gitlab-ci.yml. Both or neither is an
// error naming --platform.
func DetectPlatform(fsys fs.FS) (string, error) {
	gh, err := fs.Stat(fsys, ".github")
	github := err == nil && gh.IsDir()
	_, err = fs.Stat(fsys, ".gitlab-ci.yml")
	gitlab := err == nil
	switch {
	case github && gitlab:
		return "", errors.New("found both .github/ and .gitlab-ci.yml: pass --platform github|gitlab")
	case github:
		return "github", nil
	case gitlab:
		return "gitlab", nil
	}
	return "", errors.New("found neither .github/ nor .gitlab-ci.yml: pass --platform github|gitlab")
}

// Lines is how many non-blank lines content has: the size #452's
// pactification budget counts.
func Lines(content string) int {
	n := 0
	for l := range strings.SplitSeq(content, "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}
