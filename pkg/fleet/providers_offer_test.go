package fleet

import (
	"reflect"
	"strings"
	"testing"
)

var argoOffer = ProviderOffer{ID: "argocd-repo-server-plugin", Text: "Gate every Argo CD sync with one repo-server plugin", Doc: ArgoSyncGateDoc}

func argoRow() Provider {
	return Provider{ID: ProviderArgoCD, Name: "Argo CD", UsedFor: []string{UseRepositoryProvenance}, Status: SourceAvailable}
}

func offerRows(t *testing.T, rows ...Provider) []Provider {
	t.Helper()
	return buildProviders(t, NewMemorySource("k8s", SourceKindKubernetes, &Collection{Providers: rows})).Providers
}

func TestArgoRowCarriesOffer(t *testing.T) {
	got := providerByID(t, offerRows(t, argoRow()), ProviderArgoCD).Offers
	if !reflect.DeepEqual(got, []ProviderOffer{argoOffer}) {
		t.Fatalf("offers = %+v", got)
	}
}

func TestOffersMergeByID(t *testing.T) {
	row := argoRow()
	row.Offers = []ProviderOffer{{ID: "z-other", Text: "Gate every z with y", Doc: "https://example.com"}, {ID: argoOffer.ID, Text: "stale"}}
	got := providerByID(t, offerRows(t, row), ProviderArgoCD).Offers
	want := []ProviderOffer{argoOffer, row.Offers[0]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("offers = %+v, want %+v", got, want)
	}
	if row.Offers[1].Text != "stale" {
		t.Error("finalize wrote the source's slice")
	}
}

func TestUnavailableRowHasNoOffer(t *testing.T) {
	row := argoRow()
	row.Status = SourceUnavailable
	if got := providerByID(t, offerRows(t, row), ProviderArgoCD).Offers; got != nil {
		t.Fatalf("offers = %+v", got)
	}
}

func TestOtherRowsHaveNoOffer(t *testing.T) {
	if got := providerByID(t, offerRows(t, crdRow(), argoRow()), ProviderPactoCRD).Offers; got != nil {
		t.Fatalf("offers = %+v", got)
	}
}

func TestRestrictKeepsOffer(t *testing.T) {
	snap := buildProviders(t, NewMemorySource("k8s", SourceKindKubernetes, &Collection{Targets: []RawTarget{workload("team-a", "web")}, Providers: []Provider{argoRow()}}))
	r := NewQuery(snap).Restrict(func(ns string) bool { return ns == "team-a" }, nil).SnapshotForSerialization()
	if got := providerByID(t, r.Providers, ProviderArgoCD).Offers; !reflect.DeepEqual(got, []ProviderOffer{argoOffer}) {
		t.Fatalf("restricted offers = %+v", got)
	}
}

func TestProviderOffersShareWording(t *testing.T) {
	for id, offers := range providerOffers {
		for _, o := range offers {
			if !strings.HasPrefix(o.Text, "Gate every ") || strings.HasSuffix(o.Text, ".") || !strings.HasPrefix(o.Doc, "https://pacto.run/latest/") || o.ID == "" {
				t.Errorf("%s offer %+v breaks the wording rule", id, o)
			}
		}
	}
}
