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

// The operator and pacto-delivery images publish through one set of steps each:
// the delivery jobs alias the operator jobs' anchored steps, so a
// release-incident fix lands once. One chart, pacto-operator, installs both. This fails if someone copies the steps back.
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
// coordinates in job env, it must still find every signed image and the chart.
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
		"ghcr.io/trianalab/pacto/charts/pacto-operator",
		"ghcr.io/trianalab/pacto/dashboard",
		"ghcr.io/trianalab/pacto/delivery",
		"ghcr.io/trianalab/pacto/operator",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("signed coordinates = %v, want %v", got, want)
	}
}

// runDocsCheck loads docs_check.py as a module and runs a Python snippet
// against it (the module is bound to m; argv[2], when given, is the doc path).
func runDocsCheck(t *testing.T, snippet string, args ...string) string {
	t.Helper()
	root := repoRoot(t)
	argv := append([]string{"-I", "-c", `
import importlib.util, sys
spec = importlib.util.spec_from_file_location("dc", sys.argv[1])
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)
` + snippet, filepath.Join(root, "release", "scripts", "docs_check.py")}, args...)
	cmd := exec.Command("python3", argv...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docs_check.py: %v\n%s", err, out)
	}
	return string(out)
}

// The attested coordinates are read off release.yml the same way the signed
// ones are; the dashboard names its literal, operator and delivery go through
// the shared steps and their job env.
func TestDocsCheckSeesEveryAttestedCoordinate(t *testing.T) {
	out := runDocsCheck(t, `
print("\n".join(sorted(m.attested_coordinates())))
print("cli", m.cli_attested())
`)
	want := "ghcr.io/trianalab/pacto/dashboard\nghcr.io/trianalab/pacto/delivery\nghcr.io/trianalab/pacto/operator\ncli True"
	if strings.TrimSpace(out) != want {
		t.Errorf("attested coordinates =\n%s\nwant\n%s", out, want)
	}
}

const lastSupplyChainVerdict = `
m.SUPPLY_CHAIN_DOC = sys.argv[2]
if len(sys.argv) > 3:
    m.OPERATOR_README = sys.argv[3]
m.check_supply_chain()
ok, name, detail = m.ledger[-1]
print("OK" if ok else "FAIL", name, "--", detail)
`

// supplyChainVerdict runs check (n) on doc and, when given, an operator README.
func supplyChainVerdict(t *testing.T, doc string, readme ...string) string {
	t.Helper()
	dir := t.TempDir()
	args := []string{}
	for i, body := range append([]string{doc}, readme...) {
		p := filepath.Join(dir, fmt.Sprintf("doc%d.md", i))
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		args = append(args, p)
	}
	return lastLine(runDocsCheck(t, lastSupplyChainVerdict, args...))
}

// lastLine drops what record() printed on the way.
func lastLine(s string) string {
	s = strings.TrimSpace(s)
	return s[strings.LastIndex(s, "\n")+1:]
}

// Check (n) runs on every PR through ci-gates; the docs-check workflow's path
// filter does not see a PR that edits only release.yml.
func TestDocsSupplyChainTableMatchesTheRealWorkflow(t *testing.T) {
	out := lastLine(runDocsCheck(t, lastSupplyChainVerdict, filepath.Join(repoRoot(t), "docs", "installation.md")))
	if !strings.HasPrefix(out, "OK ") {
		t.Errorf("check (n) on the real docs and release.yml: %s", out)
	}
}

func TestDocsSupplyChainCheckBitesOnAMissingClaim(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "installation.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	lines := strings.Split(doc, "\n")
	dropProvenance := func(row string) string {
		out := append([]string(nil), lines...)
		for i, l := range out {
			if strings.HasPrefix(l, row) {
				out[i] = strings.ReplaceAll(l, " and SLSA build provenance", "")
			}
		}
		return strings.Join(out, "\n")
	}
	cases := map[string]struct{ doc, want string }{
		"operator row without provenance": {dropProvenance("| `ghcr.io/trianalab/pacto/operator`"), "ghcr.io/trianalab/pacto/operator"},
		"CLI row without provenance":      {dropProvenance("| CLI binaries"), "CLI binaries"},
		"verify without the main ref": {
			strings.ReplaceAll(doc, "--cert-identity https://github.com/TrianaLab/pacto/.github/workflows/release.yml@refs/heads/main",
				"--signer-workflow TrianaLab/pacto/.github/workflows/release.yml"),
			"refs/heads/main",
		},
		"unguarded --source-digest": {
			strings.ReplaceAll(doc, `"${sha:?tag not found}"`, `"$(git rev-list -n1 v<version>)"`),
			"--source-digest",
		},
		"predicateType promised in the human output": {
			strings.ReplaceAll(doc, "`--format json` and read `.[].verificationResult.statement.predicateType`", "`predicateType`"),
			"predicateType",
		},
	}
	readme := readFile(t, repoRoot(t), "integrations", "kubernetes", "README.md")
	if bad := strings.ReplaceAll(readme, "release.yml@refs/heads/main", "release.yml"); bad == readme {
		t.Error("README without the main ref: the mutation changed nothing; the README moved")
	} else if out := supplyChainVerdict(t, doc, bad); !strings.HasPrefix(out, "FAIL ") || !strings.Contains(out, "refs/heads/main") {
		t.Errorf("README without the main ref: check (n) = %s, want a failure naming refs/heads/main", out)
	}
	for name, c := range cases {
		if c.doc == doc {
			t.Errorf("%s: the mutation changed nothing; the docs moved", name)
			continue
		}
		out := supplyChainVerdict(t, c.doc)
		if !strings.HasPrefix(out, "FAIL ") || !strings.Contains(out, c.want) {
			t.Errorf("%s: check (n) = %s, want a failure naming %s", name, out, c.want)
		}
	}
}
