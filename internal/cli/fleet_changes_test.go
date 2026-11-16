package cli_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// TestFleetEntityDemoFindingChanges: the demo revisions that differ in
// validation list the findings each one introduced, labelled validation.
// storefront-web 1.1.0 could not verify its environment values, so no
// environment finding is compared across it and the answer says so.
func TestFleetEntityDemoFindingChanges(t *testing.T) {
	for svc, want := range map[string][]string{
		"catalog-service": {"1.3.0 added DEPENDENCY_SERVICE_DEPRECATED ->error 1.2.1"},
		"storefront-web":  nil,
	} {
		out, _, err := execFleet(t, "fleet", "entity", "service", svc, "--local", demoBundles, "--output-format", "json")
		if err != nil {
			t.Fatalf("%s: %v", svc, err)
		}
		var res struct {
			Service struct {
				Changes struct {
					CompletenessReason string
					Items              []struct {
						Kind, Change, Subject, From, To, Source, Provenance string
						Revision                                            struct{ Version string }
						Previous                                            struct{ Version string }
					}
				}
			}
		}
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, e := range res.Service.Changes.Items {
			if e.Kind != "finding" {
				continue
			}
			if e.Source != "validation" || e.Provenance != "declared" {
				t.Errorf("%s: event %+v", svc, e)
			}
			got = append(got, e.Revision.Version+" "+e.Change+" "+e.Subject+" "+e.From+"->"+e.To+" "+e.Previous.Version)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s finding changes = %q, want %q", svc, got, want)
		}
		if want == nil && !strings.Contains(res.Service.Changes.CompletenessReason, "validation did not run the same checks") {
			t.Errorf("%s reason %q", svc, res.Service.Changes.CompletenessReason)
		}
	}
}
