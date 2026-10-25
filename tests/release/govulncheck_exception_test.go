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
// real gate script against synthetic SARIF: the accepted ID alone passes, any
// other reachable ID still fails, and an absent ID prints the stale notice.
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
	var script string
	for _, s := range wf.Jobs["govulncheck"].Steps {
		if len(s.Env) > 0 {
			t.Errorf("step %q sets its own env; keep the exception in the single workflow-level IGNORED_VULN", s.Name)
		}
		if s.Name == "Fail on reachable vulnerabilities" {
			script = s.Run
		}
	}
	if script == "" {
		t.Fatal(`govulncheck job has no "Fail on reachable vulnerabilities" step`)
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Fatal("jq is required to exercise the govulncheck gate script")
	}

	cases := []struct {
		name     string
		results  []string // "ruleId:level"
		wantPass bool
		wantOut  string
	}{
		{"accepted only", []string{accepted + ":error"}, true, ""},
		{"other reachable", []string{accepted + ":error", "GO-2099-0001:error"}, false, ""},
		{"other reachable alone", []string{"GO-2099-0001:error"}, false, "no longer reported"},
		{"clean is stale", nil, true, "no longer reported"},
		{"unreachable ignored", []string{"GO-2099-0001:warning"}, true, "no longer reported"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var rs []string
			for _, r := range c.results {
				id, lvl, _ := strings.Cut(r, ":")
				rs = append(rs, fmt.Sprintf(`{"ruleId":%q,"level":%q}`, id, lvl))
			}
			dir := t.TempDir()
			sarif := `{"runs":[{"results":[` + strings.Join(rs, ",") + `]}]}`
			if err := os.WriteFile(filepath.Join(dir, "govulncheck.sarif"), []byte(sarif), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", "-e", "-c", script)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "IGNORED_VULN="+wf.Env["IGNORED_VULN"])
			out, err := cmd.CombinedOutput()
			if (err == nil) != c.wantPass {
				t.Fatalf("pass=%v, want %v; output:\n%s", err == nil, c.wantPass, out)
			}
			stale := strings.Contains(string(out), "no longer reported")
			if stale != (c.wantOut != "") {
				t.Fatalf("stale notice printed=%v, want %v; output:\n%s", stale, c.wantOut != "", out)
			}
		})
	}
}
