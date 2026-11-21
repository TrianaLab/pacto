package release

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/internal/ci"
)

// The dogfood ci-step job in pacto.yml calls the pacto-actions reusable
// workflow exactly as the caller pacto ci init writes, over the demo
// monorepo, with the pacto this pull request builds.
type dogfoodJob struct {
	If          string            `yaml:"if"`
	Uses        string            `yaml:"uses"`
	Permissions map[string]string `yaml:"permissions"`
	With        map[string]any    `yaml:"with"`
	Outputs     map[string]string `yaml:"outputs"`
}

func loadPactoWorkflow(t *testing.T) map[string]dogfoodJob {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "pacto.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Jobs map[string]dogfoodJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse pacto.yml: %v", err)
	}
	if _, ok := doc.Jobs["ci-step"]; !ok {
		t.Fatal("pacto.yml has no ci-step job")
	}
	return doc.Jobs
}

// generatedCaller is the caller pacto ci init writes for GitHub, parsed.
func generatedCaller(t *testing.T) (uses string, permissions map[string]string) {
	t.Helper()
	_, content := ci.Caller("github")
	var doc struct {
		Permissions map[string]string `yaml:"permissions"`
		Jobs        map[string]struct {
			Uses string `yaml:"uses"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		t.Fatalf("parse ci.Caller(github): %v", err)
	}
	return doc.Jobs["pacto"].Uses, doc.Permissions
}

func TestDogfoodCallsTheGeneratedCaller(t *testing.T) {
	want, _ := generatedCaller(t)
	if got := loadPactoWorkflow(t)["ci-step"].Uses; got != want {
		t.Errorf("ci-step uses %q, pacto ci init writes %q: move ActionsSHA and ActionsTag in internal/ci/caller.go to the pin Dependabot bumped", got, want)
	}
}

func TestDogfoodGrantsTheCallerCeiling(t *testing.T) {
	_, want := generatedCaller(t)
	if got := loadPactoWorkflow(t)["ci-step"].Permissions; !maps.Equal(got, want) {
		t.Errorf("ci-step permissions %v, the generated caller grants %v: GitHub refuses to start the reusable workflow when any of its jobs asks for more", got, want)
	}
}

func TestDogfoodNeverPublishes(t *testing.T) {
	if got, ok := loadPactoWorkflow(t)["ci-step"].With["publish"].(bool); !ok || got {
		t.Errorf("ci-step with.publish = %v, want false: pacto.yml also runs on push to main", loadPactoWorkflow(t)["ci-step"].With["publish"])
	}
}

func TestDogfoodFilterIsWired(t *testing.T) {
	jobs := loadPactoWorkflow(t)
	if jobs["changes"].Outputs["ci-step"] == "" {
		t.Error("changes.outputs has no ci-step: every ci-step job would be skipped and pacto-required would read skipped as a pass")
	}
	for _, name := range []string{"pacto-bin", "ci-step"} {
		if !strings.Contains(jobs[name].If, "needs.changes.outputs.ci-step") {
			t.Errorf("job %s if %q does not read needs.changes.outputs.ci-step", name, jobs[name].If)
		}
	}
}
