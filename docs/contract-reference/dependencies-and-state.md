# Dependencies and state

What a service depends on, what it keeps and how it runs. The other sections are
in [Contract sections](sections.md),
[Configuration and policy](configuration-and-policy.md) and
[Readiness](readiness.md).

## `dependencies`

Declares dependencies on other services via their Pacto contracts.

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `name` | string | Yes | Non-empty identifier for the dependency |
| `ref` | string | Yes | Non-empty. OCI reference (`oci://...`) or local path (`file://...` or bare path) |
| `required` | boolean | Yes | Whether the dependency is mandatory. Has no default — every entry must set it |
| `compatibility` | string | Yes | Non-empty. Valid semver constraint |

!!! note
    `true` means the service cannot function without the dependency; `false` means it degrades gracefully when the dependency is unavailable. This is declared *intent* that consumers read — the dashboard works out the affected consumers from it — not something Pacto checks at deployment time.

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
