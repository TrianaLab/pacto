package fleet

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// crossplaneRow is a detected crossplane row of cluster (id and name) whose
// evidence starts with value, as detect.Crossplane and ProviderRows write it.
func crossplaneRow(id, name string, status SourceStatus, value string) Provider {
	at := fixedNow()
	p := Provider{ID: ProviderCrossplane, Name: "Crossplane", Evidence: value + " (from detection, cluster " + name + ")", Status: status,
		Provenance: ProvenanceInferred, AsOf: &at, Cluster: name, ClusterID: id, UsedFor: []string{"provisioning engine"}}
	if !strings.HasPrefix(value, CrossplaneNoneFound) {
		p.Scope = ScopeCluster
	}
	return p
}

const (
	crossplaneRuns   = "Crossplane v2.1.0 runs and serves apiextensions.crossplane.io/v2"
	crossplaneNone   = CrossplaneNoneFound + " apiextensions.crossplane.io is not served"
	crossplaneNoCtrl = "Crossplane APIs served, controller not found serves apiextensions.crossplane.io/v2"
)

// idpQuery: orders (consuming postgresql's db, in environment production)
// runs in staging (uid-1, namespace team-a); api runs in prod-eu (uid-2,
// namespace shop). rows are detected rows, each from its own detection, as
// each cluster's operator reports its own; evidence, when set, is what an
// evidence source reports.
func idpQuery(t *testing.T, base *Collection, evidence *Collection, rows ...Provider) *Query {
	t.Helper()
	orders := workload("team-a", "orders")
	orders.Domain, orders.Labels = acme, map[string]string{TargetEnvironmentLabel: "production"}
	sources := []Source{
		NewMemorySource("oci", "oci", base),
		NewMemorySource("k8s-1", SourceKindKubernetes, &Collection{Cluster: clusterOf("uid-1", "staging", ClusterNameFromContext), Targets: []RawTarget{orders}}),
		NewMemorySource("k8s-2", SourceKindKubernetes, &Collection{Cluster: clusterOf("uid-2", "prod-eu", ClusterNameFromContext), Targets: []RawTarget{workload("shop", "api")}}),
	}
	for i, row := range rows {
		sources = append(sources, NewMemorySource(fmt.Sprintf("detection-%d", i), SourceKindDetection,
			&Collection{Advisory: true, Providers: []Provider{row}, State: &SourceState{Status: SourceAvailable}}))
	}
	if evidence != nil {
		sources = append(sources, NewMemorySource("evidence-0", "evidence-http", evidence))
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, sources...)
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

// suggestions is the Suggestion of each status of orders' db use.
func suggestions(t *testing.T, q *Query) []string {
	t.Helper()
	u, _ := use(t, q, "orders", "postgresql", RelationshipConfigRef)
	out := make([]string, len(u.Provisioning))
	for i, st := range u.Provisioning {
		out[i] = st.Suggestion
	}
	return out
}

func wantSuggestion(t *testing.T, q *Query, want string) {
	t.Helper()
	for _, got := range suggestions(t, q) {
		if got != want {
			t.Errorf("suggestion = %q, want %q", got, want)
		}
	}
}

func TestProvisioningSuggestionCrossplaneDetected(t *testing.T) {
	q := idpQuery(t, ordersFleet(), nil, crossplaneRow("uid-2", "prod-eu", SourceAvailable, crossplaneRuns))
	wantSuggestion(t, q, "Crossplane detected in prod-eu: accept its inferred offerings")
}

func TestProvisioningSuggestionNoProvisionerFound(t *testing.T) {
	q := idpQuery(t, ordersFleet(), nil, crossplaneRow("uid-1", "staging", SourceAvailable, crossplaneNone),
		crossplaneRow("uid-2", "prod-eu", SourceAvailable, crossplaneNone))
	wantSuggestion(t, q, suggestNoProvisioner)
	if !strings.Contains(suggestNoProvisioner, "https://pacto.run/latest/provision-infrastructure/#write-an-adapter") {
		t.Errorf("no doc link in %q", suggestNoProvisioner)
	}
}

// Amended AC4: a third-party adapter that reported counts as a provisioner,
// so a none-found row suggests nothing.
func TestProvisioningSuggestionThirdPartyAdapterReportedNoCrossplane(t *testing.T) {
	base := ordersFleet()
	base.Revisions = append(base.Revisions, consumer("billing", "1.0.0", "postgresql"))
	billing := report(func(r *RawProvisioning) {
		r.Service, r.Digest, r.Producer = "billing", testDigest(acme, "billing", "1.0.0"), "ack-adapter"
	})
	q := idpQuery(t, base, &Collection{Provisioning: []RawProvisioning{billing}}, crossplaneRow("uid-1", "staging", SourceAvailable, crossplaneNone),
		crossplaneRow("uid-2", "prod-eu", SourceAvailable, crossplaneNone))
	wantSuggestion(t, q, "")
}

func TestProvisioningSuggestionStaleCrossplaneRowSuggestsNothing(t *testing.T) {
	wantSuggestion(t, idpQuery(t, ordersFleet(), nil, crossplaneRow("uid-2", "prod-eu", SourceStale, crossplaneRuns)), "")
	wantSuggestion(t, idpQuery(t, ordersFleet(), nil, crossplaneRow("uid-2", "prod-eu", SourceStale, crossplaneNone)), "")
}

func TestProvisioningSuggestionDiscoveryUnknownSuggestsNothing(t *testing.T) {
	wantSuggestion(t, idpQuery(t, ordersFleet(), nil, crossplaneRow("uid-2", "prod-eu", SourceUnavailable, "API discovery failed")), "")
	// No row at all is unknown too.
	wantSuggestion(t, idpQuery(t, ordersFleet(), nil), "")
}

func TestProvisioningSuggestionHidesCrossplaneOfHiddenCluster(t *testing.T) {
	q := idpQuery(t, ordersFleet(), nil, crossplaneRow("uid-2", "prod-eu", SourceAvailable, crossplaneRuns))
	wantSuggestion(t, q, "Crossplane detected in prod-eu: accept its inferred offerings")
	r := q.Restrict(anyCluster(readerOf("team-a")), anyClusterKind(nil))
	for _, got := range suggestions(t, r) {
		if got != "" || strings.Contains(got, "prod-eu") || strings.Contains(got, "in :") {
			t.Errorf("a viewer without prod-eu got %q", got)
		}
	}
	// A hidden none-found row says nothing either: the view knows no cluster.
	none := idpQuery(t, ordersFleet(), nil, crossplaneRow("uid-2", "prod-eu", SourceAvailable, crossplaneNone))
	wantSuggestion(t, none.Restrict(anyCluster(readerOf("team-a")), anyClusterKind(nil)), "")
}

func TestProvisioningSuggestionNoProvisionerNeedsEveryClusterKnown(t *testing.T) {
	for _, other := range []Provider{
		crossplaneRow("uid-2", "prod-eu", SourceStale, crossplaneNone),
		crossplaneRow("uid-2", "prod-eu", SourcePartial, crossplaneNoCtrl),
		crossplaneRow("uid-2", "prod-eu", SourceUnavailable, "API discovery failed"),
	} {
		q := idpQuery(t, ordersFleet(), nil, crossplaneRow("uid-1", "staging", SourceAvailable, crossplaneNone), other)
		wantSuggestion(t, q, "")
	}
}

// CRDs Crossplane left behind, with no controller, never read as a running
// Crossplane.
func TestProvisioningSuggestionCRDsWithoutControllerSuggestNothing(t *testing.T) {
	wantSuggestion(t, idpQuery(t, ordersFleet(), nil, crossplaneRow("uid-2", "prod-eu", SourcePartial, crossplaneNoCtrl)), "")
}

func TestProvisioningSuggestionNamesTheEnvironmentsCluster(t *testing.T) {
	base := ordersFleet()
	cur := consumer("orders", "1.3.0", "postgresql")
	cur.Bundle.Contract.Environments = []contract.Environment{{Name: "dev", File: "dev.yaml"}, {Name: "production", File: "production.yaml"}}
	base.Revisions[2] = cur
	// prod-eu sorts after staging; production targets staging.
	q := idpQuery(t, base, nil, crossplaneRow("uid-2", "prod-eu", SourceAvailable, crossplaneRuns), crossplaneRow("uid-1", "staging", SourceAvailable, crossplaneRuns))
	u, _ := use(t, q, "orders", "postgresql", RelationshipConfigRef)
	got := map[string]string{}
	for _, st := range u.Provisioning {
		got[st.Environment] = st.Suggestion
	}
	if got["production"] != "Crossplane detected in staging: accept its inferred offerings" ||
		got["dev"] != "Crossplane detected in prod-eu: accept its inferred offerings" {
		t.Errorf("suggestions = %q", got)
	}
}

func TestProvisioningSuggestionOnlyBesideNoReport(t *testing.T) {
	q := idpQuery(t, ordersFleet(), &Collection{Provisioning: []RawProvisioning{report()}}, crossplaneRow("uid-2", "prod-eu", SourceAvailable, crossplaneRuns))
	u, _ := use(t, q, "orders", "postgresql", RelationshipConfigRef)
	if len(u.Provisioning) != 1 || u.Provisioning[0].Phase != "Ready" || u.Provisioning[0].Suggestion != "" {
		t.Errorf("statuses = %+v", u.Provisioning)
	}
	// The status of a use whose relationships name no known revision carries it.
	got, _ := q.provisioningFor(OfferingUse{}, []*Relationship{{FromRevision: "missing", Type: RelationshipConfigRef, To: "db"}}, nil, nil, q.provisioningHint())
	if len(got) != 1 || got[0].Suggestion != "Crossplane detected in prod-eu: accept its inferred offerings" {
		t.Errorf("statuses = %+v", got)
	}
}

// Attention never reads detected rows: its items are the same with and
// without a running Crossplane, and carry no suggestion.
func TestProvisioningAttentionIgnoresSuggestion(t *testing.T) {
	failed := report(func(r *RawProvisioning) { r.Phase = "Failed" })
	with := idpQuery(t, ordersFleet(), &Collection{Provisioning: []RawProvisioning{failed}}, crossplaneRow("uid-2", "prod-eu", SourceAvailable, crossplaneRuns))
	without := idpQuery(t, ordersFleet(), &Collection{Provisioning: []RawProvisioning{failed}})
	a, b := provisioningItems(t, with), provisioningItems(t, without)
	if len(a) != 1 || len(a) != len(b) || a[0].Reason != b[0].Reason || a[0].NextStep != b[0].NextStep || strings.Contains(a[0].NextStep, "Crossplane") {
		t.Errorf("attention with Crossplane %+v, without %+v", a, b)
	}
}

// Concurrent restricted and unrestricted reads of one query never race: the
// suggestion is computed per call (run under -race).
func TestProvisioningSuggestionConcurrentRestrict(t *testing.T) {
	q := idpQuery(t, ordersFleet(), nil, crossplaneRow("uid-2", "prod-eu", SourceAvailable, crossplaneRuns))
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			r := q
			if i%2 == 0 {
				r = q.Restrict(anyCluster(readerOf("team-a")), anyClusterKind(nil))
			}
			d, err := r.EntityDetail(KindOffering, "postgresql")
			if err != nil || len(d.Offering.Consumers.Items) == 0 {
				t.Errorf("detail %v %+v", err, d)
			}
		})
	}
	wg.Wait()
}
