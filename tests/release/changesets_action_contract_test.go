package release

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The Version Packages step is the one release step no test run ever executes.
// release.yml's changesets job is gated on `github.event_name == 'push'`, so a
// pull request never reaches it, and the push run only reports whether the action
// exited zero — not whether it did the work.
//
// That matters because changesets/action v2 renamed every input this repository
// passes (version -> version-script, title -> pr-title, commit -> commit-message)
// and moved authentication from the GITHUB_TOKEN environment variable to a
// github-token input. GitHub Actions treats an undeclared `with:` key as a
// WARNING, never an error. So bumping the pin without renaming the keys leaves a
// green step that silently ignores all of them: the action falls back to a bare
// `changeset version`, which skips build-release-plan.mjs and
// apply-release-plan.mjs entirely. The Version PR would carry bumped manifests
// with no release transaction and stale version-derived integration docs, and
// detect.mjs would then decline to publish anything. The first observation of that
// failure is a release that quietly ships nothing.
//
// This gate is the substitute for the run that never happens. It reads the
// contract off the workflow the way GitHub reads it — a parsed document, not text,
// because the `uses:` line carries a trailing `# v2.1.0` comment a grep would
// treat as part of the reference — and pairs it with the Changesets CLI major the
// action drives, since the action and the CLI are two halves of one contract.

// The accepted v2 pin: a full commit, never a tag. changesets/action v2.1.0.
const changesetsActionPin = "changesets/action@198f833dd7d863100ea6e28967bc9a9fdefadb0a"

// The canonical version command. It is not `changeset version`: the repository's
// script chains the transaction builder, the plan applier and the version-derived
// docs regeneration, which is precisely what a dropped input would discard.
const changesetsVersionScript = "release:version:docs"

// changesets/action v2 drives the Changesets CLI v3 line. Anything older is the
// other half of a half-finished migration.
const changesetsCLIMinMajor = 3

// Every input changesets/action v2.1.0 declares at the pinned commit. A key
// outside this set is a v1 leftover or a typo, and both are ignored at runtime.
var changesetsV2Inputs = map[string]bool{
	"github-token":           true,
	"publish-script":         true,
	"version-script":         true,
	"commit-message":         true,
	"pr-title":               true,
	"pr-draft":               true,
	"pr-base-branch":         true,
	"create-github-releases": true,
	"push-git-tags":          true,
	"push-with-git-cli":      true,
	"cwd":                    true,
}

// The v1 names this repository actually passed. v2 removed them outright, so their
// presence is the exact shape of an unfinished migration.
var changesetsV1Inputs = []string{"version", "title", "commit"}

// changesetsSteps returns every step in every workflow that uses changesets/action,
// keyed by "<workflow>:<step name>". Scanning all workflows rather than only
// release.yml means a second, unmigrated copy of the step cannot appear elsewhere.
func changesetsSteps(t *testing.T, root string) map[string]wfStep {
	t.Helper()
	out := map[string]wfStep{}
	for _, w := range loadWorkflows(t, root) {
		if !strings.Contains(w.text, "changesets/action") {
			continue
		}
		for job, j := range workflowJobs(t, root, w.name) {
			for _, s := range j.Steps {
				if strings.HasPrefix(s.Uses, "changesets/action@") {
					out[w.name+":"+job] = s
				}
			}
		}
	}
	return out
}

func TestChangesetsActionV2Contract(t *testing.T) {
	root := repoRoot(t)
	steps := changesetsSteps(t, root)
	if len(steps) == 0 {
		t.Fatal("no workflow step uses changesets/action — the Version Packages step is the only way a release transaction is ever created")
	}

	for where, s := range steps {
		t.Run(where, func(t *testing.T) {
			if s.Uses != changesetsActionPin {
				t.Errorf("changesets/action must be pinned to the accepted v2 commit\n got: %s\nwant: %s", s.Uses, changesetsActionPin)
			}

			// Only v2 input names. This is the check that bites when a v1 key
			// survives the bump, and it names the offending key.
			for k := range s.With {
				if !changesetsV2Inputs[k] {
					t.Errorf("input %q is not declared by changesets/action v2 — it is silently ignored at runtime", k)
				}
			}

			// The three v1 names, called out by name: they are the ones this
			// repository passed, so they are what an incomplete migration leaves.
			for _, legacy := range changesetsV1Inputs {
				if _, ok := s.With[legacy]; ok {
					t.Errorf("legacy v1 input %q is still present — v2 removed it; use its v2 replacement", legacy)
				}
			}

			// Authentication travels through the input, not the environment. v2
			// reads no token from env, so an env-only step authenticates as the
			// action's default rather than as what this workflow chose to pass.
			tok, _ := s.With["github-token"].(string)
			if strings.TrimSpace(tok) == "" {
				t.Error("authentication must be passed through the github-token input")
			} else if !strings.Contains(tok, "secrets.GITHUB_TOKEN") {
				t.Errorf("github-token should carry the workflow token, got %q", tok)
			}
			if _, ok := s.Env["GITHUB_TOKEN"]; ok {
				t.Error("the obsolete GITHUB_TOKEN environment authentication path must be removed — v2 reads the token from the github-token input")
			}

			// The version command still routes through the canonical script, and
			// that script still exists. A rename on either side breaks the release.
			vs, _ := s.With["version-script"].(string)
			if !strings.Contains(vs, changesetsVersionScript) {
				t.Errorf("version-script must invoke %q (got %q) — the bare `changeset version` default skips the transaction, the plan and the docs regeneration", changesetsVersionScript, vs)
			}
		})
	}

	// The action half is only correct if the CLI half matches: v2 drives CLI v3.
	t.Run("changesets CLI major matches action v2", func(t *testing.T) {
		var pkg struct {
			Scripts         map[string]string `json:"scripts"`
			DevDependencies map[string]string `json:"devDependencies"`
		}
		if err := json.Unmarshal([]byte(readFile(t, root, "package.json")), &pkg); err != nil {
			t.Fatalf("parse package.json: %v", err)
		}
		if _, ok := pkg.Scripts[changesetsVersionScript]; !ok {
			t.Fatalf("package.json declares no %q script, but the release workflow invokes it", changesetsVersionScript)
		}
		spec, ok := pkg.DevDependencies["@changesets/cli"]
		if !ok {
			t.Fatal("package.json does not declare @changesets/cli")
		}
		major, err := strconv.Atoi(strings.SplitN(strings.TrimLeft(spec, "^~>=v "), ".", 2)[0])
		if err != nil {
			t.Fatalf("cannot read a major version from @changesets/cli %q: %v", spec, err)
		}
		if major < changesetsCLIMinMajor {
			t.Errorf("@changesets/cli %s is v%d; changesets/action v2 requires v%d or newer", spec, major, changesetsCLIMinMajor)
		}
	})

	// Every release unit is a private package: these are Go modules, images and
	// charts, and none of them is published to npm. Changesets only versions them
	// because privatePackages.version says so — and @changesets/config v4, which
	// CLI v3 pulls in, flipped that default from true to false. Under the flipped
	// default `changeset version` exits 0, prints "All files have been updated",
	// consumes no changeset and bumps nothing: a release that silently does not
	// happen. So the value is pinned explicitly here rather than inherited.
	//
	// release-version-test would catch it, but only when the `release` path filter
	// fires, and .changeset/ is not in that filter — a lone edit to the config
	// would skip it green. ci-gates has no path filter, so this is the check that
	// always runs.
	t.Run("private release units stay versionable", func(t *testing.T) {
		units, err := filepath.Glob(filepath.Join(root, "release", "units", "*", "package.json"))
		if err != nil || len(units) == 0 {
			t.Fatalf("no release unit packages found: %v", err)
		}
		var private []string
		for _, u := range units {
			b, err := os.ReadFile(u)
			if err != nil {
				t.Fatalf("read %s: %v", u, err)
			}
			var p struct {
				Name    string `json:"name"`
				Private bool   `json:"private"`
			}
			if err := json.Unmarshal(b, &p); err != nil {
				t.Fatalf("parse %s: %v", u, err)
			}
			if p.Private {
				private = append(private, p.Name)
			}
		}
		if len(private) == 0 {
			return // nothing private: the default cannot hurt anyone.
		}

		var cfg struct {
			PrivatePackages *struct {
				Version *bool `json:"version"`
			} `json:"privatePackages"`
		}
		if err := json.Unmarshal([]byte(readFile(t, root, ".changeset", "config.json")), &cfg); err != nil {
			t.Fatalf("parse .changeset/config.json: %v", err)
		}
		if cfg.PrivatePackages == nil || cfg.PrivatePackages.Version == nil || !*cfg.PrivatePackages.Version {
			t.Errorf("%d release units are private (%s) but .changeset/config.json does not set privatePackages.version=true — @changesets/config v4 defaults it to false, so `changeset version` would bump nothing and consume no changeset",
				len(private), strings.Join(private, ", "))
		}
	})
}
