package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

func TestLoadSnapshotProducesACommand(t *testing.T) {
	svc := app.NewService(nil, nil)
	c := &Context{
		Svc:   svc,
		Fleet: app.FleetOptions{},
		Send:  &sender{},
	}
	cmd := loadSnapshot(c)
	if cmd == nil {
		t.Fatal("loadSnapshot returned nil command")
	}
	// Calling the command produces a snapshotMsg
	msg := cmd()
	if _, ok := msg.(snapshotMsg); !ok {
		t.Fatalf("command produced %T, want snapshotMsg", msg)
	}
}

func TestLoadSnapshotWithCancelledContext(t *testing.T) {
	// Save and restore the test seam
	orig := testCtx
	defer func() { testCtx = orig }()

	// Inject a cancelled context to trigger an error
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	testCtx = func() context.Context { return ctx }

	svc := app.NewService(nil, nil)
	c := &Context{
		Svc:   svc,
		Fleet: app.FleetOptions{LocalRoots: []string{t.TempDir()}},
		Send:  &sender{},
	}
	got := loadSnapshot(c)()
	msg, ok := got.(snapshotMsg)
	if !ok {
		t.Fatalf("got %T, want snapshotMsg", got)
	}
	if msg.err == nil {
		t.Fatal("want an error for cancelled context")
	}
}

func TestSnapshotMsgInstallsTheQueryAndSwapsTheScreen(t *testing.T) {
	m := New(testOptions())
	next, _ := m.Update(snapshotMsg{snap: testSnapshot(t)})
	got := next.(*Model)
	if got.ctx.Query == nil {
		t.Fatal("Query was not installed on the context")
	}
	if got.top().Title() == "Loading" {
		t.Fatal("still on the loading screen after the snapshot arrived")
	}
	if len(got.stack) != 1 {
		t.Fatalf("stack depth = %d, want 1 — the loading screen must be replaced, not pushed over", len(got.stack))
	}
}

func TestSnapshotMsgWithAnErrorSurfacesItAndStops(t *testing.T) {
	m := New(testOptions())
	next, _ := m.Update(snapshotMsg{err: errBoom})
	got := next.(*Model)
	if got.err == nil {
		t.Fatal("the load error was swallowed")
	}
	if got.ctx.Query != nil {
		t.Fatal("a failed load must not install a query")
	}
}

func TestDepResolvedUpdatesTheLoadingScreen(t *testing.T) {
	m := New(testOptions())
	next, _ := m.Update(depResolvedMsg{})
	if !strings.Contains(next.View().Content, "resolved") {
		t.Fatalf("loading screen did not react to depResolvedMsg:\n%s", next.View().Content)
	}
}

func TestLoadedScreenTitle(t *testing.T) {
	ls := loadedScreen{n: 5}
	if got := ls.Title(); got != "Services" {
		t.Fatalf("Title() = %q, want %q", got, "Services")
	}
}

func TestLoadedScreenView(t *testing.T) {
	ls := loadedScreen{n: 42}
	view := ls.View(&Context{})
	if !strings.Contains(view, "42") {
		t.Fatalf("View() = %q, want it to contain entity count", view)
	}
	if !strings.Contains(view, "entities") {
		t.Fatalf("View() = %q, want it to contain 'entities'", view)
	}
}

func TestLoadedScreenUpdate(t *testing.T) {
	ls := loadedScreen{n: 5}
	c := &Context{}
	next, cmd := ls.Update(c, statusMsg{text: "test"})
	if next != ls {
		t.Fatal("Update should return the receiver unchanged")
	}
	if cmd != nil {
		t.Fatal("Update should return nil command")
	}
}

func TestNewListScreen(t *testing.T) {
	snap := testSnapshot(t)
	q := fleet.NewQuery(snap)
	c := &Context{Query: q}
	s := newListScreen(c)
	if s.Title() != "Services" {
		t.Fatal("newListScreen should return a screen with title 'Services'")
	}
}

func TestLoadSnapshotSuccess(t *testing.T) {
	root := t.TempDir()
	bundleDir := filepath.Join(root, "test-svc")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pactoYAML := "pactoVersion: \"2.0\"\nservice:\n  name: test-svc\n  version: \"1.0.0\"\n"
	if err := os.WriteFile(filepath.Join(bundleDir, "pacto.yaml"), []byte(pactoYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := app.NewService(nil, nil)
	c := &Context{
		Svc:   svc,
		Fleet: app.FleetOptions{LocalRoots: []string{root}},
		Send:  &sender{},
	}
	got := loadSnapshot(c)()
	msg, ok := got.(snapshotMsg)
	if !ok {
		t.Fatalf("got %T, want snapshotMsg", got)
	}
	if msg.err != nil {
		t.Fatalf("unexpected error: %v", msg.err)
	}
	if msg.snap == nil {
		t.Fatal("want a snapshot")
	}
}

// testSnapshot creates a minimal fleet snapshot for testing.
func testSnapshot(t *testing.T) *fleet.FleetSnapshot {
	t.Helper()
	root := t.TempDir()
	bundleDir := filepath.Join(root, "test-svc")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pactoYAML := "pactoVersion: \"2.0\"\nservice:\n  name: test-svc\n  version: \"1.0.0\"\n"
	if err := os.WriteFile(filepath.Join(bundleDir, "pacto.yaml"), []byte(pactoYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := app.NewService(nil, nil)
	snap, err := svc.Fleet(context.Background(), app.FleetOptions{LocalRoots: []string{root}})
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	return snap
}
