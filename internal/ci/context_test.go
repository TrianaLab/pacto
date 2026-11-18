package ci

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// fakeEnv is a getenv over m that records every name it is asked.
func fakeEnv(m map[string]string, asked *[]string) func(string) string {
	return func(k string) string {
		*asked = append(*asked, k)
		return m[k]
	}
}

func TestDetectGitHub(t *testing.T) {
	var asked []string
	c := Detect(fakeEnv(map[string]string{
		"GITHUB_ACTIONS": "true", "GITHUB_BASE_REF": "main", "ACTIONS_ID_TOKEN_REQUEST_URL": "https://x",
		"GITHUB_STEP_SUMMARY": "/tmp/summary", "GITHUB_REPOSITORY_OWNER": "Acme",
	}, &asked))
	if c.Platform != "github" || c.Base.String() != "main (from GITHUB_BASE_REF, context only)" || !c.OIDC || c.Summary != "/tmp/summary" || c.Owner != "Acme" {
		t.Fatalf("context = %+v", c)
	}
}

func TestDetectGitLab(t *testing.T) {
	var asked []string
	c := Detect(fakeEnv(map[string]string{
		"GITLAB_CI": "true", "CI_MERGE_REQUEST_DIFF_BASE_SHA": "4e1a9c0d5b6f", "CI_PROJECT_DIR": "/builds/x",
		"CI_REGISTRY_IMAGE": "registry.gitlab.com/acme/repo",
	}, &asked))
	if c.Platform != "gitlab" || c.Base.Value != "4e1a9c0" || c.Base.Source != "CI_MERGE_REQUEST_DIFF_BASE_SHA, context only" ||
		c.ArtifactDir != "/builds/x" || c.RegistryImage != "registry.gitlab.com/acme/repo" || c.OIDC {
		t.Fatalf("context = %+v", c)
	}
	if c := Detect(fakeEnv(map[string]string{"GITLAB_CI": "true"}, &asked)); c.Base.String() != "none" {
		t.Fatalf("no merge request base: %+v", c)
	}
}

func TestDetectNone(t *testing.T) {
	var asked []string
	if c := Detect(fakeEnv(nil, &asked)); c != (Context{}) || c.Base.String() != "none" {
		t.Fatalf("context = %+v", c)
	}
	if c := Detect(nil); c != (Context{}) {
		t.Fatalf("nil getenv = %+v", c)
	}
}

func TestDetectReadsOnlyListedVariables(t *testing.T) {
	want := []string{
		"GITHUB_ACTIONS", "GITHUB_REPOSITORY_OWNER", "GITHUB_SERVER_URL", "GITHUB_BASE_REF", "GITHUB_STEP_SUMMARY",
		"ACTIONS_ID_TOKEN_REQUEST_URL", "GITLAB_CI", "CI_REGISTRY_IMAGE", "CI_MERGE_REQUEST_DIFF_BASE_SHA", "CI_PROJECT_DIR",
	}
	for _, env := range []map[string]string{nil, {"GITHUB_ACTIONS": "true"}, {"GITLAB_CI": "true"}} {
		var asked []string
		Detect(fakeEnv(env, &asked))
		slices.Sort(asked)
		sorted := slices.Sorted(slices.Values(want))
		if !slices.Equal(asked, sorted) {
			t.Fatalf("asked %v, want %v", asked, sorted)
		}
	}
}

func TestDetectNeverReadsPersonVariables(t *testing.T) {
	env := map[string]string{
		"GITHUB_ACTIONS": "true", "GITHUB_ACTOR": "alice", "GITHUB_TRIGGERING_ACTOR": "alice",
		"GITLAB_USER_LOGIN": "alice", "GITLAB_USER_NAME": "alice", "USER": "alice",
	}
	var asked []string
	c := Detect(fakeEnv(env, &asked))
	for _, k := range asked {
		if k == "USER" || strings.Contains(k, "ACTOR") || strings.HasPrefix(k, "GITLAB_USER") {
			t.Fatalf("Detect read %s", k)
		}
	}
	if s := fmt.Sprintf("%+v", c); strings.Contains(s, "alice") {
		t.Fatalf("context holds a person's name: %s", s)
	}
}
