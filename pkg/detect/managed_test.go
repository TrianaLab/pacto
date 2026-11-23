package detect

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func TestEncodeRefusesTooManyResources(t *testing.T) {
	if _, err := Encode(Result{ManagedResources: make([]fleet.RawManagedResource, MaxManagedResources+1)}); err == nil {
		t.Error("Encode wrote more managed resources than MaxManagedResources")
	}
	if _, err := Encode(Result{SecretRefs: make([]fleet.RawSecretRef, MaxSecretRefs+1)}); err == nil {
		t.Error("Encode wrote more Secret refs than MaxSecretRefs")
	}
	in := Result{ManagedResources: []fleet.RawManagedResource{{Source: "crossplane", Identity: "aws/rds", Name: "orders\u202edb", Missing: []string{"region"}}},
		ManagedResourcesTotal: 1, SecretRefs: []fleet.RawSecretRef{{Namespace: "shop", Kind: "Deployment", Name: "orders", Secret: "orders-db-conn"}}}
	b, err := Encode(in)
	if err != nil {
		t.Fatal(err)
	}
	if in.ManagedResources[0].Name != "orders\u202edb" {
		t.Error("Encode changed the caller's resources")
	}
	got, err := Decode(b)
	if err != nil || got.ManagedResources[0].Name != "ordersdb" || got.SecretRefs[0].Secret != "orders-db-conn" || got.SchemaVersion != "1" {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
}

// TestDecodeCapsAndCleansManagedResources: a hand-edited ConfigMap with more
// resources than the bound and control or bidi characters decodes capped and
// clean, and still reads capped.
func TestDecodeCapsAndCleansManagedResources(t *testing.T) {
	dirty := "a\u001b[2Jb\u202ec\u2066d\u200ee\u0085f"
	m := fleet.RawManagedResource{Source: dirty, Identity: dirty, Missing: []string{dirty}, Entry: dirty, Catalog: dirty, Type: dirty,
		Cluster: dirty, ClusterName: dirty, Namespace: dirty, Name: dirty, Location: dirty, ConnectionSecrets: []string{dirty},
		Claim: dirty, Engine: dirty, EngineRequested: dirty}
	r := fleet.RawSecretRef{Cluster: dirty, ClusterName: dirty, Namespace: dirty, Kind: dirty, Name: dirty, Secret: dirty}
	raw, _ := json.Marshal(Result{SchemaVersion: SchemaVersion, ManagedResources: slices.Repeat([]fleet.RawManagedResource{m}, 600),
		SecretRefs: slices.Repeat([]fleet.RawSecretRef{r}, MaxSecretRefs+5)})
	res, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ManagedResources) != MaxManagedResources || len(res.SecretRefs) != MaxSecretRefs || res.ManagedResourcesTotal != 600 || !res.ManagedCapped() {
		t.Fatalf("not capped: %d resources (total %d), %d refs", len(res.ManagedResources), res.ManagedResourcesTotal, len(res.SecretRefs))
	}
	g, s := res.ManagedResources[499], res.SecretRefs[0]
	for _, v := range []string{g.Source, g.Identity, g.Missing[0], g.Entry, g.Catalog, g.Type, g.Cluster, g.ClusterName, g.Namespace, g.Name,
		g.Location, g.ConnectionSecrets[0], g.Claim, g.Engine, g.EngineRequested, s.Cluster, s.ClusterName, s.Namespace, s.Kind, s.Name, s.Secret} {
		if v != "a[2Jbcdef" {
			t.Errorf("%q kept a control or bidi character", v)
		}
	}
}

// TestDetectRowsPartialWhenCapped: a complete managed row of a capped Result
// reads partial and says how many resources were not kept; a row of another
// kind, and an uncapped Result, are unchanged.
func TestDetectRowsPartialWhenCapped(t *testing.T) {
	entries := []Entry{{Kind: fleet.ProviderManagedResources, Completeness: Complete}, {Kind: "terraform", Completeness: Unknown, Reason: "no location"}, {Kind: "registry", Completeness: Complete}}
	res := Result{AsOf: asOf, Entries: entries, ManagedResources: make([]fleet.RawManagedResource, 2), ManagedResourcesTotal: 5}
	rows := ProviderRows(res, "operator", asOf)
	managed := row(t, rows, fleet.ProviderManagedResources)
	if managed.Status != fleet.SourcePartial || len(managed.Limitations) != 1 || managed.Limitations[0].Code != fleet.LimitationManagedResourcesCapped ||
		!strings.HasPrefix(managed.Limitations[0].Message, "3 of 5 managed resources") {
		t.Fatalf("capped managed row = %+v", managed)
	}
	if !slices.Equal(managed.UsedFor, []string{managedSentence, fleet.UseManagedResources}) || managed.Name != "Managed resources" {
		t.Errorf("managed row = %+v", managed)
	}
	if tf := row(t, rows, "terraform"); tf.Status != fleet.SourceUnavailable || len(tf.Limitations) != 1 {
		t.Errorf("an unknown managed row is lowered no further: %+v", tf)
	}
	if reg := row(t, rows, "registry"); reg.Status != fleet.SourceAvailable || reg.Limitations != nil {
		t.Errorf("another kind changed: %+v", reg)
	}
	res.ManagedResourcesTotal = 2
	if got := row(t, ProviderRows(res, "operator", asOf), fleet.ProviderManagedResources); got.Status != fleet.SourceAvailable || got.Limitations != nil {
		t.Errorf("uncapped managed row = %+v", got)
	}
}
