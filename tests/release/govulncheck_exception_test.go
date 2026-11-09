package release

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// security.yml accepts no govulncheck advisory: every reachable vulnerability
// fails the job. An exception list only ever grows unless something stops it,
// so this test refuses any exception variable and runs the real gate script
// against synthetic SARIF from both modules: an error-level result in either
// module fails, and a clean report or warning-level (imported but unreachable)
// results pass.
func TestGovulncheckGateAcceptsNoException(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "security.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "IGNORED_VULN") {
		t.Fatal("security.yml declares a govulncheck exception (IGNORED_VULN); every reachable advisory must fail the job")
	}
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(b, &wf); err != nil {
		t.Fatal(err)
	}
	var script, scan string
	for _, s := range wf.Jobs["govulncheck"].Steps {
		switch s.Name {
		case "Fail on reachable vulnerabilities":
			script = s.Run
		case "Run govulncheck (SARIF)":
			scan = s.Run
		}
	}
	// What ships is scanned: the operator module (the pacto-delivery and
	// operator images) and the kms-tagged signing providers both.
	for _, want := range []string{"-tags kms ./... > govulncheck.sarif", "cd integrations/kubernetes && govulncheck -format sarif -tags kms ./..."} {
		if !strings.Contains(scan, want) {
			t.Errorf("govulncheck scan step does not run %q:\n%s", want, scan)
		}
	}
	if script == "" {
		t.Fatal(`govulncheck job has no "Fail on reachable vulnerabilities" step`)
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Fatal("jq is required to exercise the govulncheck gate script")
	}

	type res struct{ file, id, level string }
	cases := []struct {
		name     string
		results  []res
		wantPass bool
	}{
		{"clean", nil, true},
		{"unreachable only", []res{{"govulncheck.sarif", "GO-2099-0001", "warning"}, {"govulncheck-k8s.sarif", "GO-2099-0002", "note"}}, true},
		{"reachable", []res{{"govulncheck.sarif", "GO-2099-0001", "error"}}, false},
		{"reachable in the operator module", []res{{"govulncheck-k8s.sarif", "GO-2099-0001", "error"}}, false},
		{"formerly accepted advisory", []res{{"govulncheck.sarif", "GO-2026-5932", "error"}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			files := map[string][]string{"govulncheck.sarif": nil, "govulncheck-k8s.sarif": nil}
			for _, r := range c.results {
				files[r.file] = append(files[r.file], fmt.Sprintf(`{"ruleId":%q,"level":%q}`, r.id, r.level))
			}
			for name, rs := range files {
				sarif := `{"runs":[{"results":[` + strings.Join(rs, ",") + `]}]}`
				if err := os.WriteFile(filepath.Join(dir, name), []byte(sarif), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("bash", "-e", "-c", script)
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if (err == nil) != c.wantPass {
				t.Fatalf("pass=%v, want %v; output:\n%s", err == nil, c.wantPass, out)
			}
		})
	}
}

// Every image the release ships gets a Trivy leg, the pacto-delivery recorder
// included. It is built from the same Dockerfile target and CMD the release
// uses, with no GO_TAGS override, so the scan sees the KMS SDKs the shipped
// image links by default.
func TestTrivyScansEveryShippedImage(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "security.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Jobs map[string]struct {
			Strategy struct {
				Matrix struct {
					Include []map[string]string `yaml:"include"`
				} `yaml:"matrix"`
			} `yaml:"strategy"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(b, &wf); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, leg := range wf.Jobs["trivy-image"].Strategy.Matrix.Include {
		got[leg["image"]] = leg["build"]
	}
	want := map[string]string{
		"cli":      ".",
		"operator": "--file integrations/kubernetes/Dockerfile .",
		"delivery": "--file integrations/kubernetes/Dockerfile --target delivery --build-arg CMD=cmd/delivery .",
	}
	for img, build := range want {
		if got[img] != build {
			t.Errorf("trivy-image leg %q builds %q, want %q", img, got[img], build)
		}
	}
	if strings.Contains(got["delivery"], "GO_TAGS") {
		t.Error("the delivery scan overrides GO_TAGS; it must scan the default (kms) image the release ships")
	}
}
