package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The operator and pacto-delivery images and charts publish through one set of
// steps each: the delivery jobs alias the operator jobs' anchored steps, so a
// release-incident fix lands once. This fails if someone copies the steps back.
func TestDeliveryJobsReuseOperatorSteps(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	steps := map[string]*yaml.Node{}
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "jobs" {
			continue
		}
		jobs := root.Content[i+1]
		for j := 0; j+1 < len(jobs.Content); j += 2 {
			job := jobs.Content[j+1]
			for k := 0; k+1 < len(job.Content); k += 2 {
				if job.Content[k].Value == "steps" {
					steps[jobs.Content[j].Value] = job.Content[k+1]
				}
			}
		}
	}
	for delivery, operator := range map[string]string{
		"delivery-image-build": "operator-image-build",
		"delivery-image":       "operator-image",
		"delivery-chart":       "operator-chart",
	} {
		d, o := steps[delivery], steps[operator]
		if d == nil || o == nil {
			t.Errorf("release.yml lacks the steps of %s or %s", delivery, operator)
			continue
		}
		if d.Kind != yaml.AliasNode || d.Alias != o {
			t.Errorf("%s does not reuse %s's steps by YAML alias; the two copies would drift", delivery, operator)
		}
	}
}

// docs_check.py reads the signed coordinates straight off release.yml. With
// the delivery jobs sharing the operator steps by alias and naming their
// coordinates in job env, it must still find every signed image and chart.
func TestDocsCheckSeesEverySignedCoordinate(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command("python3", "-I", "-c", `
import importlib.util, sys
spec = importlib.util.spec_from_file_location("dc", sys.argv[1])
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)
print("\n".join(sorted(m.signed_coordinates())))
`, filepath.Join(root, "release", "scripts", "docs_check.py"))
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docs_check.py: %v\n%s", err, out)
	}
	got := strings.Fields(string(out))
	want := []string{
		"ghcr.io/trianalab/pacto/charts/pacto-delivery",
		"ghcr.io/trianalab/pacto/charts/pacto-operator",
		"ghcr.io/trianalab/pacto/dashboard",
		"ghcr.io/trianalab/pacto/delivery",
		"ghcr.io/trianalab/pacto/operator",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("signed coordinates = %v, want %v", got, want)
	}
}
