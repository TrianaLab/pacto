package fleet

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/workloadmanager"
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
	check := func(from string, o ProviderOffer) {
		if !strings.HasPrefix(o.Text, "Gate every ") || strings.HasSuffix(o.Text, ".") || !strings.HasPrefix(o.Doc, "https://pacto.run/latest/") || o.ID == "" {
			t.Errorf("%s offer %+v breaks the wording rule", from, o)
		}
	}
	for id, offers := range providerOffers {
		for _, o := range offers {
			check(id, o)
		}
	}
	// The fingerprint offers the chart and the docs ship follow the same rule.
	decoded := 0
	for from, fp := range shippedFingerprints(t) {
		for _, f := range fp.Promoters {
			if f.Offer != nil {
				decoded++
				check(from+" "+f.Product, ProviderOffer(*f.Offer))
			}
		}
	}
	if decoded < 4 {
		t.Errorf("decoded %d fingerprint offers, want the two Kargo entries in the chart and in the docs", decoded)
	}
}

// shippedFingerprints are the chart's default fingerprints and the docs
// example, decoded the way Pacto reads them.
func shippedFingerprints(t *testing.T) map[string]workloadmanager.Fingerprints {
	t.Helper()
	values, err := os.ReadFile("../../integrations/kubernetes/charts/pacto-operator/values.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var chart struct {
		Fingerprints yaml.Node `yaml:"fingerprints"`
	}
	if err := yaml.Unmarshal(values, &chart); err != nil {
		t.Fatal(err)
	}
	raw, err := yaml.Marshal(&chart.Fingerprints)
	if err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile("../../docs/operational-graph-providers.md")
	if err != nil {
		t.Fatal(err)
	}
	_, example, _ := strings.Cut(string(page), "## Fingerprints")
	_, example, _ = strings.Cut(example, "```yaml\n")
	example, _, _ = strings.Cut(example, "```")
	out := map[string]workloadmanager.Fingerprints{}
	for from, text := range map[string]string{"chart": string(raw), "docs": example} {
		fp, err := workloadmanager.ParseFingerprints(strings.NewReader(text))
		if err != nil {
			t.Fatalf("%s: %v", from, err)
		}
		out[from] = fp
	}
	return out
}

var kargoOffer = ProviderOffer{ID: "kargo-analysis-template", Text: "Gate every Kargo promotion with one shared AnalysisTemplate", Doc: "https://pacto.run/latest/integrations/kubernetes/gitops-kargo/#kargo"}

// promotionRow is a promotion row as the workload source makes it from two
// fingerprints of one product that carry the same offer.
func promotionRow() Provider {
	return Provider{ID: "promotion/Kargo", Name: "Kargo", UsedFor: []string{UsePromotion}, Evidence: PromotionOwnedBy + "Kargo (API group kargo.akuity.io served)",
		Objects: []ProviderObject{{Kind: "Deployment", Namespace: "team-b", Name: "api"}}, Offers: []ProviderOffer{kargoOffer, kargoOffer}}
}

func TestPromotionOffersMergeByID(t *testing.T) {
	if got := providerByID(t, offerRows(t, promotionRow()), "promotion/Kargo").Offers; !reflect.DeepEqual(got, []ProviderOffer{kargoOffer}) {
		t.Fatalf("offers = %+v", got)
	}
}

func TestRestrictKeepsKargoOffer(t *testing.T) {
	snap := buildProviders(t, NewMemorySource("k8s", SourceKindKubernetes, &Collection{Targets: []RawTarget{workload("team-a", "web")}, Providers: []Provider{promotionRow()}}))
	r := NewQuery(snap).Restrict(func(ns string) bool { return ns == "team-a" }, nil).SnapshotForSerialization()
	row := providerByID(t, r.Providers, "promotion/Kargo")
	if len(row.Objects) != 0 || !reflect.DeepEqual(row.Offers, []ProviderOffer{kargoOffer}) {
		t.Fatalf("restricted row = %+v", row)
	}
}

func TestUnavailablePromoterRowHasNoOffer(t *testing.T) {
	row := promotionRow()
	row.Status = SourceUnavailable
	if got := providerByID(t, offerRows(t, row), "promotion/Kargo").Offers; got != nil {
		t.Fatalf("offers = %+v", got)
	}
}
