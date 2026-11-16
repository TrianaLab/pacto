package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const (
	clusterEU = "9f1c4e2a-0000-4000-8000-00000000000a"
	clusterUS = "77ad31b0-0000-4000-8000-00000000000b"
)

// twoClusterBase serves a snapshot with payments in two clusters and orders in
// one, plus any extra sources.
func twoClusterBase(t *testing.T, extra ...fleet.Source) string {
	t.Helper()
	at := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	wl := func(name string) fleet.RawTarget {
		return fleet.RawTarget{Scope: name, Kind: "kubernetes", Name: name, Service: name, Compliance: fleet.StatusCompliant, EvidenceAt: &at}
	}
	sources := append([]fleet.Source{
		fleet.NewMemorySource("eu", fleet.SourceKindKubernetes, &fleet.Collection{
			Cluster: &fleet.Cluster{ID: clusterEU, Name: "eu-prod", NameSource: fleet.ClusterNameFromContext},
			Targets: []fleet.RawTarget{wl("payments"), wl("orders")},
		}),
		fleet.NewMemorySource("us", fleet.SourceKindKubernetes, &fleet.Collection{
			Cluster: &fleet.Cluster{ID: clusterUS, Name: "us-prod", NameSource: fleet.ClusterNameFromContext},
			Targets: []fleet.RawTarget{wl("payments")},
		}),
	}, extra...)
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return at }}, sources...)
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(snap)
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, nil)
	t.Cleanup(cancel)
	return base
}

var legacyPayments = string(fleet.NewTargetKey("payments", "kubernetes", "payments"))

// problemCandidates GETs url, expects 409 and returns the candidates detail
// entries as key -> label.
func problemCandidates(t *testing.T, url string) map[string]string {
	t.Helper()
	var p huma.ErrorModel
	getJSON(t, url, http.StatusConflict, &p)
	out := map[string]string{}
	for _, d := range p.Errors {
		if d.Location != "candidates" {
			t.Errorf("detail location = %q", d.Location)
		}
		key, _ := d.Value.(string)
		out[key] = d.Message
	}
	return out
}

func wantPaymentsCandidates(t *testing.T, got map[string]string) {
	t.Helper()
	want := map[string]string{
		string(fleet.NewClusterTargetKey(clusterEU, "payments", "kubernetes", "payments")): "eu-prod / payments / payments",
		string(fleet.NewClusterTargetKey(clusterUS, "payments", "kubernetes", "payments")): "us-prod / payments / payments",
	}
	if len(got) != len(want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
	for k, l := range want {
		if got[k] != l {
			t.Errorf("candidate %q label = %q, want %q", k, got[k], l)
		}
	}
}

func TestEntityDetailAmbiguousTargetKey409(t *testing.T) {
	base := twoClusterBase(t)
	wantPaymentsCandidates(t, problemCandidates(t, base+"/api/fleet/entities/target?key="+url.QueryEscape(legacyPayments)))
}

func TestTargetDetailAmbiguousKey409(t *testing.T) {
	base := twoClusterBase(t)
	wantPaymentsCandidates(t, problemCandidates(t, base+"/api/fleet/target?key="+url.QueryEscape(legacyPayments)))
}

func TestFleetGraphAmbiguousTargetKey409(t *testing.T) {
	base := twoClusterBase(t)
	wantPaymentsCandidates(t, problemCandidates(t, base+"/api/fleet/services/payments/graph?target="+url.QueryEscape(legacyPayments)))
}

func TestEntityDetailClusterlessKeyAmbiguous409(t *testing.T) {
	base := twoClusterBase(t, fleet.NewMemorySource("inventory", "inventory", &fleet.Collection{Targets: []fleet.RawTarget{{
		Scope: "payments", Kind: "kubernetes", Name: "payments", Service: "payments", Compliance: fleet.StatusCompliant,
	}}}))
	got := problemCandidates(t, base+"/api/fleet/entities/target?key="+url.QueryEscape(legacyPayments))
	unknownKey := "_/" + legacyPayments
	if len(got) != 3 || got[unknownKey] != "payments/kubernetes/payments (cluster unknown)" {
		t.Errorf("the clusterless target is one of three candidates, under a key of its own: %v", got)
	}
	// That key opens it, so the chooser never leads back to itself.
	resp, err := http.Get(base + "/api/fleet/entities/target?key=" + url.QueryEscape(unknownKey))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("the clusterless candidate key answers %d", resp.StatusCode)
	}
}

func TestEntityDetailNameAmbiguityStays422(t *testing.T) {
	base := twoClusterBase(t)
	expectStatus(t, base+"/api/fleet/entities/target?key=payments", http.StatusUnprocessableEntity)
	expectStatus(t, base+"/api/fleet/target?key=payments", http.StatusUnprocessableEntity)
}

func TestEntityDetailLegacyKeyAnswersCanonicalKey(t *testing.T) {
	base := twoClusterBase(t)
	var det fleet.EntityDetail
	getJSON(t, base+"/api/fleet/entities/target?key="+url.QueryEscape(string(fleet.NewTargetKey("orders", "kubernetes", "orders"))), http.StatusOK, &det)
	if want := string(fleet.NewClusterTargetKey(clusterEU, "orders", "kubernetes", "orders")); det.Entity.Key != want {
		t.Errorf("entity key = %q, want the canonical %q", det.Entity.Key, want)
	}
}

func TestTargetDetailCarriesCluster(t *testing.T) {
	base := twoClusterBase(t)
	key := string(fleet.NewClusterTargetKey(clusterUS, "payments", "kubernetes", "payments"))
	var raw map[string]any
	getJSON(t, base+"/api/fleet/entities/target?key="+url.QueryEscape(key), http.StatusOK, &raw)
	b, _ := json.Marshal(raw["target"])
	var td struct {
		Cluster         *fleet.Cluster `json:"cluster"`
		ClusterRelation string         `json:"clusterRelation"`
	}
	if err := json.Unmarshal(b, &td); err != nil {
		t.Fatal(err)
	}
	want := fleet.Cluster{ID: clusterUS, Name: "us-prod", NameSource: fleet.ClusterNameFromContext}
	if td.Cluster == nil || *td.Cluster != want || td.ClusterRelation != fleet.ClusterRelationRunsIn {
		t.Errorf("cluster = %+v relation = %q", td.Cluster, td.ClusterRelation)
	}
}
