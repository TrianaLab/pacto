package fleet

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const acme = "ghcr.io/acme"

// pgRef pins the postgresql provider revision a consumer references.
func pgRef(name string) string {
	return "oci://" + acme + "/" + name + "@" + testDigest(acme, name, "16.1.0")
}

// provider is a postgresql offering provider with one configuration.
func provider(name string) RawRevision {
	c := svc25(name, "16.1.0", "postgresql")
	c.Configurations = []contract.Configuration{{Name: "db", Schema: "s.json"}}
	return ociRev(acme, c, nil)
}

// consumer is a revision of name that references providerName's configuration
// as db and depends on postgresql.
func consumer(name, version, providerName string) RawRevision {
	c := svc25(name, version, "")
	c.Configurations = []contract.Configuration{{Name: "db", Ref: pgRef(providerName)}}
	c.Dependencies = []contract.Dependency{{Name: "postgresql", Ref: "oci://" + acme + "/postgresql", Required: true, Compatibility: "^16.0.0"}}
	return ociRev(acme, c, nil)
}

// report is a fresh, signed Ready report for orders 1.3.0's db in production.
func report(mods ...func(*RawProvisioning)) RawProvisioning {
	at := fixedNow().Add(-time.Hour)
	r := RawProvisioning{
		Service: "orders", Domain: acme, Digest: testDigest(acme, "orders", "1.3.0"), Kind: "configuration", Name: "db",
		Environment: "production", Provider: "aws", ExternalRef: "orders-db", Phase: "Ready", Outcome: "Observed",
		Producer: "adapter", ProducerKeyID: "k1", ObservedAt: at, AcceptedAt: &at, Verified: true,
	}
	for _, m := range mods {
		m(&r)
	}
	return r
}

func provisioningSnap(t *testing.T, window time.Duration, base *Collection, reports ...[]RawProvisioning) *FleetSnapshot {
	t.Helper()
	sources := []Source{NewMemorySource("oci", "oci", base)}
	for i, recs := range reports {
		sources = append(sources, NewMemorySource(fmt.Sprintf("evidence-%d", i), "evidence-http", &Collection{Provisioning: recs}))
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, FreshnessWindow: window}, sources...)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func ordersFleet() *Collection {
	return &Collection{Revisions: []RawRevision{provider("postgresql"), consumer("orders", "1.2.0", "postgresql"), consumer("orders", "1.3.0", "postgresql")}}
}

// use is the offering use of consumer by via on the postgresql offering, and
// the detail's meta limitations.
func use(t *testing.T, q *Query, consumer, providerName, via string) (OfferingUse, []Limitation) {
	t.Helper()
	d, err := q.EntityDetail(KindOffering, "postgresql")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range d.Offering.Consumers.Items {
		if u.Consumer.Label == consumer && u.Provider.Label == providerName && u.Via == via {
			return u, d.Meta.Limitations
		}
	}
	t.Fatalf("no %s use of %s by %s in %+v", via, providerName, consumer, d.Offering.Consumers.Items)
	return OfferingUse{}, nil
}

// statusLine is the part of a status the table compares.
func statusLine(s ProvisioningStatus) string {
	rev := ""
	if s.Revision != nil {
		rev = s.Revision.Version
	}
	return fmt.Sprintf("%s %s reported=%s stale=%v conflict=%v verified=%v source=%s/%s rev=%s reason=%s",
		s.Environment, s.Phase, s.ReportedPhase, s.Stale, s.Conflict, s.Verified, s.Source, s.SourceKind, rev, s.Reason)
}

func TestOfferingUseProvisioning(t *testing.T) {
	stale := fixedNow().Add(-25 * time.Hour)
	old := fixedNow().Add(-2 * time.Hour)
	const (
		ready   = "production Ready reported=Ready stale=false conflict=[] verified=true source=evidence-0/evidence-http rev=1.3.0 reason="
		none    = " Unknown reported= stale=false conflict=[] verified=false source=/ rev= reason=no provider has reported provisioning for this use"
		stale13 = "production Unknown reported=Ready stale=true conflict=[] verified=true source=evidence-0/evidence-http rev=1.3.0 reason=the last report is older than the provisioning window"
		clash   = "production Unknown reported= stale=false conflict=[Failed Ready] verified=false source=/ rev= reason=the providers that report on this use disagree"
	)
	cases := []struct {
		name      string
		window    time.Duration
		reports   [][]RawProvisioning
		via       string
		want      []string
		conflicts int
	}{
		{name: "no report", want: []string{none}},
		{name: "fresh Ready", reports: [][]RawProvisioning{{report()}}, want: []string{ready}},
		{name: "stale Ready", window: 2 * time.Hour, reports: [][]RawProvisioning{{report(func(r *RawProvisioning) { r.ObservedAt, r.AcceptedAt = old.Add(-time.Hour), &old })}},
			want: []string{stale13}},
		{name: "window 0 defaults to a day", reports: [][]RawProvisioning{{report(func(r *RawProvisioning) { r.ObservedAt, r.AcceptedAt = stale, &stale })}}, want: []string{stale13}},
		{name: "outcome Insufficient", reports: [][]RawProvisioning{{report(func(r *RawProvisioning) {
			r.Outcome, r.Phase, r.Provider, r.ExternalRef, r.Environment = "Insufficient", "", "", "", ""
		})}}, want: []string{" Unknown reported= stale=false conflict=[] verified=true source=evidence-0/evidence-http rev=1.3.0 reason=the provider reported insufficient"}},
		{name: "two sources disagree", reports: [][]RawProvisioning{{report()}, {report(func(r *RawProvisioning) { r.Phase = "Failed" })}}, want: []string{clash}, conflicts: 1},
		{name: "two producers in one source disagree", reports: [][]RawProvisioning{{report(), report(func(r *RawProvisioning) { r.Phase, r.Producer = "Failed", "other" })}},
			want: []string{clash}, conflicts: 1},
		{name: "one producer's newest report wins", reports: [][]RawProvisioning{{report(func(r *RawProvisioning) { r.Phase, r.ObservedAt, r.AcceptedAt = "Provisioning", old, &old }), report()}},
			want: []string{ready}},
		{name: "agreeing reporters: the newest speaks", reports: [][]RawProvisioning{
			{report(func(r *RawProvisioning) { r.ObservedAt, r.AcceptedAt, r.Verified = old, nil, false })},
			{report(func(r *RawProvisioning) { r.ObservedAt, r.AcceptedAt = old.Add(-time.Hour), &old })},
			{report()},
		}, want: []string{strings.Replace(ready, "evidence-0", "evidence-2", 1)}},
		{name: "two environments", reports: [][]RawProvisioning{{report(), report(func(r *RawProvisioning) { r.Environment, r.Phase = "staging", "Provisioning" })}},
			want: []string{ready, "staging Provisioning reported=Provisioning stale=false conflict=[] verified=true source=evidence-0/evidence-http rev=1.3.0 reason="}},
		{name: "dependency form", via: RelationshipDependency, reports: [][]RawProvisioning{{report(func(r *RawProvisioning) { r.Kind, r.Name = "dependency", "postgresql" })}},
			want: []string{ready}},
		{name: "the dependency use ignores a configuration report", via: RelationshipDependency, reports: [][]RawProvisioning{{report()}}, want: []string{none}},
		{name: "a report for another name never matches", reports: [][]RawProvisioning{{report(func(r *RawProvisioning) { r.Kind, r.Name = "dependency", "postgresql" })}},
			want: []string{none}},
		{name: "report for an older consumer revision", reports: [][]RawProvisioning{{report(func(r *RawProvisioning) { r.Digest = testDigest(acme, "orders", "1.2.0") })}},
			want: []string{"production Unknown reported=Ready stale=false conflict=[] verified=true source=evidence-0/evidence-http rev=1.2.0 reason=last report is for 1.2.0; 1.3.0 has no report yet"}},
		{name: "file-sourced report is unverified", reports: [][]RawProvisioning{{report(func(r *RawProvisioning) { r.AcceptedAt, r.Verified = nil, false })}},
			want: []string{"production Ready reported=Ready stale=false conflict=[] verified=false source=evidence-0/evidence-http (unsigned) rev=1.3.0 reason="}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, lims := use(t, NewQuery(provisioningSnap(t, tc.window, ordersFleet(), tc.reports...)), "orders", "postgresql", cmp.Or(tc.via, RelationshipConfigRef))
			var got []string
			for _, s := range u.Provisioning {
				got = append(got, statusLine(s))
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("statuses:\n got %q\nwant %q", got, tc.want)
			}
			if n := countLimitations(lims, LimitationProvisioningConflict); n != tc.conflicts {
				t.Errorf("%d conflicts in %+v", n, lims)
			}
		})
	}
}

func countLimitations(ls []Limitation, code string) int {
	n := 0
	for _, l := range ls {
		if l.Code == code {
			n++
		}
	}
	return n
}

// A consumer that switched provider under the same configuration name never
// shows the old provider's Ready on the new provider's row.
func TestOfferingUseProvisioning_ProviderSwitch(t *testing.T) {
	base := &Collection{Revisions: []RawRevision{provider("postgresql"), provider("aurora"),
		consumer("orders", "1.2.0", "postgresql"), consumer("orders", "1.3.0", "aurora")}}
	q := NewQuery(provisioningSnap(t, 0, base, []RawProvisioning{report(func(r *RawProvisioning) { r.Digest = testDigest(acme, "orders", "1.2.0") })}))
	if u, _ := use(t, q, "orders", "aurora", RelationshipConfigRef); u.Provisioning[0].Phase != StatusUnknown || u.Provisioning[0].Source != "" {
		t.Errorf("aurora status = %+v", u.Provisioning)
	}
	if u, _ := use(t, q, "orders", "postgresql", RelationshipConfigRef); u.Provisioning[0].Phase != "Ready" {
		t.Errorf("postgresql status = %+v", u.Provisioning)
	}
}

// A policy reference provisions nothing, and a service's consumed offerings
// carry the same statuses and conflicts as the offering page.
func TestOfferingUseProvisioning_PolicyAndServiceDetail(t *testing.T) {
	p := svc25("postgresql", "16.1.0", "postgresql")
	p.Policies = []contract.Policy{{Name: "p", Schema: "p.json"}}
	c := svc25("orders", "1.3.0", "")
	c.Policies = []contract.Policy{{Name: "guard", Ref: pgRef("postgresql")}}
	c.Dependencies = []contract.Dependency{{Name: "postgresql", Ref: "oci://" + acme + "/postgresql", Required: true}}
	base := &Collection{Revisions: []RawRevision{ociRev(acme, p, nil), ociRev(acme, c, nil)}}
	dep := func(r *RawProvisioning) { r.Kind, r.Name = "dependency", "postgresql" }
	q := NewQuery(provisioningSnap(t, 0, base, []RawProvisioning{report(dep)}, []RawProvisioning{report(dep, func(r *RawProvisioning) { r.Phase = "Failed" })}))
	if u, _ := use(t, q, "orders", "postgresql", RelationshipPolicyRef); u.Provisioning != nil {
		t.Errorf("policy status = %+v", u.Provisioning)
	}
	d, err := q.EntityDetail(KindService, string(NewServiceKeyDomain(acme, "orders")))
	if err != nil {
		t.Fatal(err)
	}
	var dependency *OfferingUse
	for i, u := range d.Service.ConsumedOfferings.Items {
		if u.Via == RelationshipDependency {
			dependency = &d.Service.ConsumedOfferings.Items[i]
		}
	}
	if dependency == nil || dependency.Provisioning[0].Conflict == nil || countLimitations(d.Meta.Limitations, LimitationProvisioningConflict) != 1 {
		t.Errorf("service detail = %+v, meta %+v", d.Service.ConsumedOfferings.Items, d.Meta.Limitations)
	}
}

func TestOfferingUseProvisioningRestricted(t *testing.T) {
	billing := func(r *RawProvisioning) {
		r.Service, r.Digest, r.ExternalRef, r.Producer = "billing", testDigest(acme, "billing", "1.0.0"), "billing-db-secret-ref", "billing-adapter"
	}
	base := ordersFleet()
	base.Revisions = append(base.Revisions, consumer("billing", "1.0.0", "postgresql"))
	base.Targets = []RawTarget{
		{Scope: "open", Kind: SourceKindKubernetes, Name: "orders", Service: "orders", Domain: acme, Digest: testDigest(acme, "orders", "1.3.0")},
		{Scope: "secret", Kind: SourceKindKubernetes, Name: "billing", Service: "billing", Domain: acme, Digest: testDigest(acme, "billing", "1.0.0")},
	}
	snap := provisioningSnap(t, 0, base, []RawProvisioning{report(), report(billing)}, []RawProvisioning{report(billing, func(r *RawProvisioning) { r.Phase = "Failed" })})
	q := NewQuery(snap)
	if _, lims := use(t, q, "billing", "postgresql", RelationshipConfigRef); countLimitations(lims, LimitationProvisioningConflict) != 1 {
		t.Fatalf("the unrestricted view raises the billing conflict: %+v", lims)
	}
	// A report about a service the snapshot does not hold is dropped by Restrict too.
	snap.Provisioning = append(snap.Provisioning, ProvisioningRecord{ServiceKey: NewServiceKeyDomain(acme, "ghost"), Service: "ghost", Producer: "ghost-adapter", ExternalRef: "ghost-ref"})
	r := q.Restrict(func(ns string) bool { return ns != "secret" })
	d, err := r.EntityDetail(KindOffering, "postgresql")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range d.Offering.Consumers.Items {
		if u.Consumer.Label == "billing" {
			t.Errorf("hidden consumer use %+v", u)
		}
	}
	if countLimitations(d.Meta.Limitations, LimitationProvisioningConflict) != 0 {
		t.Errorf("restricted conflicts %+v", d.Meta.Limitations)
	}
	if u, _ := use(t, r, "orders", "postgresql", RelationshipConfigRef); u.Provisioning[0].Phase != "Ready" {
		t.Errorf("orders status = %+v", u.Provisioning)
	}
	data, err := json.Marshal(r.SnapshotForSerialization())
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"billing-db-secret-ref", "billing-adapter", "ghost", testDigest(acme, "billing", "1.0.0")} {
		if strings.Contains(string(data), leak) {
			t.Errorf("restricted snapshot leaks %q", leak)
		}
	}
}

// Restrict drops an orphaned report even when nothing else is hidden.
func TestRestrict_OrphanedProvisioningDropped(t *testing.T) {
	snap := provisioningSnap(t, 0, ordersFleet(), []RawProvisioning{report()})
	snap.Provisioning = append(snap.Provisioning, ProvisioningRecord{ServiceKey: "ghost", Service: "ghost"})
	r := NewQuery(snap).Restrict(func(string) bool { return true })
	if got := r.SnapshotForSerialization().Provisioning; len(got) != 1 || got[0].Service != "orders" {
		t.Errorf("provisioning = %+v", got)
	}
}
