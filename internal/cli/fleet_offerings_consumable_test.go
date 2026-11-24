package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const consumablePGSchema = `{"type":"object","additionalProperties":false,"required":["engine","class","storageGB"],"properties":{
"engine":{"type":"string","enum":["postgres"],"x-pacto-tier":"contract"},
"class":{"type":"string","enum":["small","large"]},
"storageGB":{"type":"integer","minimum":10,"maximum":500,"default":20,"x-pacto-tier":"tunable"},
"backupRetentionDays":{"type":"integer","minimum":1,"maximum":35,"x-pacto-tier":"tunable"}}}`

// consumableQuery is a fleet of postgresql, a provider accepted under a
// publisher rule with a closed schema, and redis, a provider no source
// checked, so the consumable answer lists postgresql and is partial.
func consumableQuery(t *testing.T) *fleet.Query {
	t.Helper()
	rev := func(name, version, offering string, prov *fleet.BundleProvenance) fleet.RawRevision {
		c := &contract.Contract{PactoVersion: "2.4", Workload: contract.WorkloadService,
			Service: contract.Service{Name: name, Version: version, Offering: offering}}
		return fleet.RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Domain: "ghcr.io/acme",
			RequestedRef: "oci://ghcr.io/acme/" + name + ":" + version, Digest: "sha256:" + strings.Repeat("4c", 32), Provenance: prov}
	}
	pg := rev("pg", "17.0.0", "postgresql", &fleet.BundleProvenance{Source: "oci", Signature: fleet.BundleSignature{Status: fleet.SignatureVerified,
		Source: "rule", Signer: "https://github.com/acme/pg/.github/workflows/publish.yml@refs/heads/main"}})
	pg.Bundle.Contract.Configurations = []contract.Configuration{{Name: "db", Schema: "configuration/schema.json", Required: true}}
	pg.Bundle.Contract.Interfaces = []contract.Interface{{Name: "sql", Type: "http", Guarantees: &contract.Guarantees{Timeout: "5s"}}}
	pg.Bundle.FS = fstest.MapFS{"configuration/schema.json": {Data: []byte(consumablePGSchema)}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("oci", "oci",
		&fleet.Collection{Revisions: []fleet.RawRevision{pg, rev("redis", "7.0.0", "redis", nil)}}))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func consumableAnswer(t *testing.T) *fleetOfferings {
	t.Helper()
	q := consumableQuery(t)
	res, err := q.Entities(fleet.EntityFilter{Kinds: []fleet.EntityKind{fleet.KindOffering}, Consumable: true})
	if err != nil {
		t.Fatal(err)
	}
	return offeringsAnswer(q.SnapshotForSerialization(), res)
}

func TestFleetOfferingsConsumableText(t *testing.T) {
	ans := consumableAnswer(t)
	got := textOf(t, func(cmd *cobra.Command) error { return printFleetOfferings(cmd, ans, "consumable", "", "text") })
	wantLines(t, got,
		"postgresql  17.0.0  sha256:4c4c4c4c4c4c...  declared, publisher https://github.com/acme/pg/.github/workflows/publish.yml@refs/heads/main\n",
		"  classes     small, large\n",
		"  contract    class, engine  (set once, same in every environment)\n",
		"  tunable     backupRetentionDays 1-35, storageGB 10-500\n",
		"  guarantees  sql: timeout 5s\n",
		"  snippet     dependency and configuration values in --output-format json\n",
		"completeness partial")
	if strings.Contains(got, "redis") || strings.Contains(got, "OFFERING ") {
		t.Errorf("the consumable list shows more than the consumable offering:\n%s", got)
	}
}

func TestFleetOfferingsConsumableLines(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	for _, c := range []struct {
		p    fleet.OfferingParameter
		want string
	}{
		{fleet.OfferingParameter{}, ""},
		{fleet.OfferingParameter{Minimum: f(1)}, ">=1"},
		{fleet.OfferingParameter{ExclusiveMinimum: f(0), ExclusiveMaximum: f(1.5)}, ">0 <1.5"},
		{fleet.OfferingParameter{Maximum: f(9)}, "<=9"},
	} {
		if got := boundsText(c.p); got != c.want {
			t.Errorf("boundsText(%+v) = %q, want %q", c.p, got, c.want)
		}
	}
	var buf bytes.Buffer
	printConsumable(&buf, fleet.EntityRef{Label: "x"})
	printConsumable(&buf, fleet.EntityRef{Label: "pg", Consumable: &fleet.ConsumableOffering{ProvidersTotal: 12, ProvidersTruncated: true,
		Providers: []fleet.ConsumableProvider{{Version: "1.0.0", Digest: "sha256:1", Publisher: "p", Lifecycle: fleet.LifecycleDeprecated,
			Parameters: []fleet.OfferingParameter{{Path: "size", Tier: "tunable"}}, ParametersTotal: 60, ParametersTruncated: true}}}})
	wantLines(t, buf.String(), "pg  1.0.0  sha256:1  declared, deprecated, publisher p\n", "  tunable     size\n", "  parameters  1 of 60 shown\n",
		"  guarantees  declares no guarantees\n", "  ... 1 of 12 providers shown\n")
	if strings.Contains(buf.String(), "contract ") || strings.Contains(buf.String(), "classes") {
		t.Errorf("empty lines printed:\n%s", buf.String())
	}
}

func TestFleetOfferingsConsumableJSON(t *testing.T) {
	ans := consumableAnswer(t)
	out := textOf(t, func(cmd *cobra.Command) error { return printFleetOfferings(cmd, ans, "consumable", "", "json") })
	doc := decode(t, out)
	rows, _ := doc["offerings"].([]any)
	if len(rows) != 1 || doc["asOf"] == nil || doc["completeness"] != "partial" {
		t.Fatalf("doc = %v", doc)
	}
	row, _ := rows[0].(map[string]any)
	if c, _ := row["consumable"].(map[string]any); c == nil || c["providers"] == nil {
		t.Errorf("row lacks consumable: %v", row)
	}
}

func TestFleetOfferingsConsumableRefusesInferred(t *testing.T) {
	_, _, err := runRoot(t, defaultDeps(), nil, "fleet", "offerings", "--consumable", "--inferred", "--local", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "consumable") {
		t.Fatalf("err = %v, want the flags refused together", err)
	}
}

func TestFleetOfferingsConsumablePartialSaysUnknown(t *testing.T) {
	// A local fleet checks no signature: nothing is consumable and the
	// answer is partial, so a missing offering is unknown.
	dir := t.TempDir()
	out, _, err := runRoot(t, defaultDeps(), nil, "fleet", "offerings", "--consumable", "--local", dir, "--output-format", "json")
	doc := decode(t, out)
	if err != nil || doc["offerings"] == nil || doc["asOf"] == nil || doc["completeness"] == nil {
		t.Fatalf("err %v, doc %v", err, doc)
	}
	ans := &fleetOfferings{Offerings: []fleetOffering{}, Completeness: fleet.CompletenessPartial}
	got := textOf(t, func(cmd *cobra.Command) error { return printFleetOfferings(cmd, ans, "consumable", "", "text") })
	wantLines(t, got, "No consumable offerings read: the answer is partial, so whether there are any is unknown")
	ans.Completeness = fleet.CompletenessComplete
	wantLines(t, textOf(t, func(cmd *cobra.Command) error { return printFleetOfferings(cmd, ans, "consumable", "", "text") }), "No consumable offerings\n")
}
