# Dependencies and state

Three fields: `dependencies` lists the other services this one calls, `workload`
says how it runs (a long-running service, a job or a scheduled job) and `state`
says what data it keeps. Impact analysis, the lockfile and the Kubernetes
operator all read them. The other sections are
in [Contract sections](sections.md),
[Configuration and policy](configuration-and-policy.md) and
[Readiness](readiness.md).

## `dependencies`

Declares dependencies on other services via their Pacto contracts.

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `name` | string | Yes | Non-empty identifier for the dependency |
| `ref` | string | Yes, unless `external` | Non-empty. OCI reference (`oci://...`) or local path (`file://...` or bare path) |
| `required` | boolean | Yes | Whether the dependency is mandatory. Has no default, so every entry must set it |
| `compatibility` | string | With `ref` | Non-empty. Valid semver constraint |
| `external` | boolean | No | `true` when something outside this Pacto system delivers the dependency. Needs `pactoVersion: "2.1"` |
| `version` | string | Only for `external` without `ref` | The version it runs against, as free text |
| `requires` | list | No | Guarantees this service relies on from the provider. See [Required guarantees](#required-guarantees). Needs `pactoVersion: "2.2"` |

!!! note
    `true` means the service cannot function without the dependency. `false` means it degrades gracefully when the dependency is unavailable. The field is declared *intent*: consumers read it and the dashboard uses it to work out the affected consumers. Pacto checks nothing about it at deployment time.

### Required guarantees

Each `requires` entry names a provider `interface` and the
[guarantees](sections.md#guarantees) this service relies on. The provider must
declare each one at least as strong as written.

```yaml
dependencies:
  - name: orders
    ref: oci://ghcr.io/acme/pacto/orders
    compatibility: "^2.0.0"
    required: true
    requires:
      - interface: order-events
        delivery: at-least-once
        dedupeWindow: 5m
```

`pacto validate` reads the provider contract and reports every unmet guarantee
in one run as `GUARANTEE_UNMET`, naming both services, the guarantee and both
values. It is an error on a required dependency and a warning on an optional
one. When Pacto cannot read the provider contract, nothing confirms the
requirement, so validation warns with `GUARANTEE_UNVERIFIED` and reports the
`guarantees` stage as `SKIP` with a reason naming the dependency. The stage
passes only when every requirement was checked. `pacto graph`
marks each refused edge. [`pacto impact`](../impact.md) lists the consumers
whose requirement a change breaks and blocks the release when one of them runs.

### External dependencies

`external: true` marks a dependency that something outside this Pacto
installation delivers. Your company may still run it. Pacto expects no running
instance of it, never proposes moving it first and leaves it out of the
unresolved list. It takes one of two forms:

```yaml
dependencies:
  - name: ledger                 # someone wrote a contract for it
    ref: oci://ghcr.io/acme/pacto/ledger
    compatibility: "^4.0.0"      # compared like any other dependency
    required: true
    external: true
  - name: model                  # no contract: name the version instead
    external: true
    version: anthropic/claude-sonnet-4-5
    required: true
```

With `ref`, versions are compared as usual. Without it, `version` is required:
it shows in the graph and in [`pacto diff`](diff.md), where any change is
potentially breaking. Pacto has no contract to compare it with, so anything
that relies on it is Unknown and never passes. A dependency with no `ref` and no
`external: true` fails validation with `DEPENDENCY_REF_REQUIRED`. An external
dependency with no `ref` and no `version` fails with `EXTERNAL_VERSION_REQUIRED`.

### A dependency whose contract is not published yet

For a service your Pacto system delivers whose contract nobody has published
yet, leave `external` out. Set `ref` to the repository its contract will be
published at and `compatibility` to the versions you accept:

```yaml
dependencies:
  - name: ledger
    ref: oci://ghcr.io/acme/contracts/ledger   # published later
    compatibility: "^1.0.0"
    required: true
```

The contract validates. Until `ledger` publishes, Pacto lists the dependency
as unresolved, so the gap stays visible. With `external: true` and a `version`
in its place, compatibility stays Unknown for good.

### Dependency reference schemes

| Scheme | Example | Description |
|--------|---------|-------------|
| `oci://` | `oci://ghcr.io/acme/auth-pacto:1.0.0` | OCI registry reference (required for `pacto push`) |
| `oci://` (no tag) | `oci://ghcr.io/acme/auth-pacto` | Resolved to the highest semver tag satisfying `compatibility` |
| `file://` | `file://../shared-db` | Local filesystem path |
| *(bare path)* | `../shared-db` | Local filesystem path (shorthand for `file://`) |

When an `oci://` reference omits the tag, pacto queries the registry for available tags and selects the highest semver version that satisfies the `compatibility` constraint. For example, with `compatibility: "^2.0.0"` and available tags `1.0.0`, `2.0.0`, `2.3.0`, `3.0.0`, pacto resolves to `2.3.0`. Pacto caches tag listings in memory for the duration of the command, so dependencies that point to the same repository share one registry query.

!!! warning "`compatibility` applies only to untagged references"
    Pacto reads the constraint **only** when the reference omits its tag. A `ref`
    that names a tag or a digest resolves to exactly that version, and validation
    skips the `compatibility` check. This example validates with no warning:

    ```yaml
    dependencies:
      - name: auth
        ref: oci://ghcr.io/acme/auth-pacto:1.0.0   # resolves to 1.0.0 …
        required: true
        compatibility: "^2.0.0"                    # … which this does not satisfy
    ```

    Pinning and constraining are separate decisions. If you pin, the pin wins.
    Keep `compatibility` accurate anyway, because consumers and the
    [lockfile](../lockfile.md) read it as your declared range.

    At runtime the Kubernetes operator does check the range: when the provider
    runs under its own Pacto resource, the contract version it reports outside
    `compatibility` is `DEPENDENCY_VERSION_MISMATCH`, an error for a required
    dependency and a warning for an optional one. An unknown version stays
    unverified.

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
    Use local dependency references (`file://` and bare paths) during development. `pacto push` rejects contracts with local dependencies, so switch every ref to `oci://` before you publish.

Practices:

- **Pin by digest in production.** Use `oci://...@sha256:...`. A tag-based reference produces a validation warning.
- **Give cloud-managed resources a contract when you can.** A lightweight Pacto contract for GCP Cloud SQL, AWS SNS or Azure Service Bus, referenced as an external dependency, makes it version-tracked alongside your own. When nobody writes one, name its version with `version`.
- **Pin the whole closure with `pacto lock`.** When a `pacto.lock` file is present, every resolved dependency must match the pinned digest or the command fails. See [Lockfile](../lockfile.md).

---

## `workload`

Top-level string describing the execution pattern of the workload. Optional. Enum: `service`, `job`, `scheduled`.

| Value | Description |
|-------|-------------|
| `service` | A long-running process that serves requests continuously |
| `job` | A one-shot task that runs to completion and then exits |
| `scheduled` | A task that runs on a recurring schedule (for example cron) |

```yaml
workload: service
```

---

## `state`

Top-level section declaring how the service manages state. Optional: a minimal contract, such as a lightweight dependency declaration, can omit it. The contract tells the platform whether the service needs persistent storage or a stable network identity.

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
| `hybrid` | Handles requests statelessly but keeps selective in-memory or local state that enriches behavior. Losing that state degrades the service but leaves it running. | APIs with local caches, services with in-memory session stores |

**How platforms interpret state:**

The combination of `state.type`, `persistence.scope` and `persistence.durability` tells a platform what infrastructure a service needs. These signals are platform-agnostic. See [Platform engineers](../platform-engineers.md) for the full contract-field → platform-decision mapping (Deployment, StatefulSet, PVC and the equivalents on Nomad, ECS or a custom platform).

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

- **`local`**: data stays on a single instance. Each replica has its own.
- **`shared`**: all instances share data through a common store.
- **`ephemeral`**: data can be lost on restart without impact. Caches, temp files and reconstructible state.
- **`persistent`**: data must survive restarts. Requires durable storage.

At runtime the Kubernetes operator reads `scope` from the workload: one
PersistentVolumeClaim in the pod template of a workload with more than one
replica is shared, so `local` scope with such a claim is `PERSISTENCE_MISMATCH`.
`shared` scope with per-replica storage stays unverified, because the shared
store may live outside the cluster.

### State invariants

| Condition | Constraint |
|---|---|
| `type: stateless` | `durability` must be `ephemeral` |

The JSON Schema rejects a stateless service with persistent storage.

```yaml
state:
  type: stateful
  persistence:
    scope: shared
    durability: persistent
  dataCriticality: high
```
