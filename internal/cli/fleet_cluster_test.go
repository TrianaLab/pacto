package cli_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// twoClusterTargetState writes one target-state file per cluster: payments in
// both, orders only in eu-prod. It returns the shared fleet flags.
func twoClusterTargetState(t *testing.T) []string {
	t.Helper()
	dir := t.TempDir()
	file := func(name, id, cluster string, targets ...string) string {
		body := "schemaVersion: pacto.dev/fleet-targets/v1\ncluster:\n  id: " + id + "\n  name: " + cluster + "\ntargets:\n"
		for _, n := range targets {
			body += "  - scope: " + n + "\n    kind: kubernetes\n    name: " + n + "\n    service: " + n + "\n    compliance: Compliant\n"
		}
		p := filepath.Join(dir, name)
		mustWrite(t, p, body)
		return p
	}
	return []string{
		"--local", t.TempDir(),
		"--target-state", file("eu.yaml", "uid-eu", "eu-prod", "payments", "orders"),
		"--target-state", file("us.yaml", "uid-us", "us-prod", "payments"),
	}
}

func TestFleetEntityAmbiguousTargetKeyExitsWithCandidates(t *testing.T) {
	args := append([]string{"fleet", "entity", "target", "payments/kubernetes/payments"}, twoClusterTargetState(t)...)
	_, _, err := execFleet(t, args...)
	if err == nil {
		t.Fatal("an ambiguous target key fails, never a guess")
	}
	for _, want := range []string{
		"eu-prod / payments / payments", "uid-eu/payments/kubernetes/payments",
		"us-prod / payments / payments", "uid-us/payments/kubernetes/payments",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
}

func TestFleetGetTargetLegacyKeyResolves(t *testing.T) {
	args := append([]string{"fleet", "get", "--target", "orders/kubernetes/orders", "--output-format", "json"}, twoClusterTargetState(t)...)
	out, _, err := execFleet(t, args...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"key": "uid-eu/orders/kubernetes/orders"`) {
		t.Errorf("a unique legacy key answers with the canonical key:\n%s", out)
	}
}

// --cluster keeps the targets of one cluster and the services with a target
// there; an unknown id answers an empty list.
func TestFleetEntitiesClusterFlag(t *testing.T) {
	flags := twoClusterTargetState(t)
	out, _, err := execFleet(t, append([]string{"fleet", "entities", "--kind", "target", "--cluster", "uid-us", "--output-format", "json"}, flags...)...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"key": "uid-us/payments/kubernetes/payments"`) || strings.Contains(out, "uid-eu/") {
		t.Errorf("targets of uid-us:\n%s", out)
	}
	out, _, err = execFleet(t, append([]string{"fleet", "entities", "--kind", "service", "--cluster", "uid-us", "--output-format", "json"}, flags...)...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"key": "payments"`) || strings.Contains(out, `"key": "orders"`) {
		t.Errorf("services in uid-us:\n%s", out)
	}
	out, _, err = execFleet(t, append([]string{"fleet", "entities", "--kind", "target", "--cluster", "uid-none", "--output-format", "json"}, flags...)...)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, `"key": "uid-`) {
		t.Errorf("an unknown cluster answers no entity:\n%s", out)
	}
}
