package detect

import (
	"encoding/json"
	"errors"
	"fmt"
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
	// A hand-edited list past the cap still reads capped.
	if res.SecretRefsTotal != MaxSecretRefs+5 || !res.SecretRefsCapped() {
		t.Fatalf("refs total %d", res.SecretRefsTotal)
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

func TestManagedEntryUnreadIsPartial(t *testing.T) {
	none := ManagedEntry(nil, nil, 0, 0, false)
	if none.Completeness != Complete || none.Value != "none found" || none.Scope != fleet.ScopeCluster {
		t.Errorf("no group = %+v", none)
	}
	read := ManagedEntry([]string{"rds.aws.upbound.io"}, nil, 3, 0, false)
	if read.Completeness != Complete || read.Value != "3 managed resources" || read.Evidence != "lists the catalog kinds of rds.aws.upbound.io" || read.Details != nil {
		t.Errorf("read = %+v", read)
	}
	unread := []string{"a.io/x", "b.io/x", "c.io/x", "d.io/x", "e.io/x", "f.io/x"}
	e := ManagedEntry([]string{"rds.aws.upbound.io"}, unread, 3, 2, false)
	if e.Completeness != Partial || e.Hint != ManagedUpgradeHint || !strings.HasPrefix(e.Reason, "6 managed-resource kinds") ||
		len(e.Details) != MaxDetails || !strings.HasPrefix(e.Details[0], ClaimUnverified+": 2 resources") || e.Details[1] != "unread a.io/x" {
		t.Errorf("unread = %+v", e)
	}
	if _, err := Encode(Result{Entries: []Entry{e}}); err != nil {
		t.Errorf("the entry does not fit: %v", err)
	}
}

func TestManagedEntryScopedIsUnknown(t *testing.T) {
	if e := ManagedEntry([]string{"rds.aws.upbound.io"}, []string{"a.io/x"}, 3, 0, true); e.Completeness != Unknown ||
		e.Reason != "a namespace-scoped install does not read managed resources" || e.Kind != fleet.ProviderManagedResources {
		t.Errorf("scoped = %+v", e)
	}
}

func TestTerraformEntryFailedLocation(t *testing.T) {
	all := TerraformEntry([]string{"s3://a/x.tfstate", "gs://b/y.tfstate"}, nil)
	if all.Kind != "terraform" || all.Completeness != Complete || all.Value != "2 listed states read" ||
		all.Evidence != "reads s3://a/x.tfstate, gs://b/y.tfstate" || all.Scope != fleet.ScopeCluster {
		t.Errorf("all read = %+v", all)
	}
	some := TerraformEntry([]string{"s3://a/x.tfstate"}, map[string]string{"gs://b/y.tfstate": "not found"})
	if some.Completeness != Partial || some.Reason != "1 of 2 listed states could not be read" ||
		!slices.Equal(some.Details, []string{"gs://b/y.tfstate: not found"}) {
		t.Errorf("some failed = %+v", some)
	}
	failed := map[string]string{}
	for i := range MaxDetails + 2 {
		failed[fmt.Sprintf("s3://a/%02d.tfstate", i)] = "access denied"
	}
	none := TerraformEntry(nil, failed)
	if none.Completeness != Unknown || none.Value != "" || none.Evidence != "" || len(none.Details) != MaxDetails ||
		none.Details[0] != "s3://a/00.tfstate: access denied" {
		t.Errorf("none read = %+v", none)
	}
}

func TestCloudInventoryRowOffWithoutIdentity(t *testing.T) {
	if e := CloudInventoryEntry("off", "", "", false, nil); e.Kind != "" {
		t.Errorf("off with no identity wrote %+v", e)
	}
	if e := CloudInventoryEntry("", "", "", false, nil); e.Kind != "" {
		t.Errorf("unset with no identity wrote %+v", e)
	}
	e := CloudInventoryEntry("aws", "", "no read-only workload identity on the operator ServiceAccount", false, nil)
	if e.Kind != "cloud-inventory" || e.Value != "off" || e.Completeness != Unknown ||
		e.Reason != "no read-only workload identity on the operator ServiceAccount" || !strings.Contains(e.Hint, "serviceAccount.annotations") {
		t.Errorf("on with no identity = %+v", e)
	}
	if e := CloudInventoryEntry("gcp", "x@p.iam.gserviceaccount.com", "", true, errors.New("status 403")); e.Completeness != Unknown ||
		e.Reason != "reading the gcp inventory failed: status 403" {
		t.Errorf("failed = %+v", e)
	}
	if e := CloudInventoryEntry("azure", "client 1", "", true, nil); e.Completeness != Complete || e.Value != "azure" || e.Evidence != "read with client 1" {
		t.Errorf("read = %+v", e)
	}
}

func TestCloudInventoryRowOffNamesOptIn(t *testing.T) {
	e := CloudInventoryEntry("off", "aws identity arn:aws:iam::1:role/pacto", "", true, nil)
	if e.Kind != "cloud-inventory" || e.Value != "off" || e.Completeness != Unknown ||
		!strings.Contains(e.Reason, "controller.cloudInventory") || !strings.Contains(e.Reason, "arn:aws:iam::1:role/pacto") ||
		!strings.Contains(e.Hint, "--set controller.cloudInventory=") {
		t.Errorf("off with an identity = %+v", e)
	}
}

// TestDetectRowsPartialWhenSecretRefsCapped: Secret refs not all kept or
// read lower the managed-resources row alone, with a count-free reason.
func TestDetectRowsPartialWhenSecretRefsCapped(t *testing.T) {
	entries := []Entry{{Kind: fleet.ProviderManagedResources, Completeness: Complete}, {Kind: "terraform", Completeness: Complete}}
	res := Result{AsOf: asOf, Entries: entries, SecretRefs: make([]fleet.RawSecretRef, 2), SecretRefsTotal: 3}
	rows := ProviderRows(res, "operator", asOf)
	managed := row(t, rows, fleet.ProviderManagedResources)
	if managed.Status != fleet.SourcePartial || len(managed.Limitations) != 1 || managed.Limitations[0].Code != fleet.LimitationSecretRefsCapped {
		t.Fatalf("managed row = %+v", managed)
	}
	if tf := row(t, rows, "terraform"); tf.Status != fleet.SourceAvailable {
		t.Errorf("terraform row = %+v", tf)
	}
}

// TestReadsPartial: a managed-kind entry that did not read everything is one
// SOURCE_PARTIAL; an off entry (with a hint), a complete one and another
// kind are none.
func TestReadsPartial(t *testing.T) {
	res := Result{Entries: []Entry{
		TerraformEntry([]string{"s3://a/x"}, map[string]string{"s3://a/y": "503"}),
		CloudInventoryEntry("aws", "role", "", true, errors.New("throttled")),
		CloudInventoryEntry("off", "role", "", true, nil),
		{Kind: fleet.ProviderManagedResources, Completeness: Complete},
		{Kind: "registry", Completeness: Unknown},
	}}
	got := ReadsPartial(res, "detection")
	if len(got) != 2 || got[0].Code != fleet.LimitationSourcePartial || !strings.Contains(got[0].Message, "terraform did not read everything: 1 of 2") ||
		!strings.Contains(got[1].Message, "cloud-inventory did not read everything: reading the aws inventory failed") {
		t.Fatalf("%+v", got)
	}
}
