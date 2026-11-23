package cli_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// writeExternalFixture writes checkout (dependencies[0] Stripe) and pay-agent
// (dependencies[1] stripe-api), both joined to the default catalog's stripe
// entry, and pay-agent's legacy-mq, joined by name only.
func writeExternalFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(name, deps string) {
		mustWrite(t, filepath.Join(root, name, "pacto.yaml"), `pactoVersion: "2.0"
service:
  name: `+name+`
  version: "1.0.0"
dependencies:
`+deps+`workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`)
	}
	write("checkout", "  - name: Stripe\n    external: true\n    required: true\n")
	write("pay-agent", "  - name: legacy-mq\n    external: true\n  - name: stripe-api\n    external: true\n")
	return root
}

func TestFleetGetExternalText(t *testing.T) {
	root := writeExternalFixture(t)
	out, _, err := execFleet(t, "fleet", "get", "external:stripe", "--local", root)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	d := externalcatalog.Digest(externalcatalog.DefaultBytes())
	want := "stripe  external, vendor-api  (used by 2 consumers)\n" +
		"  owner    unclaimed, routed to the default approvers (routing only)\n" +
		"  sources  checkout pacto.yaml dependencies[0] (name: Stripe)\n" +
		"           pay-agent pacto.yaml dependencies[1] (name: stripe-api)\n" +
		"  catalog  default " + d[:19] + "... entry stripe\n"
	if out != want {
		t.Errorf("get external text:\n%s\nwant:\n%s", out, want)
	}
	out, _, err = execFleet(t, "fleet", "get", "external:legacy-mq", "--local", root)
	if err != nil || !strings.Contains(out, "(used by 1 consumer)") || !strings.Contains(out, "catalog  none (joined by name)") {
		t.Errorf("name-only external: %v\n%s", err, out)
	}
}

func TestFleetGetExternalJSON(t *testing.T) {
	root := writeExternalFixture(t)
	out, _, err := execFleet(t, "fleet", "get", "external:stripe", "--local", root, "--output-format", "json")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var v fleet.ExternalView
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if v.External.Key != "stripe" || v.External.ConsumersTotal != 2 || v.Catalog == nil || v.Meta.AsOf.IsZero() {
		t.Errorf("view = %+v", v)
	}
}

func TestFleetGetExternalNotFound(t *testing.T) {
	root := writeExternalFixture(t)
	if _, _, err := execFleet(t, "fleet", "get", "external:nobody", "--local", root); err == nil || !strings.Contains(err.Error(), "nobody") {
		t.Errorf("err = %v, want a not-found naming nobody", err)
	}
}

func TestFleetGraphExternalDependents(t *testing.T) {
	root := writeExternalFixture(t)
	out, _, err := execFleet(t, "fleet", "graph", "external:stripe", "--direction", "dependents", "--local", root)
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	if !strings.Contains(out, "dependents of external:stripe") || !strings.Contains(out, "checkout") || !strings.Contains(out, "pay-agent") {
		t.Errorf("graph:\n%s", out)
	}
	if out, _, err := execFleet(t, "fleet", "graph", "external:stripe", "--local", root); err != nil || !strings.Contains(out, "dependents of external:stripe") {
		t.Errorf("an unset direction on an external root means dependents: %v\n%s", err, out)
	}
	if _, _, err := execFleet(t, "fleet", "graph", "external:stripe", "--direction", "dependencies", "--local", root); err == nil {
		t.Error("an explicit dependencies graph of an external system is not an error")
	}
	if out, _, err := execFleet(t, "fleet", "graph", "checkout", "--local", root); err != nil || !strings.Contains(out, "dependencies of") {
		t.Errorf("an unset direction on a service still means dependencies: %v\n%s", err, out)
	}
}

func TestFleetExplainExternal(t *testing.T) {
	root := writeExternalFixture(t)
	out, _, err := execFleet(t, "fleet", "explain", "external:stripe", "--local", root)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	for _, want := range []string{"external external:stripe: Unknown", fleet.ReasonExternalDeclared, fleet.ReasonExternalCatalogMatch} {
		if !strings.Contains(out, want) {
			t.Errorf("explain lacks %q:\n%s", want, out)
		}
	}
}

func TestFleetSearchPrintsExternals(t *testing.T) {
	root := writeExternalFixture(t)
	out, _, err := execFleet(t, "fleet", "search", "stripe", "--local", root)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !strings.Contains(out, "EXTERNALS (1 of 1):") || !strings.Contains(out, "external:stripe") || !strings.Contains(out, "vendor-api") {
		t.Errorf("search:\n%s", out)
	}
	if out, _, _ = execFleet(t, "fleet", "search", "checkout", "--local", root); strings.Contains(out, "EXTERNALS") {
		t.Errorf("a search no external matches prints the section:\n%s", out)
	}
	for _, extra := range [][]string{{"--owner", "nobody-team"}, {"--offset", "5"}} {
		args := append([]string{"fleet", "search", "stripe", "--local", root}, extra...)
		if out, _, _ = execFleet(t, args...); strings.Contains(out, "EXTERNALS") {
			t.Errorf("%v lists externals:\n%s", extra, out)
		}
	}
}

func TestFleetEntitiesClassFilter(t *testing.T) {
	root := writeExternalFixture(t)
	out, _, err := execFleet(t, "fleet", "entities", "--kind", "external", "--class", "vendor-api", "--local", root)
	if err != nil {
		t.Fatalf("entities: %v", err)
	}
	if !strings.Contains(out, "stripe") || strings.Contains(out, "legacy-mq") {
		t.Errorf("class vendor-api:\n%s", out)
	}
	// --class alone lists external systems.
	out, _, err = execFleet(t, "fleet", "entities", "--class", "unclassified", "--local", root)
	if err != nil || !strings.Contains(out, "legacy-mq") || strings.Contains(out, "stripe") {
		t.Errorf("class unclassified: %v\n%s", err, out)
	}
	if _, _, err := execFleet(t, "fleet", "entities", "--kind", "external", "--class", "bogus", "--local", root); err == nil {
		t.Error("an unknown class is not an error")
	}
	out, _, err = execFleet(t, "fleet", "entity", "external", "stripe", "--local", root)
	if err != nil || !strings.Contains(out, "stripe  external, vendor-api") {
		t.Errorf("entity external: %v\n%s", err, out)
	}
}

func TestFleetOutputShowsToExternal(t *testing.T) {
	root := writeExternalFixture(t)
	out, _, err := execFleet(t, "fleet", "get", "checkout", "--local", root)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(out, "Stripe  declaredBy=") || !strings.Contains(out, "-> external:stripe") || strings.Contains(out, "resolved=false") {
		t.Errorf("a joined edge reads:\n%s", out)
	}
}

// TestFleetGetExternalShowsRepositorySource: a local folder whose
// requirements.txt names the Stripe SDK shows stripe as an inferred external
// from the revision whose record names it.
func TestFleetGetExternalShowsRepositorySource(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "checkout", "pacto.yaml"), "pactoVersion: \"2.5\"\nservice:\n  name: checkout\n  version: \"1.0.0\"\n")
	mustWrite(t, filepath.Join(root, "checkout", "requirements.txt"), "stripe==11.4.0\n")
	out, _, err := execFleet(t, "fleet", "get", "external:stripe", "--local", root)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(out, "sources  checkout sdk requirements.txt:1 (inferred, possible) from checkout@sha256:") {
		t.Errorf("get external text:\n%s", out)
	}
	out, _, err = execFleet(t, "fleet", "get", "external:stripe", "--local", root, "--output-format", "json")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var v fleet.ExternalView
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if len(v.External.Sources) != 1 || v.External.Sources[0].Kind != fleet.ExternalSourceRepository || v.External.Sources[0].Provenance != fleet.ProvenanceInferred ||
		len(v.InferredSources) != 1 || v.InferredSources[0].Revision == "" {
		t.Errorf("view = %+v", v)
	}
}

// TestFleetSearchOutsideContract: --outside-contract lists the external
// systems a local Terraform state read a resource of, none through an
// offering, and fleet get prints the resource with its contract state.
func TestFleetSearchOutsideContract(t *testing.T) {
	root := writeExternalFixture(t)
	state := filepath.Join("..", "..", "pkg", "managedres", "testdata", "state-v4.json")
	out, _, err := execFleet(t, "fleet", "entities", "--outside-contract", "--local", root, "--tf-state", state)
	if err != nil || !strings.Contains(out, "aws/rds") || strings.Contains(out, "stripe") {
		t.Fatalf("outside contract: %v\n%s", err, out)
	}
	if out, _, _ = execFleet(t, "fleet", "entities", "--outside-contract", "--local", root); strings.Contains(out, "aws/rds") || strings.Contains(out, "stripe") {
		t.Errorf("no state, yet:\n%s", out)
	}
	out, _, _ = execFleet(t, "fleet", "get", "external:aws/rds/123456789012/eu-west-1/orders-db", "--local", root, "--tf-state", state)
	if !strings.Contains(out, "  resources  local terraform state-v4.json aws_db_instance aws_db_instance.orders  postgres 16.3  outside contract\n") ||
		!strings.Contains(out, "into a contract: pacto claim aws/rds/123456789012/eu-west-1/orders-db --owner <team> --offering <offering>") {
		t.Errorf("get:\n%s", out)
	}
}
