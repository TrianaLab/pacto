package main

import (
	"net/url"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// TestTourRegressedStepCitesEvidence: the tour's regression step named by a
// digest opens a service key the dashboard's Stage filter keeps, and the
// digest is one delivery recorded a failure and a rollback against, so the
// step shows evidence rather than a limitation.
func TestTourRegressedStepCitesEvidence(t *testing.T) {
	js, err := os.ReadFile("tours.js")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`hash: "#/fleet/changes/([^"?]+)\?event=regressed&rev=(sha256:[0-9a-f]+)"`).FindSubmatch(js)
	if m == nil {
		t.Fatal("no regression step named by a digest in tours.js")
	}
	key, err := url.PathUnescape(string(m[1])) // the router decodes the segment once
	if err != nil {
		t.Fatal(err)
	}
	rev := string(m[2])
	if rev != digest("payments", "2.4.2") {
		t.Fatalf("the step names %s, not the story's payments 2.4.2", rev)
	}
	d, err := newDemoDelivery(time.Now, nil)
	if err != nil {
		t.Fatal(err)
	}
	all, err := d.Stages(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	domain, name := fleet.ParseServiceKey(fleet.ServiceKey(key))
	var stages []delivery.StageObject
	var hs []delivery.StageHistory
	for _, st := range all {
		if delivery.OfService(st, name, domain) {
			stages = append(stages, st)
			h, err := d.History(t.Context(), st.Metadata.Namespace, st.Metadata.Name, &st)
			if err != nil {
				t.Fatal(err)
			}
			hs = append(hs, delivery.StageHistory{Namespace: st.Metadata.Namespace, History: *h})
		}
	}
	if len(stages) == 0 {
		t.Fatalf("key %q keeps no Stage", key)
	}
	ev, unknown := delivery.RegressionEvidence(stages, hs, rev)
	kinds := map[string]bool{}
	for _, e := range ev {
		kinds[e.Kind] = true
	}
	if !kinds[impact.RegressionFailed] || !kinds[impact.RegressionRollback] || len(unknown) > 0 {
		t.Fatalf("evidence %+v, unknown %v", ev, unknown)
	}
}
