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
// client, with the reason. 513-S8 settled on the hosts' REST APIs over
// net/http in internal/gitexport, so no Git-host client module is linked and
// this stays empty.
var gitHostClientAllowed = map[string]string{}

// isServerPath reports whether a repository-relative Go file belongs to a
// process that would need standing Git write access: the operator and the
// dashboard and remote MCP servers (mcp_declare.go holds the HTTP
// pacto_declare beside the stdio one, whose pull request lives in
// mcp_declare_pr.go).
func isServerPath(rel string) bool {
	base := filepath.Base(rel)
	return strings.HasPrefix(rel, "integrations/kubernetes/cmd/") ||
		strings.HasPrefix(rel, "integrations/kubernetes/internal/") ||
		strings.HasPrefix(rel, "pkg/dashboard/") ||
		rel == "internal/cli/mcp_remote.go" || rel == "internal/cli/mcp_declare.go" ||
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
		{"the credential helper in internal/gitexport is not a server path", "internal/gitexport/token.go",
			"package gitexport\nimport \"os/exec\"\nfunc f() { _ = exec.Command(\"git\", \"credential\", \"fill\") }\n", false},
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

// gitExportImportAllowed reports whether a repository-relative Go file may
// import internal/gitexport: only the CLI's declare command and the stdio
// MCP adapter, which run in the person's own process with the person's own
// Git token.
func gitExportImportAllowed(rel string) bool {
	base := filepath.Base(rel)
	return rel == "internal/cli/mcp_declare_pr.go" ||
		(filepath.Dir(rel) == "internal/cli" && strings.HasPrefix(base, "declare"))
}

// gitExportUses returns why a non-test Go file breaks D7 (513-S8): it
// imports internal/gitexport from a file not allowed to, or it is a server
// path and calls exportPR or the gitexport package (a server file shares
// package cli with exportPR, so an import check alone cannot see a call).
func gitExportUses(rel string, src []byte, module string) []string {
	if strings.HasSuffix(rel, "_test.go") {
		return nil
	}
	f, err := parser.ParseFile(token.NewFileSet(), rel, src, parser.SkipObjectResolution)
	if err != nil {
		return []string{rel + ": " + err.Error()}
	}
	var out []string
	for _, imp := range f.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p == module+"/internal/gitexport" && !gitExportImportAllowed(rel) {
			out = append(out, rel+" imports internal/gitexport")
		}
	}
	if !isServerPath(rel) {
		return out
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Ident:
			if n.Name == "exportPR" {
				out = append(out, rel+" references exportPR")
			}
		case *ast.SelectorExpr:
			if x, ok := n.X.(*ast.Ident); ok && x.Name == "gitexport" {
				out = append(out, rel+" calls gitexport."+n.Sel.Name)
			}
		}
		return true
	})
	return out
}

// TestGitExportOnlyFromLocalSurfaces guards 513 D7: a pull request is
// opened only from the CLI and the stdio MCP server, with the person's own
// token. No server path (the dashboard, the remote MCP server, the
// operator) imports internal/gitexport or calls exportPR.
func TestGitExportOnlyFromLocalSurfaces(t *testing.T) {
	root := repoRoot(t)
	module := strings.TrimSpace(strings.TrimPrefix(strings.SplitN(readRepoFile(t, "go.mod"), "\n", 2)[0], "module"))
	found := 0
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
		if !strings.HasSuffix(rel, ".go") {
			return nil
		}
		src, err := os.ReadFile(path) //nolint:gosec // a path under the repository root
		if err != nil {
			return err
		}
		if strings.Contains(string(src), module+"/internal/gitexport\"") && !strings.HasSuffix(rel, "_test.go") {
			found++
		}
		for _, v := range gitExportUses(rel, src, module) {
			t.Error(v + ": only the CLI and the stdio MCP server open a pull request (513 D7)")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == 0 {
		t.Fatal("no file imports internal/gitexport: the scan read nothing")
	}
}

func TestGitExportScannerFlags(t *testing.T) {
	const module = "github.com/trianalab/pacto/v4"
	imp := "import \"" + module + "/internal/gitexport\"\n"
	cases := []struct {
		name, rel, src string
		flagged        bool
	}{
		{"the dashboard imports it", "pkg/dashboard/x.go", "package dashboard\n" + imp, true},
		{"the stdio and HTTP MCP file imports it", "internal/cli/mcp.go", "package cli\n" + imp, true},
		{"a dashboard command calls exportPR without an import", "internal/cli/dashboard_x.go",
			"package cli\nfunc f() { _ = (&exportPR{}).export }\n", true},
		{"the remote MCP server calls the package", "internal/cli/mcp_remote.go",
			"package cli\nfunc f() { gitexport.Open(nil, gitexport.Request{}) }\n", true},
		{"the HTTP pacto_declare's file calls exportPR", "internal/cli/mcp_declare.go",
			"package cli\nfunc f() { _ = (&exportPR{}).resolve }\n", true},
		{"an unparsable file", "internal/cli/x.go", "package", true},
		{"the declare command", "internal/cli/declare.go", "package cli\n" + imp, false},
		{"the stdio MCP adapter", "internal/cli/mcp_declare_pr.go", "package cli\n" + imp, false},
		{"a test file", "pkg/dashboard/x_test.go", "package dashboard\n" + imp, false},
		{"a CLI file names exportPR", "internal/cli/declare.go", "package cli\ntype exportPR struct{}\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := gitExportUses(c.rel, []byte(c.src), module); (len(got) > 0) != c.flagged {
				t.Errorf("gitExportUses(%s) = %v, want flagged=%v", c.rel, got, c.flagged)
			}
		})
	}
}
