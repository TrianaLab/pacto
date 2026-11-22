package main

import (
	"os"
	"slices"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestDemoHandover: ana declared invoices from the invoices Deployment the
// cluster runs with no contract, so the demo lists one service invoices, no
// inferred twin, and records the inferred-from replacement with its signer
// and the workload facts; its fields say one range is accepted and one is
// unknown with a proposal, one value is inferred from the folder, and
// nothing reads as ignored or unverified.
func TestDemoHandover(t *testing.T) {
	partners, err := NewEmbedSource(os.DirFS("partners"))
	if err != nil {
		t.Fatal(err)
	}
	snap, byRef, err := buildDemoFleet(bundlesFS(t), partners)
	if err != nil {
		t.Fatal(err)
	}
	r := checkHandedOver(t, snap)
	for _, l := range snap.Limitations {
		switch l.Code {
		case fleet.LimitationInferredRecordUnreadable, fleet.LimitationDeclarationSignerUnverified, fleet.LimitationInferredLinkIgnored:
			t.Errorf("limitation %s: %s", l.Code, l.Message)
		}
	}
	q := fleet.NewQuery(snap)
	checkHandoverFields(t, q)
	if rev := snap.Revisions[r.Revision]; rev == nil || byRef[rev.ResolvedRef] == nil {
		t.Fatalf("revision %s is not in the impact index", r.Revision)
	}
	d, err := q.EntityDetail(fleet.KindRevision, string(r.Revision))
	if err != nil {
		t.Fatal(err)
	}
	if d.Revision == nil || !d.Revision.Valid || d.Revision.Version != "0.1.0" || d.Revision.Declaration == nil {
		t.Errorf("the revision page does not read the declared document: %+v", d.Revision)
	}
}

// checkHandedOver returns the inferred-from replacement of the invoices
// workload, which must no longer be an inferred service.
func checkHandedOver(t *testing.T, snap *fleet.FleetSnapshot) fleet.Replacement {
	t.Helper()
	twin := fleet.NewServiceKey(fleet.K8sCaller(prodClusterID, "billing", "Deployment", "invoices"))
	if _, ok := snap.Services[twin]; ok {
		t.Errorf("the declared workload is still an inferred service")
	}
	i := slices.IndexFunc(snap.Replacements, func(r fleet.Replacement) bool { return r.Link == "inferred-from" })
	if i < 0 {
		t.Fatalf("no inferred-from replacement: %+v %+v", snap.Replacements, snap.Limitations)
	}
	r := snap.Replacements[i]
	if r.From != twin || r.To != fleet.NewServiceKey("invoices") || r.By != handoverAsker || r.Workload == nil || r.Workload.Image == nil || r.Revision == "" {
		t.Errorf("replacement = %+v", r)
	}
	return r
}

// checkHandoverFields: invoices has an accepted range, an unknown range
// with a proposal and an inferred value.
func checkHandoverFields(t *testing.T, q *fleet.Query) {
	t.Helper()
	v, err := q.GetService("invoices")
	if err != nil {
		t.Fatal(err)
	}
	if v.Fields == nil {
		t.Fatal("invoices has no fields")
	}
	var accepted, proposed, inferred bool
	for _, f := range v.Fields.Items {
		accepted = accepted || f.Provenance == "accepted" && f.By == handoverAsker
		proposed = proposed || f.Value == "unknown" && f.Proposed != ""
		inferred = inferred || f.Provenance == "inferred"
	}
	if !accepted || !proposed || !inferred {
		t.Errorf("fields %+v: accepted %v, proposed %v, inferred %v", v.Fields.Items, accepted, proposed, inferred)
	}
}
