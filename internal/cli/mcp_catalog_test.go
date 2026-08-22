package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/pkg/catalog"
)

// writeCatalogBundle writes a minimal contract bundle under parent and returns
// its directory.
func writeCatalogBundle(t *testing.T, parent, name string, deps ...string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "pactoVersion: \"2.0\"\nservice:\n  name: %s\n  version: 1.0.0\n  owner:\n    team: platform\n", name)
	if len(deps) > 0 {
		b.WriteString("dependencies:\n")
		for _, d := range deps {
			fmt.Fprintf(&b, "  - name: %s\n    ref: %s\n    required: true\n", filepath.Base(d), d)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// catalogRootsFixture writes two roots that both declare the same shared
// bundle, so the CLI wiring is exercised on more than one root and on a
// revision reached from both of them.
func catalogRootsFixture(t *testing.T) (parent, rootA, rootB string) {
	t.Helper()
	parent = t.TempDir()
	writeCatalogBundle(t, parent, "shared")
	rootA = writeCatalogBundle(t, parent, "alpha", "../shared")
	rootB = writeCatalogBundle(t, parent, "beta", "../shared")
	return parent, rootA, rootB
}

// buildCatalogMCPServer runs the real flag plumbing: it sets --root exactly as
// a repeated command-line flag would and asks the command to build its server.
func buildCatalogMCPServer(t *testing.T, ctx context.Context, roots ...string) (*mcpsdk.Server, error) {
	t.Helper()
	svc := app.NewService(nil, nil)
	cmd := newMCPCommand(svc, "v")
	cmd.SetContext(ctx)
	for _, r := range roots {
		if err := cmd.Flags().Set("root", r); err != nil {
			t.Fatalf("set --root %q: %v", r, err)
		}
	}
	return buildMCPServer(cmd, svc, "v", nil)
}

// catalogOverviewFrom reads pacto://catalog off a built server over a real MCP
// session, so the CLI test proves what a client would actually receive.
func catalogOverviewFrom(t *testing.T, server *mcpsdk.Server) struct {
	Meta  catalog.Meta   `json:"meta"`
	Roots []catalog.Root `json:"roots"`
} {
	t.Helper()
	var got struct {
		Meta  catalog.Meta   `json:"meta"`
		Roots []catalog.Root `json:"roots"`
	}
	ctx := context.Background()
	st, ct := mcpsdk.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "cli-test", Version: "1.0"}, nil)
	session, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	res, err := session.ReadResource(ctx, &mcpsdk.ReadResourceParams{URI: "pacto://catalog"})
	if err != nil {
		t.Fatalf("ReadResource(pacto://catalog): %v", err)
	}
	if len(res.Contents) != 1 {
		t.Fatalf("got %d contents, want 1", len(res.Contents))
	}
	if err := json.Unmarshal([]byte(res.Contents[0].Text), &got); err != nil {
		t.Fatalf("decoding the catalog: %v", err)
	}
	return got
}

func TestMCPCatalogMode_RepeatedRootsReachTheCatalogBuilder(t *testing.T) {
	_, rootA, rootB := catalogRootsFixture(t)
	server, err := buildCatalogMCPServer(t, context.Background(), rootA, rootB)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	got := catalogOverviewFrom(t, server)

	if got.Meta.RequestedRoots != 2 || len(got.Roots) != 2 {
		t.Fatalf("requestedRoots = %d with %d roots, want both --root values", got.Meta.RequestedRoots, len(got.Roots))
	}
	if got.Roots[0].RequestedRef != rootA || got.Roots[1].RequestedRef != rootB {
		t.Errorf("requested refs = %q, %q; want %q, %q in flag order",
			got.Roots[0].RequestedRef, got.Roots[1].RequestedRef, rootA, rootB)
	}
	for _, r := range got.Roots {
		if !r.Resolved {
			t.Fatalf("root %s did not resolve: %+v", r.RequestedRef, r.Reason)
		}
		// The local adapter is what produced this: a content hash, not a path.
		if r.Revision.Content.Scheme != catalog.SchemeLocal || r.Revision.Content.Digest == "" {
			t.Errorf("root %s identity = %+v, want a local content identity", r.RequestedRef, r.Revision.Content)
		}
	}
	// Both roots declare the same bundle, so the closure resolved it once and
	// the whole answer is complete.
	if got.Meta.Completeness != catalog.CompletenessComplete {
		t.Errorf("completeness = %q with limitations %+v, want complete", got.Meta.Completeness, got.Meta.Limitations)
	}
}

func TestMCPCatalogMode_ResolvesOnceBeforeServing(t *testing.T) {
	parent, rootA, _ := catalogRootsFixture(t)
	server, err := buildCatalogMCPServer(t, context.Background(), rootA)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	// Everything the catalog was built from is gone. A session that still had to
	// reach the filesystem could not answer; a frozen one answers unchanged.
	if err := os.RemoveAll(parent); err != nil {
		t.Fatal(err)
	}
	got := catalogOverviewFrom(t, server)
	if len(got.Roots) != 1 || !got.Roots[0].Resolved {
		t.Fatalf("roots = %+v, want the root still resolved after its bundle was deleted", got.Roots)
	}
	if got.Meta.Completeness != catalog.CompletenessComplete {
		t.Errorf("completeness = %q, want the session frozen as complete", got.Meta.Completeness)
	}
}

func TestMCPCatalogMode_EmptyRootFailsClosed(t *testing.T) {
	for _, root := range []string{"", "   "} {
		_, err := buildCatalogMCPServer(t, context.Background(), root)
		if err == nil {
			t.Fatalf("--root %q built a server, want a clear failure rather than an empty catalog", root)
		}
		if !strings.Contains(err.Error(), "--root") {
			t.Errorf("error = %q, want it to name --root", err)
		}
	}
}

func TestMCPCatalogMode_RegistryRootKeepsItsSanitizedReason(t *testing.T) {
	// No registry client is configured, so the registry root cannot resolve. It
	// stays visible as partial knowledge with a category, never a transport
	// error and never a silent drop.
	_, rootA, _ := catalogRootsFixture(t)
	server, err := buildCatalogMCPServer(t, context.Background(), rootA, "oci://ghcr.io/acme/platform:1.0.0")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	got := catalogOverviewFrom(t, server)

	if got.Meta.Completeness != catalog.CompletenessPartial {
		t.Errorf("completeness = %q, want partial", got.Meta.Completeness)
	}
	if len(got.Roots) != 2 {
		t.Fatalf("got %d roots, want both", len(got.Roots))
	}
	remote := got.Roots[1]
	if remote.Resolved || remote.RequestedRef != "oci://ghcr.io/acme/platform:1.0.0" {
		t.Fatalf("registry root = %+v, want it reported unresolved with its reference", remote)
	}
	if remote.Reason.Code != catalog.ReasonUnavailable {
		t.Errorf("reason = %+v, want a classified %s", remote.Reason, catalog.ReasonUnavailable)
	}
	if !slices.ContainsFunc(got.Meta.Limitations, func(l catalog.Limitation) bool {
		return l.Code == catalog.LimitationRootUnresolved
	}) {
		t.Errorf("limitations = %+v, want ROOT_UNRESOLVED", got.Meta.Limitations)
	}
}

func TestMCPCatalogMode_CancelledStartupStaysPartial(t *testing.T) {
	_, rootA, _ := catalogRootsFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server, err := buildCatalogMCPServer(t, ctx, rootA)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	got := catalogOverviewFrom(t, server)
	if got.Meta.Completeness != catalog.CompletenessPartial {
		t.Errorf("completeness = %q, want partial after a cancelled startup", got.Meta.Completeness)
	}
	if !slices.ContainsFunc(got.Meta.Limitations, func(l catalog.Limitation) bool {
		return l.Code == catalog.LimitationCancelled
	}) {
		t.Errorf("limitations = %+v, want CANCELLED", got.Meta.Limitations)
	}
}

func TestMCPModesAreMutuallyExclusive(t *testing.T) {
	_, rootA, _ := catalogRootsFixture(t)
	bundle := writeCapabilityBundle(t)

	cases := map[string]struct {
		args []string
		want []string
	}{
		"catalog and capability": {
			args: []string{"mcp", "--root", rootA, bundle},
			want: []string{"a bundle reference", "--root"},
		},
		"catalog and fleet": {
			args: []string{"mcp", "--root", rootA, "--fleet"},
			want: []string{"--root", "--fleet"},
		},
		"capability and fleet": {
			args: []string{"mcp", "--fleet", bundle},
			want: []string{"a bundle reference", "--fleet"},
		},
		"all three": {
			args: []string{"mcp", "--root", rootA, "--fleet", bundle},
			want: []string{"a bundle reference", "--root", "--fleet"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			root := NewRootCommand(app.NewService(nil, nil), VersionInfo{Version: "test"})
			root.SetArgs(tc.args)
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)

			err := root.Execute()
			if err == nil {
				t.Fatalf("%v started a server, want an explicit error rather than a silent choice", tc.args)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q must name %q", err, want)
				}
			}
		})
	}
}

func TestMCPCommand_RootFlagIsRepeatable(t *testing.T) {
	cmd := newMCPCommand(app.NewService(nil, nil), "v")
	f := cmd.Flags().Lookup("root")
	if f == nil {
		t.Fatal("expected a --root flag")
	}
	if f.Value.Type() != "stringArray" {
		t.Errorf("--root type = %s, want stringArray so it repeats", f.Value.Type())
	}
	if f.DefValue != "[]" {
		t.Errorf("--root default = %s, want no roots: absent --root is not catalog mode", f.DefValue)
	}
}

func TestBuildMCPServer_ExistingModesStillBuild(t *testing.T) {
	svc := app.NewService(nil, nil)

	authoring := func() *mcpsdk.Server {
		cmd := newMCPCommand(svc, "v")
		cmd.SetContext(context.Background())
		s, err := buildMCPServer(cmd, svc, "v", nil)
		if err != nil {
			t.Fatalf("authoring build: %v", err)
		}
		return s
	}()
	fleet := func() *mcpsdk.Server {
		cmd := newMCPCommand(svc, "v")
		cmd.SetContext(context.Background())
		_ = cmd.Flags().Set("fleet", "true")
		_ = cmd.Flags().Set("local", t.TempDir())
		s, err := buildMCPServer(cmd, svc, "v", nil)
		if err != nil {
			t.Fatalf("fleet build: %v", err)
		}
		return s
	}()
	for name, server := range map[string]*mcpsdk.Server{"authoring": authoring, "fleet": fleet} {
		if server == nil {
			t.Errorf("%s mode returned no server", name)
		}
	}
}
