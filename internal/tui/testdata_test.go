package tui

import (
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// cmdMembers runs a container command — the value tea.Batch or tea.Sequence
// hands back — and returns the commands it carries. Only the container is run
// here: firing a leaf is what the caller is testing, and a leaf may block. Both
// containers carry a []tea.Cmd but only BatchMsg is exported, so the match is on
// the shape rather than on the type.
func cmdMembers(t *testing.T, cmd tea.Cmd) []tea.Cmd {
	t.Helper()
	msg := cmd()
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Slice || v.Type().Elem() != reflect.TypeFor[tea.Cmd]() {
		t.Fatalf("expected a batched or sequenced command, got %T", msg)
	}
	out := make([]tea.Cmd, v.Len())
	for i := range out {
		out[i] = v.Index(i).Interface().(tea.Cmd)
	}
	return out
}

// readVerbPush and readVerbWorker unwrap what runRead returns, which is
// Sequence(push, worker).
func readVerbPush(t *testing.T, cmd tea.Cmd) tea.Cmd {
	t.Helper()
	return cmdMembers(t, cmd)[0]
}

func readVerbWorker(t *testing.T, cmd tea.Cmd) tea.Cmd {
	t.Helper()
	seq := cmdMembers(t, cmd)
	return seq[len(seq)-1]
}

// testServiceName is the service the test fixture bundle declares.
const testServiceName = "test-svc"

// firstEntityOfKind finds the first entity of a given kind in the test snapshot.
// A fixture with no such entity fails the test rather than skipping it: a test
// that verifies nothing must not report success.
func firstEntityOfKind(t *testing.T, c *Context, kind fleet.EntityKind) fleet.EntityRef {
	t.Helper()
	list, err := c.Query.Entities(fleet.EntityFilter{Kinds: []fleet.EntityKind{kind}, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Entities) == 0 {
		t.Fatalf("the fixture has no %s entity", kind)
	}
	return list.Entities[0]
}

// newLoadedModel returns the root model with the fixture snapshot already
// delivered, so the list is the top of a real stack.
//
// Tests that press keys should go through this rather than through a screen's
// own Update: globalKey runs BEFORE the top screen, and it is that ordering,
// not the screen's switch statement, that decides what a key does. Calling
// listScreen.Update directly is how esc came to be documented as "clears the
// filter" while the only path a reader has bound it to quit.
func newLoadedModel(t *testing.T) *Model {
	t.Helper()
	m := New(testOptions())
	m.Update(snapshotMsg{snap: testSnapshot(t)})
	return m
}

// press drives one key press through the root model and returns what it asked
// for. The model updates in place; a different one coming back is a bug in the
// test rather than something to accommodate.
func press(t *testing.T, m *Model, k tea.KeyPressMsg) tea.Cmd {
	t.Helper()
	next, cmd := m.Update(k)
	if next.(*Model) != m {
		t.Fatalf("%s produced a different model", k.String())
	}
	return cmd
}

// pressAndRun presses a key and then delivers the message its command produced,
// which is what the bubbletea runtime does. A test that only calls Update sees
// the pushMsg but never the stack it changes.
func pressAndRun(t *testing.T, m *Model, k tea.KeyPressMsg) {
	t.Helper()
	if cmd := press(t, m, k); cmd != nil {
		m.Update(cmd())
	}
}

// verbBoundTo returns the verb bound to a key. An unbound key fails the test:
// a table-driven test that silently ran against a zero Verb would assert on
// nothing and report success.
func verbBoundTo(t *testing.T, c *Context, key string) Verb {
	t.Helper()
	for _, v := range verbList(c) {
		if v.Key == key {
			return v
		}
	}
	t.Fatalf("no verb bound to %q", key)
	return Verb{}
}

// cursorOnKind moves a list screen's cursor to the first row of a given kind.
// The unfiltered list is sorted by kind and owners come first, so a test about
// any other kind has to say which row it means.
func cursorOnKind(t *testing.T, l *listScreen, kind fleet.EntityKind) {
	t.Helper()
	for i, e := range l.entities {
		if e.Kind == kind {
			l.tbl.SetCursor(i)
			return
		}
	}
	t.Fatalf("the list holds no %s row", kind)
}
