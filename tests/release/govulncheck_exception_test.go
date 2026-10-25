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

// security.yml accepts exactly one govulncheck advisory, GO-2026-5932
// (golang.org/x/crypto/openpgp, reached only through sigstore-go -> rekor's
// pkg/pki/pgp, never used by Pacto). An exception list only ever grows unless
// something stops it, so this test pins the list to that one ID and runs the
// real gate script against synthetic SARIF from both modules: the accepted ID
// passes only on init or interface traces, any other reachable ID still fails,
// and an absent ID fails so the stale exception has to be deleted.
func TestGovulncheckExceptionIsExactlyOneAdvisory(t *testing.T) {
	const accepted = "GO-2026-5932"
	b, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "security.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Env  map[string]string `yaml:"env"`
		Jobs map[string]struct {
			Steps []struct {
				Name string            `yaml:"name"`
				Env  map[string]string `yaml:"env"`
				Run  string            `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(b, &wf); err != nil {
		t.Fatal(err)
	}
	if got := wf.Env["IGNORED_VULN"]; got != accepted {
		t.Fatalf("security.yml IGNORED_VULN = %q; the only accepted exception is %s", got, accepted)
	}
	var script, scan string
	for _, s := range wf.Jobs["govulncheck"].Steps {
		if len(s.Env) > 0 {
			t.Errorf("step %q sets its own env; keep the exception in the single workflow-level IGNORED_VULN", s.Name)
		}
		if s.Name == "Fail on reachable vulnerabilities" {
			script = s.Run
		}
		if s.Name == "Run govulncheck (SARIF)" {
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

	// One result per entry: "file|ruleId|level|terminal frame" ("" frame = no trace).
	type res struct{ file, id, level, frame string }
	const initFrame = "golang.org/x/crypto/openpgp.init"
	cases := []struct {
		name      string
		results   []res
		wantPass  bool
		wantStale bool
	}{
		{"accepted init trace", []res{{"govulncheck.sarif", accepted, "error", initFrame}}, true, false},
		{"accepted interface trace", []res{{"govulncheck.sarif", accepted, "error", "golang.org/x/crypto/openpgp/armor.*openpgpReader.Read"}}, true, false},
		{"accepted only in the operator module", []res{{"govulncheck-k8s.sarif", accepted, "error", "golang.org/x/crypto/openpgp/errors.StructuralError.Error"}}, true, false},
		{"direct openpgp call", []res{{"govulncheck.sarif", accepted, "error", "golang.org/x/crypto/openpgp.ReadArmoredKeyRing"}}, false, false},
		{"direct packet parser call", []res{{"govulncheck.sarif", accepted, "error", "golang.org/x/crypto/openpgp/packet.Read"}}, false, false},
		{"direct package-level Error func", []res{{"govulncheck-k8s.sarif", accepted, "error", "golang.org/x/crypto/openpgp/errors.Error"}}, false, false},
		{"accepted packet reader method", []res{{"govulncheck.sarif", accepted, "error", "golang.org/x/crypto/openpgp/packet.*spanReader.Read"}}, true, false},
		{"accepted without a trace", []res{{"govulncheck.sarif", accepted, "error", ""}}, false, false},
		{"other reachable", []res{{"govulncheck.sarif", accepted, "error", initFrame}, {"govulncheck.sarif", "GO-2099-0001", "error", "x.F"}}, false, false},
		{"other reachable in the operator module", []res{{"govulncheck.sarif", accepted, "error", initFrame}, {"govulncheck-k8s.sarif", "GO-2099-0001", "error", "x.F"}}, false, false},
		{"other reachable alone", []res{{"govulncheck.sarif", "GO-2099-0001", "error", "x.F"}}, false, true},
		{"clean is stale and fails", nil, false, true},
		{"unreachable ignored but stale", []res{{"govulncheck.sarif", "GO-2099-0001", "warning", "x.F"}}, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			files := map[string][]string{"govulncheck.sarif": nil, "govulncheck-k8s.sarif": nil}
			for _, r := range c.results {
				flows := `[]`
				if r.frame != "" {
					flows = fmt.Sprintf(`[{"threadFlows":[{"locations":[{"location":{"message":{"text":"github.com/trianalab/pacto/v4/pkg/x.F"}}},{"location":{"message":{"text":%q}}}]}]}]`, r.frame)
				}
				files[r.file] = append(files[r.file], fmt.Sprintf(`{"ruleId":%q,"level":%q,"codeFlows":%s}`, r.id, r.level, flows))
			}
			for name, rs := range files {
				sarif := `{"runs":[{"results":[` + strings.Join(rs, ",") + `]}]}`
				if err := os.WriteFile(filepath.Join(dir, name), []byte(sarif), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("bash", "-e", "-c", script)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "IGNORED_VULN="+wf.Env["IGNORED_VULN"])
			out, err := cmd.CombinedOutput()
			if (err == nil) != c.wantPass {
				t.Fatalf("pass=%v, want %v; output:\n%s", err == nil, c.wantPass, out)
			}
			stale := strings.Contains(string(out), "no longer reported")
			if stale != c.wantStale {
				t.Fatalf("stale error printed=%v, want %v; output:\n%s", stale, c.wantStale, out)
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
