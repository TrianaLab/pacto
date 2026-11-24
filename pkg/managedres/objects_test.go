package managedres

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// object is the testdata object name, decoded as an unstructured map.
func object(t *testing.T, name string) map[string]any {
	t.Helper()
	b, err := os.ReadFile("testdata/objects/" + name + ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := yaml.Unmarshal(b, &obj); err != nil {
		t.Fatal(err)
	}
	return obj
}

// fromObject reads the testdata object name with the default catalog.
func fromObject(t *testing.T, name string) (fleet.RawManagedResource, ClaimCandidate) {
	t.Helper()
	obj := object(t, name)
	group, _, _ := strings.Cut(obj["apiVersion"].(string), "/")
	r, c, ok := FromObject(group, obj["kind"].(string), obj, externalcatalog.Default(), testNow)
	if !ok {
		t.Fatalf("%s: no catalog row", name)
	}
	if r.Catalog != externalcatalog.DefaultDigest() || !r.ObservedAt.Equal(testNow) {
		t.Errorf("%s: provenance %+v", name, r)
	}
	return r, c
}

func TestFromObjectCrossplaneARN(t *testing.T) {
	r, c := fromObject(t, "crossplane")
	if r.Source != SourceCrossplane || r.Identity != "aws/rds/123456789012/eu-west-1/orders-db" || r.Missing != nil || r.Entry != "aws/rds" ||
		r.Type != "rds.aws.upbound.io/Instance" || r.Namespace != "" || r.Name != "orders-db-x7k2p" ||
		!slices.Equal(r.ConnectionSecrets, []string{"crossplane-system/orders-db-conn-x7k2p"}) || r.Engine != "postgres 15.4" || r.EngineRequested != "" {
		t.Errorf("resource = %+v", r)
	}
	if c != (ClaimCandidate{Namespace: "shop", Name: "orders-db"}) {
		t.Errorf("candidate = %+v", c)
	}
}

func TestFromObjectCrossplaneV2Namespaced(t *testing.T) {
	r, c := fromObject(t, "crossplane-v2")
	// No account was read: the entry identity stays, with what is missing.
	if r.Identity != "aws/rds" || !slices.Equal(r.Missing, []string{"account"}) || r.Namespace != "shop" ||
		!slices.Equal(r.ConnectionSecrets, []string{"shop/invoices-db-conn"}) || r.Engine != "" || r.EngineRequested != "postgres 16" {
		t.Errorf("resource = %+v", r)
	}
	if c != (ClaimCandidate{}) {
		t.Errorf("candidate = %+v", c)
	}
}

func TestFromObjectACK(t *testing.T) {
	r, _ := fromObject(t, "ack")
	if r.Source != SourceACK || r.Identity != "aws/rds/210987654321/us-east-1/ledger" || r.Namespace != "payments" || r.ConnectionSecrets != nil ||
		r.EngineRequested != "postgres 14.9" {
		t.Errorf("resource = %+v", r)
	}
}

func TestFromObjectConfigConnector(t *testing.T) {
	r, _ := fromObject(t, "config-connector")
	if r.Source != SourceConfigConnector || r.Identity != "gcp/cloudsql/acme-prod/europe-west1/events-sql" || r.EngineRequested != "postgres_15" {
		t.Errorf("resource = %+v", r)
	}
	// Without resourceID the object name is the id.
	obj := object(t, "config-connector")
	delete(obj["spec"].(map[string]any), "resourceID")
	if r, _, _ := FromObject("sql.cnrm.cloud.google.com", "SQLInstance", obj, externalcatalog.Default(), testNow); r.Identity != "gcp/cloudsql/acme-prod/europe-west1/events" {
		t.Errorf("name as id = %+v", r)
	}
}

func TestFromObjectASO(t *testing.T) {
	r, _ := fromObject(t, "aso")
	if r.Source != SourceASO || r.Identity != "azure/sql/0a1b2c3d-0000-4000-8000-000000000001/westeurope/billing-sql" || r.EngineRequested != "12.0" {
		t.Errorf("resource = %+v", r)
	}
}

// TestFromObjectCrossplaneAzureAndGCP: a Crossplane provider for Azure names
// its resource with the Azure id, one for GCP with the project it was asked for.
func TestFromObjectCrossplaneAzureAndGCP(t *testing.T) {
	az := map[string]any{"metadata": map[string]any{"name": "x"}, "spec": map[string]any{"forProvider": map[string]any{"location": "westeurope"}},
		"status": map[string]any{"atProvider": map[string]any{"id": "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.Sql/servers/main"}}}
	if r, _, _ := FromObject("sql.azure.upbound.io", "MSSQLServer", az, externalcatalog.Default(), testNow); r.Identity != "azure/sql/sub-1/westeurope/main" {
		t.Errorf("azure = %+v", r)
	}
	gcp := map[string]any{"metadata": map[string]any{"name": "x", "annotations": map[string]any{"crossplane.io/external-name": "events"}},
		"spec": map[string]any{"forProvider": map[string]any{"project": "acme", "region": "europe-west1"}}}
	if r, _, _ := FromObject("sql.gcp.upbound.io", "DatabaseInstance", gcp, externalcatalog.Default(), testNow); r.Identity != "gcp/cloudsql/acme/europe-west1/events" {
		t.Errorf("gcp = %+v", r)
	}
}

func TestFromObjectUnclassifiedKind(t *testing.T) {
	if _, _, ok := FromObject("example.org", "Database", object(t, "crossplane"), externalcatalog.Default(), testNow); ok {
		t.Error("a kind no catalog row names was read")
	}
}

func TestManagedMissingAccountKeepsEntryIdentityObject(t *testing.T) {
	obj := object(t, "ack")
	delete(obj, "status")
	r, _, _ := FromObject("rds.services.k8s.aws", "DBInstance", obj, externalcatalog.Default(), testNow)
	if r.Identity != "aws/rds" || !slices.Equal(r.Missing, []string{"id", "account", "region"}) {
		t.Errorf("resource = %+v", r)
	}
	// A cluster-scoped resource with a Secret ref and no namespace names no Secret.
	obj = object(t, "crossplane")
	delete(obj["spec"].(map[string]any)["writeConnectionSecretToRef"].(map[string]any), "namespace")
	if r, _, _ := FromObject("rds.aws.upbound.io", "Instance", obj, externalcatalog.Default(), testNow); r.ConnectionSecrets != nil {
		t.Errorf("secret without a namespace = %+v", r.ConnectionSecrets)
	}
}

func TestFromObjectClaimLabelsAreCandidates(t *testing.T) {
	r, c := fromObject(t, "forged")
	if r.Claim != "" || c != (ClaimCandidate{Namespace: "shop", Name: "orders-db"}) {
		t.Errorf("resource %+v, candidate %+v", r, c)
	}
}

// composites is the testdata composite keyed by uid.
func composites(t *testing.T) map[string]ComposedClaim {
	xr := object(t, "composite")
	ref := xr["spec"].(map[string]any)["claimRef"].(map[string]any)
	return map[string]ComposedClaim{"xr-uid": {Kind: ref["kind"].(string), Namespace: ref["namespace"].(string), Name: ref["name"].(string)}}
}

func TestVerifiedClaimThroughComposite(t *testing.T) {
	obj := object(t, "crossplane")
	_, c := fromObject(t, "crossplane")
	if ref, ok := VerifiedClaim(obj, c, composites(t)); !ok || ref != "PostgreSQLInstance shop/orders-db" {
		t.Errorf("verified = %q, %v", ref, ok)
	}
	// A nested composite reaches the root's claimRef; a cycle ends.
	nested := map[string]ComposedClaim{"xr-uid": {Owners: []string{"root", "xr-uid"}}, "root": composites(t)["xr-uid"]}
	if ref, ok := VerifiedClaim(obj, c, nested); !ok || ref != "PostgreSQLInstance shop/orders-db" {
		t.Errorf("nested = %q, %v", ref, ok)
	}
}

func TestFromObjectIgnoresForgedClaimLabels(t *testing.T) {
	forged := object(t, "forged")
	_, c := fromObject(t, "forged")
	// The label namespace is not the resource's own.
	if _, ok := VerifiedClaim(forged, c, composites(t)); ok {
		t.Error("a namespaced resource took a claim in another namespace")
	}
	// No composite owns it, or the composite names another claim.
	obj := object(t, "crossplane")
	if _, ok := VerifiedClaim(obj, c, nil); ok {
		t.Error("labels with no composite were trusted")
	}
	other := map[string]ComposedClaim{"xr-uid": {Kind: "PostgreSQLInstance", Namespace: "shop", Name: "invoices-db"}}
	if _, ok := VerifiedClaim(obj, c, other); ok {
		t.Error("a composite naming another claim verified it")
	}
	if _, ok := VerifiedClaim(obj, ClaimCandidate{}, composites(t)); ok {
		t.Error("no labels verified a claim")
	}
}

func TestFromClaimSecret(t *testing.T) {
	if secret, ref := FromClaim("PostgreSQLInstance", object(t, "claim")); secret != "shop/orders-db-conn" || ref != "PostgreSQLInstance shop/orders-db" {
		t.Errorf("claim = %q, %q", secret, ref)
	}
	if secret, _ := FromClaim("PostgreSQLInstance", map[string]any{}); secret != "" {
		t.Errorf("no ref named %q", secret)
	}
}

// TestFromObjectReadsNoSecret: the password Secret refs and their keys an
// object names never reach the resource; only the connection Secret's name does.
func TestFromObjectReadsNoSecret(t *testing.T) {
	for _, name := range []string{"crossplane", "ack", "aso"} {
		r, _ := fromObject(t, name)
		b, _ := json.Marshal(r)
		for _, leak := range []string{"password", "master", "admin"} {
			if strings.Contains(string(b), leak) {
				t.Errorf("%s: %s kept %q", name, b, leak)
			}
		}
	}
}

func TestEngineKeys(t *testing.T) {
	for _, tc := range []struct {
		fields map[string]any
		want   string
	}{
		{map[string]any{"engine": "Postgres", "engineVersion": "16.1"}, "postgres 16.1"},
		{map[string]any{"engine": "mysql", "version": "8.0"}, "mysql 8.0"},
		{map[string]any{"databaseVersion": "POSTGRES_15"}, "postgres_15"},
		{map[string]any{"version": "14"}, "14"},
		{map[string]any{"tier": "db-f1"}, ""},
	} {
		if got, _ := Engine(map[string]any{"status": map[string]any{"atProvider": tc.fields}}); got != tc.want {
			t.Errorf("%v = %q, want %q", tc.fields, got, tc.want)
		}
	}
}

func TestEngineOrigin(t *testing.T) {
	spec := map[string]any{"forProvider": map[string]any{"engine": "postgres", "engineVersion": "16"}}
	if v, o := Engine(map[string]any{"spec": spec, "status": map[string]any{"atProvider": map[string]any{"engine": "postgres", "engineVersion": "15"}}}); v != "postgres 15" || o != EngineObserved {
		t.Errorf("status = %q %q", v, o)
	}
	if v, o := Engine(map[string]any{"spec": spec, "status": map[string]any{"version": "15"}}); v != "15" || o != EngineObserved {
		t.Errorf("family status = %q %q", v, o)
	}
	if v, o := Engine(map[string]any{"spec": spec}); v != "postgres 16" || o != EngineDesired {
		t.Errorf("spec = %q %q", v, o)
	}
	if v, o := Engine(map[string]any{"spec": map[string]any{"engine": "redis"}}); v != "redis" || o != EngineDesired {
		t.Errorf("top spec = %q %q", v, o)
	}
	if v, o := Engine(map[string]any{}); v != "" || o != "" {
		t.Errorf("none = %q %q", v, o)
	}
}

func TestEngineDropsOddValue(t *testing.T) {
	for _, odd := range []string{strings.Repeat("9", 65), "postgres\n16", "pg;rm -rf", "pöstgres"} {
		if v, _ := Engine(map[string]any{"status": map[string]any{"version": odd}}); v != "" {
			t.Errorf("%q kept as %q", odd, v)
		}
	}
}

func TestIdentityHelpers(t *testing.T) {
	for _, arn := range []string{"", "not-an-arn", "arn:aws:s3"} {
		if arnID(arn) != "" {
			t.Errorf("%q has an id", arn)
		}
	}
	if arnID("arn:aws:sqs:eu-west-1:1:orders") != "orders" || arnID("arn:aws:s3:::bucket/x") != "x" {
		t.Error("arn ids")
	}
	if s, n := azureParts("/resourceGroups/x/y"); s != "" || n != "" {
		t.Errorf("azure = %q %q", s, n)
	}
	if ownerUIDs(map[string]any{"metadata": map[string]any{"ownerReferences": []any{map[string]any{"name": "x"}}}}) != nil {
		t.Error("an owner with no uid")
	}
}
