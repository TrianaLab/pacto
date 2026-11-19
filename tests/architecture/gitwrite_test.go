package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// gitClientModules are the Git and Git-host client modules a Pacto server
// must never link: with one of them, a process could write to Git.
var gitClientModules = []string{
	"github.com/go-git/",
	"github.com/libgit2/",
	"gopkg.in/src-d/go-git",
	"github.com/google/go-github/",
	"gitlab.com/gitlab-org/api/client-go",
	"github.com/xanzy/go-gitlab",
	"code.gitea.io/sdk/gitea",
}

// gitHostClientAllowed names the CLI-only packages that may import a Git-host
// client, with the reason. It stays empty until 513-S8 (`pacto declare --pr`)
// settles its package: that one commits through the Git host API with the
// person's own token, from the CLI or the stdio MCP server only, and never
// from the operator or a server path.
var gitHostClientAllowed = map[string]string{}

// isServerPath reports whether a repository-relative Go file belongs to a
// process that would need standing Git write access: the operator and the
// dashboard and remote MCP servers.
func isServerPath(rel string) bool {
	base := filepath.Base(rel)
	return strings.HasPrefix(rel, "integrations/kubernetes/cmd/") ||
		strings.HasPrefix(rel, "integrations/kubernetes/internal/") ||
		strings.HasPrefix(rel, "pkg/dashboard/") ||
		rel == "internal/cli/mcp_remote.go" ||
		(filepath.Dir(rel) == "internal/cli" && strings.HasPrefix(base, "dashboard"))
}

// gitWrites returns why a server Go source could write to Git: an import of a
// Git or Git-host client, or a "git" string literal (exec.Command("git", ...)).
func gitWrites(rel string, src []byte) []string {
	if !isServerPath(rel) || strings.HasSuffix(rel, "_test.go") {
		return nil
	}
	f, err := parser.ParseFile(token.NewFileSet(), rel, src, parser.SkipObjectResolution)
	if err != nil {
		return []string{rel + ": " + err.Error()}
	}
	var out []string
	for _, imp := range f.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		for _, m := range gitClientModules {
			if strings.HasPrefix(path, m) {
				out = append(out, rel+" imports "+path)
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if ok && lit.Kind == token.STRING {
			if v, _ := strconv.Unquote(lit.Value); v == "git" {
				out = append(out, rel+` holds the literal "git"`)
			}
		}
		return true
	})
	return out
}

// TestPactoServersNeverWriteToGit guards the #452 invariant "Pacto never
// needs Git write access": no operator or server Go file imports a Git or
// Git-host client or runs the git binary, and neither go.mod requires such a
// client unless gitHostClientAllowed names the CLI-only package that uses it.
//
// A static scan cannot see a commit made through a plain HTTP call to a Git
// host API (PUT /repos/.../contents). The server paths hold no Git host
// credentials to send one: the operator authenticates to Kubernetes and
// registries, and the dashboard and remote MCP server act with the caller's
// Kubernetes token.
func TestPactoServersNeverWriteToGit(t *testing.T) {
	root := repoRoot(t)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".claude", "vendor", "node_modules", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if !strings.HasSuffix(rel, ".go") || !isServerPath(rel) {
			return nil
		}
		src, err := os.ReadFile(path) //nolint:gosec // a path under the repository root
		if err != nil {
			return err
		}
		for _, v := range gitWrites(rel, src) {
			t.Error(v + ": the operator and the servers never write to Git (#452)")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, mod := range []string{"go.mod", "integrations/kubernetes/go.mod"} {
		for _, m := range goModGitClients(readRepoFile(t, mod)) {
			if mod != "go.mod" || len(gitHostClientAllowed) == 0 {
				t.Errorf("%s requires %s: only a CLI-only package named in gitHostClientAllowed may use a Git-host client", mod, m)
			}
		}
	}
}

// goModGitClients lists the Git client modules a go.mod requires.
func goModGitClients(gomod string) []string {
	var out []string
	for line := range strings.SplitSeq(gomod, "\n") {
		fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "require "))
		if len(fields) == 0 {
			continue
		}
		for _, m := range gitClientModules {
			if strings.HasPrefix(fields[0], m) {
				out = append(out, fields[0])
			}
		}
	}
	return out
}

func TestGitWriteScannerFlags(t *testing.T) {
	cases := []struct {
		name, rel, src string
		flagged        bool
	}{
		{"git push in the dashboard", "pkg/dashboard/server.go",
			"package dashboard\nimport \"os/exec\"\nfunc f() { _ = exec.Command(\"git\", \"push\") }\n", true},
		{"git literal in the operator", "integrations/kubernetes/internal/delivery/x.go",
			"package delivery\nvar tool = \"git\"\n", true},
		{"go-git in the operator command", "integrations/kubernetes/cmd/delivery/main.go",
			"package main\nimport _ \"github.com/go-git/go-git/v5\"\n", true},
		{"go-github in the remote MCP server", "internal/cli/mcp_remote.go",
			"package cli\nimport _ \"github.com/google/go-github/v70/github\"\n", true},
		{"GitLab client-go in a dashboard command", "internal/cli/dashboard_delivery.go",
			"package cli\nimport _ \"gitlab.com/gitlab-org/api/client-go\"\n", true},
		{"Gitea SDK in the dashboard", "pkg/dashboard/x.go",
			"package dashboard\nimport _ \"code.gitea.io/sdk/gitea\"\n", true},
		{"go-github in a CLI-only package is not scanned", "internal/cli/declare.go",
			"package cli\nimport _ \"github.com/google/go-github/v70/github\"\n", false},
		{"a server test file is not scanned", "pkg/dashboard/x_test.go",
			"package dashboard\nvar tool = \"git\"\n", false},
		{"a clean server file", "pkg/dashboard/x.go",
			"package dashboard\nvar s = \"gitops\"\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := gitWrites(c.rel, []byte(c.src)); (len(got) > 0) != c.flagged {
				t.Errorf("gitWrites(%s) = %v, want flagged=%v", c.rel, got, c.flagged)
			}
		})
	}
	gomod := "module x\n\nrequire (\n\tgithub.com/go-git/go-git/v5 v5.0.0 // indirect\n\tgithub.com/spf13/cobra v1.0.0\n)\nrequire code.gitea.io/sdk/gitea v0.1.0\n"
	if got := goModGitClients(gomod); len(got) != 2 {
		t.Errorf("goModGitClients = %v, want go-git and gitea", got)
	}
	if got := goModGitClients("module x\nrequire github.com/spf13/cobra v1.0.0\n"); len(got) != 0 {
		t.Errorf("goModGitClients = %v, want none", got)
	}
}
