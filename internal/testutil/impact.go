package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile writes body to path, creating its directory.
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// WriteOrders writes the changed service `orders` at version/workload into dir.
// A workload change between two revisions is BREAKING; a version change is not.
func WriteOrders(t *testing.T, dir, version, workload string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "pacto.yaml"), `pactoVersion: "2.0"
service:
  name: orders
  version: "`+version+`"
workload: `+workload+`
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`)
}

// WriteConsumer writes a webapp bundle declaring a dependency on orders with the
// given compatibility range, so orders gains a dependent in the fleet.
func WriteConsumer(t *testing.T, dir, compat string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "pacto.yaml"), `pactoVersion: "2.0"
service:
  name: webapp
  version: "1.0.0"
  owner:
    team: frontend
dependencies:
  - name: orders
    ref: oci://x/orders
    required: true
    compatibility: "`+compat+`"
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`)
}

// ImpactFleet builds a fleet root (orders + a webapp consumer with compat) plus a
// target-state file that makes webapp an active target.
func ImpactFleet(t *testing.T, compat string) (root, evidence string) {
	t.Helper()
	root = t.TempDir()
	WriteOrders(t, filepath.Join(root, "orders"), "2.0.0", "service")
	WriteConsumer(t, filepath.Join(root, "webapp"), compat)
	evidence = filepath.Join(t.TempDir(), "targets.yaml")
	writeFile(t, evidence, `schemaVersion: pacto.dev/fleet-targets/v1
targets:
  - scope: prod
    kind: k8s
    name: webapp
    service: webapp
    compliance: Compliant
`)
	return root, evidence
}
