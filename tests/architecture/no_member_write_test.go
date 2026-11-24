package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// clusterClientFiles are the non-test Go files under internal/ and pkg/ that
// build a client for a cluster (they mention clientcmd, rest.Config or
// rest.InClusterConfig), frozen from feat/452-adoption-w7 when member
// delivery (#570) landed. The hub delivers to a member only through signed
// requests in the registry, which the member verifies and authorizes itself;
// a new file here is a new way to reach a cluster and needs a review against
// that rule before it joins the list. An entry whose file is absent from the
// tree is skipped, so the list holds across the wave's story branches.
var clusterClientFiles = []string{
	"internal/app/fleet_contexts.go",
	"internal/app/scope.go",
	"internal/app/service.go",
	"internal/authn/authn.go",
	"internal/authn/mcptoken.go",
	"internal/authn/token.go",
	"internal/cli/cluster_install.go",
	"internal/cli/cluster_reviews.go",
	"internal/cli/dashboard.go",
	"internal/cli/dashboard_clusters.go",
	"internal/cli/dashboard_declare.go",
	"internal/cli/dashboard_delivery.go",
	"internal/cli/dashboard_identity.go",
	"internal/cli/dashboard_readcache.go",
	"internal/cli/mcp_remote.go",
	"internal/cli/root.go",
	"internal/k8sclient/client.go",
}

// memberFrames are the live-channel frame types: two handshake frames and
// the review and answer pair. Neither carries a write.
var memberFrames = []string{"answer", "challenge", "hello", "review"}

var clusterClientPattern = regexp.MustCompile(`clientcmd|rest\.Config|rest\.InClusterConfig`)

// TestHubNeverWritesIntoAMember holds the hub to reading a member: no new
// file builds a cluster client, and the live channel gains no frame.
func TestHubNeverWritesIntoAMember(t *testing.T) {
	root := repoRoot(t)
	if diff := clusterClientDrift(t, root, clusterClientFiles); diff != "" {
		t.Error(diff)
	}
	if got := frameTypes(t, filepath.Join(root, "pkg/memberlink/frames.go")); !slices.Equal(got, memberFrames) {
		t.Errorf("pkg/memberlink/frames.go declares frames %v, want %v: the hub reaches a member only through its registry, never through a new live-channel message", got, memberFrames)
	}
}

// TestHubNeverWritesIntoAMemberFixtures makes each half fail on a fixture: a
// new file that builds a cluster client, and a new frame type.
func TestHubNeverWritesIntoAMemberFixtures(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("internal/cli/known.go", "package cli\n\nvar _ *rest.Config\n")
	write("internal/cli/known_test.go", "package cli\n\nvar _ *rest.Config\n")
	if diff := clusterClientDrift(t, root, []string{"internal/cli/known.go", "internal/cli/gone.go"}); diff != "" {
		t.Fatalf("frozen fixture drifted: %s", diff)
	}
	write("pkg/fleet/member.go", "package fleet\n\nvar _ = clientcmd.NewDefaultClientConfigLoadingRules\n")
	if diff := clusterClientDrift(t, root, []string{"internal/cli/known.go"}); !strings.Contains(diff, "pkg/fleet/member.go") {
		t.Errorf("a new cluster client file was not reported: %q", diff)
	}
	write("pkg/fleet/member.go", "package fleet\n")
	write("internal/cli/known.go", "package cli\n")
	if diff := clusterClientDrift(t, root, []string{"internal/cli/known.go"}); !strings.Contains(diff, "internal/cli/known.go") {
		t.Errorf("a stale entry was not reported: %q", diff)
	}

	frames := filepath.Join(root, "frames.go")
	write("frames.go", "package memberlink\n\nconst (\n\tframeChallenge, frameHello = \"challenge\", \"hello\"\n\tframeReview, frameAnswer = \"review\", \"answer\"\n\tframeApply = \"apply\"\n)\n")
	if got := frameTypes(t, frames); slices.Equal(got, memberFrames) {
		t.Errorf("a new frame was not seen: %v", got)
	}
}

// clusterClientDrift compares the files under root that build a cluster
// client with frozen and describes any difference; empty means none.
func clusterClientDrift(t *testing.T, root string, frozen []string) string {
	t.Helper()
	found := clusterClientFilesUnder(t, root)
	var msgs []string
	for _, f := range found {
		if !slices.Contains(frozen, f) {
			msgs = append(msgs, f+" builds a cluster client and is not in clusterClientFiles: the hub must never write into a member")
		}
	}
	for _, f := range frozen {
		if _, err := os.Stat(filepath.Join(root, f)); err == nil && !slices.Contains(found, f) {
			msgs = append(msgs, f+" no longer builds a cluster client: remove it from clusterClientFiles")
		}
	}
	return strings.Join(msgs, "; ")
}

// clusterClientFilesUnder lists the non-test Go files under root's internal/
// and pkg/ that mention a cluster client.
func clusterClientFilesUnder(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	for _, dir := range []string{"internal", "pkg"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && (d.Name() == "node_modules" || d.Name() == "testdata") {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err == nil && clusterClientPattern.Match(b) {
				rel, _ := filepath.Rel(root, path)
				found = append(found, filepath.ToSlash(rel))
			}
			return err
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	return found
}

// frameTypes returns the sorted values of the frame* constants in file.
func frameTypes(t *testing.T, file string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, decl := range f.Decls {
		g, ok := decl.(*ast.GenDecl)
		if !ok || g.Tok != token.CONST {
			continue
		}
		for _, spec := range g.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, n := range vs.Names {
				if strings.HasPrefix(n.Name, "frame") && i < len(vs.Values) {
					if lit, ok := vs.Values[i].(*ast.BasicLit); ok {
						out = append(out, strings.Trim(lit.Value, `"`))
					}
				}
			}
		}
	}
	slices.Sort(out)
	return out
}
