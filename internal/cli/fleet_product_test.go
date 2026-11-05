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

	if _, _, err := execFleet(t, append(append([]string{}, base...), "--sort", "bogus")...); err == nil {
		t.Error("an invalid --sort is an error")
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
	if _, _, err := execFleet(t, append(append([]string{}, base...), "--offset", "-1")...); err == nil {
		t.Error("a negative --offset is an error")
	}
}

func TestFleetProductPartialWarning(t *testing.T) {
	root, _ := writeFleetFixture(t)
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	for _, args := range [][]string{
		{"fleet", "entities"},
		{"fleet", "entity", "service", "orders"},
		{"fleet", "attention"},
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
