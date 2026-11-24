package main

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestDemoExtensionChecks: payments-service 2.0.1's dispute extension reads
// covered by dispute-auditor and 1.2.1's refund extension failing by
// refund-checker. No payments-service target runs either revision and
// neither is the current one, so no check counts toward the service status
// and the status, the attention list and the overview equal a demo with no
// extension reports. If a revision a target runs, or the current revision,
// starts declaring an extension, this test fails instead of a tally moving.
func TestDemoExtensionChecks(t *testing.T) {
	snap := demoFleet(t)
	checks := map[string]fleet.ExtensionCheck{}
	for _, r := range snap.Revisions {
		if r.Service != "payments-service" {
			continue
		}
		for _, c := range r.Extensions {
			if c.CountsTowardStatus {
				t.Errorf("%s %s counts toward the service status", r.Version, c.Ref)
			}
			checks[r.Version+" "+c.Ref] = c
		}
	}
	if c := checks["2.0.1 payments.trianalab.com/dispute"]; c.State != fleet.ExtensionCovered || c.Producer != "dispute-auditor" ||
		c.ProducerKeyID != "dispute-auditor-2026" || len(c.Details) == 0 {
		t.Errorf("2.0.1 dispute check = %+v, want covered by dispute-auditor with details", c)
	}
	if c := checks["1.2.1 payments.trianalab.com/refund"]; c.State != fleet.ExtensionFailing || !reflect.DeepEqual(c.Producers, []string{"refund-checker"}) {
		t.Errorf("1.2.1 refund check = %+v, want failing by refund-checker", c)
	}
	if len(checks) != 2 {
		t.Errorf("checks = %+v, want exactly two", checks)
	}

	with := demoTallies(t, snap)
	demoExtensionRecords = func(*EmbedSource) []fleet.RawExtension { return nil }
	defer func() { demoExtensionRecords = demoExtensions }()
	if without := demoTallies(t, demoFleet(t)); with != without {
		t.Errorf("extension reports moved a tally:\nwith    %s\nwithout %s", with, without)
	}
}

// demoTallies is payments-service's status, the attention list and the
// overview, as one JSON string without the snapshot id.
func demoTallies(t *testing.T, snap *fleet.FleetSnapshot) string {
	t.Helper()
	q := fleet.NewQuery(snap)
	attention, err := q.Attention(fleet.AttentionFilter{Limit: fleet.MaxAttentionLimit})
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[fleet.ServiceKey]string{}
	for k, s := range snap.Services {
		if s.Name == "payments-service" {
			statuses[k] = s.Status
		}
	}
	// The snapshot id hashes the snapshot, extension records included.
	overview := q.Overview()
	attention.Meta.SnapshotID, overview.Meta.SnapshotID = "", ""
	// Attention orders equal-rank items arbitrarily, so compare them as sets.
	items, preview := itemSet(t, attention.Items), itemSet(t, overview.Attention.Items)
	attention.Items, overview.Attention.Items = nil, nil
	return mustJSON(t, []any{statuses, attention, items, overview, preview})
}

func itemSet(t *testing.T, items []fleet.AttentionItem) []string {
	t.Helper()
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = mustJSON(t, it)
	}
	slices.Sort(out)
	return out
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
