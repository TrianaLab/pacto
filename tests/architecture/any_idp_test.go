package architecture

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// idpVendor names IDP vendors by their vendor-specific spellings. A bare
// "Port" stays allowed: it is a Kubernetes field name.
// ponytail: a fixed denylist only knows the vendors on it; add a name when a
// doc or issue names a new IDP.
var idpVendor = regexp.MustCompile(`(?i)\b(humanitec|getport\.io|port\.io|port-labs|port ocean|score\.dev|score\.yaml|score-spec|kratix|opslevel|cortexapps)\b`)

// idpVendorPathspecs are the tracked sources core ships: Go code and module
// files in either module, the dashboard frontend sources, the operator and
// chart YAML and the fake private IDP script.
var idpVendorPathspecs = []string{
	":(glob)**/*.go",
	":(glob)**/go.mod",
	":(glob)**/go.sum",
	":(glob)pkg/dashboard/frontend/src/**/*.ts",
	":(glob)pkg/dashboard/frontend/src/**/*.svelte",
	":(glob)pkg/dashboard/frontend/src/**/*.js",
	":(glob)integrations/kubernetes/**/*.yaml",
	":(glob)tests/anyidp/*.sh",
}

func TestIDPVendorPatternCatchesNames(t *testing.T) {
	cases := map[string]bool{
		"Humanitec":                          true,
		"humanitec.io":                       true,
		"getport.io":                         true,
		"port-labs/ocean":                    true,
		"Port Ocean":                         true,
		"score.yaml":                         true,
		"score.dev":                          true,
		"score-spec":                         true,
		"Kratix":                             true,
		"import x from 'getport.io/sdk'":     true,
		"<span>Humanitec</span>":             true,
		"portLabs: port-labs/ocean":          true,
		"yq score.yaml":                      true,
		"TargetPort":                         false,
		"Port: 80":                           false,
		"DashboardPort":                      false,
		"--port int":                         false,
		"Backstage":                          false,
		"        containerPort: 8080":        false,
		"ports: [{name: http, port: 8080}]":  false,
		"export const score = rank(service)": false,
	}
	for line, want := range cases {
		if got := idpVendor.MatchString(line); got != want {
			t.Errorf("idpVendor.MatchString(%q) = %v, want %v", line, got, want)
		}
	}
}

// TestCoreNamesNoIDPVendor holds two #452 rules, "Extension seams" and "Any
// IDP": Pacto sits on top of any IDP through generic seams, so core names no
// IDP vendor and vendor examples live outside core. Backstage is allowed:
// decision 26 of #452 keeps the read-only Backstage export in core. The scan
// lists tracked files only, so untracked scratch files, node_modules and the
// .claude/worktrees checkouts of other branches never count.
func TestCoreNamesNoIDPVendor(t *testing.T) {
	root := repoRoot(t)
	args := append([]string{"-C", root, "ls-files", "-z", "--"}, idpVendorPathspecs...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	files := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	if len(files) == 0 {
		t.Fatal("git ls-files listed no core file")
	}
	for _, file := range files {
		if strings.HasPrefix(file, "docs/") || strings.HasPrefix(file, "pkg/dashboard/ui/") ||
			file == "tests/architecture/any_idp_test.go" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, file)) //nolint:gosec // a tracked file under the repository root
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if m := idpVendor.FindString(line); m != "" {
				t.Errorf("%s:%d names IDP vendor %q; keep vendor code outside core", file, i+1, m)
			}
		}
	}
}
