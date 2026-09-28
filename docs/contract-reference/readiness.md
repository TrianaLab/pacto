# Readiness

The operational readiness a service declares about itself. The other sections
are in [Contract sections](sections.md),
[Configuration and policy](configuration-and-policy.md) and
[Dependencies and state](dependencies-and-state.md).

## `readiness`

Optional. A `pactoVersion: "2.0"` feature. Declares operational readiness
state for the service in a provider-neutral way. Each claim has a completion status,
optional category and weight. The assessment includes an expiry date and scoring
configuration. Pacto computes a readiness score from claim statuses and weights.

Readiness is a **declared self-assessment**: it is what the service's authors say
they have done, and Pacto checks the arithmetic and the expiry, not the underlying
work. It is therefore a different question from **compliance**, which is decided
from observed [evidence](../evidence.md) about a running workload. The
dashboard keeps them apart: readiness appears on a revision and as a *Needs
attention* category, never as a compliance verdict.

```yaml
readiness:
  expires: "2027-06-30"  # assessment-level expiry (YYYY-MM-DD)
  minScore: 80           # gate: the derived score must be >= this (omitted ⇒ 100)
  partialCredit: 0.5     # weight multiplier for partial claims (omitted ⇒ 0.5)

  claims:
    - id: dashboard
      type: url
      status: done
      category: observability
      weight: 20
      evidence: https://grafana.example.com/d/service-dashboard
      description: Main production dashboard

    - id: runbook
      type: document
      status: partial
      category: documentation
      weight: 15
      evidence: docs/runbook.md
      description: Incident runbook exists but missing escalation procedures

    - id: security-review
      type: ticket
      status: not-done
      category: security
      weight: 25
      evidence: JIRA-1234
      description: Security assessment pending

    - id: legacy-cleanup
      type: ticket
      status: deferred
      category: code-quality
      weight: 10
      evidence: TECH-5678
      description: Post-launch cleanup task

  history:
    - date: "2026-12-15"
      version: 1.0.0
      author: alice
      description: Initial readiness assessment
```

`readiness.expires`, `readiness.claims[]` and per-claim `status` are required when `readiness`
is present. `readiness.minScore`, `readiness.partialCredit` and `readiness.history[]` are optional.

**`readiness` fields:**

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `expires` | string | Yes | Assessment-level expiry as a strict `YYYY-MM-DD` date. If the current date is past this date, ALL claims earn 0 weight regardless of status. Parsing is exact round-trip: zero-padded fields required and the value must re-serialize unchanged, so `2026-1-1`, RFC 3339 timestamps and impossible dates (e.g. `2026-02-30`) are rejected. |
| `minScore` | integer | No | Gate threshold on the same 0–100 scale as the score. Omitted ⇒ `100` (every claim must be complete). Checked by `pacto validate --readiness` and the operator. |
| `partialCredit` | number | No | Multiplier for `partial` status claims (0.0–1.0). Omitted ⇒ `0.5` (half credit). |
| `claims` | [Claim](#claim-fields)[] | Yes | At least one claim. |
| `history` | [HistoryEntry](#history-entry)[] | No | Audit trail of readiness assessment changes. |

### Claim fields

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `id` | string | Yes | Stable readiness requirement id. Pattern: `^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$`. Unique within the contract. Policies usually target this field. |
| `type` | string | Yes | Enum: `url`, `document`, `ticket`, `report`, `artifact`, `identifier`, `other`. Evidence type. |
| `status` | string | Yes | Enum: `done`, `partial`, `not-done`, `deferred`. Completion state for the claim. |
| `category` | string | No | Enum: `architecture`, `testing`, `code-quality`, `observability`, `security`, `documentation`, `infrastructure`, `ci-cd`, `deployment`, `resilience`, `backup-recovery`, `incident-response`, `compliance`, `other`. Categorizes the requirement type. |
| `weight` | integer | Yes | Contribution to the readiness score. Range `0`–`100`. |
| `evidence` | string | Yes | Where the evidence lives — a URL, a bundle-relative file path, a ticket ID, anything. Checked for non-emptiness only: Pacto never fetches it, never resolves a path and never verifies the target exists. This is a claim you are making, not one Pacto audits. (Unlike `interfaces[].ref` and `configurations[].schema`, which must exist in the bundle.) |
| `description` | string | No | Optional human-readable explanation (non-blank when present). |

### History entry

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `date` | string | Yes | Date of the change as `YYYY-MM-DD`. |
| `version` | string | Yes | Contract version at the time of the change. Non-blank. |
| `author` | string | Yes | Person or system that made the change. Non-blank. |
| `description` | string | Yes | Description of what changed. |

The service owner is declared at the contract level, so readiness claims carry no
per-claim owner. The readiness score is **not** stored in the contract — it is
computed by tooling (`pacto explain`, the dashboard and the operator) from claim
statuses, weights, the assessment expiry and the current time.

### Readiness score and gate

```text
If assessment is expired (today > readiness.expires):
  score = 0

Otherwise:
  earnedWeight = sum of earned weights for all in-scope claims
    - deferred claims: excluded from both numerator and denominator
    - done: full weight
    - partial: round(weight × partialCredit)
    - not-done: 0

  totalWeight = sum of weights for all in-scope claims (excluding deferred)
  score = round(earnedWeight / totalWeight × 100)

passing = score >= minScore          # minScore defaults to 100
```

**Worked example.** Using the four claims from the example above (weights `20`, `15`, `25`, `10`):

- `legacy-cleanup` has `status: deferred` → excluded from both numerator and denominator
- In-scope weights: `20 + 15 + 25 = 60` (total)
- `dashboard` (`done`, weight `20`) → earns `20`
- `runbook` (`partial`, weight `15`) → earns `round(15 × 0.5) = 8`
- `security-review` (`not-done`, weight `25`) → earns `0`
- Earned weight: `20 + 8 + 0 = 28`
- Score: `round(28 / 60 × 100) = round(46.7) = 47`

**Weights are relative.** Only the *ratio* of weights matters — the score
normalizes by `totalWeight`, so a `weight` of `20` reads as "20%" only when the
weights sum to 100. They can sum to anything; making them sum to 100 just makes
each read as a percentage directly. `pacto explain` and the dashboard show each
claim's normalized contribution so you never have to do the math.

**The gate (`minScore`)** turns the score from informational into actionable. It
is a readiness threshold: with `minScore: 80` you require 80% weighted completion;
a score below that threshold fails the gate. The gate is evaluated by tooling, not
baked into contract validity:

- `pacto explain` shows `Gate: PASS/FAIL (score N / minScore M)`.
- `pacto validate --readiness` (off by default) **fails** when `score < minScore`.
  It is opt-in because it depends on the current time — making it time-dependent —
  which would otherwise make plain `validate` non-deterministic.
- The operator sets `status.readiness.passing` and the `ReadinessSatisfied`
  condition from the same rule.

Because `minScore` is an authored literal, a [policy](configuration-and-policy.md#policies) can still require
it org-wide (e.g. require `readiness.minScore >= 80`) — presence rules stay in
policies, the threshold bar lives here.

### Requiring claims with policies

The base schema never requires a specific claim. Organizational standards are
expressed as [policies](configuration-and-policy.md#policies) using standard JSON Schema. For example, to
require a `dashboard` claim with `status: done`, `category: observability` and `weight >= 20`:

```json
{
  "type": "object",
  "required": ["readiness"],
  "properties": {
    "readiness": {
      "type": "object",
      "required": ["claims"],
      "properties": {
        "claims": {
          "type": "array",
          "contains": {
            "type": "object",
            "required": ["id", "status", "category", "weight"],
            "properties": {
              "id": { "const": "dashboard" },
              "status": { "const": "done" },
              "category": { "const": "observability" },
              "weight": { "minimum": 20 }
            }
          }
        }
      }
    }
  }
}
```

Combine multiple `contains` under `allOf` to require several claims (e.g.
`dashboard` + `runbook` + `security-review`). Constraints JSON Schema cannot
express — such as "total weight must equal 100" — are left to a future policy
engine.
