package dashboard

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// The service entity route returns the field provenance the query builds.
func TestServiceDetailFieldsMatchQuery(t *testing.T) {
	q := testutil.HandoverQuery(t)
	want, err := q.GetService("invoices")
	if err != nil || want.Fields == nil {
		t.Fatalf("get: %v %+v", err, want)
	}
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, nil)
	t.Cleanup(cancel)
	var d struct {
		Service struct {
			Fields   *fleet.FieldsPreview
			Replaces []fleet.Replacement
		}
	}
	getJSON(t, base+"/api/fleet/entities/service?key=invoices", http.StatusOK, &d)
	if !reflect.DeepEqual(d.Service.Fields, want.Fields) {
		t.Errorf("fields %+v\nwant %+v", d.Service.Fields, want.Fields)
	}
	if len(d.Service.Replaces) != 1 || d.Service.Replaces[0].Link != "inferred-from" {
		t.Errorf("replaces %+v", d.Service.Replaces)
	}
}
