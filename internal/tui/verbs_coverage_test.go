package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// msgRecorder is a test msgSink that records all messages sent.
type msgRecorder struct {
	msgs []tea.Msg
}

func (r *msgRecorder) Send(m tea.Msg) {
	r.msgs = append(r.msgs, m)
}

// writeBundle writes a minimal bundle to dir with the given extra YAML.
func writeBundle(t *testing.T, dir, name, version, extra string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	y := "pactoVersion: \"2.0\"\nservice:\n  name: " + name + "\n  version: \"" + version + "\"\n" + extra
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// newContextWithService builds a Context with a real service over bundles on disk.
func newContextWithService(t *testing.T) (*Context, string) {
	t.Helper()
	root := t.TempDir()
	bundleDir := filepath.Join(root, "test-svc")
	writeBundle(t, bundleDir, "test-svc", "1.0.0", "")

	svc := app.NewService(nil, nil)
	c := &Context{
		Ctx:      context.Background(),
		Svc:      svc,
		Query:    fleet.NewQuery(testSnapshot(t)),
		Snapshot: testSnapshot(t),
		Send:     &sender{},
		Width:    100,
		Height:   30,
	}
	return c, bundleDir
}

// TestVerbValidateWithValidBundle runs verbValidate against a real bundle and
// asserts the rendered output indicates success.
func TestVerbValidateWithValidBundle(t *testing.T) {
	c, bundleDir := newContextWithService(t)
	rec := &msgRecorder{}
	c.Send.p = rec

	sel := Selection{Ref: bundleDir, Label: "test-svc"}
	cmd := verbValidate(c, sel)
	if cmd == nil {
		t.Fatal("verbValidate returned nil command")
	}

	batch := cmd().(tea.BatchMsg)
	batch[len(batch)-1]()

	var lines []string
	var done bool
	for _, m := range rec.msgs {
		if ol, ok := m.(outputLineMsg); ok {
			lines = append(lines, ol.line)
		}
		if od, ok := m.(outputDoneMsg); ok {
			done = true
			if od.err != nil {
				t.Fatalf("verbValidate reported error: %v", od.err)
			}
		}
	}

	if !done {
		t.Fatal("no outputDoneMsg received")
	}

	output := strings.Join(lines, "\n")
	if !strings.Contains(output, "valid") {
		t.Fatalf("output does not indicate success:\n%s", output)
	}
	if !strings.Contains(output, bundleDir) {
		t.Fatalf("output does not mention the bundle path:\n%s", output)
	}
}

// TestVerbValidateReportsUnparseableYamlAsAResult pins the trap the verb is
// written around: unparseable YAML comes back as (result, nil) with a
// PARSE_ERROR inside, never as a returned error. A verb that reported only the
// error would show a clean pane for a bundle that does not parse.
func TestVerbValidateReportsUnparseableYamlAsAResult(t *testing.T) {
	c, _ := newContextWithService(t)
	rec := &msgRecorder{}
	c.Send.p = rec

	root := t.TempDir()
	badDir := filepath.Join(root, "unreadable")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "pacto.yaml"), []byte("not yaml at all: [[[["), 0o644); err != nil {
		t.Fatal(err)
	}

	sel := Selection{Ref: badDir, Label: "bad"}
	cmd := verbValidate(c, sel)
	batch := cmd().(tea.BatchMsg)
	batch[len(batch)-1]()

	var lines []string
	for _, m := range rec.msgs {
		if ol, ok := m.(outputLineMsg); ok {
			lines = append(lines, ol.line)
		}
	}

	output := strings.Join(lines, "\n")
	if !strings.Contains(output, "PARSE_ERROR") {
		t.Fatalf("unparseable YAML did not surface as a PARSE_ERROR in the result:\n%s", output)
	}
	if !strings.Contains(output, "invalid") {
		t.Fatalf("unparseable YAML did not render as invalid:\n%s", output)
	}
}

// TestVerbValidateWithInvalidBundle runs verbValidate against a contract that
// will not parse and asserts the output shows it as invalid rather than valid.
func TestVerbValidateWithInvalidBundle(t *testing.T) {
	c, _ := newContextWithService(t)
	rec := &msgRecorder{}
	c.Send.p = rec

	root := t.TempDir()
	badDir := filepath.Join(root, "bad-svc")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "pacto.yaml"), []byte("pactoVersion: \"2.0\"\nservice:\n  version: \"1.0.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sel := Selection{Ref: badDir, Label: "bad"}
	cmd := verbValidate(c, sel)
	batch := cmd().(tea.BatchMsg)
	batch[len(batch)-1]()

	var lines []string
	for _, m := range rec.msgs {
		if ol, ok := m.(outputLineMsg); ok {
			lines = append(lines, ol.line)
		}
	}

	output := strings.Join(lines, "\n")
	if strings.Contains(output, "valid") && !strings.Contains(output, "invalid") {
		t.Fatalf("invalid bundle rendered as valid:\n%s", output)
	}
	if !strings.Contains(output, "error") && !strings.Contains(output, "invalid") {
		t.Fatalf("invalid bundle did not show error or invalid:\n%s", output)
	}
}

// TestVerbExplainLocalSuccess runs verbExplainLocal against a real bundle.
func TestVerbExplainLocalSuccess(t *testing.T) {
	c, bundleDir := newContextWithService(t)
	rec := &msgRecorder{}
	c.Send.p = rec

	sel := Selection{Ref: bundleDir, Label: "test-svc"}
	cmd := verbExplainLocal(c, sel)
	batch := cmd().(tea.BatchMsg)
	batch[len(batch)-1]()

	var lines []string
	var done bool
	for _, m := range rec.msgs {
		if ol, ok := m.(outputLineMsg); ok {
			lines = append(lines, ol.line)
		}
		if od, ok := m.(outputDoneMsg); ok {
			done = true
			if od.err != nil {
				t.Fatalf("verbExplainLocal reported error: %v", od.err)
			}
		}
	}

	if !done {
		t.Fatal("no outputDoneMsg received")
	}

	output := strings.Join(lines, "\n")
	if !strings.Contains(output, "test-svc") {
		t.Fatalf("output does not mention the service name:\n%s", output)
	}
}

// TestVerbExplainLocalError runs verbExplainLocal with a nonexistent path.
func TestVerbExplainLocalError(t *testing.T) {
	c, _ := newContextWithService(t)
	rec := &msgRecorder{}
	c.Send.p = rec

	sel := Selection{Ref: "/nonexistent", Label: "missing"}
	cmd := verbExplainLocal(c, sel)
	batch := cmd().(tea.BatchMsg)
	batch[len(batch)-1]()

	var foundError bool
	for _, m := range rec.msgs {
		if od, ok := m.(outputDoneMsg); ok && od.err != nil {
			foundError = true
		}
	}
	if !foundError {
		t.Fatal("verbExplainLocal with nonexistent path did not report an error")
	}
}

// TestVerbFleetExplainSuccess runs verbFleetExplain against an entity in the test snapshot.
func TestVerbFleetExplainSuccess(t *testing.T) {
	c := newLoadedContext(t)
	c.Svc = app.NewService(nil, nil)
	rec := &msgRecorder{}
	c.Send.p = rec

	ref := firstEntityOfKind(t, c, fleet.KindService)
	sel := Selection{Kind: ref.Kind, Key: ref.Key, Label: ref.Label}

	cmd := verbFleetExplain(c, sel)
	batch := cmd().(tea.BatchMsg)
	batch[len(batch)-1]()

	var done bool
	var lines []string
	for _, m := range rec.msgs {
		if ol, ok := m.(outputLineMsg); ok {
			lines = append(lines, ol.line)
		}
		if od, ok := m.(outputDoneMsg); ok {
			done = true
			if od.err != nil {
				t.Fatalf("verbFleetExplain reported error: %v", od.err)
			}
		}
	}

	if !done {
		t.Fatal("no outputDoneMsg received")
	}

	output := strings.Join(lines, "\n")
	if len(output) == 0 {
		t.Fatal("no output from verbFleetExplain")
	}
}

// TestVerbFleetExplainError runs verbFleetExplain with a key that does not exist.
func TestVerbFleetExplainError(t *testing.T) {
	c := newLoadedContext(t)
	c.Svc = app.NewService(nil, nil)
	rec := &msgRecorder{}
	c.Send.p = rec

	sel := Selection{Kind: fleet.KindRevision, Key: "nonexistent", Label: "missing"}
	cmd := verbFleetExplain(c, sel)
	batch := cmd().(tea.BatchMsg)
	batch[len(batch)-1]()

	var foundError bool
	for _, m := range rec.msgs {
		if od, ok := m.(outputDoneMsg); ok && od.err != nil {
			foundError = true
		}
	}
	if !foundError {
		t.Fatal("verbFleetExplain with nonexistent key did not report an error")
	}
}

// TestVerbLockCheckSuccess runs verbLockCheck against a real bundle.
func TestVerbLockCheckSuccess(t *testing.T) {
	c, bundleDir := newContextWithService(t)

	if _, err := c.Svc.Lock(c.Ctx, app.LockOptions{Path: bundleDir}); err != nil {
		t.Fatalf("Lock write: %v", err)
	}

	rec := &msgRecorder{}
	c.Send.p = rec

	sel := Selection{Ref: bundleDir, Label: "test-svc"}
	cmd := verbLockCheck(c, sel)
	batch := cmd().(tea.BatchMsg)
	batch[len(batch)-1]()

	var done bool
	var lines []string
	for _, m := range rec.msgs {
		if ol, ok := m.(outputLineMsg); ok {
			lines = append(lines, ol.line)
		}
		if od, ok := m.(outputDoneMsg); ok {
			done = true
			if od.err != nil {
				t.Fatalf("verbLockCheck reported error: %v", od.err)
			}
		}
	}

	if !done {
		t.Fatal("no outputDoneMsg received")
	}

	output := strings.Join(lines, "\n")
	if len(output) == 0 {
		t.Fatal("no output from verbLockCheck")
	}
}

// TestVerbLockCheckError runs verbLockCheck with a nonexistent path.
func TestVerbLockCheckError(t *testing.T) {
	c, _ := newContextWithService(t)
	rec := &msgRecorder{}
	c.Send.p = rec

	sel := Selection{Ref: "/nonexistent", Label: "missing"}
	cmd := verbLockCheck(c, sel)
	batch := cmd().(tea.BatchMsg)
	batch[len(batch)-1]()

	var foundError bool
	for _, m := range rec.msgs {
		if od, ok := m.(outputDoneMsg); ok && od.err != nil {
			foundError = true
		}
	}
	if !foundError {
		t.Fatal("verbLockCheck with nonexistent path did not report an error")
	}
}

// TestVerbDiffRunArm runs the second press of verbDiff after arming.
func TestVerbDiffRunArm(t *testing.T) {
	c, bundleDir := newContextWithService(t)
	rec := &msgRecorder{}
	c.Send.p = rec

	sel := Selection{Ref: bundleDir, Label: "test-svc"}
	c.pendingDiff = sel

	cmd := verbDiff(c, sel)
	batch := cmd().(tea.BatchMsg)
	batch[len(batch)-1]()

	if c.pendingDiff.Ref != "" {
		t.Fatal("pendingDiff was not cleared after running")
	}

	var done bool
	for _, m := range rec.msgs {
		if od, ok := m.(outputDoneMsg); ok {
			done = true
			if od.err != nil {
				t.Fatalf("verbDiff reported error: %v", od.err)
			}
		}
	}

	if !done {
		t.Fatal("no outputDoneMsg received")
	}
}

// TestVerbDiffError runs verbDiff with a nonexistent path.
func TestVerbDiffError(t *testing.T) {
	c, bundleDir := newContextWithService(t)
	rec := &msgRecorder{}
	c.Send.p = rec

	c.pendingDiff = Selection{Ref: bundleDir, Label: "test-svc"}
	sel := Selection{Ref: "/nonexistent", Label: "missing"}

	cmd := verbDiff(c, sel)
	batch := cmd().(tea.BatchMsg)
	batch[len(batch)-1]()

	var foundError bool
	for _, m := range rec.msgs {
		if od, ok := m.(outputDoneMsg); ok && od.err != nil {
			foundError = true
		}
	}
	if !foundError {
		t.Fatal("verbDiff with nonexistent path did not report an error")
	}
}

// TestVerbImpactRunArm runs the second press of verbImpact after arming.
func TestVerbImpactRunArm(t *testing.T) {
	c, bundleDir := newContextWithService(t)
	rec := &msgRecorder{}
	c.Send.p = rec

	sel := Selection{Ref: bundleDir, Label: "test-svc"}
	c.pendingImpact = sel

	cmd := verbImpact(c, sel)
	batch := cmd().(tea.BatchMsg)
	batch[len(batch)-1]()

	if c.pendingImpact.Ref != "" {
		t.Fatal("pendingImpact was not cleared after running")
	}

	var done bool
	for _, m := range rec.msgs {
		if od, ok := m.(outputDoneMsg); ok {
			done = true
			if od.err != nil {
				t.Fatalf("verbImpact reported error: %v", od.err)
			}
		}
	}

	if !done {
		t.Fatal("no outputDoneMsg received")
	}
}

// TestVerbImpactError runs verbImpact with a nonexistent path.
func TestVerbImpactError(t *testing.T) {
	c, bundleDir := newContextWithService(t)
	rec := &msgRecorder{}
	c.Send.p = rec

	c.pendingImpact = Selection{Ref: bundleDir, Label: "test-svc"}
	sel := Selection{Ref: "/nonexistent", Label: "missing"}

	cmd := verbImpact(c, sel)
	batch := cmd().(tea.BatchMsg)
	batch[len(batch)-1]()

	var foundError bool
	for _, m := range rec.msgs {
		if od, ok := m.(outputDoneMsg); ok && od.err != nil {
			foundError = true
		}
	}
	if !foundError {
		t.Fatal("verbImpact with nonexistent path did not report an error")
	}
}

// TestDispatchVerbDoesNotApply dispatches a bundle-only verb at an owner.
func TestDispatchVerbDoesNotApply(t *testing.T) {
	c := newLoadedContext(t)
	c.Svc = app.NewService(nil, nil)

	l := newListScreen(c).(*listScreen)
	l.kindIx = 0
	for i, k := range l.kinds() {
		if k == fleet.KindOwner {
			l.kindIx = i
			break
		}
	}
	l.load(c, fleet.EntityFilter{Kinds: []fleet.EntityKind{fleet.KindOwner}, Limit: 100})
	if len(l.entities) == 0 {
		t.Fatal("test fixture has no owners")
	}

	cmd, handled := dispatchVerb(c, l, tea.KeyPressMsg{Code: 'v', Text: "v"})
	if !handled {
		t.Fatal("v was not handled")
	}

	msg := cmd().(statusMsg)
	if !strings.Contains(msg.text, "does not apply") {
		t.Fatalf("status = %q, want it to mention does not apply", msg.text)
	}
	if !strings.Contains(msg.text, "owner") {
		t.Fatalf("status = %q, want it to mention owner", msg.text)
	}
}

// TestSelectionOfScreenWithoutSelected calls selectionOf on confirmScreen.
func TestSelectionOfScreenWithoutSelected(t *testing.T) {
	c := newLoadedContext(t)
	s := newConfirmScreen("test", []string{"pacto"}, func() tea.Cmd { return nil })

	sel, ok := selectionOf(c, s)
	if ok {
		t.Fatalf("selectionOf on confirmScreen returned ok=true with selection %+v", sel)
	}
}

// TestSelectionOfWithNoSelection calls selectionOf on a list with no entities.
func TestSelectionOfWithNoSelection(t *testing.T) {
	c := newLoadedContext(t)
	l := newListScreen(c).(*listScreen)
	l.entities = nil

	sel, ok := selectionOf(c, l)
	if ok {
		t.Fatalf("selectionOf on empty list returned ok=true with selection %+v", sel)
	}
}

// TestSelectionOfWithQueryError calls selectionOf when resolveSelection returns an error.
func TestSelectionOfWithQueryError(t *testing.T) {
	c := newLoadedContext(t)
	l := newListScreen(c).(*listScreen)

	// Manually add an entity with a key that doesn't exist in the snapshot.
	// This will cause EntityDetail to fail during resolveSelection.
	l.entities = []fleet.EntityRef{{
		Kind:  fleet.KindRevision,
		Key:   "nonexistent-revision-key",
		Label: "nonexistent",
	}}

	sel, ok := selectionOf(c, l)
	if ok {
		t.Fatalf("selectionOf with nonexistent entity returned ok=true with selection %+v", sel)
	}
}

// TestOrSelfWithNonEmptyA tests the non-empty arm of orSelf.
func TestOrSelfWithNonEmptyA(t *testing.T) {
	got := orSelf("left", "right")
	if got != "left" {
		t.Fatalf("orSelf(left, right) = %q, want left", got)
	}
}

// TestDiffColorsUsed runs renderDiff against a result with GraphDiff.
func TestDiffColorsUsed(t *testing.T) {
	c, bundleDir := newContextWithService(t)

	bundleDir2 := filepath.Join(filepath.Dir(bundleDir), "test-svc-v2")
	writeBundle(t, bundleDir2, "test-svc", "2.0.0", "dependencies:\n  - name: dep\n    ref: ./dep\n")

	res, err := c.Svc.Diff(c.Ctx, app.DiffOptions{OldPath: bundleDir, NewPath: bundleDir2})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}

	if res.GraphDiff == nil {
		t.Fatal("the two fixtures produced no GraphDiff, so the diff colourisers never run")
	}

	output := renderDiff(res)
	if !strings.Contains(output, "Graph changes") {
		t.Fatalf("output does not contain graph changes:\n%s", output)
	}
	// The v2 bundle adds a dependency the v1 bundle does not have, so the added
	// colouriser must have had something to colour.
	if !strings.Contains(output, "dep") {
		t.Fatalf("the added dependency is not named in the graph changes:\n%s", output)
	}
}

// TestGraphScreenDispatchVerbBranch tests graphscreen.go:59 where dispatchVerb is called.
func TestGraphScreenDispatchVerbBranch(t *testing.T) {
	c := newLoadedContext(t)
	c.Svc = app.NewService(nil, nil)
	rec := &msgRecorder{}
	c.Send.p = rec

	ref := firstEntityOfKind(t, c, fleet.KindRevision)
	g := newGraphScreen(c, ref)

	next, cmd := g.Update(c, tea.KeyPressMsg{Code: 'e', Text: "e"})
	if next != g {
		t.Fatal("Update should return the same screen")
	}
	if cmd == nil {
		t.Fatal("verbFleetExplain should return a command")
	}
}

// TestSenderWithNilSender tests that send() is safe when the sender itself is nil.
func TestSenderWithNilSender(t *testing.T) {
	var s *sender
	s.send(statusMsg{text: "test"})
}

// TestVerbImpactArming tests the first press of impact which arms it.
func TestVerbImpactArming(t *testing.T) {
	c, bundleDir := newContextWithService(t)

	sel := Selection{Ref: bundleDir, Label: "test-svc"}
	cmd := verbImpact(c, sel)

	msg := cmd().(statusMsg)
	if !strings.Contains(msg.text, "armed") {
		t.Fatalf("first impact press should arm, got status: %q", msg.text)
	}
	if c.pendingImpact.Ref == "" {
		t.Fatal("pending impact was not set")
	}
}

// TestVerbLockWithDependencies runs verbLockCheck with a bundle that has dependencies
// to exercise the onDep callback.
func TestVerbLockWithDependencies(t *testing.T) {
	root := t.TempDir()
	depDir := filepath.Join(root, "dep-svc")
	writeBundle(t, depDir, "dep-svc", "1.0.0", "")

	mainDir := filepath.Join(root, "main-svc")
	writeBundle(t, mainDir, "main-svc", "1.0.0", "dependencies:\n  - name: dep\n    ref: "+depDir+"\n")

	svc := app.NewService(nil, nil)
	c := &Context{
		Ctx:      context.Background(),
		Svc:      svc,
		Query:    fleet.NewQuery(testSnapshot(t)),
		Snapshot: testSnapshot(t),
		Send:     &sender{},
		Width:    100,
		Height:   30,
	}

	if _, err := c.Svc.Lock(c.Ctx, app.LockOptions{Path: mainDir}); err != nil {
		t.Fatalf("Lock write: %v", err)
	}

	rec := &msgRecorder{}
	c.Send.p = rec

	sel := Selection{Ref: mainDir, Label: "main-svc"}
	cmd := verbLockCheck(c, sel)
	batch := cmd().(tea.BatchMsg)
	batch[len(batch)-1]()

	var foundDepMsg bool
	for _, m := range rec.msgs {
		if _, ok := m.(depResolvedMsg); ok {
			foundDepMsg = true
		}
	}

	if !foundDepMsg {
		t.Fatal("no depResolvedMsg received despite having dependencies")
	}
}

// TestRenderExplainNilDirect calls renderExplain directly with nil.
func TestRenderExplainNilDirect(t *testing.T) {
	got := renderExplain(nil)
	if got != "(no result)" {
		t.Fatalf("renderExplain(nil) = %q, want (no result)", got)
	}
}

// TestRenderLockNilDirect calls renderLock directly with nil.
func TestRenderLockNilDirect(t *testing.T) {
	got := renderLock(nil)
	if got != "(no result)" {
		t.Fatalf("renderLock(nil) = %q, want (no result)", got)
	}
}

// TestRenderValidateWithWarnings covers the Warnings loop in renderValidate.
func TestRenderValidateWithWarnings(t *testing.T) {
	r := &app.ValidateResult{
		Path:  "test.yaml",
		Valid: true,
		Warnings: []contract.ValidationWarning{
			{Code: "TEST_WARN", Message: "something might be wrong"},
		},
	}
	got := renderValidate(r)
	if !strings.Contains(got, "TEST_WARN") {
		t.Fatal("renderValidate should include warning code")
	}
	if !strings.Contains(got, "something might be wrong") {
		t.Fatal("renderValidate should include warning message")
	}
}

// TestRenderExplainWithOwner covers the Owner rendering branch in renderExplain.
func TestRenderExplainWithOwner(t *testing.T) {
	r := &app.ExplainResult{
		Name:    "test",
		Version: "1.0.0",
		Owner: contract.Owner{
			Team: "platform",
		},
	}
	got := renderExplain(r)
	if !strings.Contains(got, "platform") {
		t.Fatalf("renderExplain should include owner, got:\n%s", got)
	}
}

// TestDiffColorsClosures exercises all four diffColors closures.
func TestDiffColorsClosures(t *testing.T) {
	colors := diffColors()

	testString := "test"

	// Name should return unchanged
	if colors.Name(testString) != testString {
		t.Errorf("Name should return input unchanged")
	}

	// Added, Removed, Changed should style the string (not return it unchanged)
	added := colors.Added(testString)
	if added == "" {
		t.Error("Added should not return empty string")
	}

	removed := colors.Removed(testString)
	if removed == "" {
		t.Error("Removed should not return empty string")
	}

	changed := colors.Changed(testString)
	if changed == "" {
		t.Error("Changed should not return empty string")
	}
}
