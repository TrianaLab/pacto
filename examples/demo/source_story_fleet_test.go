package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// storyFleet is the story collection alone, as buildDemoFleet adds it to the
// browser demo's fleet.
func storyFleet(t *testing.T) *fleet.FleetSnapshot {
	t.Helper()
	revs, _, err := storyRevisions()
	if err != nil {
		t.Fatal(err)
	}
	snap, err := fleet.Build(t.Context(), fleet.BuildOptions{}, fleet.NewMemorySource("story-registry", "oci", &fleet.Collection{Revisions: revs}))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// TestStoryRevisionsJoinStages: every digest a demo Stage runs or offers
// names a contract the demo fleet holds as a revision of the same service,
// so the dashboard links each Stage row to its revision page.
func TestStoryRevisionsJoinStages(t *testing.T) {
	revs, byRef, err := storyRevisions()
	if err != nil {
		t.Fatal(err)
	}
	service := map[string]string{} // contract digest -> service
	for _, r := range revs {
		service[r.Digest] = r.Bundle.Contract.Service.Name
		if byRef[r.ResolvedRef] != r.Bundle {
			t.Errorf("%s is not indexed by its resolved ref", r.ResolvedRef)
		}
	}
	d, err := newDemoDelivery(time.Now, nil)
	if err != nil {
		t.Fatal(err)
	}
	stages, _ := d.Stages(t.Context(), "")
	joined := 0
	for _, st := range stages {
		for _, dg := range []string{st.Status.Pointer, st.Status.Candidate} {
			if dg == "" {
				continue
			}
			r := d.Describe(t.Context(), &st, dg)
			if got := service[delivery.ContractDigest(r.ContractRef)]; got != st.Spec.Service.Name {
				t.Errorf("%s/%s %s names contract %q, which the story fleet holds for %q", st.Metadata.Namespace, st.Metadata.Name, dg, r.ContractRef, got)
			}
			joined++
		}
	}
	if joined == 0 {
		t.Fatal("no demo Stage runs or offers anything")
	}

	rowsLinkRevisions(t, d, storyFleet(t))
}

// rowsLinkRevisions: with the story fleet wired, every payments row that
// runs a version links to its revision.
func rowsLinkRevisions(t *testing.T, d *demoDelivery, snap *fleet.FleetSnapshot) {
	t.Helper()
	for _, l := range snap.Limitations {
		if l.Code == fleet.LimitationSourceRecordInvalid {
			t.Errorf("%s: %s", l.Code, l.Message)
		}
	}
	for k, s := range snap.Services {
		if s.Name == "payments" {
			t.Logf("the story payments service key: %s", k)
		}
	}
	q := fleet.NewQuery(snap)
	opts := dashboard.Options{Fleet: func(context.Context) (*fleet.Query, error) { return q, nil }}
	d.wire(&opts)
	mux := http.NewServeMux()
	dashboard.NewServer(opts).RegisterOperations(humago.New(mux, dashboard.APIConfig()))
	var out struct {
		Environments []dashboard.EnvironmentRow `json:"environments"`
	}
	if code := call(t, mux, "GET", "/api/delivery/environments?service=payments", "", &out); code != 200 || len(out.Environments) == 0 {
		t.Fatalf("payments environments %d %+v", code, out)
	}
	for _, row := range out.Environments {
		if row.Pointer != "" && row.Revision == "" {
			t.Errorf("payments %s runs %s with no revision link", row.Namespace, row.Version)
		}
	}
}
