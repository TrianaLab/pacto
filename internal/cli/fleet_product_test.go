package cli_test

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
