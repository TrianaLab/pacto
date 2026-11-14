package fleet

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// mailProvider is email-provider, a service that declares the offering
// (none when empty) and no configuration.
func mailProvider(offering string) RawRevision {
	return ociRev(acme, svc24("email-provider", "1.0.0", offering), nil)
}

// notifier is a revision of notifier declaring envs that depends on
// email-provider as email-provider and, with pg, references postgresql's configuration
// as db.
func notifier(version string, pg bool, envs ...string) RawRevision {
	c := svc24("notifier", version, "")
	c.Dependencies = []contract.Dependency{{Name: "email-provider", Ref: "oci://" + acme + "/email-provider", Required: true, Compatibility: "^1.0.0"}}
	if pg {
		c.Configurations = []contract.Configuration{{Name: "db", Ref: pgRef("postgresql")}}
	}
	for _, e := range envs {
		c.Environments = append(c.Environments, contract.Environment{Name: e, File: e + ".yaml"})
	}
	return ociRev(acme, c, nil)
}

// mailReport is a fresh, signed report about notifier 1.0.0's email-provider
// dependency in production.
func mailReport(mods ...func(*RawProvisioning)) RawProvisioning {
	return report(append([]func(*RawProvisioning){func(r *RawProvisioning) {
		r.Service, r.Digest, r.Kind, r.Name, r.ExternalRef = "notifier", testDigest(acme, "notifier", "1.0.0"), "dependency", "email-provider", "mail-1"
	}}, mods...)...)
}

func failed(r *RawProvisioning) { r.Phase = "Failed" }

var notifierKey = string(NewServiceKeyDomain(acme, "notifier"))

func notifierDetail(t *testing.T, q *Query) *EntityDetail {
	t.Helper()
	d, err := q.EntityDetail(KindService, notifierKey)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// mailUse is notifier's use of email-provider, nil when absent.
func mailUse(d *EntityDetail) *OfferingUse {
	for i, u := range d.Service.ConsumedOfferings.Items {
		if u.Provider.Label == "email-provider" {
			return &d.Service.ConsumedOfferings.Items[i]
		}
	}
	return nil
}

func mailFleet(revs ...RawRevision) *Collection {
	return &Collection{Revisions: append([]RawRevision{mailProvider(""), provider("postgresql")}, revs...)}
}

func TestServiceUsesKeepReportedNonOfferingProvider(t *testing.T) {
	q := NewQuery(provisioningSnap(t, 0, mailFleet(notifier("1.0.0", false, "production")), []RawProvisioning{mailReport(failed)}))
	d := notifierDetail(t, q)
	u := mailUse(d)
	if u == nil || u.Offering != "" || u.Via != RelationshipDependency || len(u.Provisioning) != 1 || u.Provisioning[0].Phase != "Failed" {
		t.Fatalf("use = %+v in %+v", u, d.Service.ConsumedOfferings.Items)
	}
	if p := d.Service.ConsumedOfferings; !slices.Equal(refLabels(p.Failed.Items), []string{"notifier"}) || p.Phases.Failed != 1 {
		t.Errorf("preview failed %+v phases %+v", p.Failed, p.Phases)
	}
}

func refLabels(refs []EntityRef) []string {
	var out []string
	for _, r := range refs {
		out = append(out, r.Label)
	}
	return out
}

func TestServiceUsesSkipUnreportedPlainDependency(t *testing.T) {
	d := notifierDetail(t, NewQuery(provisioningSnap(t, 0, mailFleet(notifier("1.0.0", true, "production")))))
	if u := mailUse(d); u != nil {
		t.Errorf("a plain dependency nothing reported on is a resource: %+v", u)
	}
	if len(d.Service.ConsumedOfferings.Items) != 1 {
		t.Errorf("the postgresql use is gone: %+v", d.Service.ConsumedOfferings.Items)
	}
}

func TestServiceUsesStaleReportKeepsNonOffering(t *testing.T) {
	old := fixedNow().Add(-3 * time.Hour)
	q := NewQuery(provisioningSnap(t, 2*time.Hour, mailFleet(notifier("1.0.0", false, "production")),
		[]RawProvisioning{mailReport(failed, func(r *RawProvisioning) { r.ObservedAt, r.AcceptedAt = old, &old })}))
	u := mailUse(notifierDetail(t, q))
	if u == nil || u.Provisioning[0].Phase != StatusUnknown || !u.Provisioning[0].Stale || u.Provisioning[0].ReportedPhase != "Failed" {
		t.Errorf("stale use = %+v", u)
	}
}

func TestServiceUsesKeepConflictingNonOfferingProvider(t *testing.T) {
	q := NewQuery(provisioningSnap(t, 0, mailFleet(notifier("1.0.0", false, "production")), []RawProvisioning{mailReport()}, []RawProvisioning{mailReport(failed)}))
	d := notifierDetail(t, q)
	u := mailUse(d)
	if u == nil || u.Provisioning[0].Conflict == nil || u.Provisioning[0].Source != "" {
		t.Fatalf("conflicting use = %+v", u)
	}
	var msgs []string
	for _, l := range d.Meta.Limitations {
		if l.Code == LimitationProvisioningConflict {
			msgs = append(msgs, l.Message)
		}
	}
	if len(msgs) != 1 || !strings.Contains(msgs[0], "notifier on email-provider") {
		t.Errorf("conflict limitations = %q", msgs)
	}
}

func TestOfferingPageIgnoresNonOfferingProvider(t *testing.T) {
	base := mailFleet(notifier("1.0.0", true, "production"))
	reports := []RawProvisioning{mailReport(failed), report(func(r *RawProvisioning) {
		r.Service, r.Digest, r.Kind, r.Name = "notifier", testDigest(acme, "notifier", "1.0.0"), "configuration", "db"
	})}
	q := NewQuery(provisioningSnap(t, 0, base, reports))
	d, err := q.EntityDetail(KindOffering, "postgresql")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range d.Offering.Consumers.Items {
		if u.Provider.Label != "postgresql" {
			t.Errorf("offering page lists %+v", u)
		}
	}
	if d.Offering.Consumers.Total != 1 {
		t.Errorf("consumers = %+v", d.Offering.Consumers)
	}
	l, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindOffering}})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entities) != 1 || l.Entities[0].Key != "postgresql" || l.Entities[0].Consumers != 1 || l.Entities[0].FailedRequests != 0 {
		t.Errorf("offering list = %+v", l.Entities)
	}
	if got := keysOf(t, q, EntityFilter{Kinds: []EntityKind{KindService}, Consumes: "postgresql", Phase: PhaseBucketFailed}); len(got) != 0 {
		t.Errorf("the email failure counts on postgresql: %v", got)
	}
}

func keysOf(t *testing.T, q *Query, f EntityFilter) []string {
	t.Helper()
	l, err := q.Entities(f)
	if err != nil {
		t.Fatal(err)
	}
	return refLabels(l.Entities)
}

func TestPlacementCountsNonOfferingProvisioning(t *testing.T) {
	q := NewQuery(provisioningSnap(t, 0, mailFleet(notifier("1.0.0", false, "production")), []RawProvisioning{mailReport(failed)}))
	it := placementItem(t, servicePlacement(t, q, notifierKey), "production")
	if !it.ProvisioningTracked || it.ProvisioningReason != "" || (it.Provisioning != PhaseTally{Failed: 1}) {
		t.Errorf("production = %+v", it)
	}
}

func TestPlacementNoProvisioningHereStillTrue(t *testing.T) {
	q := NewQuery(provisioningSnap(t, 0, mailFleet(notifier("1.0.0", false, "production"))))
	if it := placementItem(t, servicePlacement(t, q, notifierKey), "production"); it.ProvisioningTracked || it.ProvisioningReason != noProvisioningHere {
		t.Errorf("production = %+v", it)
	}
}

func TestPlacementSeedFollowsNonOfferingUse(t *testing.T) {
	running := notifier("1.0.0", false, "production", "staging")
	published := notifier("1.1.0", false, "production")
	base := mailFleet(running, published)
	base.Targets = []RawTarget{{Scope: "prod", Kind: "k8s", Name: "notifier", Service: "notifier", Domain: acme, Digest: testDigest(acme, "notifier", "1.0.0"),
		Compliance: StatusCompliant, Labels: map[string]string{TargetEnvironmentLabel: "staging"}}}
	q := NewQuery(provisioningSnap(t, 0, base, []RawProvisioning{mailReport(func(r *RawProvisioning) { r.Digest = testDigest(acme, "notifier", "1.1.0") })}))
	st := placementItem(t, servicePlacement(t, q, notifierKey), "staging")
	if st.ProvisioningTracked || !strings.Contains(st.ProvisioningReason, "seeded from notifier 1.1.0") {
		t.Errorf("staging = tracked %v, reason %q", st.ProvisioningTracked, st.ProvisioningReason)
	}
}

// hiddenMailFleet runs email-provider (declaring offering) and postgresql in
// the hush namespace and notifier, which reports name, in shop, restricted to
// shop.
func hiddenMailFleet(t *testing.T, offering string, reports ...RawProvisioning) *Query {
	t.Helper()
	base := &Collection{Revisions: []RawRevision{mailProvider(offering), provider("postgresql"), notifier("1.0.0", true, "production")}}
	k := SourceKindKubernetes
	base.Targets = []RawTarget{
		{Scope: "hush", Kind: k, Name: "email-provider", Service: "email-provider", Domain: acme, Compliance: StatusCompliant},
		{Scope: "hush", Kind: k, Name: "postgresql", Service: "postgresql", Domain: acme, Compliance: StatusCompliant},
		{Scope: "shop", Kind: k, Name: "notifier", Service: "notifier", Domain: acme, Digest: testDigest(acme, "notifier", "1.0.0"), Compliance: StatusCompliant},
	}
	return NewQuery(provisioningSnap(t, 0, base, reports)).Restrict(func(ns string) bool { return ns != "hush" })
}

func dbReport(r *RawProvisioning) {
	r.Service, r.Digest, r.Kind, r.Name = "notifier", testDigest(acme, "notifier", "1.0.0"), "configuration", "db"
}

func TestHiddenProviderCounted(t *testing.T) {
	d := notifierDetail(t, hiddenMailFleet(t, "", report(dbReport)))
	if d.Service.HiddenProviders != 1 || len(d.Service.ConsumedOfferings.Items) != 0 || d.Service.HiddenDependencies != 2 {
		t.Errorf("hidden providers %d, hidden deps %d, uses %+v", d.Service.HiddenProviders, d.Service.HiddenDependencies, d.Service.ConsumedOfferings.Items)
	}
	assertNotNamed(t, d, "postgresql")
}

func TestHiddenReportedNonOfferingProviderCounted(t *testing.T) {
	d := notifierDetail(t, hiddenMailFleet(t, "", mailReport(failed)))
	if d.Service.HiddenProviders != 1 || len(d.Service.ConsumedOfferings.Items) != 0 {
		t.Errorf("hidden providers %d, uses %+v", d.Service.HiddenProviders, d.Service.ConsumedOfferings.Items)
	}
	if d := notifierDetail(t, hiddenMailFleet(t, "", mailReport(failed), report(dbReport))); d.Service.HiddenProviders != 2 {
		t.Errorf("two reported hidden providers: %d", d.Service.HiddenProviders)
	}
}

func TestHiddenProviderCountIgnoresProviderOffering(t *testing.T) {
	marshal := func(offering string, reports ...RawProvisioning) string {
		d := notifierDetail(t, hiddenMailFleet(t, offering, reports...))
		b, err := json.Marshal(d.Service)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if a, b := marshal("", mailReport()), marshal("mail", mailReport()); a != b {
		t.Errorf("the hidden provider's offering shows:\n%s\n%s", a, b)
	}
	d := notifierDetail(t, hiddenMailFleet(t, "mail"))
	if d.Service.HiddenProviders != 0 || d.Service.HiddenDependencies != 2 {
		t.Errorf("no report: hidden providers %d, hidden deps %d", d.Service.HiddenProviders, d.Service.HiddenDependencies)
	}
}

// assertNotNamed fails when the detail names the hidden provider: its service
// key, entity key or revision digest.
func assertNotNamed(t *testing.T, d *EntityDetail, name string) {
	t.Helper()
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{`"` + string(NewServiceKeyDomain(acme, name)) + `"`, `"label":"` + name, testDigest(acme, name, "1.0.0")} {
		if strings.Contains(string(b), leak) {
			t.Errorf("detail names %q", leak)
		}
	}
}

func TestHiddenProviderNotNamed(t *testing.T) {
	d := notifierDetail(t, hiddenMailFleet(t, "", mailReport(failed), report(dbReport)))
	assertNotNamed(t, d, "email-provider")
	assertNotNamed(t, d, "postgresql")
}

func TestPlacementHiddenProviderIsUnknownNotNone(t *testing.T) {
	r := hiddenMailFleet(t, "", mailReport(failed))
	if it := placementItem(t, servicePlacement(t, r, notifierKey), "production"); it.ProvisioningTracked || it.ProvisioningReason != hiddenProviderMayProvision {
		t.Errorf("production = %+v", it)
	}
}

// A golden path's edges to a hidden service count as hidden requirements
// only: restriction leaves them as before and the path's service detail
// counts no hidden provider.
func TestTemplateEdgesRestrictedUnchanged(t *testing.T) {
	r := goldenRestrictFleet(t, templated("http-service", "1.0.0")).Restrict(shopOnly)
	d, err := r.EntityDetail(KindService, string(NewServiceKeyDomain(acme, "http-service")))
	if err != nil {
		t.Fatal(err)
	}
	if tpl := d.Service.Template; tpl == nil || tpl.HiddenRequirements != 1 || tpl.Requirements.Total != 0 {
		t.Fatalf("template = %+v", d.Service.Template)
	}
	if d.Service.HiddenProviders != 0 {
		t.Errorf("the golden path counts %d hidden providers", d.Service.HiddenProviders)
	}
}
