//go:build e2e

package e2e

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// readinessContractYAML is a pactoVersion 1.1 contract with one current
// (far-future) and one expired (far-past) readiness check, so derived status is
// deterministic regardless of when the suite runs.
const readinessContractYAML = `pactoVersion: "1.1"
service:
  name: readiness-svc
  version: 1.0.0
  owner: team/readiness
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
  checks:
    - id: dashboard
      type: url
      evidence: https://grafana.example.com/d/readiness-svc
      weight: 60
      expires: "2099-12-31"
      description: Main production dashboard
    - id: security-review
      type: ticket
      evidence: SEC-1842
      weight: 40
      expires: "2000-01-15"
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
		assertContains(t, out, "Pacto Version: 1.1")
		assertContains(t, out, "Readiness:")
		assertContains(t, out, "Score: 60")
		// No minScore declared → default 100 → gate fails (score 60 < 100).
		assertContains(t, out, "Gate: FAIL (score 60 / minScore 100)")
		assertContains(t, out, "Current Weight: 60")
		assertContains(t, out, "Total Weight: 100")
		assertContains(t, out, "Expired Checks: 1")
		assertContains(t, out, "dashboard")
		assertContains(t, out, "current")
		assertContains(t, out, "security-review")
		assertContains(t, out, "expired")
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
				Score        int  `json:"score"`
				MinScore     int  `json:"minScore"`
				Passing      bool `json:"passing"`
				TotalWeight  int  `json:"totalWeight"`
				ExpiredCount int  `json:"expiredCount"`
				Checks       []struct {
					ID     string `json:"id"`
					Status string `json:"status"`
				} `json:"checks"`
			} `json:"readiness"`
		}
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, out)
		}
		if result.Readiness == nil {
			t.Fatalf("expected readiness in JSON, got:\n%s", out)
		}
		if result.Readiness.Score != 60 || result.Readiness.TotalWeight != 100 || result.Readiness.ExpiredCount != 1 {
			t.Errorf("unexpected readiness summary: %+v", result.Readiness)
		}
		if result.Readiness.MinScore != 100 || result.Readiness.Passing {
			t.Errorf("expected default minScore 100 and not passing, got minScore=%d passing=%v", result.Readiness.MinScore, result.Readiness.Passing)
		}
		if len(result.Readiness.Checks) != 2 || result.Readiness.Checks[0].Status != "Current" || result.Readiness.Checks[1].Status != "Expired" {
			t.Errorf("unexpected readiness checks: %+v", result.Readiness.Checks)
		}
	})
}

func TestReadinessValidateGate(t *testing.T) {
	t.Parallel()

	t.Run("--readiness fails on a stale contract", func(t *testing.T) {
		t.Parallel()
		path := writeReadinessBundle(t) // security-review expired (2000-01-15), no minScore → 100
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
}

func TestReadinessDoc(t *testing.T) {
	t.Parallel()
	path := writeReadinessBundle(t)
	out, err := runCommand(t, nil, "doc", path)
	if err != nil {
		t.Fatalf("doc failed: %v\n%s", err, out)
	}
	assertContains(t, out, "Readiness")
	assertContains(t, out, "| ID | Type | Evidence | Weight | Expires | Description |")
	assertContains(t, out, "| `dashboard` | `url` |")
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
		yaml := `pactoVersion: "1.1"
service:
  name: dup-svc
  version: 1.0.0
readiness:
  checks:
    - id: dashboard
      type: url
      evidence: https://x
      weight: 50
      expires: "2099-12-31"
    - id: dashboard
      type: url
      evidence: https://y
      weight: 50
      expires: "2099-12-31"
`
		path := writeBundleDir(t, dir, yaml, nil)
		out, _ := runCommand(t, nil, "validate", path)
		assertContains(t, out, "DUPLICATE_READINESS_ID")
		assertContains(t, out, "readiness.checks[1].id")
	})

	t.Run("invalid expires date rejected", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "bad-date")
		yaml := `pactoVersion: "1.1"
service:
  name: bad-date-svc
  version: 1.0.0
readiness:
  checks:
    - id: dashboard
      type: url
      evidence: https://x
      weight: 50
      expires: not-a-date
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
  checks:
    - id: dashboard
      type: url
      evidence: https://x
      weight: 50
      expires: "2099-12-31"
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
		yaml := `pactoVersion: "1.1"
service:
  name: policy-ok-svc
  version: 1.0.0
policies:
  - name: readiness
    schema: policy/schema.json
readiness:
  checks:
    - id: dashboard
      type: url
      evidence: https://x
      weight: 30
      expires: "2099-12-31"
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
		yaml := `pactoVersion: "1.1"
service:
  name: policy-bad-svc
  version: 1.0.0
policies:
  - name: readiness
    schema: policy/schema.json
readiness:
  checks:
    - id: dashboard
      type: url
      evidence: https://x
      weight: 10
      expires: "2099-12-31"
`
		path := writeBundleDirWithPolicy(t, dir, yaml, readinessPolicy)
		out, _ := runCommand(t, nil, "validate", path)
		assertContains(t, out, "POLICY_VIOLATION")
	})
}
