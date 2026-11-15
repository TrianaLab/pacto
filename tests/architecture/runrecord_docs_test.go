package architecture

import (
	"reflect"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/runrecord"
)

// The run record reference documents every JSON key a record can hold, so a
// field added to the schema without a row in the field table fails here.
func TestRunRecordReferenceListsEveryField(t *testing.T) {
	doc := readRepoFile(t, "docs", "run-record-reference.md")
	for _, typ := range []reflect.Type{
		reflect.TypeFor[runrecord.Record](),
		reflect.TypeFor[runrecord.Gate](),
		reflect.TypeFor[runrecord.Consumers](),
		reflect.TypeFor[runrecord.StageRef](),
		reflect.TypeFor[runrecord.GateResult](),
	} {
		for f := range typ.Fields() {
			key, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			// A nested key is listed under its parent, as `gate.verdict`.
			if !strings.Contains(doc, "`"+key+"`") && !strings.Contains(doc, "."+key+"`") {
				t.Errorf("docs/run-record-reference.md does not list %s.%s as `%s`", typ.Name(), f.Name, key)
			}
		}
	}
}
