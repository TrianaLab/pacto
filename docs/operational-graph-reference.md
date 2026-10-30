# Operational graph reference

This page describes the sources of [the operational graph](operational-graph.md),
the target-state file format, what each query returns and how owners are keyed.

## Sources

The graph is assembled from **sources**, each contributing the revisions and
targets it can observe right now. The dashboard lists them as **Data sources**. A
source is where the graph reads records *from*. A
[collector](model.md#collectors-and-the-evidence-boundary) produces the
compliance evidence that a source carries.

- **Local bundles** (`--local`): the revision a developer is editing. The scan
  defaults to the working directory, descends 8 levels and skips hidden
  directories, `node_modules` and `vendor`. A directory the operating system
  refuses is reported as a gap and stepped over.
- **A contract root and its closure** (`--root <path|oci://ref>`): the root you
  name *plus every revision it declares*, followed transitively. It is the same
  discovery [`pacto mcp --root`](mcp-integration.md) freezes into a catalog,
  through the same resolver. Roots and dependencies that fail to resolve stay
  visible as limitations.
- **Contracts in OCI** (`--oci <ref>`): the published revision catalogue,
  resolved cache-first so a pulled ref works offline.
- **Local OCI cache** (`--cache`): every bundle already pulled to disk. Opt-in,
  because on a machine that has pulled contracts for months the cache holds
  everything anyone ever fetched. That is a useful offline baseline but a poor
  picture of the running fleet.
- **Live Kubernetes** (`--k8s [--namespace]`): Pacto CRs read from a running
  cluster: which revision runs in which target, and its operator-computed
  compliance, findings, coverage and observed runtime.
- **Ingested external evidence** (`--evidence-url <url>`): a running [Evidence
  Server](evidence.md)'s read-only contribution over HTTP, exposing a
  remote environment's signed `EvidenceSet` reports as operational targets.
- **Offline target-state fixtures** (`--target-state`): an unsigned demo and
  test adapter for supplying targets without a cluster.
- **Offline trace exports** (`--traces`, or `--trace-source NAME=PATH` on `pacto
  dashboard` only): observed dependency edges. Pacto reads trace files only and
  ships **no live OTLP receiver** on ports 4317 or 4318. Run a Collector you own
  and point a source at the file it exports.

`pacto fleet` and `pacto impact` read every source flag above. `pacto mcp
--fleet` reads all but `--root`, which on `pacto mcp` selects a frozen catalog
and cannot be combined with `--fleet`. Two are narrower: `impact` takes a single
`--traces` file, and `--trace-source` is `pacto dashboard` only.

### What a target-state fixture looks like

`--target-state` is the only source you author yourself: a single YAML or JSON
document with `schemaVersion: pacto.dev/fleet-targets/v1`. Pacto rejects a second
`---` document, an unknown field or any other `schemaVersion`.

```yaml
schemaVersion: pacto.dev/fleet-targets/v1
targets:
  - service: orders-service          # required: the service this target runs
    name: commerce/orders-service    # required: unique within the scope
    scope: production-eu             # the environment, for example a cluster
    kind: kubernetes-workload        # what sort of target it is
    labels: { env: production, region: eu }
    requestedRef: oci://ghcr.io/acme/orders-service:1.2.0
    resolvedRef: oci://ghcr.io/acme/orders-service:1.2.0
    digest: sha256:…
    compliance: NonCompliant
    coverage: { evaluated: 5, required: 5 }
    evidenceAt: 2026-07-29T09:40:00Z     # when the evidence was gathered
    reconciledAt: 2026-07-29T09:41:00Z   # when the target was last reconciled
    observedRuntime: { replicas: 3 }     # free-form; surfaced as a bounded preview
    findings:
      - code: STATELESS_PERSISTENT_CONFLICT   # required within a finding
        severity: error
        category: RuntimeDrift
        subjectKind: state
        subjectName: orders-db
        message: declared stateless but the observed workload mounts a volume
state:                               # optional: the source's own health
  status: available
  message: ""
```

Only `service` and `name` are required. Omit `evidenceAt` to model a target the
collector could not observe. A file may declare at most
5000 targets. An entry that fails validation is skipped with a
`SOURCE_RECORD_INVALID` limitation and the rest is kept. A failure of the *file*
drops the whole source with `SOURCE_UNAVAILABLE` and marks the snapshot
`partial`. Three fields are closed sets:

- `compliance`: `Compliant`, `NonCompliant`, `Unknown`, `Invalid`, `Reference`,
  `NotEvaluated`, `Warning` or empty
- `findings[].severity`: `error`, `warning`, `info`, `unknown` or empty
- `state.status`: `available`, `stale`, `partial` or `unavailable`. A value
  outside this set is **silently coerced to `available`**, so a typo here reads
  as a healthy source

!!! warning "A malformed fixture is reported the same way as a missing one"
    Both produce exactly `SOURCE_UNAVAILABLE`, and the output omits the parse
    error, even with `-v`. If the source drops and the path is right,
    suspect the file: check `schemaVersion` first, then field spelling and the
    closed sets above.

[`examples/demo/fleet-targets.yaml`](https://github.com/TrianaLab/pacto/blob/main/examples/demo/fleet-targets.yaml)
is a complete worked fixture, and it is the file the live demo runs on.

## Query semantics

The read model answers five kinds of question, each a pure operation with no
I/O. A single snapshot serves concurrent queries.

| Query | Answers |
|-------|---------|
| **search** | Which logical services match this filter (owner, label, status, compliance, capability, dependency, readiness). Bounded and deterministically ordered. |
| **get** | Everything about one service (its owner, revisions, targets, declared dependencies, dependents, tools and skills) or one target. |
| **graph** | Traverse dependencies or dependents from a service, direct or transitive, cycle-safe, with unresolved edges surfaced. |
| **status** | What needs attention: non-compliant or unknown targets, invalid contracts, stale evidence, missing readiness, unresolved dependencies and dependencies outside their accepted range. With no category selected it reports every category. |
| **explain** | Deterministic, structured reasons for a subject's state. An agent can turn them into prose; Pacto itself embeds no model. |

Two more operations sit beside the queries: `snapshot` emits the whole read model and
`reconcile` compares declared dependencies with observed ones. Every answer
carries a `meta` envelope, here from
`pacto fleet search --output-format json` with an unreachable OCI source:

```json
{
  "meta": {
    "schemaVersion": "pacto.dev/fleet/v1",
    "snapshotId": "sha256:c81df8fd572ecaa7c884968e728daff5b47c40b8e8c5cbcf1926c19740db95a9",
    "asOf": "2026-07-29T10:00:00Z",
    "completeness": "partial",
    "limitations": [
      { "code": "SOURCE_UNAVAILABLE", "source": "oci",
        "message": "source oci is unavailable; its records are missing from this snapshot" }
    ],
    "sources": [
      { "id": "local", "kind": "local", "status": "available",
        "lastSuccessfulSync": "2026-07-29T10:00:00Z",
        "revisionCount": 25, "targetCount": 0 },
      { "id": "oci", "kind": "oci", "status": "unavailable",
        "error": { "code": "UNAVAILABLE", "message": "the source is unavailable" },
        "revisionCount": 0, "targetCount": 0 }
    ]
  },
  "total": 1,
  "count": 1,
  "services": [
    { "key": "payments-service", "name": "payments-service",
      "owner": { "team": "payments" }, "status": "NotEvaluated",
      "revisionCount": 6, "targetCount": 0, "sources": ["local"] }
  ]
}
```

`schemaVersion` is the compatibility contract to branch on and `snapshotId` the
content digest that proves two answers came from the same system view. On this
envelope `count` is how many rows the page carries and `total` the whole matched
population. Match on `key`, the canonical identity, because `name` can repeat
across domains. A bounded preview nested in a `get` answer is the
exception: it omits `total` when the walk was itself bounded, so an absent
`total` means "we stopped counting".

Beside the rows, the dashboard's entity list and the TUI carry an aggregate over
the complete matched population, computed before paging:

| Tally | Partitions | Buckets |
|-------|-----------|---------|
| `serviceCompliance` | matched services | compliance states, rolled up from each service's targets |
| `targetCompliance` | matched operational targets | compliance states as observed per target |
| `ownership` | matched services | `consistent` · `conflicting` · `unowned` |
| `readiness` | matched contract revisions | `passing` · `belowThreshold` · `expired` · `notDeclared` |

`notDeclared` is its own readiness bucket so that a missing assessment stays
separate from a failing one.

`get`, `graph` and `explain` name a single subject and carry no envelope: a
missing subject is a *failure*. `pacto fleet get ghost` exits 1
with `service "ghost" not found in the fleet snapshot` on stderr. These answers have no `meta` and so no `completeness`. Read `completeness` from
`search` before you treat a subject miss as an absence.

### Resolved dependencies and accepted ranges

A declared dependency has two separate answers. `resolved: true` means a
service with that name exists in the fleet. `rangeStatus` says whether the
version the fleet holds satisfies the consumer's `compatibility` range:

| `rangeStatus` | Meaning |
|---------------|---------|
| `satisfied` | Every revision of the provider in the fleet (or the one `pacto.lock` pins) is inside the range. |
| `unsatisfied` | None is. `status` reports it as `DEPENDENCY_RANGE_UNSATISFIED` in the `out-of-range` category. |
| `unknown` | The range or a version cannot be read as semver, the provider has no revision in the fleet, or its revisions disagree. |
| *absent* | The dependency declares no range or is unresolved. |

A consumer that asks for `ledger ^2.0.0` while the fleet holds `ledger 1.6.2`
is resolved and unsatisfied.

A dependency that declares [`requires`](contract-reference/dependencies-and-state.md#required-guarantees)
also carries `guaranteeStatus`, checked against every provider revision it can
reach: the one `pacto.lock` pins, or else every revision inside its range.

| `guaranteeStatus` | Meaning |
|-------------------|---------|
| `met` | Every revision it can reach offers what it requires. |
| `unmet` | None does. `unmet` lists what the newest one refuses. |
| `unknown` | The provider is not in the fleet, no revision is in range, its revisions disagree or the caller cannot see it. `guaranteeReason` says which. |
| *absent* | The dependency requires nothing. |

`deprecatedInterfaces` lists each deprecated interface of the newest provider
revision the dependency reaches, with its `deprecated`, `sunset` and
`successor`. The dashboard shows both on the dependency, on the consumer's page
and on the provider's.

## Ownership and the canonical owner key

The graph aggregates and navigates by a canonical owner key **namespaced by which
field named the owner**, written `kind:name`:

1. If owner has `team` → `team:<team>`
2. If owner has `dri` (no team) → `dri:<dri>`
3. If owner has only contacts → no canonical key. The service still counts as
   owned, but has no owner to rank or link to.

The namespace is part of the identity, so `team:payments` and `dri:payments` are
different owners, and only the name is shown on screen, with a `Team` / `DRI` badge
where two owners would otherwise be indistinguishable. The separate free-text
`owner` filter is a human search over team, DRI and contacts. It can match
several owners at once.

`search` and `get` return `owner` in one shape, the contract's own
`{team, dri, contacts}`, and leave it out when no contract declares one. The
owner comes from the newest revision that declares one. A service known only
from Kubernetes has no revisions, so its owner comes from the owner the
operator read from the running contract (`status.contract.owner`).
