package fleet

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

var catalogKey = string(NewServiceKeyDomain(acme, "catalog"))

// ownedPG is postgresql 16.1.0, owned by team db, with a provisioning
// configuration and a policy.
func ownedPG() RawRevision {
	c := svc24("postgresql", "16.1.0", "postgresql")
	c.Service.Owner = contract.Owner{Team: "db"}
	c.Configurations = []contract.Configuration{{Name: "provisioning", Schema: "s.json"}}
	c.Policies = []contract.Policy{{Name: "p", Schema: "p.json"}}
	return ociRev(acme, c, nil)
}

// cacheProvider is redis 7.0.0, owned only through a contact.
func cacheProvider() RawRevision {
	c := svc24("redis", "7.0.0", "redis")
	c.Service.Owner = contract.Owner{Contacts: []contract.OwnerContact{{Type: "email", Value: "cache@acme.io"}}}
	return ociRev(acme, c, nil)
}

// catalog is catalog-service's shape: a dependency on postgresql, a
// configuration reference to it with request values, a policy reference to
// it and a dependency on redis.
func catalog() RawRevision {
	c := svc24("catalog", "1.0.0", "")
	c.Dependencies = []contract.Dependency{
		{Name: "postgresql", Ref: "oci://" + acme + "/postgresql", Required: true, Compatibility: "^16.0.0"},
		{Name: "redis", Ref: "oci://" + acme + "/redis", Required: true, Compatibility: "^7.0.0"},
	}
	c.Configurations = []contract.Configuration{{Name: "db", Ref: pgRef("postgresql"), ProviderConfiguration: "provisioning", Values: map[string]any{"size": "medium", "storageGi": 50}}}
	c.Policies = []contract.Policy{{Name: "guard", Ref: pgRef("postgresql")}}
	return ociRev(acme, c, nil)
}

func catalogQuery(t *testing.T, extra ...RawTarget) *Query {
	t.Helper()
	return NewQuery(provisioningSnap(t, 0, &Collection{Revisions: []RawRevision{ownedPG(), cacheProvider(), catalog()}, Targets: extra}))
}

// catalogUses is catalog's service-page uses keyed by provider label and via.
func catalogUses(t *testing.T, q *Query) map[string]OfferingUse {
	t.Helper()
	d, err := q.EntityDetail(KindService, catalogKey)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]OfferingUse{}
	for _, u := range d.Service.ConsumedOfferings.Items {
		out[u.Provider.Label+"/"+u.Via] = u
	}
	return out
}

func TestUseCarriesProviderOwner(t *testing.T) {
	uses := catalogUses(t, catalogQuery(t))
	pg := uses["postgresql/configuration_reference"].ProviderOwner
	if pg == nil || !pg.Declared || pg.Ref == nil || pg.Ref.Key != "team:db" {
		t.Errorf("postgresql owner = %+v", pg)
	}
	redis := uses["redis/dependency"].ProviderOwner
	if redis == nil || !redis.Declared || redis.Ref != nil {
		t.Errorf("redis owner = %+v", redis)
	}
}

func TestUseCarriesProviderAvailability(t *testing.T) {
	q := catalogQuery(t)
	want := q.providerAvailability(q.snap.Services[NewServiceKeyDomain(acme, "postgresql")])
	if got := catalogUses(t, q)["postgresql/dependency"].ProviderAvailability; got == nil || got.State != want.State || len(got.ByEnvironment) != len(want.ByEnvironment) {
		t.Errorf("availability = %+v, want %+v", got, want)
	}
	pgIn := func(ns string) RawTarget {
		return RawTarget{Scope: ns, Kind: SourceKindKubernetes, Name: "pg", Service: "postgresql", Domain: acme, Compliance: StatusCompliant}
	}
	hidden := catalogQuery(t, pgIn("db"), pgIn("secret")).Restrict(func(ns string) bool { return ns != "secret" })
	av := catalogUses(t, hidden)["postgresql/dependency"].ProviderAvailability
	if av == nil || av.State != AvailabilityUnknown || av.ByEnvironment[0].Reason != targetsIncompleteReason {
		t.Errorf("hidden-target availability = %+v", av)
	}
}

func TestRequestedJoinsConfigurationValues(t *testing.T) {
	u := catalogUses(t, catalogQuery(t))["postgresql/configuration_reference"]
	if len(u.Requested) != 1 || u.RequestedTruncated {
		t.Fatalf("requested = %+v", u.Requested)
	}
	r := u.Requested[0]
	if r.Entry != "db" || r.Configuration != "provisioning" || r.Provenance != ProvenanceDeclared || r.Revision == nil || r.Revision.Label != "postgresql 16.1.0" {
		t.Errorf("requested = %+v", r)
	}
	if got := factsOf(r.Values); !slices.Equal(got, []string{"size=medium", "storageGi=50"}) {
		t.Errorf("values = %v", got)
	}
}

func factsOf(p RuntimePreview) []string {
	var out []string
	for _, f := range p.Items {
		out = append(out, f.Key+"="+f.Value)
	}
	return out
}

func TestRequestedSkipsOtherProviders(t *testing.T) {
	uses := catalogUses(t, catalogQuery(t))
	for k, u := range uses {
		if k != "postgresql/configuration_reference" && u.Requested != nil {
			t.Errorf("%s requested %+v", k, u.Requested)
		}
	}
}

func TestRequestedOnlyOnConfigurationReferenceRow(t *testing.T) {
	q := catalogQuery(t)
	uses := catalogUses(t, q)
	if uses["postgresql/dependency"].Requested != nil || uses["postgresql/configuration_reference"].Requested == nil {
		t.Errorf("uses = %+v", uses)
	}
	pg := EntityRef{Kind: KindService, Key: string(NewServiceKeyDomain(acme, "postgresql"))}
	rows := []OfferingUse{{Consumer: EntityRef{Key: catalogKey}, Provider: pg, Via: RelationshipConfigRef}}
	cfgs := ConfigurationsPreview{Items: []ConfigurationSummary{
		{Name: "db", Resolution: &RefResolution{Resolved: true, Service: &pg}},
		{Name: "unnamed", Resolution: &RefResolution{Resolved: true, Service: &pg}},
		{Name: "local"},
	}}
	q.attachRequested(rows, cfgs)
	if len(rows[0].Requested) != 1 || rows[0].Requested[0].Entry != "db" {
		t.Errorf("an entry the use's edges do not name joined: %+v", rows[0].Requested)
	}
}

func TestRequestedTruncatedWhenPreviewTruncated(t *testing.T) {
	q := catalogQuery(t)
	rows := []OfferingUse{{Consumer: EntityRef{Key: catalogKey}, Provider: EntityRef{Key: "x"}, Via: RelationshipConfigRef}, {Via: RelationshipDependency}}
	q.attachRequested(rows, ConfigurationsPreview{Truncated: true})
	if !rows[0].RequestedTruncated || rows[1].RequestedTruncated {
		t.Errorf("rows = %+v", rows)
	}
}

func TestConsumptionNotReportedByDefault(t *testing.T) {
	uses := catalogUses(t, catalogQuery(t))
	if p := uses["postgresql/policy_reference"]; p.Consumption != nil {
		t.Errorf("a policy reference consumes: %+v", p.Consumption)
	}
	want := []ResourceConsumption{{State: "not-reported", Provenance: ProvenanceObserved, Reason: consumptionNotReportedReason}}
	for _, k := range []string{"postgresql/dependency", "postgresql/configuration_reference", "redis/dependency"} {
		if got := uses[k].Consumption; !slices.Equal(got, want) {
			t.Errorf("%s consumption = %+v", k, got)
		}
	}
	if len(uses) != 4 {
		t.Errorf("uses = %v", len(uses))
	}
}

func TestGoldenPathRequirementsCarryNoConsumption(t *testing.T) {
	d, err := goldenRestrictFleet(t, templated("http-service", "1.0.0")).EntityDetail(KindService, string(NewServiceKeyDomain(acme, "http-service")))
	if err != nil {
		t.Fatal(err)
	}
	reqs := d.Service.Template.Requirements
	if reqs.Total == 0 {
		t.Fatal("the golden path has no requirements")
	}
	for _, u := range reqs.Items {
		if u.Consumption != nil || u.Requested != nil || u.ProviderOwner != nil || u.ProviderAvailability != nil {
			t.Errorf("requirement = %+v", u)
		}
	}
}

// TestOfferingUseJSONKeepsFields: every key a use and its preview had before
// the resource facts keeps its value; the new keys are only added.
func TestOfferingUseJSONKeepsFields(t *testing.T) {
	q := catalogQuery(t)
	d, err := q.EntityDetail(KindService, catalogKey)
	if err != nil {
		t.Fatal(err)
	}
	bare, _ := q.offeringUses(func(*ServiceRecord) bool { return true }, NewServiceKeyDomain(acme, "catalog"))
	for i := range bare {
		bare[i].Consumption = nil
	}
	var before, after map[string]any
	unmarshalJSON(t, offeringUsesPreview(bare), &before)
	unmarshalJSON(t, d.Service.ConsumedOfferings, &after)
	for k, v := range before {
		if k == "items" {
			continue
		}
		if !jsonEqual(t, v, after[k]) {
			t.Errorf("%s: %v != %v", k, v, after[k])
		}
	}
	items, got := before["items"].([]any), after["items"].([]any)
	if len(items) != len(got) {
		t.Fatalf("items %d != %d", len(items), len(got))
	}
	for i, it := range items {
		for k, v := range it.(map[string]any) {
			if !jsonEqual(t, v, got[i].(map[string]any)[k]) {
				t.Errorf("items[%d].%s: %v != %v", i, k, v, got[i].(map[string]any)[k])
			}
		}
	}
}

func unmarshalJSON(t *testing.T, v any, out any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatal(err)
	}
}

func jsonEqual(t *testing.T, a, b any) bool {
	t.Helper()
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
