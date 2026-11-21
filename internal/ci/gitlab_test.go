package ci

import (
	"strings"
	"testing"
)

const testURL = "https://raw.githubusercontent.com/TrianaLab/pacto-actions/new/templates/pacto.yml"

func addInclude(t *testing.T, src string) string {
	t.Helper()
	out, err := AddGitLabInclude([]byte(src), testURL)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestAddGitLabIncludeAppends(t *testing.T) {
	for src, want := range map[string]string{
		"":                        "include:\n  - remote: " + testURL + "\n",
		"stages: [test]\n":        "stages: [test]\n\ninclude:\n  - remote: " + testURL + "\n",
		"stages: [test]":          "stages: [test]\n\ninclude:\n  - remote: " + testURL + "\n",
		"# only a comment\n":      "# only a comment\n\ninclude:\n  - remote: " + testURL + "\n",
		"build:\n  script: [x]\n": "build:\n  script: [x]\n\ninclude:\n  - remote: " + testURL + "\n",
	} {
		if got := addInclude(t, src); got != want {
			t.Errorf("%q: got\n%s\nwant\n%s", src, got, want)
		}
	}
}

func TestAddGitLabIncludeInsertsIntoBlockList(t *testing.T) {
	for src, want := range map[string]string{
		"include:\n  - local: a.yml\n":                        "include:\n  - remote: " + testURL + "\n  - local: a.yml\n",
		"include:\n    - local: a.yml\n":                      "include:\n    - remote: " + testURL + "\n    - local: a.yml\n",
		"include:\n- local: a.yml\n":                          "include:\n- remote: " + testURL + "\n- local: a.yml\n",
		"include:\n  - project: g/p\n    file: x.yml\nb: 1\n": "include:\n  - remote: " + testURL + "\n  - project: g/p\n    file: x.yml\nb: 1\n",
		"include:\n  - other.yml\n":                           "include:\n  - remote: " + testURL + "\n  - other.yml\n",
	} {
		if got := addInclude(t, src); got != want {
			t.Errorf("%q: got\n%s\nwant\n%s", src, got, want)
		}
	}
}

func TestAddGitLabIncludeReplacesExistingPin(t *testing.T) {
	old := "https://raw.githubusercontent.com/TrianaLab/pacto-actions/0ld/templates/pacto.yml"
	for src, want := range map[string]string{
		"include:\n  - local: a.yml\n  - remote: " + old + " # pacto\n": "include:\n  - local: a.yml\n  - remote: " + testURL + " # pacto\n",
		"include:\n  - remote: \"" + old + "\"\n":                       "include:\n  - remote: \"" + testURL + "\"\n",
		"include:\n  - '" + old + "'\n":                                 "include:\n  - '" + testURL + "'\n",
		"include:\n  - remote: " + testURL + "\n":                       "include:\n  - remote: " + testURL + "\n",
		"include:\n  - {x: é, remote: " + old + "}\n":                   "include:\n  - {x: é, remote: " + testURL + "}\n",
	} {
		if got := addInclude(t, src); got != want {
			t.Errorf("%q: got\n%s\nwant\n%s", src, got, want)
		}
	}
}

func TestAddGitLabIncludeFlowOrScalarIncludeRefused(t *testing.T) {
	for _, src := range []string{
		"include: other.yml\n",
		"include:\n  local: a.yml\n",
		"include: [a.yml]\n",
		"include:\n",
		"include:\n  -\n    local: a.yml\n",
		"include:\n  - \"https://raw.githubusercontent.com/TrianaLab/pacto-actions/\\x30ld/templates/pacto.yml\"\n",
		// A spec: header document before the pipeline, whose include the
		// first document does not show.
		"spec:\n  inputs:\n    env:\n      default: dev\n---\nstages: [test]\ninclude:\n  - local: a.yml\nbuild:\n  script: make\n",
	} {
		_, err := AddGitLabInclude([]byte(src), testURL)
		if err == nil || !strings.Contains(err.Error(), "  - remote: "+testURL) {
			t.Errorf("%q: err %v", src, err)
		}
	}
}

func TestAddGitLabIncludePreservesBytes(t *testing.T) {
	src := "# CI for acme\n\n.defaults: &defaults\n  image: alpine  # pinned later\n\n\ninclude:\n  # shared jobs\n  - local: a.yml\n\nbuild:\n  <<: *defaults\n  script:\n    - make\n"
	want := "# CI for acme\n\n.defaults: &defaults\n  image: alpine  # pinned later\n\n\ninclude:\n  # shared jobs\n  - remote: " + testURL + "\n  - local: a.yml\n\nbuild:\n  <<: *defaults\n  script:\n    - make\n"
	if got := addInclude(t, src); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestAddGitLabIncludeInvalidYAML(t *testing.T) {
	for _, src := range []string{"a: [\n", "- a\n- b\n", "a: 1\n---\nb: [\n"} {
		if _, err := AddGitLabInclude([]byte(src), testURL); err == nil || !strings.Contains(err.Error(), ".gitlab-ci.yml") {
			t.Errorf("%q: err %v", src, err)
		}
	}
}
