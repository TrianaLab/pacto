package tui

import (
	"testing"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

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
