//go:build deadcodegate

// This gate is being written while the audit-remediation clusters are still
// landing, and it is red until every one of them has. The build tag keeps it
// out of `go test ./tests/architecture/...` in the meantime, so a shared tree
// does not hand every other agent a failure that is not theirs. Remove the tag
// once the last cluster lands and the allowlist below describes the final tree.

package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Theme 5 of the 2026-09-10 architecture audit: the 100% coverage gate keeps
// dead code alive and makes it look used. A symbol whose only callers are
// _test.go files is covered, so the gate is satisfied, and every reader who
// greps for callers finds some. The audit found roughly twenty of them.
//
// This is the tripwire for the next one. It reports every production symbol in
// the v3 module whose only references outside its own declaration live in test
// files.
//
// Matching is by NAME, not by resolved identity, because golang.org/x/tools is
// not a dependency of this module and `go mod tidy` is forbidden here, so
// go/packages is unavailable and go/parser is what remains. That is imprecise
// in exactly one direction: a production symbol sharing a name with anything
// else used in production reads as used, so the gate under-reports. The
// direction matters more than the precision -- a red run is always a real
// finding, and a green run is the weaker claim.
//
// A live example of that under-report, so the limitation is not theoretical:
// pkg/otelobserver.Observe has no caller, but the operator calls a method also
// named Observe on its own runtime observer, and the two are indistinguishable
// here. Resolving them apart needs the import graph, and a first pass at that
// showed why it is not worth it: qualifying non-method uses is easy, but a
// method use is a selector on a value whose type only a type-checker knows, so
// every method in the repo would read as dead.

// deadCodeAllowlist names symbols the gate would otherwise report, each with
// the reason it is allowed to have no production caller. A stale entry fails
// the test as loudly as a missing one: an exemption that has stopped applying
// is how the next real instance hides.
var deadCodeAllowlist = map[string]string{
	// The rest is filled in when the tag comes off. Every entry is
	// "<pkg>.<Symbol>": reason.

	"pkg/contract.PolicyTargetContract":     schemaVocabulary,
	"pkg/contract.CategoryArchitecture":     schemaVocabulary,
	"pkg/contract.CategoryBackupRecovery":   schemaVocabulary,
	"pkg/contract.CategoryCICD":             schemaVocabulary,
	"pkg/contract.CategoryCodeQuality":      schemaVocabulary,
	"pkg/contract.CategoryCompliance":       schemaVocabulary,
	"pkg/contract.CategoryDeployment":       schemaVocabulary,
	"pkg/contract.CategoryDocumentation":    schemaVocabulary,
	"pkg/contract.CategoryIncidentResponse": schemaVocabulary,
	"pkg/contract.CategoryInfrastructure":   schemaVocabulary,
	"pkg/contract.CategoryObservability":    schemaVocabulary,
	"pkg/contract.CategoryOther":            schemaVocabulary,
	"pkg/contract.CategoryResilience":       schemaVocabulary,
	"pkg/contract.CategorySecurity":         schemaVocabulary,
	"pkg/contract.CategoryTesting":          schemaVocabulary,
	"pkg/contract.EvidenceTypeArtifact":     schemaVocabulary,
	"pkg/contract.EvidenceTypeDocument":     schemaVocabulary,
	"pkg/contract.EvidenceTypeIdentifier":   schemaVocabulary,
	"pkg/contract.EvidenceTypeOther":        schemaVocabulary,
	"pkg/contract.EvidenceTypeReport":       schemaVocabulary,
	"pkg/contract.EvidenceTypeTicket":       schemaVocabulary,
	"pkg/contract.EvidenceTypeURL":          schemaVocabulary,
}

// schemaVocabulary is the reason a whole const block can be live with no Go
// caller at all. The values a bundle author may write are declared twice --
// as these constants and as an "enum" in the JSON Schema pkg/validation embeds
// -- and only the schema copy is enforced. So the block's readers are bundles,
// not Go code, and pkg/contract's TestVocabularyParityWithSchema is what keeps
// the two copies from drifting. Deleting a member here would leave the schema
// still accepting a value Pacto no longer names.
const schemaVocabulary = "bundle vocabulary mirrored by a JSON Schema enum; pinned by pkg/contract.TestVocabularyParityWithSchema"

// runtimeDispatched are methods the standard library calls through an
// interface it discovers by reflection, never by name. errors.Is walks Unwrap,
// fmt prints through Error and String, encoding/json consults MarshalJSON.
// Nothing in this repo will ever name them at a call site, so "no production
// caller" is their normal state rather than evidence of death.
var runtimeDispatched = map[string]bool{
	"Error": true, "Unwrap": true, "Is": true, "As": true, "String": true,
	"MarshalJSON": true, "UnmarshalJSON": true,
	"MarshalYAML": true, "UnmarshalYAML": true,
}

type decl struct {
	pkgDir string
	name   string
	pos    token.Position
	doc    string
	method bool
	// group identifies the parenthesized const block this symbol was declared
	// in, or "" for everything else. See the enum rule in the test.
	group string
}

func (d decl) key() string { return d.pkgDir + "." + d.name }

func TestNoProductionSymbolIsReachableOnlyFromTests(t *testing.T) {
	root := repoDir(t)
	fset := token.NewFileSet()

	var decls []decl
	prodUses := map[string]bool{}
	testUses := map[string]bool{}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipTree(root, path) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if perr != nil {
			return perr
		}
		if isGenerated(f) {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		pkgDir := filepath.ToSlash(filepath.Dir(rel))
		isTestFile := strings.HasSuffix(path, "_test.go")

		// Uses are collected from the whole tree, because a call is a call
		// wherever it is written: examples/ ships as demo programs and
		// integrations/ is a second module that imports pkg/, so deleting a
		// symbol either one calls breaks a real build. Only some of the tree
		// counts as PRODUCTION use, and only some of it is the gate's to
		// police -- those are separate questions with separate answers.
		into := prodUses
		if isTestFile || isTestSupport(pkgDir) {
			into = testUses
		}
		collectUses(f, into)
		if !isTestFile && policedDecls(pkgDir) {
			decls = append(decls, declaredIn(f, fset, pkgDir)...)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	// An enum is used as a set. A const block whose members spell the legal
	// values of one field stays whole even when Pacto itself only ever
	// branches on some of them: contract.StatusDeferred is a value users write
	// in a bundle, and deleting it because no Go code compares against it
	// would amputate the format's own vocabulary. So one live member keeps the
	// block alive.
	//
	// A block with no live member at all is still reported. That is usually a
	// vocabulary nothing speaks -- but not always, because a block whose only
	// readers are bundles has no Go caller by construction. Those are named in
	// deadCodeAllowlist under schemaVocabulary, where the exemption states
	// which test pins them to the schema instead.
	liveGroup := map[string]bool{}
	for _, d := range decls {
		if d.group != "" && prodUses[d.name] {
			liveGroup[d.group] = true
		}
	}

	reported := map[string]string{}
	for _, d := range decls {
		switch {
		case prodUses[d.name], !testUses[d.name]:
			continue
		case d.method && runtimeDispatched[d.name]:
			continue
		case d.group != "" && liveGroup[d.group]:
			continue
		// An exported symbol under pkg/ carrying a Deprecated: marker is
		// public API of a released major this branch has promised not to
		// break, so no in-repo caller is its expected state. Unexported
		// symbols get no such pass: nothing outside the package can call one,
		// so a deprecated unexported symbol is simply dead.
		case strings.HasPrefix(d.pkgDir, "pkg/") && ast.IsExported(d.name) && strings.Contains(d.doc, "Deprecated:"):
			continue
		}
		if _, dup := reported[d.key()]; !dup {
			reported[d.key()] = d.pos.String()
		}
	}

	var dead []string
	for key, pos := range reported {
		if deadCodeAllowlist[key] != "" {
			continue
		}
		dead = append(dead, key+"\n\t\tdeclared at "+pos+", referenced only from _test.go")
	}
	sort.Strings(dead)

	if len(dead) > 0 {
		t.Errorf("%d production symbol(s) reachable only from tests.\n"+
			"Each is dead code to delete, or public API of the released v3 major\n"+
			"that needs a Deprecated: marker, or an exemption that belongs in\n"+
			"deadCodeAllowlist with its reason:\n\t%s",
			len(dead), strings.Join(dead, "\n\t"))
	}

	for key, reason := range deadCodeAllowlist {
		if _, still := reported[key]; !still {
			t.Errorf("deadCodeAllowlist exempts %q (%s), which the gate no longer reports; delete the entry", key, reason)
		}
	}
}

// declaredIn returns every top-level symbol f declares: functions, methods,
// types, constants and variables. Methods count -- a method whose only caller
// is a test is exactly as dead as a function.
func declaredIn(f *ast.File, fset *token.FileSet, pkgDir string) []decl {
	var out []decl
	for _, d := range f.Decls {
		switch n := d.(type) {
		case *ast.FuncDecl:
			// main and init are entry points; nothing calls them by name.
			if n.Name.Name == "main" || n.Name.Name == "init" {
				continue
			}
			out = append(out, decl{
				pkgDir: pkgDir,
				name:   n.Name.Name,
				pos:    fset.Position(n.Pos()),
				doc:    docText(n.Doc),
				method: n.Recv != nil,
			})
		case *ast.GenDecl:
			if n.Tok != token.TYPE && n.Tok != token.CONST && n.Tok != token.VAR {
				continue
			}
			// Only a parenthesized const block forms an enum group; a lone
			// `const X = ...` is its own symbol and answers for itself.
			group := ""
			if n.Tok == token.CONST && n.Lparen.IsValid() && len(n.Specs) > 1 {
				group = fset.Position(n.Pos()).String()
			}
			for _, s := range n.Specs {
				switch sp := s.(type) {
				case *ast.TypeSpec:
					out = append(out, decl{pkgDir, sp.Name.Name, fset.Position(sp.Pos()), docText(n.Doc) + docText(sp.Doc), false, ""})
				case *ast.ValueSpec:
					for _, id := range sp.Names {
						if id.Name == "_" {
							continue
						}
						out = append(out, decl{pkgDir, id.Name, fset.Position(id.Pos()), docText(n.Doc) + docText(sp.Doc), false, group})
					}
				}
			}
		}
	}
	return out
}

// collectUses records every identifier f reads. A declaration's own name is
// skipped, so declaring a symbol never counts as using it.
func collectUses(f *ast.File, into map[string]bool) {
	declPos := map[token.Pos]bool{}
	for _, d := range f.Decls {
		switch n := d.(type) {
		case *ast.FuncDecl:
			declPos[n.Name.Pos()] = true
		case *ast.GenDecl:
			for _, s := range n.Specs {
				switch sp := s.(type) {
				case *ast.TypeSpec:
					declPos[sp.Name.Pos()] = true
				case *ast.ValueSpec:
					for _, id := range sp.Names {
						declPos[id.Pos()] = true
					}
				}
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch e := n.(type) {
		case *ast.Ident:
			if !declPos[e.Pos()] {
				into[e.Name] = true
			}
		case *ast.SelectorExpr:
			into[e.Sel.Name] = true
		}
		return true
	})
}

func docText(g *ast.CommentGroup) string {
	if g == nil {
		return ""
	}
	return g.Text()
}

func isGenerated(f *ast.File) bool {
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if strings.Contains(c.Text, "Code generated") && strings.Contains(c.Text, "DO NOT EDIT") {
				return true
			}
		}
	}
	return false
}

// skipTree excludes directories that are not this repo's own source, so nothing
// in them is read as either a declaration or a use. Dot-prefixed trees matter
// most: .claude/worktrees holds whole stale checkouts of this repo, and every
// call in one would otherwise read as a live caller of code nothing calls
// anymore.
func skipTree(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return true
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		return false
	}
	if strings.HasPrefix(filepath.Base(rel), ".") {
		return true
	}
	return rel == "node_modules" || rel == "vendor" ||
		strings.Contains(rel, "/node_modules") ||
		strings.Contains(rel, "/frontend/")
}

// isTestSupport reports whether a package exists only to serve tests. Its files
// are not named _test.go, but nothing ships them, so a symbol they alone reach
// is exactly as dead as one reached only from a _test.go file.
func isTestSupport(pkgDir string) bool {
	return pkgDir == "tests" || strings.HasPrefix(pkgDir, "tests/") ||
		pkgDir == "testutil" || strings.HasSuffix(pkgDir, "/testutil")
}

// policedDecls reports whether the gate answers for dead code in a package. It
// does not answer for the other Go module, for the demo programs the coverage
// gate also excludes, or for test support: each may hold whatever it needs, and
// what each calls still counts as real use.
func policedDecls(pkgDir string) bool {
	if isTestSupport(pkgDir) {
		return false
	}
	for _, ex := range [...]string{"integrations", "examples"} {
		if pkgDir == ex || strings.HasPrefix(pkgDir, ex+"/") {
			return false
		}
	}
	return true
}
