//go:build integration

package integration

import (
	"fmt"
	"path/filepath"
	"testing"
)

// diffGranularityBundle writes a minimal valid bundle from an inline contract,
// used by the field-granularity diff tests below.
func diffGranularityBundle(t *testing.T, name, contractYAML string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	return writeBundleDir(t, dir, contractYAML, nil)
}

// TestDiffFieldGranularity exercises the contract-level field diffs that
// previously rendered opaquely or were dropped entirely.
func TestDiffFieldGranularity(t *testing.T) {
	t.Parallel()

	const ownerBase = `pactoVersion: "2.0"
service:
  name: granular-svc
  version: 1.0.0
  owner:
    team: foundations-team
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`

	// Reproduces the reported bug: a contact added while the team is unchanged
	// must surface the granular contact change, not "team -> team".
	t.Run("owner contact added is granular", func(t *testing.T) {
		t.Parallel()
		v1 := diffGranularityBundle(t, "owner-v1", ownerBase)
		v2 := diffGranularityBundle(t, "owner-v2", `pactoVersion: "2.0"
service:
  name: granular-svc
  version: 1.0.0
  owner:
    team: foundations-team
    contacts:
      - type: slack
        value: "#foundations"
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`)
		output, _ := runCommand(t, nil, "diff", v1, v2)

		assertContains(t, output, "service.owner.contacts[slack:#foundations]")
		assertContains(t, output, "added")
		// The opaque whole-owner modification must no longer appear.
		assertNotContains(t, output, "service.owner (modified)")
	})

	t.Run("owner team modified", func(t *testing.T) {
		t.Parallel()
		v1 := diffGranularityBundle(t, "team-v1", ownerBase)
		v2 := diffGranularityBundle(t, "team-v2", `pactoVersion: "2.0"
service:
  name: granular-svc
  version: 1.0.0
  owner:
    team: platform-team
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`)
		output, _ := runCommand(t, nil, "diff", v1, v2)

		assertContains(t, output, "service.owner.team")
		assertContains(t, output, "foundations-team")
		assertContains(t, output, "platform-team")
	})

	// v2 pins pactoVersion to a single value ("2.0"), so a pactoVersion delta
	// between two parseable contracts is unobservable. The equivalent contract-
	// level scalar diff is a state.type change (stateless -> stateful).
	t.Run("state type change detected", func(t *testing.T) {
		t.Parallel()
		v1 := diffGranularityBundle(t, "state-v1", ownerBase)
		v2 := diffGranularityBundle(t, "state-v2", `pactoVersion: "2.0"
service:
  name: granular-svc
  version: 1.0.0
  owner:
    team: foundations-team
workload: service
state:
  type: stateful
  persistence:
    scope: shared
    durability: persistent
  dataCriticality: high
`)
		output, _ := runCommand(t, nil, "diff", v1, v2)

		assertContains(t, output, "state.type")
		assertNotContains(t, output, "No changes detected")
	})

	// v2 has no service.image; the equivalent granular per-field toggle is an
	// interface visibility flip (public <-> internal), which must surface as a
	// granular interfaces.visibility change rather than an opaque interface diff.
	t.Run("interface visibility toggle detected", func(t *testing.T) {
		t.Parallel()
		base := `pactoVersion: "2.0"
service:
  name: granular-svc
  version: 1.0.0
  owner:
    team: foundations-team
interfaces:
  - name: api
    type: grpc
    ref: interfaces/api.json
`
		v1 := diffGranularityBundle(t, "vis-v1", base+"    visibility: public\n"+ownerRuntime)
		v2 := diffGranularityBundle(t, "vis-v2", base+"    visibility: internal\n"+ownerRuntime)
		output, _ := runCommand(t, nil, "diff", v1, v2)

		assertContains(t, output, "interfaces.visibility")
		assertContains(t, output, "public")
		assertContains(t, output, "internal")
	})
}

// ownerRuntime is the shared top-level workload+state block appended to the
// interface-toggle contracts.
const ownerRuntime = `workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`

// TestDiffReadinessGranularity verifies readiness changes surface in the diff,
// which previously had no coverage at all.
func TestDiffReadinessGranularity(t *testing.T) {
	t.Parallel()

	v1 := writeReadinessBundle(t)

	// Same contract with the dashboard check regressed done -> not-done and the
	// gate lowered.
	const regressed = `pactoVersion: "2.0"
service:
  name: readiness-svc
  version: 1.0.0
  owner:
    team: readiness
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
readiness:
  expires: "2099-12-31"
  minScore: 70
  partialCredit: 0.5
  claims:
    - id: dashboard
      type: url
      evidence: https://grafana.example.com/d/readiness-svc
      weight: 60
      status: not-done
      category: observability
      description: Main production dashboard
    - id: security-review
      type: ticket
      evidence: SEC-1842
      weight: 40
      status: not-done
      category: security
      description: Security review ticket
`
	v2 := diffGranularityBundle(t, "readiness-regressed", regressed)

	output, _ := runCommand(t, nil, "diff", v1, v2)

	assertContains(t, output, "readiness.claims[dashboard]")
	assertContains(t, output, "readiness.minScore")
	assertNotContains(t, output, "No changes detected")
}

// TestDiffConfigValuesGranularity verifies inline configuration value changes
// are diffed key-by-key.
func TestDiffConfigValuesGranularity(t *testing.T) {
	t.Parallel()

	base := `pactoVersion: "2.0"
service:
  name: cfgvals-svc
  version: 1.0.0
  owner:
    team: foundations-team
configurations:
  - name: app
    schema: configuration/schema.json
    required: true
    values:
      replicas: %d
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`
	v1 := diffGranularityBundle(t, "cfgvals-v1", fmt.Sprintf(base, 1))
	v2 := diffGranularityBundle(t, "cfgvals-v2", fmt.Sprintf(base, 3))

	output, _ := runCommand(t, nil, "diff", v1, v2)

	assertContains(t, output, "configurations[app].values.replicas")
	assertNotContains(t, output, "No changes detected")
}
