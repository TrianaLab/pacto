package main

import (
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/inferredcontract"
)

// demoExternal is the demo external key as a query serves it.
func demoExternal(t *testing.T, q *fleet.Query, key fleet.ExternalKey) *fleet.ExternalDetailData {
	t.Helper()
	d, err := q.EntityDetail(fleet.KindExternal, string(key))
	if err != nil || d.External == nil {
		t.Fatalf("external %s = %+v, %v", key, d, err)
	}
	return d.External
}

// TestDemoManagedOutsideContract: orders-db, read from its Crossplane object
// on the production cluster with its observed engine, is outside the
// contract, and the externals filter keeps it.
func TestDemoManagedOutsideContract(t *testing.T) {
	q := fleet.NewQuery(demoFleet(t))
	rs := demoExternal(t, q, demoOrdersDB).External.Resources
	if len(rs) != 1 {
		t.Fatalf("orders-db resources = %+v, want one", rs)
	}
	r := rs[0]
	if r.Source != "crossplane" || r.Type != "rds.aws.upbound.io/Instance" || r.ClusterName != "production" || r.Engine != "postgres 16" ||
		r.Contract != fleet.ContractOutside || r.Offering != nil || r.Provenance != fleet.ProvenanceInferred || r.ObservedAt == nil {
		t.Errorf("orders-db resource = %+v, want an inferred Crossplane object outside the contract", r)
	}
	l, err := q.Entities(fleet.EntityFilter{Kinds: []fleet.EntityKind{fleet.KindExternal}, OutsideContract: true, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, e := range l.Entities {
		keys = append(keys, e.Key)
	}
	if !slices.Equal(keys, []string{string(demoOrdersDB), string(demoOrderEvents)}) && !slices.Equal(keys, []string{string(demoOrderEvents), string(demoOrdersDB)}) {
		t.Errorf("outside contract externals = %v, want orders-db and order-events", keys)
	}
}

// TestDemoManagedThroughOffering: ticketing-db, produced by the verified
// PostgreSQLInstance claim the platform adapter's signed report names, reads
// through the postgresql offering, and that report raises no conflict.
func TestDemoManagedThroughOffering(t *testing.T) {
	snap := demoFleet(t)
	rs := demoExternal(t, fleet.NewQuery(snap), demoTicketingDB).External.Resources
	if len(rs) != 1 || rs[0].Contract != fleet.ContractOffering || rs[0].Offering == nil || rs[0].Offering.Key != "postgresql" ||
		rs[0].Claim != demoTicketingClaim || !strings.HasPrefix(rs[0].Evidence, "reported by platform-adapter at ") {
		t.Fatalf("ticketing-db resources = %+v, want one through the postgresql offering", rs)
	}
	for _, l := range snap.Limitations {
		if l.Code == fleet.LimitationProvisioningConflict {
			t.Errorf("the claim report raised %+v", l)
		}
	}
}

// TestDemoManagedConsumersJoined: the two shop workloads that read
// orders-db's connection Secret are its inferred consumers, joined by Secret
// name only.
func TestDemoManagedConsumersJoined(t *testing.T) {
	x := demoExternal(t, fleet.NewQuery(demoFleet(t)), demoOrdersDB)
	if len(x.Consumers) != 2 {
		t.Fatalf("orders-db consumers = %+v, want two", x.Consumers)
	}
	for _, c := range x.Consumers {
		if c.Provenance != fleet.ProvenanceInferred || !strings.Contains(c.Service.Key, "%2Fshop%2FDeployment%2F") {
			t.Errorf("consumer = %+v, want an inferred shop workload", c)
		}
	}
	for _, s := range x.InferredSources {
		if s.Kind != "connectionSecret" || !strings.HasSuffix(s.Where, " secret/orders-db-conn") || s.Source != "cluster-detection" {
			t.Errorf("inferred source = %+v, want the connection Secret", s)
		}
	}
}

// TestDemoManagedCoverage: one of the three managed resources the demo read
// went through an offering, the Overview's figure; the map counts orders-db's
// resource outside the contract.
func TestDemoManagedCoverage(t *testing.T) {
	m, err := fleet.NewQuery(demoFleet(t)).Map(fleet.MapQuery{})
	if err != nil {
		t.Fatal(err)
	}
	outside := 0
	for _, g := range m.Groups {
		outside += g.OutsideContract
	}
	if outside != 2 {
		t.Errorf("map groups count %d resources outside contract, want orders-db's and order-events'", outside)
	}
	r := m.Match.Resources
	if r == nil || r.Discovered != 3 || r.ThroughOfferings != 1 {
		t.Fatalf("resource coverage = %+v, want 1 of 3", r)
	}
}

// TestDemoUncheckedEngineChangeRaisesNoAttention: orders-db's two unsigned
// inferred revisions record its observed engine moving from postgres 15 to
// 16; the change reads unchecked and raises no EXTERNAL_CHANGED item. The
// revisions are byte-identical to the publisher's.
func TestDemoUncheckedEngineChangeRaisesNoAttention(t *testing.T) {
	q := fleet.NewQuery(demoFleet(t))
	p := demoExternal(t, q, demoOrdersDB).External.Provider
	if p == nil || p.Change == nil || p.Change.Checked || p.Change.EngineFrom != "postgres 15" || p.Change.EngineTo != "postgres 16" {
		t.Fatalf("orders-db provider = %+v, want an unchecked postgres 15 -> 16 change", p)
	}
	l, err := q.Attention(fleet.AttentionFilter{Limit: fleet.MaxAttentionLimit})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range l.Items {
		if it.Code == fleet.AttentionExternalChanged {
			t.Errorf("an unchecked change raised %+v", it)
		}
	}
	for _, v := range []string{"0.0.1", "0.0.2"} {
		got := demoOrdersDBRevision(v).Bundle
		want, err := inferredcontract.Bundle(demoOrdersDB, demoOrdersDBSurface(v), v,
			contract.InferredInputs{Pacto: "demo", Discovery: inferredcontract.Version, Sources: []contract.InferredSourceFile{}}, "", demoOrdersDBEngine[v])
		if err != nil {
			t.Fatal(err)
		}
		g, _ := fs.ReadFile(got.FS, contract.InferredRecordFile)
		w, _ := fs.ReadFile(want.FS, contract.InferredRecordFile)
		if string(got.RawYAML) != string(want.RawYAML) || string(g) != string(w) {
			t.Errorf("%s = %q %s, want %q %s", v, got.RawYAML, g, want.RawYAML, w)
		}
	}
}

// TestDemoCloudInventoryRowOff: the three managed rows are on Data sources;
// cloud inventory is off and names the opt-in for the bound identity.
func TestDemoCloudInventoryRowOff(t *testing.T) {
	rows := map[string]fleet.Provider{}
	for _, p := range demoFleet(t).Providers {
		if p.Source == "cluster-detection" {
			rows[p.ID] = p
		}
	}
	if m := rows[fleet.ProviderManagedResources]; m.Status != fleet.SourceAvailable || !strings.Contains(m.Evidence, "2 managed resources") {
		t.Errorf("managed-resources row = %+v", m)
	}
	if tf := rows["terraform"]; tf.Status != fleet.SourceAvailable || !strings.Contains(tf.Evidence, demoTerraformState) {
		t.Errorf("terraform row = %+v", tf)
	}
	c := rows["cloud-inventory"]
	if c.Status != fleet.SourceUnavailable || len(c.Limitations) != 1 ||
		c.Limitations[0].Message != "set controller.cloudInventory to read the inventory with "+demoOperatorIdentity ||
		!strings.Contains(c.Hint, "controller.cloudInventory=<cloud>") {
		t.Errorf("cloud-inventory row = %+v, want off naming the opt-in", c)
	}
}
