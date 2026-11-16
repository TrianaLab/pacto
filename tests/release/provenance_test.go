package release

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The release attests SLSA build provenance for every CLI binary and
// checksums.txt with actions/attest-build-provenance, and only when its own
// release.yml@main attestation over the transaction's source commit does not
// verify yet. provenanceProblems is the one checker every test and bite case
// below calls, so the bite cases prove the same rules the real file passes.

const attestAction = "actions/attest-build-provenance@"

var attestPin = regexp.MustCompile(`actions/attest-build-provenance@([0-9a-f]{40})\b`)

func releaseWorkflowBytes(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("read release.yml: %v", err)
	}
	return b
}

func provenanceProblems(src []byte) []string {
	var doc struct {
		Jobs map[string]wfJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return []string{"parse release.yml: " + err.Error()}
	}
	var out []string
	job, ok := doc.Jobs["release"]
	if !ok {
		return []string{"release.yml has no release job"}
	}
	for _, p := range []string{"contents", "id-token", "attestations"} {
		if job.Permissions[p] != "write" {
			out = append(out, fmt.Sprintf("release: permission %s: write is missing", p))
		}
	}
	out = append(out, cliAttestProblems(job.Steps)...)
	for name, j := range doc.Jobs {
		if j.Uses != "" {
			out = append(out, fmt.Sprintf("%s: a reusable workflow (uses: %s) breaks the skip-safe DAG", name, j.Uses))
		}
	}
	return out
}

// cliAttestProblems checks the probe, the attest step and their order inside
// the release job.
func cliAttestProblems(steps []wfStep) []string {
	var out []string
	build, probe, attest, finalize := -1, -1, -1, -1
	for i, s := range steps {
		switch {
		case strings.HasPrefix(s.Name, "Build reproducible binaries"):
			build = i
		case s.ID == "attested":
			probe = i
		case strings.HasPrefix(s.Uses, attestAction):
			attest = i
		case strings.Contains(s.Run, "finalize-release.sh"):
			finalize = i
		}
	}
	if probe < 0 {
		return append(out, "release: no probe step (id: attested)")
	}
	if attest < 0 {
		return append(out, "release: no actions/attest-build-provenance step")
	}
	if build < 0 || build > probe || probe > attest || attest > finalize {
		out = append(out, fmt.Sprintf("release: order must be build < probe < attest < finalize, got %d %d %d %d", build, probe, attest, finalize))
	}
	out = append(out, probeProblems("release", steps[probe], "dist/pacto_*", "dist/checksums.txt")...)
	a := steps[attest]
	if !attestPin.MatchString(a.Uses) {
		out = append(out, "release: attest step is not pinned to a 40-hex commit: "+a.Uses)
	}
	out = append(out, gatedOnProbe("release", a)...)
	subj, _ := a.With["subject-path"].(string)
	if got := strings.Fields(subj); strings.Join(got, " ") != "dist/pacto_* dist/checksums.txt" {
		out = append(out, fmt.Sprintf("release: subject-path must be exactly dist/pacto_* and dist/checksums.txt, got %q", got))
	}
	return out
}

// probeProblems holds every probe to the signer-bound verify a reviewer runs,
// plus the two guards that refuse to sign a false ref or build commit.
func probeProblems(job string, s wfStep, mustName ...string) []string {
	var out []string
	if s.ContinueOnError != nil {
		out = append(out, job+": the probe must not set continue-on-error")
	}
	want := append([]string{
		"gh attestation verify",
		"--cert-identity",
		"release.yml@refs/heads/main",
		"--source-digest",
		`test "$GITHUB_REF" = refs/heads/main`,
		`test "$GITHUB_SHA" = "$SRC"`,
	}, mustName...)
	for _, w := range want {
		if !strings.Contains(s.Run, w) {
			out = append(out, fmt.Sprintf("%s: the probe does not contain %q", job, w))
		}
	}
	if strings.Contains(s.Run, "/attestations/") {
		out = append(out, job+": the probe counts stored attestations instead of verifying its own")
	}
	return out
}

func gatedOnProbe(job string, s wfStep) []string {
	var out []string
	if strings.TrimSpace(s.If) != "steps.attested.outputs.need == 'true'" {
		out = append(out, fmt.Sprintf("%s: attest step must run only when steps.attested.outputs.need == 'true', got %q", job, s.If))
	}
	if s.ContinueOnError != nil {
		out = append(out, job+": the attest step must not set continue-on-error")
	}
	return out
}

func TestReleaseJobAttestsEveryBinaryAndTheChecksums(t *testing.T) {
	for _, p := range provenanceProblems(releaseWorkflowBytes(t)) {
		t.Error(p)
	}
}

func TestEveryAttestStepIsGatedOnTheProbe(t *testing.T) {
	jobs := workflowJobs(t, repoRoot(t), "release.yml")
	n := 0
	for name, j := range jobs {
		for i, s := range j.Steps {
			if !strings.HasPrefix(s.Uses, attestAction) {
				continue
			}
			n++
			if i == 0 || j.Steps[i-1].ID != "attested" {
				t.Errorf("%s: the step before %q must be the probe (id: attested)", name, s.Name)
			}
			for _, p := range gatedOnProbe(name, s) {
				t.Error(p)
			}
		}
	}
	if n == 0 {
		t.Fatal("release.yml has no attest step")
	}
}

func TestAttestPinsAreCommitShasAndIdentical(t *testing.T) {
	pins := map[string]bool{}
	for _, w := range loadWorkflows(t, repoRoot(t)) {
		for _, line := range strings.Split(w.text, "\n") {
			if !strings.Contains(line, attestAction) {
				continue
			}
			m := attestPin.FindStringSubmatch(line)
			if m == nil {
				t.Errorf("%s: %s is not pinned to a 40-hex commit", w.name, strings.TrimSpace(line))
				continue
			}
			pins[m[1]] = true
		}
	}
	if len(pins) > 1 {
		t.Errorf("actions/attest-build-provenance is pinned to %d different commits; use one", len(pins))
	}
}

func TestOnlyTheReleaseWorkflowAttests(t *testing.T) {
	attest := regexp.MustCompile(`uses:\s*actions/attest(-build-provenance)?@`)
	for _, w := range loadWorkflows(t, repoRoot(t)) {
		if w.name != "release.yml" && attest.MatchString(w.text) {
			t.Errorf("%s attests; only release.yml signs build provenance", w.name)
		}
	}
}

func TestReleaseWorkflowHasNoReusableWorkflow(t *testing.T) {
	for name, j := range workflowJobs(t, repoRoot(t), "release.yml") {
		if j.Uses != "" {
			t.Errorf("%s calls the reusable workflow %s; a skipped reusable workflow poisons the DAG", name, j.Uses)
		}
	}
}

// releaseSection splits the text at the release job, so a mutation lands in
// that job even when an image job carries the same strings.
func releaseSection(t *testing.T, src string) (string, string) {
	t.Helper()
	i := strings.Index(src, "\n  release:\n")
	if i < 0 {
		t.Fatal("release.yml has no release job")
	}
	return src[:i], src[i:]
}

// stepBlock returns the text of the step whose first line contains marker.
func stepBlock(t *testing.T, section, marker string) string {
	t.Helper()
	i := strings.Index(section, marker)
	if i < 0 {
		t.Fatalf("no step with %q", marker)
	}
	start := strings.LastIndex(section[:i], "\n") + 1
	end := strings.Index(section[start+1:], "\n      - ")
	if end < 0 {
		t.Fatalf("step %q is the last one", marker)
	}
	return section[start : start+1+end+1]
}

func TestTheProvenanceGateBites(t *testing.T) {
	const attestName = "- name: Attest SLSA build provenance for the binaries and checksums"
	replace := func(old, new string) func(*testing.T, string) string {
		return func(t *testing.T, rel string) string { return strings.Replace(rel, old, new, 1) }
	}
	cases := []struct {
		name   string
		mutate func(*testing.T, string) string
	}{
		{"delete the attest step", func(t *testing.T, rel string) string {
			return strings.Replace(rel, stepBlock(t, rel, attestName), "", 1)
		}},
		{"drop attestations: write", replace("      attestations: write\n", "")},
		{"move the attest step after finalize", func(t *testing.T, rel string) string {
			blk := stepBlock(t, rel, attestName)
			rel = strings.Replace(rel, blk, "", 1)
			fin := "run: bash release/orchestrator/finalize-release.sh \"$TAG\" \"$SRC\"\n"
			return strings.Replace(rel, fin, fin+blk, 1)
		}},
		{"gate the attest step on always()", replace("if: steps.attested.outputs.need == 'true'", "if: always()")},
		{"add continue-on-error", replace("if: steps.attested.outputs.need == 'true'\n", "if: steps.attested.outputs.need == 'true'\n        continue-on-error: true\n")},
		{"drop the checksums subject", replace("            dist/checksums.txt\n", "")},
		{"pin by tag", func(t *testing.T, rel string) string {
			return attestPin.ReplaceAllString(rel, "actions/attest-build-provenance@v4")
		}},
		{"delete the --cert-identity line", func(t *testing.T, rel string) string {
			return regexp.MustCompile(`(?m)^\s+--cert-identity .*\n`).ReplaceAllString(rel, "")
		}},
		{"drop the ref from the identity", replace("release.yml@refs/heads/main", "release.yml")},
		{"delete --source-digest", replace(`--source-digest "$SRC" `, "")},
		{"delete the GITHUB_REF guard", func(t *testing.T, rel string) string {
			return regexp.MustCompile(`(?m)^\s+test "\$GITHUB_REF".*\n`).ReplaceAllString(rel, "")
		}},
		{"delete the GITHUB_SHA guard", func(t *testing.T, rel string) string {
			return regexp.MustCompile(`(?m)^\s+test "\$GITHUB_SHA".*\n`).ReplaceAllString(rel, "")
		}},
		{"count stored attestations instead of verifying", func(t *testing.T, rel string) string {
			blk := stepBlock(t, rel, "- name: Check for a verified provenance attestation")
			i := strings.Index(blk, "        run: |\n")
			count := "        run: |\n" +
				"          n=$(gh api \"repos/$GITHUB_REPOSITORY/attestations/sha256:x\" --jq '.attestations | length')\n" +
				"          echo \"need=$([ \"$n\" = 0 ] && echo true || echo false)\" >> \"$GITHUB_OUTPUT\"\n"
			return strings.Replace(rel, blk, blk[:i]+count, 1)
		}},
	}
	src := string(releaseWorkflowBytes(t))
	if p := provenanceProblems([]byte(src)); len(p) != 0 {
		t.Fatalf("the real release.yml already fails: %v", p)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			head, rel := releaseSection(t, src)
			mutated := c.mutate(t, rel)
			if mutated == rel {
				t.Fatal("the mutation changed nothing")
			}
			if len(provenanceProblems([]byte(head+mutated))) == 0 {
				t.Error("provenanceProblems accepts the mutated workflow")
			}
		})
	}
}
