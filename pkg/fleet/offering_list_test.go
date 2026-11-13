package fleet

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// rowFleet is three offerings: postgresql (pg deprecated, team a; pg-ha
// supported, team b), cache (redis past sunset, team a) and queue (mq
// supported, team a, observed only besides). app consumes both postgresql
// providers, web consumes pg and redis.
func rowFleet(t *testing.T) *Query {
	t.Helper()
	pg := svc24("pg", "16.1.0", "postgresql")
	pg.Service.Owner, pg.Service.Lifecycle = contract.Owner{Team: "a"}, &contract.InterfaceLifecycle{Deprecated: "2026-01-01", Sunset: "2099-01-01"}
	ha := svc24("pg-ha", "1.0.0", "postgresql")
	ha.Service.Owner = contract.Owner{Team: "b"}
	redis := svc24("redis", "7.0.0", "cache")
	redis.Service.Owner, redis.Service.Lifecycle = contract.Owner{Team: "a"}, &contract.InterfaceLifecycle{Deprecated: "2025-01-01", Sunset: "2026-01-01"}
	mq := svc24("mq", "1.0.0", "queue")
	mq.Service.Owner = contract.Owner{Team: "a"}
	dep := func(name, rng string) contract.Dependency {
		return contract.Dependency{Name: name, Ref: "oci://" + acme + "/" + name, Required: true, Compatibility: rng}
	}
	app := svc24("app", "1.0.0", "")
	app.Dependencies = []contract.Dependency{dep("pg", "^16.0.0"), dep("pg-ha", "^1.0.0")}
	web := svc24("web", "1.0.0", "")
	web.Dependencies = []contract.Dependency{dep("pg", "^16.0.0"), dep("redis", "^7.0.0")}
	return buildDomainSnap(t, "oci", &Collection{
		Revisions: []RawRevision{ociRev(acme, pg, nil), ociRev(acme, ha, nil), ociRev(acme, redis, nil), ociRev(acme, mq, nil), ociRev(acme, app, nil), ociRev(acme, web, nil)},
	})
}

func offeringRows(t *testing.T, q *Query, f EntityFilter) map[string]EntityRef {
	t.Helper()
	f.Kinds = []EntityKind{KindOffering}
	l, err := q.Entities(f)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]EntityRef{}
	for _, r := range l.Entities {
		out[r.Key] = r
	}
	return out
}

func TestOfferingRowOwners(t *testing.T) {
	rows := offeringRows(t, rowFleet(t), EntityFilter{})
	if got := rows["postgresql"].Owner; got != "2 owners claim it" || got != ownersClaimLabel(2) {
		t.Errorf("postgresql owner = %q", got)
	}
	if got := rows["cache"].Owner; got != "a" {
		t.Errorf("cache owner = %q", got)
	}
	// Providers that agree on an owner claim it once.
	var c ownerClaimSet
	c.merge(ownerClaimSet{keys: []string{"team:a"}, keyless: []contract.Owner{{Contacts: []contract.OwnerContact{{Type: "email", Value: "x@y"}}}}})
	c.merge(ownerClaimSet{keys: []string{"team:a"}})
	if c.len() != 2 {
		t.Errorf("merged = %+v", c)
	}
}

func TestOfferingRowLifecycleWorst(t *testing.T) {
	rows := offeringRows(t, rowFleet(t), EntityFilter{})
	for key, want := range map[string]string{"postgresql": LifecycleDeprecated, "cache": LifecycleSunset, "queue": LifecycleSupported} {
		if got := rows[key].Lifecycle; got != want {
			t.Errorf("%s lifecycle = %q, want %q", key, got, want)
		}
	}
	for _, tt := range []struct{ a, b, want string }{
		{LifecycleSupported, LifecycleUnknown, LifecycleUnknown},
		{LifecycleUnknown, LifecycleDeprecated, LifecycleDeprecated},
		{LifecycleSunset, LifecycleDeprecated, LifecycleSunset},
		{LifecycleDeprecated, LifecycleSupported, LifecycleDeprecated},
	} {
		if got := worstLifecycle(tt.a, tt.b); got != tt.want {
			t.Errorf("worst(%s, %s) = %s", tt.a, tt.b, got)
		}
	}
	if got := revisionLifecycle(&ContractRevision{}, availNow); got != LifecycleUnknown {
		t.Errorf("no contract = %s", got)
	}
}

func TestOfferingRowLifecycleUnknownObservedOnly(t *testing.T) {
	a := newAvailSnap()
	a.svc("pg", "postgresql")
	a.target("pg", "pg-0", "db", "prod", time.Hour)
	rows := offeringRows(t, a.query(), EntityFilter{})
	if got := rows["postgresql"].Lifecycle; got != LifecycleUnknown {
		t.Errorf("observed-only lifecycle = %q", got)
	}
}

func TestOfferingRowConsumersDistinct(t *testing.T) {
	rows := offeringRows(t, rowFleet(t), EntityFilter{})
	if rows["postgresql"].Consumers != 2 || rows["cache"].Consumers != 1 || rows["queue"].Consumers != 0 {
		t.Errorf("consumers = %d %d %d", rows["postgresql"].Consumers, rows["cache"].Consumers, rows["queue"].Consumers)
	}
}

// failedFleet is ordersFleet with a Failed report for orders 1.3.0's db.
func failedFleet(t *testing.T) *Query {
	t.Helper()
	return NewQuery(provisioningSnap(t, 0, ordersFleet(), []RawProvisioning{report(func(r *RawProvisioning) { r.Phase = "Failed" })}))
}

func TestOfferingRowFailedRequests(t *testing.T) {
	rows := offeringRows(t, failedFleet(t), EntityFilter{})
	if r := rows["postgresql"]; r.FailedRequests != 1 || r.Consumers != 1 {
		t.Errorf("row = %+v", r)
	}
	rows = offeringRows(t, NewQuery(provisioningSnap(t, 0, ordersFleet(), []RawProvisioning{report()})), EntityFilter{})
	if r := rows["postgresql"]; r.FailedRequests != 0 {
		t.Errorf("ready row = %+v", r)
	}
}

func TestOfferingRowAvailability(t *testing.T) {
	a := newAvailSnap()
	a.svc("pg", "postgresql")
	consumersOf(a, "prod", 2, 2)
	a.svc("redis", "cache")
	a.target("redis", "r-0", "db", "prod", time.Hour)
	rows := offeringRows(t, a.query(), EntityFilter{})
	if r := rows["postgresql"]; r.Availability != AvailabilityUnavailable || r.AvailabilityProvenance != ProvenanceInferred {
		t.Errorf("postgresql = %s (%s)", r.Availability, r.AvailabilityProvenance)
	}
	if r := rows["cache"]; r.Availability != AvailabilityAvailable || r.AvailabilityProvenance != ProvenanceObserved {
		t.Errorf("cache = %s (%s)", r.Availability, r.AvailabilityProvenance)
	}
	// The worst environment says how Pacto knows: staging unavailable here.
	envs := []EnvironmentAvailability{{State: AvailabilityAvailable, Provenance: ProvenanceObserved}, {State: AvailabilityUnavailable, Provenance: ProvenanceInferred}, {State: AvailabilityDegraded, Provenance: ProvenanceObserved}}
	if got := worstProvenance(envs); got != ProvenanceInferred {
		t.Errorf("worst provenance = %s", got)
	}
}

func TestOfferingRowTargetsUnknownWhenHidden(t *testing.T) {
	a := newAvailSnap()
	a.svc("pg", "postgresql")
	a.target("pg", "pg-0", "db", "prod", time.Hour)
	a.target("pg", "pg-1", "secret", "prod", time.Hour)
	if r := offeringRows(t, a.query(), EntityFilter{})["postgresql"]; r.Targets != 2 || r.TargetsUnknown {
		t.Errorf("full view row = %+v", r)
	}
	r := offeringRows(t, a.query().Restrict(func(ns string) bool { return ns != "secret" }), EntityFilter{})["postgresql"]
	if r.Targets != 0 || !r.TargetsUnknown {
		t.Errorf("restricted row = %+v", r)
	}
	if blob, _ := json.Marshal(r); !strings.Contains(string(blob), `"targetsUnknown":true`) {
		t.Errorf("row JSON = %s", blob)
	}
}

func TestOfferingRowTargetsUnknownWhenPartial(t *testing.T) {
	a := newAvailSnap()
	a.svc("pg", "postgresql")
	a.target("pg", "pg-0", "db", "prod", time.Hour)
	a.Sources = []SourceState{{ID: "cluster", Kind: SourceKindKubernetes, Status: SourceStale}}
	if r := offeringRows(t, a.query(), EntityFilter{})["postgresql"]; r.Targets != 0 || !r.TargetsUnknown {
		t.Errorf("partial row = %+v", r)
	}
}

func TestOfferingRowRestricted(t *testing.T) {
	a := newAvailSnap()
	a.svc("pg", "postgresql")
	a.target("pg", "pg-0", "db", "prod", time.Hour)
	a.dep("app", "pg", "db")
	a.target("app", "a-0", "apps", "prod", time.Hour)
	a.dep("secret-app", "pg", "db")
	a.dep("secret-job", "pg", "db")
	a.target("secret-app", "s-0", "secret", "prod", time.Hour)
	a.target("secret-job", "j-0", "secret", "prod", time.Hour)
	r := a.query().Restrict(func(ns string) bool { return ns != "secret" })
	row := offeringRows(t, r, EntityFilter{})["postgresql"]
	if row.Consumers != 1 || row.HiddenConsumers != 2 {
		t.Errorf("row = %+v", row)
	}
	if blob, _ := json.Marshal(row); strings.Contains(string(blob), "secret") {
		t.Errorf("a hidden consumer leaks: %s", blob)
	}
}

func TestEntityFilterLifecycleOffering(t *testing.T) {
	q := rowFleet(t)
	if rows := offeringRows(t, q, EntityFilter{Lifecycle: LifecycleDeprecated}); len(rows) != 1 || rows["postgresql"].Key == "" {
		t.Errorf("deprecated = %v", slices.Collect(maps.Keys(rows)))
	}
	if rows := offeringRows(t, q, EntityFilter{Lifecycle: LifecycleSunset}); len(rows) != 1 || rows["cache"].Key == "" {
		t.Errorf("sunset = %v", slices.Collect(maps.Keys(rows)))
	}
}

func TestEntityFilterLifecycleService(t *testing.T) {
	q := rowFleet(t)
	l, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindService, KindOwner}, Lifecycle: LifecycleDeprecated})
	if err != nil {
		t.Fatal(err)
	}
	if l.Total != 1 || l.Entities[0].Key != string(NewServiceKeyDomain(acme, "pg")) {
		t.Errorf("deprecated services = %+v", l.Entities)
	}
}

func TestEntityFilterLifecycleInvalid(t *testing.T) {
	q := rowFleet(t)
	for _, f := range []EntityFilter{
		{Kinds: []EntityKind{KindOffering}, Lifecycle: "supported"},
		{Kinds: []EntityKind{KindTarget}, Lifecycle: LifecycleDeprecated},
	} {
		var iq *InvalidQueryError
		if _, err := q.Entities(f); !errors.As(err, &iq) || iq.Field != "lifecycle" {
			t.Errorf("%+v: err = %v", f, err)
		}
	}
}

func TestEntityFilterFailed(t *testing.T) {
	q := failedFleet(t)
	if rows := offeringRows(t, q, EntityFilter{Failed: true}); len(rows) != 1 || rows["postgresql"].FailedRequests != 1 {
		t.Errorf("failed = %+v", rows)
	}
	if rows := offeringRows(t, rowFleet(t), EntityFilter{Failed: true}); len(rows) != 0 {
		t.Errorf("no failure = %+v", rows)
	}
	// A service never matches failed, even on a mixed query.
	l, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindService, KindOffering}, Failed: true})
	if err != nil || l.Total != 1 || l.Entities[0].Kind != KindOffering {
		t.Errorf("mixed = %+v, %v", l, err)
	}
}

func TestEntityFilterFailedNeedsOffering(t *testing.T) {
	q := rowFleet(t)
	for _, kinds := range [][]EntityKind{nil, {KindService}} {
		var iq *InvalidQueryError
		if _, err := q.Entities(EntityFilter{Kinds: kinds, Failed: true}); !errors.As(err, &iq) || iq.Field != "failed" || iq.Reason != "failed keeps offerings; ask kind offering" {
			t.Errorf("%v: err = %v", kinds, err)
		}
	}
}

func sortedKeys(t *testing.T, q *Query, sort string) []string {
	t.Helper()
	l, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindOffering}, Sort: sort})
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, r := range l.Entities {
		keys = append(keys, r.Key)
	}
	return keys
}

func TestEntitiesSortFailed(t *testing.T) {
	refs := []EntityRef{{Label: "b"}, {Label: "c", FailedRequests: 2}, {Label: "a"}, {Label: "d", FailedRequests: 1}}
	sortEntityList(refs, SortFailed)
	if got := fmt.Sprint(labels(refs)); got != "[c d a b]" {
		t.Errorf("order = %s", got)
	}
	if got := sortedKeys(t, failedFleet(t), SortFailed); len(got) != 1 {
		t.Errorf("served = %v", got)
	}
}

func TestEntitiesSortConsumers(t *testing.T) {
	if got := fmt.Sprint(sortedKeys(t, rowFleet(t), SortConsumers)); got != "[postgresql cache queue]" {
		t.Errorf("order = %s", got)
	}
}

func labels(refs []EntityRef) []string {
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.Label
	}
	return out
}

// TestOfferingListOneUsesPass: an offering list over 10,000 relationships
// across 50 offerings reads the uses once, aggregate included.
func TestOfferingListOneUsesPass(t *testing.T) {
	a := newAvailSnap()
	for i := range 50 {
		a.svc(fmt.Sprintf("p%d", i), fmt.Sprintf("o%d", i))
	}
	for i := range 10000 {
		a.dep(fmt.Sprintf("c%d", i%400), fmt.Sprintf("p%d", i%50), fmt.Sprintf("d%d", i))
	}
	q := a.query()
	before := offeringUsesInCalls.Load()
	l, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindOffering}, Limit: MaxEntityLimit})
	if err != nil {
		t.Fatal(err)
	}
	if n := offeringUsesInCalls.Load() - before; n != 1 || l.Total != 50 || l.Aggregate.OfferingCount != 50 {
		t.Errorf("uses passes = %d, total %d", n, l.Total)
	}
}
