# Dependencies, state and readiness

What a service depends on, what it keeps, how it runs and how far along it is.
The other sections are in [Contract sections](sections.md) and
[Configuration and policy](configuration-and-policy.md).

## `dependencies`

Declares dependencies on other services via their Pacto contracts.

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `name` | string | Yes | Non-empty identifier for the dependency |
| `ref` | string | Yes | Non-empty. OCI reference (`oci://...`) or local path (`file://...` or bare path) |
| `required` | boolean | Yes | Whether the dependency is mandatory. Has no default — every entry must set it |
| `compatibility` | string | Yes | Non-empty. Valid semver constraint |

!!! note
    `required` is mandatory (no default) — every dependency must declare it. `true` means the service cannot function without the dependency; `false` means it degrades gracefully when the dependency is unavailable. It is a declaration of *intent* that downstream consumers act on (the dashboard uses it to compute blast radius; deployment tooling may use it to gate rollout), not a deployment-time guard Pacto itself enforces.

### Dependency reference schemes

| Scheme | Example | Description |
|--------|---------|-------------|
| `oci://` | `oci://ghcr.io/acme/auth-pacto:1.0.0` | OCI registry reference (required for `pacto push`) |
| `oci://` (no tag) | `oci://ghcr.io/acme/auth-pacto` | Resolved to the highest semver tag satisfying `compatibility` |
| `file://` | `file://../shared-db` | Local filesystem path |
| *(bare path)* | `../shared-db` | Local filesystem path (shorthand for `file://`) |

When an `oci://` reference omits the tag, pacto queries the registry for available tags and selects the highest semver version that satisfies the `compatibility` constraint. For example, with `compatibility: "^2.0.0"` and available tags `1.0.0`, `2.0.0`, `2.3.0`, `3.0.0`, pacto resolves to `2.3.0`. Tag listings are cached in memory for the duration of the command, so multiple dependencies pointing to the same repository only trigger a single registry query.

!!! warning "`compatibility` selects a version; it does not police one"
    The constraint is consulted **only** when the reference omits its tag. A `ref`
    that already names a tag or a digest is taken as-is, and the resolved version
    is never checked back against `compatibility`. So this validates clean, with
    no warning:

    ```yaml
    dependencies:
      - name: auth
        ref: oci://ghcr.io/acme/auth-pacto:1.0.0   # resolves to 1.0.0 …
        required: true
        compatibility: "^2.0.0"                    # … which this does not satisfy
    ```

    Pinning and constraining are two separate decisions here. If you pin, the pin
    wins; keep `compatibility` truthful anyway, because consumers and the
    [lockfile](../lockfile.md) read it as your declared range.

!!! note
    Validation rejects OCI references whose tag or digest is malformed. Tags must follow the OCI tag grammar (`[A-Za-z0-9_][A-Za-z0-9._-]{0,127}`), and digests must be well-formed (`sha256:<64 hex>` or `sha512:<128 hex>`). The same check applies to config and policy refs.

### Compatibility constraint examples

Pacto uses [Masterminds/semver](https://github.com/Masterminds/semver#checking-version-constraints) constraint syntax:

| Constraint | Matches | Use case |
|------------|---------|----------|
| `^2.0.0` | `>= 2.0.0`, `< 3.0.0` | Accept patches and minors within a major version |
| `~2.1.0` | `>= 2.1.0`, `< 2.2.0` | Accept only patches within a minor version |
| `>= 2.0.0` | `2.0.0` and above (including `3.x`, `4.x`, …) | Track the latest version above a floor |
| `>= 2.0.0, < 4.0.0` | `2.x` and `3.x` only | Constrain to a range of major versions |
| `*` | Any version | Always resolve to the absolute latest |

!!! warning
    Local dependency references (`file://` and bare paths) are only allowed during development. `pacto push` rejects contracts with local dependencies — all refs must use `oci://` before publishing.

Practices:

- **Pin by digest in production.** `oci://...@sha256:...`; a tag-based reference produces a validation warning.
- **Give cloud-managed resources a contract.** A lightweight Pacto contract for GCP Cloud SQL, AWS SNS or Azure Service Bus, referenced as a dependency, makes that dependency explicit and version-tracked alongside your own.
- **Pin the whole closure with `pacto lock`.** When a `pacto.lock` file is present, every resolved dependency must match the pinned digest or the command fails. See [Lockfile](../lockfile.md).

---

## `workload`

Top-level string describing the execution pattern of the workload. Optional. Enum: `service`, `job`, `scheduled`.

| Value | Description |
|-------|-------------|
| `service` | A long-running process that serves requests continuously |
| `job` | A one-shot task that runs to completion and then exits |
| `scheduled` | A task that runs on a recurring schedule (e.g. cron) |

```yaml
workload: service
```

---

## `state`

Top-level section declaring how the service manages state. Optional — a minimal contract (e.g. a lightweight dependency declaration) may omit it entirely. Instead of platforms guessing whether a service needs persistent storage or stable network identity, the contract declares it explicitly.

| Field | Type | Required | Enum values |
|-------|------|----------|-------------|
| `type` | string | Yes | `stateless`, `stateful`, `hybrid` |
| `persistence` | [Persistence](#persistence) | Yes | |
| `dataCriticality` | string | Yes | `low`, `medium`, `high` |

**State types:**

| Value | What it means | Example services |
|-------|---------------|------------------|
| `stateless` | No data retained between requests. Any instance can handle any request. Instances are interchangeable. | REST APIs, reverse proxies, API gateways |
| `stateful` | Retains data between requests. Requires stable storage or instance affinity. | Databases, message brokers, distributed caches |
| `hybrid` | Handles requests statelessly but keeps selective in-memory or local state that enriches behavior. Loss of that state degrades but doesn't break the service. | APIs with local caches, services with in-memory session stores |

**How platforms interpret state:**

The combination of `state.type`, `persistence.scope` and `persistence.durability` tells a platform exactly what infrastructure a service needs — these are platform-agnostic signals, not Kubernetes prescriptions. See [Platform engineers](../platform-engineers.md) for the full contract-field → platform-decision mapping (Deployment/StatefulSet/PVC and the equivalents on Nomad, ECS or a custom platform).

**Data criticality:**

| Value | What it means |
|-------|---------------|
| `low` | Loss of data has minimal impact. Can be regenerated or is non-essential. |
| `medium` | Loss has moderate impact. May require manual recovery. |
| `high` | Loss has severe business impact. Must be prevented. Implies backups, replication, stricter disruption budgets. |

### Persistence

| Field | Type | Required | Enum values |
|-------|------|----------|-------------|
| `scope` | string | Yes | `local`, `shared` |
| `durability` | string | Yes | `ephemeral`, `persistent` |

- **`local`** — data is confined to a single instance. Not shared across replicas.
- **`shared`** — data is shared across all instances via a common store.
- **`ephemeral`** — data can be lost on restart without impact. Caches, temp files, reconstructible state.
- **`persistent`** — data must survive restarts. Requires durable storage.

### State invariants

| Condition | Constraint |
|---|---|
| `type: stateless` | `durability` must be `ephemeral` |

A stateless service with persistent storage is a contradiction — the JSON Schema catches it.

```yaml
state:
  type: stateful
  persistence:
    scope: shared
    durability: persistent
  dataCriticality: high
```

---

## `readiness`

Optional. A `pactoVersion: "2.0"` feature. Declares operational readiness
state for the service in a provider-neutral way. Each claim has a completion status,
optional category and weight. The assessment includes an expiry date and scoring
configuration. Pacto computes a readiness score from claim statuses and weights.

Readiness is a **declared self-assessment**: it is what the service's authors say
they have done, and Pacto checks the arithmetic and the expiry, not the underlying
work. It is therefore a different question from **compliance**, which is decided
from observed [evidence](../evidence-protocol.md) about a running workload. The
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
| `minScore` | integer | No | Gate threshold on the same 0–100 scale as the score. Omitted ⇒ `100` (every claim must be complete). Enforced by `pacto validate --readiness` and the operator. |
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

If the assessment is expired (current date past `readiness.expires`), the score is `0` regardless of individual claim statuses.

**Weights are relative.** Only the *ratio* of weights matters — the score
normalizes by `totalWeight`, so a `weight` of `20` reads as "20%" only when the
weights sum to 100. They can sum to anything; making them sum to 100 just makes
each read as a percentage directly. `pacto explain` and the dashboard show each
claim's normalized contribution so you never have to do the math.

**Deferred claims** are excluded entirely from scoring. Use `status: deferred` for
post-launch cleanup tasks or requirements that don't apply to the current service stage.

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

Because `minScore` is an authored literal, a [policy](configuration-and-policy.md#policies) can still enforce
it org-wide (e.g. require `readiness.minScore >= 80`) — presence rules stay in
policies, the threshold bar lives here.

### Enforcing readiness with policies

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
