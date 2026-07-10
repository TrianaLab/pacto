//go:build e2e

package e2e

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// readinessContractYAML is a pactoVersion 1.2 contract with v1.2 readiness:
// per-check status (done/not-done), top-level expires (far-future = current),
// category, minScore, partialCredit. Deterministic regardless of when suite runs.
const readinessContractYAML = `pactoVersion: "1.2"
service:
  name: readiness-svc
  version: 1.0.0
  owner:
    team: readiness
interfaces:
  - name: api
    type: http
    port: 8080
    visibility: internal
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: api
    path: /health
readiness:
  expires: "2099-12-31"
  minScore: 80
  partialCredit: 0.5
  checks:
    - id: dashboard
      type: url
      evidence: https://grafana.example.com/d/readiness-svc
      weight: 60
      status: done
      category: observability
      description: Main production dashboard
    - id: security-review
      type: ticket
      evidence: SEC-1842
      weight: 40
      status: not-done
      category: security
      description: Security review ticket
  history:
    - version: "1"
      date: "2024-01-01"
      author: platform-team
      description: Initial readiness assessment
`

func writeReadinessBundle(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "readiness-svc")
	return writeBundleDir(t, dir, readinessContractYAML, nil)
}

func TestReadinessExplain(t *testing.T) {
	t.Parallel()

	t.Run("text output shows readiness summary and checks", func(t *testing.T) {
		t.Parallel()
		path := writeReadinessBundle(t)
		out, err := runCommand(t, nil, "explain", path)
		if err != nil {
			t.Fatalf("explain failed: %v\n%s", err, out)
		}
		assertContains(t, out, "Pacto Version: 1.2")
		assertContains(t, out, "Readiness:")
		// Score = 60 (done) + 0 (not-done, no partial) = 60. minScore 80 → gate fails.
		assertContains(t, out, "Gate: FAIL (score 60 / minScore 80)")
		assertContains(t, out, "Earned Weight: 60")
		assertContains(t, out, "Total Weight: 100")
		assertContains(t, out, "Expires: 2099-12-31")
		assertContains(t, out, "Status: 1 done, 0 partial, 1 not-done, 0 deferred")
		assertContains(t, out, "dashboard")
		assertContains(t, out, "observability")
		assertContains(t, out, "security-review")
		assertContains(t, out, "security")
		assertContains(t, out, "Revision History:")
	})

	t.Run("json output includes readiness", func(t *testing.T) {
		t.Parallel()
		path := writeReadinessBundle(t)
		out, err := runCommand(t, nil, "--output-format", "json", "explain", path)
		if err != nil {
			t.Fatalf("explain json failed: %v\n%s", err, out)
		}
		var result struct {
			Readiness *struct {
				Score        int    `json:"score"`
				MinScore     int    `json:"minScore"`
				Passing      bool   `json:"passing"`
				TotalWeight  int    `json:"totalWeight"`
				DoneCount    int    `json:"doneCount"`
				NotDoneCount int    `json:"notDoneCount"`
				Expires      string `json:"expires"`
				Checks       []struct {
					ID       string `json:"id"`
					Status   string `json:"status"`
					Category string `json:"category"`
				} `json:"checks"`
			} `json:"readiness"`
		}
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, out)
		}
		if result.Readiness == nil {
			t.Fatalf("expected readiness in JSON, got:\n%s", out)
		}
		if result.Readiness.Score != 60 || result.Readiness.TotalWeight != 100 || result.Readiness.DoneCount != 1 || result.Readiness.NotDoneCount != 1 {
			t.Errorf("unexpected readiness summary: %+v", result.Readiness)
		}
		if result.Readiness.MinScore != 80 || result.Readiness.Passing {
			t.Errorf("expected minScore 80 and not passing, got minScore=%d passing=%v", result.Readiness.MinScore, result.Readiness.Passing)
		}
		if len(result.Readiness.Checks) != 2 || result.Readiness.Checks[0].Status != "done" || result.Readiness.Checks[1].Status != "not-done" {
			t.Errorf("unexpected readiness checks: %+v", result.Readiness.Checks)
		}
		if result.Readiness.Expires != "2099-12-31" {
			t.Errorf("expected expires 2099-12-31, got %s", result.Readiness.Expires)
		}
	})
}

func TestReadinessValidateGate(t *testing.T) {
	t.Parallel()

	t.Run("--readiness fails when score below minScore", func(t *testing.T) {
		t.Parallel()
		path := writeReadinessBundle(t) // score 60 < minScore 80 → gate fails
		out, err := runCommand(t, nil, "validate", "--readiness", path)
		if err == nil {
			t.Fatalf("expected --readiness to fail, got:\n%s", out)
		}
		assertContains(t, out, "READINESS_GATE_UNMET")
	})

	t.Run("plain validate passes (gate off by default)", func(t *testing.T) {
		t.Parallel()
		path := writeReadinessBundle(t)
		out, err := runCommand(t, nil, "validate", path)
		if err != nil {
			t.Fatalf("expected plain validate to pass, got: %v\n%s", err, out)
		}
		assertContains(t, out, "is valid")
	})

	t.Run("--readiness fails when assessment expired", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "expired-assessment")
		yaml := `pactoVersion: "1.2"
service:
  name: expired-svc
  version: 1.0.0
readiness:
  expires: "2020-01-01"
  minScore: 50
  checks:
    - id: dashboard
      type: url
      evidence: https://grafana.example.com/d/expired-svc
      weight: 100
      status: done
`
		path := writeBundleDir(t, dir, yaml, nil)
		out, err := runCommand(t, nil, "validate", "--readiness", path)
		if err == nil {
			t.Fatalf("expected --readiness to fail on expired assessment, got:\n%s", out)
		}
		assertContains(t, out, "READINESS_GATE_UNMET")
	})

	t.Run("--readiness passes when score meets minScore and current", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "passing-gate")
		yaml := `pactoVersion: "1.2"
service:
  name: passing-svc
  version: 1.0.0
readiness:
  expires: "2099-12-31"
  minScore: 60
  checks:
    - id: dashboard
      type: url
      evidence: https://grafana.example.com/d/passing-svc
      weight: 60
      status: done
    - id: security-review
      type: ticket
      evidence: SEC-999
      weight: 40
      status: deferred
`
		path := writeBundleDir(t, dir, yaml, nil)
		out, err := runCommand(t, nil, "validate", "--readiness", path)
		if err != nil {
			t.Fatalf("expected --readiness to pass, got: %v\n%s", err, out)
		}
		assertContains(t, out, "is valid")
	})
}

func TestReadinessDoc(t *testing.T) {
	t.Parallel()
	path := writeReadinessBundle(t)
	out, err := runCommand(t, nil, "doc", path)
	if err != nil {
		t.Fatalf("doc failed: %v\n%s", err, out)
	}
	assertContains(t, out, "Readiness")
	assertContains(t, out, "Score `60/100` · gate `80` · expires `2099-12-31`")
	assertContains(t, out, "| ID | Type | Category | Status | Evidence | Weight | Description |")
	assertContains(t, out, "| `dashboard` | `url` | `observability` | `done` |")
	assertContains(t, out, "Revision History")
}

// TestReadinessOCIRoundtrip pushes a pactoVersion 1.1 readiness contract to the
// registry and pulls it back, asserting the readiness section survives the OCI
// pack/push/pull cycle intact. A regression in bundle tar packing that dropped
// or corrupted the 1.1 readiness block would otherwise pass silently (explain on
// the local bundle would still work).
func TestReadinessOCIRoundtrip(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	path := writeReadinessBundle(t)
	ref := "oci://" + reg.host + "/readiness-svc:1.0.0"

	if out, err := runCommand(t, reg, "push", ref, "-p", path); err != nil {
		t.Fatalf("push failed: %v\n%s", err, out)
	}

	pullDir := filepath.Join(t.TempDir(), "pulled")
	if out, err := runCommand(t, reg, "pull", ref, "-o", pullDir); err != nil {
		t.Fatalf("pull failed: %v\n%s", err, out)
	}

	// Explain the pulled bundle: readiness must be present and identical.
	out, err := runCommand(t, reg, "explain", pullDir)
	if err != nil {
		t.Fatalf("explain on pulled bundle failed: %v\n%s", err, out)
	}
	assertContains(t, out, "Pacto Version: 1.2")
	assertContains(t, out, "Readiness:")
	assertContains(t, out, "Gate: FAIL (score 60 / minScore 80)")
	assertContains(t, out, "Earned Weight: 60")
	assertContains(t, out, "Total Weight: 100")
	assertContains(t, out, "Expires: 2099-12-31")
	assertContains(t, out, "Status: 1 done, 0 partial, 1 not-done, 0 deferred")
	assertContains(t, out, "dashboard")
	assertContains(t, out, "security-review")
	assertContains(t, out, "observability")
	assertContains(t, out, "security")

	// The roundtripped bundle must be byte-equivalent at the contract level:
	// diffing the local source against the pulled copy reports no changes.
	diffOut, diffErr := runCommand(t, reg, "diff", path, pullDir)
	if diffErr != nil {
		t.Fatalf("diff failed: %v\n%s", diffErr, diffOut)
	}
	assertContains(t, diffOut, "No changes")
}

func TestReadinessValidate(t *testing.T) {
	t.Parallel()

	t.Run("valid 1.1 readiness contract", func(t *testing.T) {
		t.Parallel()
		path := writeReadinessBundle(t)
		out, err := runCommand(t, nil, "validate", path)
		if err != nil {
			t.Fatalf("validate failed: %v\n%s", err, out)
		}
		assertContains(t, out, "is valid")
	})

	t.Run("duplicate readiness id rejected", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "dup-id")
		yaml := `pactoVersion: "1.2"
service:
  name: dup-svc
  version: 1.0.0
readiness:
  expires: "2099-12-31"
  checks:
    - id: dashboard
      type: url
      evidence: https://x
      weight: 50
      status: done
    - id: dashboard
      type: url
      evidence: https://y
      weight: 50
      status: done
`
		path := writeBundleDir(t, dir, yaml, nil)
		out, _ := runCommand(t, nil, "validate", path)
		assertContains(t, out, "DUPLICATE_READINESS_ID")
		assertContains(t, out, "readiness.checks[1].id")
	})

	t.Run("invalid expires date rejected", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "bad-date")
		yaml := `pactoVersion: "1.2"
service:
  name: bad-date-svc
  version: 1.0.0
readiness:
  expires: not-a-date
  checks:
    - id: dashboard
      type: url
      evidence: https://x
      weight: 50
      status: done
`
		path := writeBundleDir(t, dir, yaml, nil)
		out, _ := runCommand(t, nil, "validate", path)
		assertContains(t, out, "INVALID_READINESS_EXPIRES")
	})

	t.Run("readiness rejected under pactoVersion 1.0", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "v10-readiness")
		yaml := `pactoVersion: "1.0"
service:
  name: v10-svc
  version: 1.0.0
readiness:
  expires: "2099-12-31"
  checks:
    - id: dashboard
      type: url
      evidence: https://x
      weight: 50
      status: done
`
		path := writeBundleDir(t, dir, yaml, nil)
		out, err := runCommand(t, nil, "validate", path)
		if err == nil {
			t.Fatalf("expected validation failure for readiness under 1.0, got:\n%s", out)
		}
		assertNotContains(t, out, "is valid")
	})

	t.Run("unsupported pactoVersion rejected", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "bad-version")
		yaml := `pactoVersion: "2.0"
service:
  name: bad-version-svc
  version: 1.0.0
`
		path := writeBundleDir(t, dir, yaml, nil)
		out, _ := runCommand(t, nil, "validate", path)
		assertContains(t, out, "UNSUPPORTED_PACTO_VERSION")
	})
}

func TestReadinessPolicyEnforcement(t *testing.T) {
	t.Parallel()

	readinessPolicy := `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["readiness"],
  "properties": {
    "readiness": {
      "type": "object",
      "required": ["checks"],
      "properties": {
        "checks": {
          "type": "array",
          "contains": {
            "type": "object",
            "required": ["id", "type", "weight"],
            "properties": {
              "id": { "const": "dashboard" },
              "type": { "const": "url" },
              "weight": { "minimum": 20 }
            }
          }
        }
      }
    }
  }
}`

	t.Run("contract satisfying readiness policy is valid", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "policy-ok")
		yaml := `pactoVersion: "1.2"
service:
  name: policy-ok-svc
  version: 1.0.0
policies:
  - name: readiness
    schema: policy/schema.json
readiness:
  expires: "2099-12-31"
  checks:
    - id: dashboard
      type: url
      evidence: https://x
      weight: 30
      status: done
`
		path := writeBundleDirWithPolicy(t, dir, yaml, readinessPolicy)
		out, err := runCommand(t, nil, "validate", path)
		if err != nil {
			t.Fatalf("validate failed: %v\n%s", err, out)
		}
		assertContains(t, out, "is valid")
	})

	t.Run("contract violating readiness policy is rejected", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "policy-bad")
		// Dashboard present but weight 10 (< 20) → violates the policy.
		yaml := `pactoVersion: "1.2"
service:
  name: policy-bad-svc
  version: 1.0.0
policies:
  - name: readiness
    schema: policy/schema.json
readiness:
  expires: "2099-12-31"
  checks:
    - id: dashboard
      type: url
      evidence: https://x
      weight: 10
      status: done
`
		path := writeBundleDirWithPolicy(t, dir, yaml, readinessPolicy)
		out, _ := runCommand(t, nil, "validate", path)
		assertContains(t, out, "POLICY_VIOLATION")
	})
}
