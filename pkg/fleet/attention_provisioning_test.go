package fleet

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func provisioningItems(t *testing.T, q *Query) []AttentionItem {
	t.Helper()
	return attentionOf(t, q, AttentionFilter{Category: categoryProvisioning}).Items
}

func staleAt(r *RawProvisioning) {
	old := fixedNow().Add(-3 * time.Hour)
	r.ObservedAt, r.AcceptedAt = old, &old
}

func TestProvisioningAttentionFailed(t *testing.T) {
	q := NewQuery(provisioningSnap(t, 0, mailFleet(notifier("1.0.0", false, "production")), []RawProvisioning{mailReport(failed)}))
	items := provisioningItems(t, q)
	if len(items) != 1 {
		t.Fatalf("items = %+v", items)
	}
	it := items[0]
	if it.Code != codeProvisioningFailed || it.Severity != severityError || it.Service != notifierKey || it.Entity.Label != "notifier" ||
		it.Label != "notifier: email-provider in production is Failed" || it.Source != "evidence-0" {
		t.Errorf("item = %+v", it)
	}
	for _, want := range []string{"aws (mail-1)", "production", "signed", fixedNow().Add(-time.Hour).Format(time.RFC3339)} {
		if !strings.Contains(it.Reason, want) {
			t.Errorf("reason %q lacks %q", it.Reason, want)
		}
	}
	if it.NextStep != "Check aws's status for mail-1; resize or delete runs in the provider's tools" {
		t.Errorf("next step %q", it.NextStep)
	}
}

func TestProvisioningAttentionStale(t *testing.T) {
	q := NewQuery(provisioningSnap(t, 2*time.Hour, mailFleet(notifier("1.0.0", false, "production")), []RawProvisioning{mailReport(staleAt)}))
	items := provisioningItems(t, q)
	if len(items) != 1 || items[0].Code != codeProvisioningStale || items[0].Severity != severityWarning ||
		items[0].Label != "notifier: email-provider in production has a stale report" || !strings.Contains(items[0].Reason, "older than the provisioning window") {
		t.Errorf("items = %+v", items)
	}
}

func TestProvisioningAttentionStaleOlderRevisionIgnored(t *testing.T) {
	q := NewQuery(provisioningSnap(t, 2*time.Hour, mailFleet(notifier("1.0.0", false, "production"), notifier("1.1.0", false, "production")),
		[]RawProvisioning{mailReport(staleAt)}))
	if items := provisioningItems(t, q); len(items) != 0 {
		t.Errorf("a stale report about an older revision raised %+v", items)
	}
}

func TestProvisioningAttentionIgnoresNeverReported(t *testing.T) {
	q := NewQuery(provisioningSnap(t, 0, mailFleet(notifier("1.0.0", true, "production"))))
	if items := provisioningItems(t, q); len(items) != 0 {
		t.Errorf("never-reported uses raised %+v", items)
	}
	// A report that is neither Failed nor stale raises nothing either.
	q = NewQuery(provisioningSnap(t, 0, mailFleet(notifier("1.0.0", false, "production")), []RawProvisioning{mailReport()}))
	if items := provisioningItems(t, q); len(items) != 0 {
		t.Errorf("a Ready report raised %+v", items)
	}
}

func TestProvisioningAttentionUnsignedSaysSo(t *testing.T) {
	noRef := func(r *RawProvisioning) { r.Verified, r.ExternalRef = false, "" }
	q := NewQuery(provisioningSnap(t, 0, mailFleet(notifier("1.0.0", false, "production")), []RawProvisioning{mailReport(failed, noRef)}))
	items := provisioningItems(t, q)
	if len(items) != 1 || !strings.Contains(items[0].Reason, "aws reported Failed in production, unsigned") ||
		items[0].NextStep != "Check aws's status for this resource; resize or delete runs in the provider's tools" {
		t.Errorf("items = %+v", items)
	}
}

func TestProvisioningAttentionEntryNamed(t *testing.T) {
	n := notifier("1.0.0", true)
	n.Bundle.Contract.Configurations = append(n.Bundle.Contract.Configurations, n.Bundle.Contract.Configurations[0])
	n.Bundle.Contract.Configurations[1].Name = "cache"
	q := NewQuery(provisioningSnap(t, 0, mailFleet(n), []RawProvisioning{report(dbReport, failed, func(r *RawProvisioning) { r.Environment = "" })}))
	items := provisioningItems(t, q)
	if len(items) != 1 || items[0].Label != "notifier: postgresql in default for db is Failed" {
		t.Errorf("items = %+v", items)
	}
}

func TestProvisioningAttentionRestricted(t *testing.T) {
	base := mailFleet(notifier("1.0.0", false, "production"))
	k := SourceKindKubernetes
	base.Targets = []RawTarget{
		{Scope: "hush", Kind: k, Name: "notifier", Service: "notifier", Domain: acme, Digest: testDigest(acme, "notifier", "1.0.0"), Compliance: StatusCompliant},
		{Scope: "shop", Kind: k, Name: "email-provider", Service: "email-provider", Domain: acme, Compliance: StatusCompliant},
	}
	q := NewQuery(provisioningSnap(t, 0, base, []RawProvisioning{mailReport(failed)}))
	if len(provisioningItems(t, q)) != 1 {
		t.Fatal("the unrestricted view raises the failed resource")
	}
	if items := provisioningItems(t, q.Restrict(func(ns string) bool { return ns != "hush" })); len(items) != 0 {
		t.Errorf("a hidden consumer's report raised %+v", items)
	}
}

func TestProvisioningAttentionHiddenProviderStillRaised(t *testing.T) {
	r := hiddenMailFleet(t, "", mailReport(failed))
	items := provisioningItems(t, r)
	if len(items) != 1 || items[0].Code != codeProvisioningFailed || items[0].Label != "notifier: a hidden provider in production is Failed" ||
		!strings.Contains(items[0].Reason, hiddenMessage) || items[0].NextStep != "Ask the provider's owner to check this resource; resize or delete runs in the provider's tools" {
		t.Fatalf("items = %+v", items)
	}
	b, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{string(NewServiceKeyDomain(acme, "email-provider")), "email-provider", "aws", "mail-1"} {
		if strings.Contains(string(b), leak) {
			t.Errorf("hidden provider item leaks %q: %s", leak, b)
		}
	}
}

func TestStatusIncludesProvisioning(t *testing.T) {
	q := NewQuery(provisioningSnap(t, 0, mailFleet(notifier("1.0.0", false, "production")), []RawProvisioning{mailReport(failed)}))
	has := func(sq StatusQuery) bool {
		return slices.ContainsFunc(q.Status(sq).Items, func(it StatusItem) bool { return it.Code == codeProvisioningFailed })
	}
	if !has(StatusQuery{NeedsAttention: true}) || !has(StatusQuery{}) {
		t.Error("status omits the failed resource")
	}
	if has(StatusQuery{NonCompliant: true}) {
		t.Error("the non-compliant filter returns the failed resource")
	}
}

func TestProvisioningAttentionSkipsGoldenPath(t *testing.T) {
	path := notifier("1.0.0", false, "production")
	path.Bundle.Contract.Service.Template = templated("x", "1.0.0").Service.Template
	q := NewQuery(provisioningSnap(t, 0, mailFleet(path), []RawProvisioning{mailReport(failed)}))
	if items := provisioningItems(t, q); len(items) != 0 {
		t.Errorf("a golden path raised %+v", items)
	}
}

func TestFailedResourceLeavesServiceStatus(t *testing.T) {
	base := func() *Collection {
		c := mailFleet(notifier("1.0.0", false, "production"))
		c.Targets = []RawTarget{{Scope: "shop", Kind: SourceKindKubernetes, Name: "notifier", Service: "notifier", Domain: acme,
			Digest: testDigest(acme, "notifier", "1.0.0"), Compliance: StatusCompliant}}
		return c
	}
	state := func(q *Query) string {
		s := q.Overview().Summary
		return fmt.Sprintf("%v %d %d %d", q.snap.Services[ServiceKey(notifierKey)].Status, s.CompliantTargets, s.NonCompliantTargets, s.UnknownTargets)
	}
	without := state(NewQuery(provisioningSnap(t, 0, base())))
	with := NewQuery(provisioningSnap(t, 0, base(), []RawProvisioning{mailReport(failed)}))
	if len(provisioningItems(t, with)) != 1 {
		t.Fatal("the failed report raised no item")
	}
	if got := state(with); got != without {
		t.Errorf("a failed resource changed the service status: %q, want %q", got, without)
	}
}

func TestAttentionCategoryOrderHasProvisioning(t *testing.T) {
	cats := AttentionCategoriesList()
	if i := slices.Index(cats, categoryProvisioning); i < 1 || cats[i-1] != categoryInvalid {
		t.Errorf("provisioning is not right after invalid: %v", cats)
	}
}
