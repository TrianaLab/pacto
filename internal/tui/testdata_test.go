package tui

import (
	"testing"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// testServiceName is the service the test fixture bundle declares.
const testServiceName = "test-svc"

// firstEntityOfKind finds the first entity of a given kind in the test snapshot.
// It skips the test when the fixture has no such entity.
func firstEntityOfKind(t *testing.T, c *Context, kind fleet.EntityKind) fleet.EntityRef {
	t.Helper()
	list, err := c.Query.Entities(fleet.EntityFilter{Kinds: []fleet.EntityKind{kind}, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Entities) == 0 {
		t.Skipf("the fixture has no %s entity", kind)
	}
	return list.Entities[0]
}
