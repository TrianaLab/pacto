package cli_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/lock"
)

func TestFleetEntities(t *testing.T) {
	root, ev := writeFleetFixture(t)
	base := []string{"fleet", "entities", "--local", root, "--target-state", ev}

	out, _, err := execFleet(t, append(append([]string{}, base...), "--kind", "owner")...)
	if err != nil {
		t.Fatalf("entities: %v", err)
	}
	if !regexp.MustCompile(`(?m)^\d+ of \d+ entit\(ies\):$`).MatchString(out) ||
		!regexp.MustCompile(`(?m)^  owner +commerce +team:commerce +`).MatchString(out) {
		t.Errorf("entities --kind owner:\n%s", out)
	}

	out, _, err = execFleet(t, append(append([]string{}, base...), "--ownership", "unowned", "--kind", "service")...)
	if err != nil || !strings.Contains(out, "lonely") || strings.Contains(out, "orders") {
		t.Errorf("entities --ownership unowned = %v:\n%s", err, out)
	}

	for _, f := range [][]string{
		{"ord"}, {"--owner", "commerce"}, {"--owner-key", "team:commerce"}, {"--business-domain", "(none)"},
		{"--offering", "redis"}, {"--status", "NonCompliant"}, {"--sort", "health"}, {"--limit", "1", "--offset", "1"},
	} {
		if _, _, err := execFleet(t, append(append([]string{}, base...), f...)...); err != nil {
			t.Errorf("entities %v: %v", f, err)
		}
	}

	jout, _, err := execFleet(t, append(append([]string{}, base...), "--output-format", "json")...)
	if err != nil || !strings.Contains(jout, `"entities"`) || !strings.Contains(jout, `"snapshotId"`) {
		t.Errorf("entities json = %v:\n%s", err, jout)
	}
	if strings.Contains(jout, `"kind": "revision"`) || strings.Contains(jout, `"kind": "target"`) {
		t.Errorf("no --kind lists services, owners, domains and offerings only:\n%s", jout)
	}

}

// TestFleetEntitiesRejects: an invalid --sort is an error, and --kind takes
// only the four kinds pacto_fleet_entities takes.
func TestFleetEntitiesRejects(t *testing.T) {
	root, ev := writeFleetFixture(t)
	for _, f := range [][]string{{"--sort", "bogus"}, {"--kind", "revision"}} {
		if _, _, err := execFleet(t, append([]string{"fleet", "entities", "--local", root, "--target-state", ev}, f...)...); err == nil {
			t.Errorf("entities %v is an error", f)
		}
	}
}

func TestFleetEntity(t *testing.T) {
	root, ev := writeFleetFixture(t)
	base := []string{"--local", root, "--target-state", ev}

	out, _, err := execFleet(t, append([]string{"fleet", "entity", "service", "orders"}, base...)...)
	if err != nil {
		t.Fatalf("entity: %v", err)
	}
	for _, want := range []string{"Kind: service\n", "Key: orders\n", "Label: orders\n", "Status: ", "Run with --output-format json for the full detail"} {
		if !strings.Contains(out, want) {
			t.Errorf("entity text lacks %q:\n%s", want, out)
		}
	}

	jout, _, err := execFleet(t, append([]string{"fleet", "entity", "owner", "team:commerce", "--output-format", "json"}, base...)...)
	if err != nil || !strings.Contains(jout, `"owner"`) {
		t.Errorf("entity owner json = %v:\n%s", err, jout)
	}

	if _, _, err := execFleet(t, append([]string{"fleet", "entity", "service", "ghost"}, base...)...); err == nil {
		t.Error("an unknown entity is an error")
	}
	if _, _, err := execFleet(t, append([]string{"fleet", "entity", "service"}, base...)...); err == nil {
		t.Error("entity takes a kind and a key")
	}
}

func TestFleetAttention(t *testing.T) {
	root, ev := writeFleetFixture(t)
	base := []string{"fleet", "attention", "--local", root, "--target-state", ev}

	out, _, err := execFleet(t, append(append([]string{}, base...), "--category", "unowned")...)
	if err != nil {
		t.Fatalf("attention: %v", err)
	}
	if !regexp.MustCompile(`(?m)^\d+ of \d+ attention item\(s\):$`).MatchString(out) ||
		!regexp.MustCompile(`(?m)^  \[info\] unowned lonely: .+ \(next: .+\)$`).MatchString(out) {
		t.Errorf("attention --category unowned:\n%s", out)
	}

	for _, f := range [][]string{
		{"--severity", "error"}, {"--owner", "commerce"}, {"--owner-key", "team:commerce"},
		{"--business-domain", "(none)"}, {"--service", "orders"}, {"--limit", "1", "--offset", "1"},
	} {
		if _, _, err := execFleet(t, append(append([]string{}, base...), f...)...); err != nil {
			t.Errorf("attention %v: %v", f, err)
		}
	}

	jout, _, err := execFleet(t, append(append([]string{}, base...), "--limit", "1", "--output-format", "json")...)
	if err != nil || !strings.Contains(jout, `"nextOffset": 1`) || !strings.Contains(jout, `"categories"`) {
		t.Errorf("attention json = %v:\n%s", err, jout)
	}
	for _, f := range [][]string{{"--offset", "-1"}, {"--category", "undeclard"}, {"--severity", "critical"}} {
		if _, _, err := execFleet(t, append(append([]string{}, base...), f...)...); err == nil {
			t.Errorf("attention %v is an error", f)
		}
	}
}

func TestFleetNeighborhood(t *testing.T) {
	root, ev := writeFleetFixture(t)
	base := []string{"fleet", "neighborhood", "payments", "--direction", "dependents", "--local", root, "--target-state", ev}

	out, _, err := execFleet(t, base...)
	if err != nil {
		t.Fatalf("neighborhood: %v", err)
	}
	if !regexp.MustCompile(`(?m)^  orders  depth=1  owner=commerce  status=\S+$`).MatchString(out) ||
		!regexp.MustCompile(`(?m)^  payments  depth=0  owner=billing  status=\S+$`).MatchString(out) ||
		!regexp.MustCompile(`(?m)^  orders -> payments  declared\S*  \S+$`).MatchString(out) {
		t.Errorf("neighborhood text:\n%s", out)
	}

	out, _, err = execFleet(t, append(base, "--max-nodes", "1")...)
	if err != nil || !strings.Contains(out, "truncated: 1 of 2 services shown; raise --max-nodes to see more\n") {
		t.Errorf("neighborhood --max-nodes 1 = %v:\n%s", err, out)
	}

	jout, _, err := execFleet(t, append(base, "--views", "expected", "--depth", "2", "--max-nodes", "5", "--output-format", "json")...)
	if err != nil || !strings.Contains(jout, `"requestedFocus"`) || !strings.Contains(jout, `"snapshotId"`) || !strings.Contains(jout, `"views": [`+"\n"+`    "expected"`+"\n"+`  ]`) {
		t.Errorf("neighborhood json = %v:\n%s", err, jout)
	}

	for _, args := range [][]string{{"ghost"}, {"payments", "--direction", "sideways"}, {"payments", "--views", "everything"}, {}} {
		if _, _, err := execFleet(t, append(append([]string{"fleet", "neighborhood"}, args...), "--local", root, "--target-state", ev)...); err == nil {
			t.Errorf("neighborhood %v is an error", args)
		}
	}
}

func TestFleetNeighborhoodMaxEdges(t *testing.T) {
	root, ev := writeFleetFixture(t)
	out, _, err := execFleet(t, "fleet", "neighborhood", "payments", "--max-edges", "1", "--local", root, "--target-state", ev)
	if err != nil || !strings.Contains(out, "  orders -> payments  declared") || strings.Contains(out, "truncated") {
		t.Errorf("neighborhood --max-edges 1 = %v:\n%s", err, out)
	}
}

func TestFleetProductPartialWarning(t *testing.T) {
	root, _ := writeFleetFixture(t)
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	for _, args := range [][]string{
		{"fleet", "entities"},
		{"fleet", "entity", "service", "orders"},
		{"fleet", "attention"},
		{"fleet", "neighborhood", "payments"},
	} {
		_, stderr, err := execFleet(t, append(args, "--local", root, "--target-state", missing)...)
		if err != nil || !strings.Contains(stderr, "warning: answer is partial") || !strings.Contains(stderr, "SOURCE_UNAVAILABLE") {
			t.Errorf("%v = %v, stderr:\n%s", args, err, stderr)
		}
	}
}

func TestFleetStatusUnownedAndUndeclared(t *testing.T) {
	root, ev := writeFleetFixture(t)
	out, _, err := execFleet(t, "fleet", "status", "--unowned", "--local", root, "--target-state", ev)
	if err != nil || !strings.Contains(out, "UNOWNED_SERVICE") || strings.Contains(out, "NON_COMPLIANT") {
		t.Errorf("status --unowned = %v:\n%s", err, out)
	}
	out, _, err = execFleet(t, "fleet", "status", "--undeclared", "--local", root, "--target-state", ev)
	if err != nil || !strings.HasPrefix(out, "0 of 0 attention item(s):") {
		t.Errorf("status --undeclared = %v:\n%s", err, out)
	}
}

// writeOfferingFixture lays out postgresql 16.0.0 (deprecated) and 16.1.0,
// which provide the postgresql offering, plus app and batch, which depend on
// it. The target-state file reports app's database Ready in production an hour
// ago and batch's Ready 25 hours ago, past the default provisioning window.
func writeOfferingFixture(t *testing.T) (root, targets string) {
	t.Helper()
	root = t.TempDir()
	state := "state:\n  type: stateless\n  persistence:\n    scope: local\n    durability: ephemeral\n  dataCriticality: low\n"
	pg := func(version, lifecycle string) {
		mustWrite(t, filepath.Join(root, "pg-"+version, "pacto.yaml"), "pactoVersion: \"2.4\"\nservice:\n  name: postgresql\n  version: \""+version+
			"\"\n  offering: postgresql\n  owner:\n    team: infra-data\n"+lifecycle+"workload: service\n"+state)
	}
	pg("16.0.0", "  lifecycle:\n    deprecated: \"2026-01-01\"\n")
	pg("16.1.0", "")
	digests := map[string]string{}
	for _, name := range []string{"app", "batch"} {
		dir := filepath.Join(root, name)
		mustWrite(t, filepath.Join(dir, "pacto.yaml"), "pactoVersion: \"2.4\"\nservice:\n  name: "+name+"\n  version: \"1.0.0\"\n"+
			"dependencies:\n  - name: postgresql\n    ref: oci://ghcr.io/acme/postgresql\n    required: true\n    compatibility: ^16.0.0\nworkload: service\n"+state)
		h, err := lock.HashFS(os.DirFS(dir))
		if err != nil {
			t.Fatal(err)
		}
		digests[name] = h
	}
	at := func(d time.Duration) string { return time.Now().Add(-d).UTC().Format(time.RFC3339) }
	targets = filepath.Join(t.TempDir(), "targets.yaml")
	mustWrite(t, targets, "schemaVersion: pacto.dev/fleet-targets/v2\ntargets: []\nprovisioning:\n"+
		"  - {service: app, digest: \""+digests["app"]+"\", kind: dependency, name: postgresql, environment: production, provider: rds, externalRef: db-1, phase: Ready, observedAt: \""+at(time.Hour)+"\"}\n"+
		"  - {service: batch, digest: \""+digests["batch"]+"\", kind: dependency, name: postgresql, provider: rds, externalRef: db-2, phase: Ready, observedAt: \""+at(25*time.Hour)+"\"}\n")
	return root, targets
}

func TestFleetVersionsCommand(t *testing.T) {
	root, _ := writeOfferingFixture(t)
	out, _, err := execFleet(t, "fleet", "versions", "postgresql", "--local", root, "--range", "^16.1.0")
	if err != nil {
		t.Fatalf("versions: %v", err)
	}
	for _, want := range []string{
		"postgresql: latest compatible 16.1.0\n",
		"  16.1.0 [satisfied]\n",
		"  16.0.0 [deprecated since 2026-01-01, unsatisfied]\n",
		"Versions come from the contract revisions this snapshot holds",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("versions text lacks %q:\n%s", want, out)
		}
	}
	out, _, err = execFleet(t, "fleet", "versions", "postgresql", "--local", root, "--provider", "postgresql")
	if err != nil || strings.Contains(out, "latest compatible") || !strings.Contains(out, "postgresql\n  16.1.0\n") {
		t.Errorf("versions without a range = %v:\n%s", err, out)
	}
	jout, _, err := execFleet(t, "fleet", "versions", "postgresql", "--local", root, "--output-format", "json")
	if err != nil || !strings.Contains(jout, `"completeness": "fleet"`) {
		t.Errorf("versions json = %v:\n%s", err, jout)
	}
	for _, args := range [][]string{{"postgresql", "--range", "bogus"}, {"ghost"}, {"postgresql", "--provider", "ghost"}} {
		if _, _, err := execFleet(t, append([]string{"fleet", "versions", "--local", root}, args...)...); err == nil {
			t.Errorf("versions %v is an error", args)
		}
	}
}

// TestFleetEntityOfferingText: the offering text lists providers with their
// owner, current revision, lifecycle and versions, then consumers with their
// provisioning status: a fresh file-sourced Ready is unsigned, and a Ready 25
// hours old reads Unknown with no --freshness.
func TestFleetEntityOfferingText(t *testing.T) {
	root, targets := writeOfferingFixture(t)
	out, _, err := execFleet(t, "fleet", "entity", "offering", "postgresql", "--local", root, "--target-state", targets)
	if err != nil {
		t.Fatalf("entity offering: %v", err)
	}
	for _, want := range []string{
		"Providers:\n  postgresql (declared)\n    owner: infra-data\n    current: postgresql 16.1.0\n",
		"    versions: 16.1.0; 16.0.0 [deprecated since 2026-01-01]\n",
		"Consumers:\n  app -> postgresql (postgresql) via dependency, range satisfied\n",
		"    provisioning production: Ready (unsigned)\n",
		"  batch -> postgresql (postgresql) via dependency, range satisfied\n    provisioning: Unknown (unsigned): the last report is older than the provisioning window\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("offering text lacks %q:\n%s", want, out)
		}
	}
}

func TestFleetEntityServiceText(t *testing.T) {
	root, targets := writeOfferingFixture(t)
	out, _, err := execFleet(t, "fleet", "entity", "service", "app", "--local", root, "--target-state", targets)
	if err != nil {
		t.Fatalf("entity service: %v", err)
	}
	if !strings.Contains(out, "Offerings used:\n  app -> postgresql (postgresql) via dependency, range satisfied\n    provisioning production: Ready (unsigned)\n") ||
		!strings.Contains(out, "Run with --output-format json for the full detail") {
		t.Errorf("service text:\n%s", out)
	}
}

func TestFleetEntitiesConsumesFlag(t *testing.T) {
	root, _ := writeOfferingFixture(t)
	out, _, err := execFleet(t, "fleet", "entities", "--local", root, "--consumes", "postgresql")
	if err != nil || !strings.Contains(out, "2 of 2 entit(ies):") || !strings.Contains(out, " app ") || !strings.Contains(out, " batch ") {
		t.Errorf("entities --consumes = %v:\n%s", err, out)
	}
	if _, _, err := execFleet(t, "fleet", "entities", "--local", root, "--consumes", "Not A Slug"); err == nil {
		t.Error("a bad --consumes slug is an error")
	}
}
