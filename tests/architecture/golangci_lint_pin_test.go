package architecture

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The engine's golangci-lint BINARY is an input to a required gate, not a detail
// of the runner. `.github/actions/ci/action.yml` pinned the ACTION by commit but
// left the binary it installs to follow `latest`, so one upstream release turned
// `ci-static` red on a tree nobody had touched: on the same bytes v2.12.2 reported
// zero issues and v2.13.0 reported ten. Two halves, two pins — a gate whose
// verdict depends on the day it ran is not a gate.
//
// The rule is expressed over the parsed document rather than its text, because
// the `uses:` line carries a trailing `# v9.2.0` comment that a grep would read as
// part of the reference, and because a step key is a structure, not a spelling.

const lintAction = "golangci/golangci-lint-action"

// lintActionCommitPin is the action half: a full 40-hex commit, never a tag.
var lintActionCommitPin = regexp.MustCompile(`^` + regexp.QuoteMeta(lintAction) + `@[0-9a-f]{40}$`)

// immutableLintVersion is the binary half. `latest` moves by definition, an absent
// or empty value IS `latest`, and a minor-only `v2.13` still floats across patch
// releases — which is exactly the granularity the SA9010 change arrived in.
var immutableLintVersion = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

type compositeStep struct {
	Name string         `yaml:"name"`
	Uses string         `yaml:"uses"`
	With map[string]any `yaml:"with"`
}

// ciActionSteps parses the composite action the CI workflow delegates every leg
// to, resolved relative to this file so the gate does not depend on the working
// directory.
func ciActionSteps(t *testing.T) (string, []compositeStep) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(thisFile))) // tests/architecture -> repo root
	path := filepath.Join(root, ".github", "actions", "ci", "action.yml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		Runs struct {
			Steps []compositeStep `yaml:"steps"`
		} `yaml:"runs"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(doc.Runs.Steps) == 0 {
		t.Fatalf("%s declares no steps — this gate would have nothing to gate", path)
	}
	return path, doc.Runs.Steps
}

// pinnedLintVersion reports why a step's `with:` block does not pin the installed
// binary to one immutable version, or nil when it does.
func pinnedLintVersion(with map[string]any) error {
	raw, ok := with["version"]
	if !ok {
		return fmt.Errorf("the step sets no `version`, so the installer takes whatever is latest that day")
	}
	v := fmt.Sprint(raw)
	if v == "" {
		return fmt.Errorf("`version` is empty, which the installer reads as latest")
	}
	if !immutableLintVersion.MatchString(v) {
		return fmt.Errorf("`version: %s` is not a full immutable version like v2.13.0", v)
	}
	return nil
}

// TestTheLintVersionRuleRejectsEveryFloatingSpelling proves the rule above bites
// before it is pointed at the real file, so a later reader can see WHICH spellings
// are refused without mutating the workflow once per spelling.
func TestTheLintVersionRuleRejectsEveryFloatingSpelling(t *testing.T) {
	for _, tc := range []struct {
		name string
		with map[string]any
		want bool // want the value accepted
	}{
		{"no version key", map[string]any{"install-only": true}, false},
		{"empty value", map[string]any{"version": ""}, false},
		{"latest", map[string]any{"version": "latest"}, false},
		{"minor only", map[string]any{"version": "v2.13"}, false},
		{"major only", map[string]any{"version": "v2"}, false},
		{"full version", map[string]any{"version": "v2.13.0"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := pinnedLintVersion(tc.with)
			if got := err == nil; got != tc.want {
				t.Errorf("pinnedLintVersion(%v) accepted = %v, want %v (err = %v)", tc.with, got, tc.want, err)
			}
		})
	}
}

// TestTheCIActionInstallsOnePinnedGolangciLintBinary is the gate proper: the
// engine installs its linter in exactly one place, and that place pins both halves.
func TestTheCIActionInstallsOnePinnedGolangciLintBinary(t *testing.T) {
	path, steps := ciActionSteps(t)

	var installers []compositeStep
	for _, s := range steps {
		if s.Uses == lintAction || strings.HasPrefix(s.Uses, lintAction+"@") {
			installers = append(installers, s)
		}
	}
	// Exactly one, in both directions: zero means the installer was renamed or
	// moved and this gate has stopped watching anything; more than one means the
	// engine has two version sources and they can disagree.
	if len(installers) != 1 {
		t.Fatalf("%s has %d steps using %s, want exactly one — the engine's linter binary must have a single version source",
			path, len(installers), lintAction)
	}
	step := installers[0]

	if !lintActionCommitPin.MatchString(step.Uses) {
		t.Errorf("%s pins the action as %q; it must stay `%s@<40-hex commit>` so the action's own code cannot move under a tag",
			path, step.Uses, lintAction)
	}
	if err := pinnedLintVersion(step.With); err != nil {
		t.Errorf("%s installs a floating golangci-lint binary: %v. The same tree lints clean on one release and red on the next, which makes a required check depend on the calendar",
			path, err)
	}
	// install-only keeps the division of labour: the action places the binary and
	// `make ci-lint` runs it, so local and CI share one invocation.
	if fmt.Sprint(step.With["install-only"]) != "true" {
		t.Errorf("%s no longer sets `install-only: true`; the action would run the linter with its own flags instead of the ones `make ci-lint` uses", path)
	}
}
