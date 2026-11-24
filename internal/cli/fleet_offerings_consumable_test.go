package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/spf13/cobra"
	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"

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
func consumableQuery(t *testing.T, extra ...fleet.Source) *fleet.Query {
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
	srcs := append([]fleet.Source{fleet.NewMemorySource("oci", "oci",
		&fleet.Collection{Revisions: []fleet.RawRevision{pg, rev("redis", "7.0.0", "redis", nil)}})}, extra...)
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, srcs...)
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
		"postgresql  ghcr.io/acme/pg  17.0.0  sha256:4c4c4c4c4c4c...  declared, publisher https://github.com/acme/pg/.github/workflows/publish.yml@refs/heads/main\n",
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
		Providers: []fleet.ConsumableProvider{{Service: fleet.EntityRef{Label: "pg-aws", Domain: "acme"}, Version: "1.0.0", Digest: "sha256:1", Publisher: "p", Lifecycle: fleet.LifecycleDeprecated,
			Parameters: []fleet.OfferingParameter{{Path: "size", Tier: "tunable"}}, ParametersTotal: 60, ParametersTruncated: true}}}})
	wantLines(t, buf.String(), "pg  acme/pg-aws  1.0.0  sha256:1  declared, deprecated, publisher p\n", "  tunable     size\n", "  parameters  1 of 60 shown\n",
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

// TestMCPFleetViewConsumableHidesProviders: a signed-in HTTP MCP caller's
// consumable answer comes from mcpFleetView's per-cluster review, so a
// provider that runs only where the caller may not read is never named.
func TestMCPFleetViewConsumableHidesProviders(t *testing.T) {
	h := &hostClusters{allow: func(host, ns string) bool { return host == "https://eu" && ns == "prod" }}
	d := readDeps(nil)
	d.dynamicFor = h.dynamicFor
	home := &rest.Config{Host: "https://eu"}
	d.clusterConfig = func() (*rest.Config, error) { return home, nil }
	entries := []app.K8sEntry{entry("eu-prod", "https://eu", "uid-eu"), entry("us-prod", "https://us", "uid-us")}
	d.resolveK8s = func(*app.Service, context.Context, bool, []string) []app.K8sEntry { return entries }
	cmd := newMCPCommand(app.NewService(nil, nil), "v", d)
	for k, val := range map[string]string{"transport": "http", "auth-issuer": "https://idp.example", "context": "eu-prod"} {
		expect(t, cmd.Flags().Set(k, val) == nil, "flag %s", k)
	}
	view, _ := mcpFleetView(cmd, app.NewService(nil, nil), d)
	us := fleet.NewMemorySource("us-prod", fleet.SourceKindKubernetes, &fleet.Collection{Cluster: &fleet.Cluster{ID: "uid-us", Name: "us-prod", NameSource: fleet.ClusterNameFromContext},
		Targets: []fleet.RawTarget{{Scope: "prod", Kind: fleet.SourceKindKubernetes, Name: "pg-0", Service: "pg", Domain: "ghcr.io/acme", Digest: "sha256:" + strings.Repeat("4c", 32)}}})
	q := consumableQuery(t, us)
	// Without a view postgresql is consumable, so its absence below is the view's.
	all, err := q.Entities(fleet.EntityFilter{Kinds: []fleet.EntityKind{fleet.KindOffering}, Consumable: true})
	expect(t, err == nil && len(all.Entities) == 1, "unrestricted: %v %+v", err, all)
	aliceCtx := context.WithValue(context.Background(), reviewedKey{}, reviewed{person: authn.Person{User: "alice"}})
	restricted, err := view(aliceCtx, nil, q)
	expect(t, err == nil, "view: %v", err)
	res, err := restricted.Entities(fleet.EntityFilter{Kinds: []fleet.EntityKind{fleet.KindOffering}, Consumable: true})
	expect(t, err == nil, "entities: %v", err)
	out, _ := json.Marshal(res)
	if len(res.Entities) != 0 || strings.Contains(string(out), "postgresql") || strings.Contains(string(out), `/pg"`) {
		t.Errorf("a hidden provider is named:\n%s", out)
	}
}

// TestFleetOfferingsConsumableNamesConfigurations: with several required
// configurations a field is named with its configuration, and a cut class
// list says so.
func TestFleetOfferingsConsumableNamesConfigurations(t *testing.T) {
	var buf bytes.Buffer
	printConsumable(&buf, fleet.EntityRef{Label: "pg", Consumable: &fleet.ConsumableOffering{ProvidersTotal: 1, Providers: []fleet.ConsumableProvider{{
		Service: fleet.EntityRef{Label: "pg"}, Version: "1.0.0", Digest: "sha256:1", Publisher: "p", SchemaPaths: []string{"db.json", "backup.json"},
		Classes: []string{"a", "b"}, ClassesTotal: 60, ClassesTruncated: true,
		Parameters: []fleet.OfferingParameter{{Configuration: "db", Path: "size", Tier: "tunable"}, {Configuration: "backup", Path: "size", Tier: "tunable"}}}}}})
	wantLines(t, buf.String(), "  classes     a, b  (2 of 60 shown)\n", "  tunable     db.size, backup.size\n")
}

// TestFleetOfferingsConsumableNotesLeftOut: a complete answer that left a
// provider out says so on stderr, never only in JSON.
func TestFleetOfferingsConsumableNotesLeftOut(t *testing.T) {
	ans := &fleetOfferings{Offerings: []fleetOffering{}, Completeness: fleet.CompletenessComplete, Limitations: []fleet.Limitation{
		{Code: fleet.LimitationOfferingNotConsumable, Message: "Not consumable, not accepted: 1 provider of an offering you may see (postgresql); fix"}}}
	cmd := &cobra.Command{}
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	if err := printFleetOfferings(cmd, ans, "consumable", "", "text"); err != nil {
		t.Fatal(err)
	}
	wantLines(t, out.String(), "No consumable offerings\n")
	wantLines(t, errBuf.String(), "note: [OFFERING_NOT_CONSUMABLE] Not consumable, not accepted: 1 provider of an offering you may see (postgresql); fix\n")
}

// TestFleetOfferingsConsumableFromAccepted: a consumable row's from and asOf
// name the provider acceptance judged, never a provider it left out that
// sorts first.
func TestFleetOfferingsConsumableFromAccepted(t *testing.T) {
	c := &contract.Contract{PactoVersion: "2.4", Workload: contract.WorkloadService, Service: contract.Service{Name: "aaa-pg", Version: "18.0.0", Offering: "postgresql"}}
	aaa := fleet.RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Domain: "ghcr.io/acme",
		RequestedRef: "oci://ghcr.io/acme/aaa-pg:18.0.0", Digest: "sha256:" + strings.Repeat("5d", 32),
		Provenance: &fleet.BundleProvenance{Source: "oci", Signature: fleet.BundleSignature{Status: fleet.SignatureUnsigned, Source: "none"}}}
	q := consumableQuery(t, fleet.NewMemorySource("oci2", "oci", &fleet.Collection{Revisions: []fleet.RawRevision{aaa}}))
	res, err := q.Entities(fleet.EntityFilter{Kinds: []fleet.EntityKind{fleet.KindOffering}, Consumable: true})
	if err != nil {
		t.Fatal(err)
	}
	ans := offeringsAnswer(q.SnapshotForSerialization(), res)
	if len(ans.Offerings) != 1 || ans.Offerings[0].From != "ghcr.io%2Facme/pg 17.0.0" {
		t.Fatalf("offerings = %+v", ans.Offerings)
	}
	if p := ans.Offerings[0].Consumable.Providers[0]; ans.Offerings[0].AsOf != p.Revision.FetchedAt {
		t.Errorf("asOf = %v, want the accepted revision's %v", ans.Offerings[0].AsOf, p.Revision.FetchedAt)
	}
}
