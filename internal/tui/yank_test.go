package tui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

func TestYankProducesARunnableInvocation(t *testing.T) {
	c := newLoadedContext(t)
	sel := Selection{Kind: fleet.KindRevision, Key: "svc@1.0.0", Label: "svc 1.0.0", Ref: "./svc"}
	got := yankLine(c, sel)
	if !strings.HasPrefix(got, "pacto ") {
		t.Fatalf("yank = %q, want it to start with pacto", got)
	}
	if !strings.Contains(got, "./svc") {
		t.Fatalf("yank = %q, want it to name the selection", got)
	}
}

// TestYankForAnEntityWithNoBundleNamesTheSubjectTheWayTheCommandDoes covers
// every kind that can reach the fallback. None of fleet get, graph or explain
// takes a kind and a key as two positionals — that line fails on arity before it
// reaches the fleet — so each kind gets the form its command actually accepts.
func TestYankForAnEntityWithNoBundleNamesTheSubjectTheWayTheCommandDoes(t *testing.T) {
	c := newLoadedContext(t)
	for _, tt := range []struct {
		name string
		sel  Selection
		want string
	}{
		{
			"an owner filters a search by its value, not its namespaced key",
			Selection{Kind: fleet.KindOwner, Key: "team:platform", Label: "platform"},
			"pacto fleet search --owner platform",
		},
		{
			"a source filters a search",
			Selection{Kind: fleet.KindSource, Key: "local", Label: "local"},
			"pacto fleet search --source local",
		},
		{
			"a target is a flag on fleet get",
			Selection{Kind: fleet.KindTarget, Key: "prod/Deployment/svc", Label: "svc"},
			"pacto fleet get --target prod/Deployment/svc",
		},
		{
			"a revision is a flag on fleet graph, the only one of the three that takes one",
			Selection{Kind: fleet.KindRevision, Key: "svc@1.0.0", Label: "svc 1.0.0"},
			"pacto fleet graph --revision svc@1.0.0",
		},
		{
			"a service is the positional every fleet command was written for",
			Selection{Kind: fleet.KindService, Key: "svc", Label: "svc"},
			"pacto fleet get svc",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := yankLine(c, tt.sel); got != tt.want {
				t.Fatalf("yank = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestYankCarriesTheSessionSourceFlags is the reason y exists: the line has to
// answer over the fleet on screen. Without the flags, a session launched with
// anything but the default source yanks a query that rebuilds a different
// snapshot and reports "not found" for the very thing it names.
func TestYankCarriesTheSessionSourceFlags(t *testing.T) {
	c := newLoadedContext(t)
	c.SourceArgs = []string{"--k8s=true", "--local=a", "--local=b"}
	for _, tt := range []struct {
		name string
		sel  Selection
		want string
	}{
		{
			"a service",
			Selection{Kind: fleet.KindService, Key: "svc", Label: "svc"},
			"pacto fleet get svc --k8s=true --local=a --local=b",
		},
		{
			"a revision",
			Selection{Kind: fleet.KindRevision, Key: "svc@1.0.0", Label: "svc"},
			"pacto fleet graph --revision svc@1.0.0 --k8s=true --local=a --local=b",
		},
		{
			"a target",
			Selection{Kind: fleet.KindTarget, Key: "prod/Deployment/svc", Label: "svc"},
			"pacto fleet get --target prod/Deployment/svc --k8s=true --local=a --local=b",
		},
		{
			"an owner",
			Selection{Kind: fleet.KindOwner, Key: "team:platform", Label: "platform"},
			"pacto fleet search --owner platform --k8s=true --local=a --local=b",
		},
		{
			"a source",
			Selection{Kind: fleet.KindSource, Key: "local", Label: "local"},
			"pacto fleet search --source local --k8s=true --local=a --local=b",
		},
		{
			// validate reads one bundle off disk and parses no source flag, so this
			// is the one line they must stay off.
			"a bundle-backed selection stays a plain validate",
			Selection{Kind: fleet.KindRevision, Key: "svc@1.0.0", Label: "svc", Ref: "./svc"},
			"pacto validate ./svc",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := yankLine(c, tt.sel); got != tt.want {
				t.Fatalf("yank = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestYankDoesNotShareTheSourceArgsBacking guards the aliasing bug the append
// invites: two yanks in one session must not let the first line's tail be
// overwritten by the second.
func TestYankDoesNotShareTheSourceArgsBacking(t *testing.T) {
	c := newLoadedContext(t)
	c.SourceArgs = []string{"--k8s=true"}
	first := yankArgv(c, Selection{Kind: fleet.KindService, Key: "one"})
	yankArgv(c, Selection{Kind: fleet.KindService, Key: "two"})
	if got := strings.Join(first, " "); got != "pacto fleet get one --k8s=true" {
		t.Fatalf("the first yanked line became %q", got)
	}
}

func TestYankFallsBackToShowingTheLineWhenThereIsNoClipboard(t *testing.T) {
	orig := clipboardWrite
	t.Cleanup(func() { clipboardWrite = orig })
	clipboardWrite = nil

	c := newLoadedContext(t)
	l := newListScreen(c)
	cmd, handled := dispatchVerb(c, l, tea.KeyPressMsg{Code: 'y', Text: "y"})
	if !handled {
		t.Fatal("y was not handled")
	}
	msg, ok := cmd().(statusMsg)
	if !ok {
		t.Fatalf("got %T, want a statusMsg showing the line", cmd())
	}
	if !strings.Contains(msg.text, "pacto ") {
		t.Fatalf("status = %q, want the invocation", msg.text)
	}
}

func TestYankReportsAClipboardFailure(t *testing.T) {
	orig := clipboardWrite
	t.Cleanup(func() { clipboardWrite = orig })
	clipboardWrite = func(string) error { return errBoom }

	c := newLoadedContext(t)
	cmd, _ := dispatchVerb(c, newListScreen(c), tea.KeyPressMsg{Code: 'y', Text: "y"})
	if msg := cmd().(statusMsg); !strings.Contains(msg.text, "boom") {
		t.Fatalf("a clipboard failure was swallowed: %q", msg.text)
	}
}

func TestRefreshReloadsTheSnapshot(t *testing.T) {
	m := New(testOptions())
	cmd, handled := globalKey(m, tea.KeyPressMsg{Code: 'r', Text: "r"})
	if !handled || cmd == nil {
		t.Fatal("r did not trigger a reload")
	}
	// Asserting on the message rather than on cmd != nil: any command at all
	// satisfies the latter, so r could be bound to a status line and this would
	// still pass.
	if _, ok := cmd().(snapshotMsg); !ok {
		t.Fatalf("r produced %T, want a snapshotMsg", cmd())
	}
}

func TestYankArgvWithBundle(t *testing.T) {
	c := newLoadedContext(t)
	sel := Selection{Ref: "./svc", Label: "svc"}
	argv := yankArgv(c, sel)
	if len(argv) != 3 || argv[0] != "pacto" || argv[1] != "validate" || argv[2] != "./svc" {
		t.Fatalf("yankArgv = %v, want [pacto validate ./svc]", argv)
	}
}

func TestYankArgvWithoutBundle(t *testing.T) {
	c := newLoadedContext(t)
	sel := Selection{Kind: fleet.KindOwner, Key: "team:x", Label: "x"}
	argv := yankArgv(c, sel)
	want := []string{"pacto", "fleet", "search", "--owner", "x"}
	if !slices.Equal(argv, want) {
		t.Fatalf("yankArgv = %v, want %v", argv, want)
	}
}

func TestVerbYankWithClipboard(t *testing.T) {
	orig := clipboardWrite
	t.Cleanup(func() { clipboardWrite = orig })
	var copied string
	clipboardWrite = func(s string) error {
		copied = s
		return nil
	}

	c := newLoadedContext(t)
	sel := Selection{Ref: "./svc", Label: "svc"}
	cmd := verbYank(c, sel)
	msg := cmd().(statusMsg)
	if !strings.Contains(msg.text, "copied") {
		t.Fatalf("status = %q, want it to say copied", msg.text)
	}
	if copied != "pacto validate ./svc" {
		t.Fatalf("copied = %q, want pacto validate ./svc", copied)
	}
}

func TestReadOnlyRejectsAWriteEvenIfOneIsDispatched(t *testing.T) {
	// Defence in depth: verbList already hides write verbs in read-only mode,
	// but runWrite is the single choke point and must refuse independently.
	c := newLoadedContext(t)
	c.ReadOnly = true
	cmd := runWrite(c, "Push?", []string{"pacto", "push", "./svc"})
	if _, ok := cmd().(pushMsg); ok {
		t.Fatal("runWrite opened a confirmation in read-only mode")
	}
	if msg, ok := cmd().(statusMsg); !ok || !strings.Contains(msg.text, "read-only") {
		t.Fatalf("got %T, want a statusMsg naming read-only mode", cmd())
	}
}
