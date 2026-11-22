package fleet

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func linksSource(links map[string]ClusterLink) Source {
	return NewMemorySource("member-links", SourceKindMemberLinks, &Collection{Advisory: true, ClusterLinks: links})
}

func liveLink(models ...IssuerModel) ClusterLink {
	return ClusterLink{Channel: LinkLive, Since: new(fixedNow()), Issuers: models}
}

func linkedFleet(t *testing.T, links map[string]ClusterLink) *Query {
	t.Helper()
	eu, us := clusterOf(clusterA, "eu-prod", ClusterNameFromApproval), clusterOf(clusterB, "us-prod", ClusterNameFromApproval)
	return NewQuery(buildClusterSnap(t,
		NewMemorySource("eu", SourceKindTargetSnapshot, &Collection{Cluster: eu, Targets: []RawTarget{workload("prod", "payments")}}),
		NewMemorySource("us", SourceKindTargetSnapshot, &Collection{Cluster: us, Targets: []RawTarget{workload("prod", "orders")}}),
		linksSource(links),
	))
}

func TestBuildCopiesClusterLink(t *testing.T) {
	link := liveLink(IssuerModel{Issuer: "https://login.acme.dev", Model: IssuerShared})
	q := linkedFleet(t, map[string]ClusterLink{clusterA: link})
	if got := clusterRecord(q.snap, clusterA).Link; got == nil || !reflect.DeepEqual(*got, link) {
		t.Errorf("eu link = %+v, want %+v", got, link)
	}
	if got := clusterRecord(q.snap, clusterB).Link; got != nil {
		t.Errorf("us link = %+v, want none", got)
	}
}

func TestLinkForUnknownClusterDropped(t *testing.T) {
	q := linkedFleet(t, map[string]ClusterLink{"0000-unknown": liveLink()})
	for _, c := range q.snap.Clusters {
		if c.Link != nil {
			t.Errorf("%s carries %+v", c.ID, c.Link)
		}
	}
}

func TestMemberLinksSourceIsAdvisory(t *testing.T) {
	without := linkedFleet(t, nil)
	with := linkedFleet(t, map[string]ClusterLink{clusterA: {Channel: LinkDown, Since: new(fixedNow())}})
	if a, b := without.ProductMeta().Completeness, with.ProductMeta().Completeness; a != b {
		t.Errorf("completeness %s with a down link, %s without", b, a)
	}
	if a, b := without.snap.ConsumerCompleteness(), with.snap.ConsumerCompleteness(); a != b {
		t.Errorf("consumer completeness %s with a down link, %s without", b, a)
	}
}

func TestMemberLinksSourceNotListed(t *testing.T) {
	without := linkedFleet(t, nil)
	with := linkedFleet(t, map[string]ClusterLink{clusterA: liveLink()})
	for _, st := range with.snap.Sources {
		if st.Kind == SourceKindMemberLinks {
			t.Errorf("member-links listed as a source: %+v", st)
		}
	}
	if !reflect.DeepEqual(with.snap.Sources, without.snap.Sources) {
		t.Errorf("sources differ: %+v vs %+v", with.snap.Sources, without.snap.Sources)
	}
	a, b := without.Overview().Summary, with.Overview().Summary
	if a.DegradedSources != b.DegradedSources || a.StaleSources != b.StaleSources || a.UnavailableSources != b.UnavailableSources {
		t.Errorf("source counts differ: %+v vs %+v", a, b)
	}
	l, err := with.Entities(EntityFilter{Kinds: []EntityKind{KindSource}})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range l.Entities {
		if e.Key == "member-links" {
			t.Errorf("member-links is a source entity: %+v", e)
		}
	}
}

func TestClusterSummaryCarriesLink(t *testing.T) {
	link := liveLink(IssuerModel{Issuer: "https://login.acme.dev", Model: IssuerShared})
	q := linkedFleet(t, map[string]ClusterLink{clusterA: link})
	for _, c := range q.ProductMeta().Clusters {
		if want := c.ID == clusterA; (c.Link != nil) != want || (want && !reflect.DeepEqual(*c.Link, link)) {
			t.Errorf("summary %s link = %+v", c.Name, c.Link)
		}
	}
	if m, p := q.Status(StatusQuery{}).Meta.Clusters, q.ProductMeta().Clusters; !reflect.DeepEqual(m, p) {
		t.Errorf("Meta.Clusters %+v != ProductMeta.Clusters %+v", m, p)
	}
}

func TestRestrictKeepsLinkOnVisibleCluster(t *testing.T) {
	q := linkedFleet(t, map[string]ClusterLink{clusterA: liveLink(), clusterB: liveLink()})
	view := q.Restrict(func(cluster, _ string) bool { return cluster == clusterA }, nil)
	if len(view.snap.Clusters) != 1 || view.snap.Clusters[0].Link == nil {
		t.Errorf("restricted clusters = %+v, want eu with its link", view.snap.Clusters)
	}
}

func issuerItems(t *testing.T, q *Query) []AttentionItem {
	t.Helper()
	l, err := q.Attention(AttentionFilter{Category: categoryCluster})
	if err != nil {
		t.Fatal(err)
	}
	return l.Items
}

func TestAttentionIssuerDiffersNamesClusters(t *testing.T) {
	const iss = "https://login.acme.dev"
	q := linkedFleet(t, map[string]ClusterLink{
		clusterA: liveLink(IssuerModel{Issuer: iss, Model: IssuerNotAccepted}, IssuerModel{Issuer: "https://other", Model: IssuerShared}),
		clusterB: liveLink(IssuerModel{Issuer: iss, Model: IssuerGroupMapping, MappedBy: "oidc:alice@acme.dev"}),
	})
	items := issuerItems(t, q)
	if len(items) != 1 {
		t.Fatalf("items = %+v, want one for %s", items, iss)
	}
	it := items[0]
	if it.Code != CodeClusterIssuerDiffers || it.Severity != severityWarning || it.NextAction != nil || it.Label != "us-prod and eu-prod" ||
		it.Entity.Kind != KindSource || it.Entity.Key != "us" || !strings.Contains(it.Reason, iss) {
		t.Errorf("item = %+v", it)
	}
	if want := "pacto cluster map-groups " + clusterA + " --issuer " + iss; !strings.Contains(it.NextStep, want) {
		t.Errorf("next step %q lacks %q", it.NextStep, want)
	}
	if len(issuerItems(t, linkedFleet(t, map[string]ClusterLink{clusterA: liveLink(IssuerModel{Issuer: iss, Model: IssuerShared})}))) != 0 {
		t.Error("a shared issuer needs attention")
	}
}

func TestAttentionIssuerDiffersWithMappingNamesApprover(t *testing.T) {
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	q := linkedFleet(t, map[string]ClusterLink{clusterB: liveLink(IssuerModel{Issuer: "https://login.acme.dev", Model: IssuerGroupMapping,
		MappedBy: "oidc:alice@acme.dev", MappedAt: &at, Mapping: "platform=readers"})})
	items := issuerItems(t, q)
	want := "Reads in us-prod use the group mapping approved by oidc:alice@acme.dev on 2026-11-20. Approvals for these clusters stay with each cluster's own sign-in."
	if len(items) != 1 || items[0].NextStep != want {
		t.Errorf("items = %+v, want next step %q", items, want)
	}
}

func TestAttentionIssuerDiffersBounded(t *testing.T) {
	names := make([]string, 8)
	for i := range names {
		names[i] = "c" + strconv.Itoa(i)
	}
	if got, want := boundedNames(names), "c0, c1, c2, c3, c4 and 3 more"; got != want {
		t.Errorf("boundedNames = %q, want %q", got, want)
	}
	if got := boundedNames(names[:1]); got != "c0" {
		t.Errorf("one name = %q", got)
	}
}

func TestStatusIncludesIssuerDiffers(t *testing.T) {
	q := linkedFleet(t, map[string]ClusterLink{clusterA: liveLink(IssuerModel{Issuer: "https://login.acme.dev", Model: IssuerNotAccepted})})
	for _, sq := range []StatusQuery{{}, {NeedsAttention: true}} {
		found := false
		for _, it := range q.Status(sq).Items {
			found = found || it.Code == CodeClusterIssuerDiffers
		}
		if !found {
			t.Errorf("status %+v lacks %s", sq, CodeClusterIssuerDiffers)
		}
	}
}
