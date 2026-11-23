# Operational graph reference

This page describes the sources of [the operational graph](operational-graph.md),
the target-state file format, what each query returns, how owners are keyed
and how services are grouped by domain.

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
  compliance, findings, coverage and observed runtime. The operator also
  reports runtime calls no contract declares and callers with no contract,
  which join the graph as `observed`. It reports only undeclared calls, so
  this observation alone leaves every declared edge `insufficient`. The
  source id is the cluster UID when the source can read the `kube-system`
  namespace, otherwise the context name. With more than one cluster, target
  keys gain a cluster component; see
  [Clusters and target keys](operational-graph-clusters.md).
  The externals the operator reads from a workload's pod spec (`status.externals`)
  join as `inferred` edges to external systems. A Pacto whose status predates
  them, reports incomplete coverage or is stale adds `EXTERNALS_UNREPORTED` to
  its target, and the external answers that consume it read `partial`.
- **Kubernetes workloads** (`--k8s`): workloads no Pacto resource targets join
  as [inferred services](operational-graph-inferred.md), `declaration: inferred`.
  A runtime call with an inferred service on one end joins as an `inferred`
  edge in `inferredRelationships`, and a Pacto target that takes over one
  records a replacement in `replacements`.
  Both report [detected providers](operational-graph-providers.md).
- **Service mesh** (`--k8s`): the calls Istio, Linkerd or Cilium Hubble
  already record in the last hour, read from the cluster's Prometheus or
  Hubble Relay as `observed` edges of source kind `mesh` (`<cluster>-mesh`).
  Like operator detection the source is advisory: a partial mesh never makes
  the answer partial. See
  [Calls a service mesh records](operational-graph-mesh.md).
- **Operator detection** (`--detected k8s:<namespace>/<name>` or a file,
  `PACTO_DASHBOARD_DETECTED` for `pacto dashboard`): what the operator
  [detected in a cluster](integrations/kubernetes/detection.md),
  read from its `<release>-detected` ConfigMap as source kind `detection`. It
  adds only [provider rows](operational-graph-providers.md#detected-rows),
  each with `provenance: inferred`. The source is advisory: a missing,
  unreadable, foreign or future-dated ConfigMap leaves it `unavailable` with
  the reason and one older than 15 minutes leaves it `stale`. Neither makes
  the answer partial. The same reference also reads the
  [workloads the operator linked](integrations/kubernetes/linked-workloads.md)
  to a published contract with no Pacto, from the ConfigMap's `linked.json`
  key (or a `linked.json` file beside the detection file), as source kind
  `kubernetes-linked`: each is a target of its contract revision with a
  `link` (`signal`, `value`, `since` and `otherVersions`), stale after three
  minutes. This source counts toward completeness: an operator that has
  written no `linked.json` key yet, such as one older than the linker, leaves
  it `unavailable` with `NOT_FOUND` and the answer partial.
- **Ingested external evidence** (`--evidence-url <url>`): a running [Evidence
  Server](evidence.md)'s read-only contribution over HTTP, exposing a
  remote environment's signed `EvidenceSet` reports as operational targets.
- **Target snapshots** (`--target-snapshot <oci://ref>`, repeatable): the
  signed snapshot each cluster's operator publishes to
  `<registry>/pacto-targets:cluster-<uid>`, read with registry access and no
  kubeconfig. A repository reads every `cluster-*` tag (at most 32); a tag or
  digest pins one cluster. Each snapshot becomes up to three sources with the
  same cluster: `targets-<uid>` of kind `target-snapshot` (its Pacto
  resources), `targets-<uid>-workloads` of kind `kubernetes-workloads` (its
  inferred services) and `targets-<uid>-evidence` of kind `evidence-http` (its
  Evidence Server's targets). `pacto impact` and `pacto ci` derive the
  repository from the change's `oci://` ref; see
  [Where consumers come from](ci.md#where-consumers-come-from) and
  [Target snapshots](#target-snapshots).
- **Offline target-state fixtures** (`--target-state`): an unsigned demo and
  test adapter for supplying targets without a cluster.
- **Offline trace exports** (`--traces`, or `--trace-source NAME=PATH` on `pacto
  dashboard` only): observed dependency edges read from an OTLP/JSON file a
  Collector you own exports.
- **Trace backends** (`pacto dashboard --detected`): when the operator
  [detected](operational-graph-providers.md#trace-backends) Jaeger, or Tempo
  or an OpenTelemetry Collector with a Prometheus next to it, the dashboard
  reads their calls of the last 15 minutes every 30 seconds as `observed`
  edges of source kind `traces`: `detected-jaeger` from Jaeger's
  `/api/dependencies` and `detected-service-graph` from the
  `traces_service_graph_request_total` series in Prometheus. Both are advisory
  and read-only, and like Mira they see only instrumented traffic, so a
  declared edge they did not see stays `insufficient`. A backend that does not
  answer leaves the source partial with its row `unavailable`. `pacto fleet`
  and `pacto mcp` show the detected rows and read no backend.
- **OTLP receiver** (`pacto dashboard --otlp-receiver http://HOST:PORT`, set by
  the operator to port 4318): a fallback for a Collector with no Prometheus.
  Collectors in the cluster export OTLP/HTTP JSON to `POST /v1/traces`; the
  dashboard maps spans to calls, keeps no span and serves nothing else on that
  port. Source `otlp-receiver`, of kind `otlp-receiver`, reports the calls of
  the last 15 minutes. Anything in the cluster can send to it, so its source is
  `cautionOnly`: its edges add uncontracted callers and undeclared calls, and
  never mark a declared dependency matched or raise impact confidence to
  `corroborated`. It turns off while the service graph or Jaeger proved
  complete coverage in the last minute. Nothing listens on 4317 and protobuf
  requests are refused.
- **Mira** (`--mira-source <url>`, or `PACTO_DASHBOARD_MIRA_SOURCES` for `pacto
  dashboard`): the calls between services that a
  [Mira](investigate-a-problem.md) node saw in its last 15 minutes of traces,
  read from its service map as `observed` edges of source kind `mira`. Like
  the operator, Mira sees only part of the traffic, so a declared edge it did
  not see stays `insufficient`. A failed read leaves the source unavailable; a
  truncated or empty map, or calls Pacto cannot attribute to a named service,
  leave it partial.

`pacto fleet` and `pacto impact` read every source flag above. `pacto mcp
--fleet` reads all but `--root`, which on `pacto mcp` selects a frozen catalog
server and takes no other mode. Two are narrower: `impact` takes a single
`--traces` file, and `--trace-source` is `pacto dashboard` only.

### Target snapshots

Snapshot sources carry:

| Field or code | Meaning |
|---|---|
| `ref` | The snapshot read, pinned as `<repository>@<digest>` |
| `signer` | The trust entry that verified it: its `name`, or `key:<fingerprint>`; empty when unverified. Never the `publisher` the payload claims |
| `publisher` | Who the payload says published it, such as the operator's ServiceAccount; a claim, with control characters dropped like the publisher's limitations |
| `cluster` | The cluster the snapshot describes, its id the `kube-system` UID |
| `lastSuccessfulSync` | The snapshot's `asOf` |
| `TARGET_SNAPSHOT_UNVERIFIED` | Read with no delivery trust config (`--delivery-trust` or `$PACTO_DELIVERY_TRUST`); the source is `partial` |
| `TARGET_SNAPSHOT_UNSCOPED` | Verified only by a key whose `streams` do not name the cluster's UID (a bare PEM included); `partial` |
| `TARGET_SNAPSHOT_STALE` | Its `asOf` is more than 15 minutes old; `stale` |
| `TARGET_SNAPSHOT_INVALID` | Missing (a pinned tag), a bad signature, a cluster other than its tag, an unknown predicate or an `asOf` in the future; not used, `unavailable` |
| `TARGET_SNAPSHOT_BOUND` | More than 32 cluster tags, a snapshot over 8 MiB or the 20 second read timeout; `partial` or `unavailable` |
| `TARGET_SNAPSHOT_SCOPED` | The publishing operator watches only the namespaces the message names (`controller.watchNamespaces`), so any other namespace is unknown; `partial` |
| Text line | `pacto fleet status` and `pacto fleet snapshot` print one per source, such as `Source targets-<uid> (target-snapshot): available, ref <repository>@<digest>, signed by <signer>, asOf <time>`; `unverified` replaces the signer when no trust entry verified it, and a source that is not available ends with its reason |

### Observed calls under a restricted view

A declared edge any source saw is `matched`. A restricted view hides each
caller with no namespace, from any source, and each unresolved observed name,
counting them in `hiddenDependents` and one `OBSERVED_IDENTITY_UNRESOLVED`
limitation per source.

- A declared external dependency is `matched` when an observed call maps to
  the same identity through the
  [external catalog](external-catalog-reference.md#matching).
- Only a name that matches more than one service, or that is neither a
  service nor an external system, is unresolved.
- An observed or inferred edge to an external system follows its caller: when
  the caller is hidden, the edge is dropped and counted among the external
  system's hidden consumers.
- An inferred edge whose inferred end is hidden is dropped and that end counted
  once, and a replacement shows only when both of its services do.

### Clusters under a restricted view

| Limitation | Raised when |
|------------|-------------|
| `CLUSTER_HIDDEN` | The view hides one cluster: you may read no namespace there, its review failed or Pacto cannot review you there ([Multi-cluster](multi-cluster.md)). A [member](member-clusters.md) reads "cluster eu-prod joined through the registry and has no live channel yet, so its services are hidden from signed-in readers". Its `source` is the cluster id; the answer is partial |
| `CLUSTER_FILTER_UNKNOWN` | A list filtered by a cluster id that no cluster of the answer has, such as a display name or a retired cluster, so its empty list says nothing about it. Completeness is unchanged |

| `meta.clusters[]` field | Meaning |
|------------|-------------|
| `id`, `name`, `nameSource` | The `kube-system` UID and a display name with how it was found |
| `status`, `access`, `targets` | The worst source status, why a restricted view shows nothing of the cluster and how many targets it shows |
| `link.channel` | A [member](member-clusters.md)'s outbound channel: `live`, or `down` when it dropped in the last 15 minutes; absent when it has none |
| `link.since` | When the channel last came up or dropped |
| `link.issuers[]` | Per token issuer, `model`: `shared-issuer`, `group-mapping` (with `mappedBy`, `mappedAt` and `mapping`) or `issuer-not-accepted`. Such issuers raise `CLUSTER_ISSUER_DIFFERS` on Needs attention ([tallies](operational-graph-tallies.md)) |

### What a target-state fixture looks like

`--target-state` is the only source you author yourself: a single YAML or JSON
document with `schemaVersion: pacto.dev/fleet-targets/v1`, or
`pacto.dev/fleet-targets/v2` when it also carries `provisioning` reports. Pacto
rejects a second `---` document, an unknown field, `provisioning` in a v1 file
or any other `schemaVersion`.

```yaml
schemaVersion: pacto.dev/fleet-targets/v1
cluster:                             # optional: the cluster of every target
  id: 9f1c4e2a-5b7d-4c1e-8a3f-2d6b0e9c7a41   # required in the block: kube-system UID
  name: eu-prod                      # a display name
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

A v2 file adds `provisioning`, one entry per report about a resource a
consumer revision asked for:

```yaml
schemaVersion: pacto.dev/fleet-targets/v2
provisioning:
  - service: catalog-service       # required: the consumer
    digest: sha256:32b570087a9cd70b16772e9cb5d3cbc1a6aea6fc31b1c021d9d848c10424b518  # required
    kind: configuration            # required: configuration or dependency
    name: database                 # required: the consumer's entry
    environment: production
    provider: platform-db          # a lowercase slug
    externalRef: databases/catalog-production
    phase: Ready                   # Requested, Provisioning, Ready, Failed or Deleting
    outcome: Observed              # the default; any other outcome carries no phase
    producer: platform-adapter
    observedAt: 2026-11-06T09:00:00Z
```

The file is unsigned, so every surface shows its reports as unverified. See
[Provisioning status](platform-offerings.md#provisioning-status) for how reports join the graph.

[`examples/demo/fleet-targets.yaml`](https://github.com/TrianaLab/pacto/blob/main/examples/demo/fleet-targets.yaml)
is the complete fixture the live demo runs on.

## Query semantics

The read model answers five kinds of question, each a pure operation with no
I/O. A single snapshot serves concurrent queries.

| Query | Answers |
|-------|---------|
| **search** | Which logical services match this filter (owner, label, status, compliance, capability, dependency, readiness). Bounded and deterministically ordered. |
| **get** | Everything about one service (its owner, revisions, targets, declared dependencies, dependents, tools and skills, and the inferred services it `replaces`) or one target. The key of a [replaced inferred service](operational-graph-inferred.md#when-a-contract-takes-over) answers with the service that replaced it and `replacedFrom`, as does **graph**. |
| **graph** | Traverse dependencies or dependents from a service, direct or transitive, cycle-safe, with unresolved edges surfaced. Resolved [configuration references](#configuration-reference-consumers) count as dependencies. The CLI and MCP graph also follow observed-only edges, and each node's `provenance` (`declared`, `observed`, `declared+observed` or `inferred`) says how the edge that first reached it was known. `inferred` has two producers: an external system read from configuration, and a runtime call one of whose ends is an [inferred service](operational-graph-inferred.md#calls-with-no-contract-on-one-end). Inferred edges are listed in `edges` and never count as dependencies or consumers. Rooted at a revision or target, a dependencies traversal takes its first step over that revision's declared edges only; deeper steps are service-level and include observed edges. |
| **status** | What needs attention, in the [attention categories](operational-graph-tallies.md#attention-categories). With no category selected it reports every category. `total` counts every match and `truncated` says the limit cut the list. |
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
content digest that proves two answers share one system view. `count` is the
rows on this page and `total` the whole matched population. Match on `key`,
because `name` can repeat across domains. A bounded preview nested in a `get`
answer omits `total` when its walk was bounded: an absent `total` means "we
stopped counting".

Beside the rows, answers carry tallies over the complete matched population,
computed before paging. The [tallies reference](operational-graph-tallies.md)
lists them, the knowledge-gap filters and the attention categories.

`get`, `graph` and `explain` name a single subject and carry no envelope, so
no `completeness`: a missing subject is a *failure*. `pacto fleet get ghost`
exits 1 with `service "ghost" not found in the fleet snapshot` on stderr. Read
`completeness` from `search` before you treat a miss as an absence.

### Where a service runs

| `placement` | Detail |
|---|---|
| Read from | A service's detail: `pacto fleet entity service <key>`, `pacto_fleet_entity` and the dashboard's service page. Targets cover services delivered without Pacto delivery too |
| Groups | The union of the environments the current revision (`currentRevision`) declares and the `delivery.pacto.trianalab.io/environment` labels the targets report; no label or conflicting labels form one unlabelled group |
| Order | Alphabetical (natural order), unlabelled last; never a promotion order, which [delivery status](integrations/kubernetes/delivery-status.md) gives |
| Declaration | `declaration`: the current revision's entry with its values check, `lifetime` (`not-declared` when absent), `expires`, `owner` and `expired`, inferred against `asOf`. Absent for an environment only targets report |
| Owner | `owner` with `provenance`: `declared` on the environment, `inferred` from the service owner, or `unknown` with a `reason` |
| Counts | `targets` (observed), then `declared`, `notDeclared` and `unknown`: whether the revision each target runs declares that environment. `targetsComplete` is false, with `targetsReason`, when you may not see every target |
| Provisioning | `provisioning`, a tally by phase of the statuses of the service's uses there (`consumedOfferings`, including reported uses of providers with no offering); `provisioningTracked` is false, with `provisioningReason`, when no provider is asked about it there, so the tally is never a zero. When a provider you may not see may provision there, the reason says its status is unknown |
| Revisions | The newest 8 distinct revisions, with `revisionsTotal` and `revisionsTruncated` |
| Differences | `differences`: each value the declared environments disagree on, by configuration and path, with `differencesTotal`, or `differencesError` saying why they are unknown. `pacto explain --environments` computes the same list |
| Restricted | Only targets, revisions and provisioning your view may see count; on a partial answer the CLI heading reads "Environments (targets Pacto can see)" |

| `changes` | Detail |
|---|---|
| Items | Every declared change across the revisions the answer holds, newest first in version order. `kind` is `owner`, `domain`, `offering`, `dependency`, `lifecycle`, `environment` or `finding` (a validation finding that appeared or resolved); `subject` names the dependency, environment or finding; `change` is `present`, `added`, `removed` or `changed`, with `fields`, `from` and `to`. `revision` is the first held revision that shows the change and `previous` the one it was compared with; `provenance` is `declared` and `source` is `contract` or `validation` |
| Times | A change carries a time only in `reached`: per Stage you may read, when its revision first arrived there (`at`), with the `record`, `verified`, `fromStatus` and the recorded `impact` verdict. It is read from delivery records as you when asked (`--reached`, `reached`, `?reached=true`); `times` says whether they were read: `not-read`, `read`, `partial` (with `timesUnreadable`) or `hidden`, with `timesReason` |
| Gaps | `gaps` counts revisions whose contract could not be read; no change is reported across them |
| Completeness | `partial` when your view or a source holds only some of the service's revisions, or when two versions are compared: no source lists every version, so a change is placed between the versions held |

### Configuration-reference consumers

A service that takes a configuration schema from another contract by `ref`
consumes that contract. Once the reference resolves to a revision in the
snapshot, by digest or through the consumer's `pacto.lock`, the edge counts
like a declared dependency: graph traversal, `dependents`, the neighborhood
and hidden counts include it, and [impact](impact-consumers.md#configuration-reference-consumers)
judges the consumer. A neighborhood edge carries the relation
`configuration_reference` and no declared-against-observed comparison, because
a reference never produces runtime calls. Policy references stay out: a policy
checks the consumer's own contract.

The fleet validates configuration references against the revisions it holds.
A reference it cannot place reads `CONFIG_REF_UNVERIFIED`, so a missing
provider never makes a revision invalid and never passes its values. A
relationship carries the provider's
[service lifecycle](contract-reference/sections.md#service-lifecycle) as
`providerLifecycle`.

### Resolved dependencies and accepted ranges

A declared dependency has two separate answers. `resolved: true` means a
service with that name exists in the fleet. `rangeStatus` says whether the
version the fleet holds satisfies the consumer's `compatibility` range:

| `rangeStatus` | Meaning |
|---------------|---------|
| `satisfied` | Every revision of the provider in the fleet (or the one `pacto.lock` pins) is inside the range. |
| `unsatisfied` | None is. `status` reports it as `DEPENDENCY_RANGE_UNSATISFIED` in the `out-of-range` category. |
| `unknown` | The range or a version cannot be read as semver, the provider has no revision in the fleet, or its revisions disagree. With `rangeReason: INFERRED_PROVIDER` the provider is an [inferred contract](inferred-contracts.md) or a revision only the operator signed, which no range is checked against and `fleet versions` never names `latestCompatible`; a view that hides the provider blanks both fields. |
| *absent* | The dependency declares no range, is unresolved or joins an [external system](#external-systems) without a ref, which is never ranged. |

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
where two owners would otherwise be indistinguishable. The free-text `owner`
filter searches team, DRI and contacts and can match several owners.

`search` and `get` return `owner` in one shape, the contract's own
`{team, dri, contacts}`, and leave it out when no contract declares one. The
owner comes from the newest revision that declares one. A service known only
from Kubernetes takes the owner the operator read from the running contract (`status.contract.owner`). A service
counts as `unowned`, in the ownership tally and in attention, only when no
revision declares an owner and no source reports one. A service with no owner
claim whose contract Pacto cannot read counts as `unknown` instead: the
dashboard labels it Owner unknown and never No declared owner.

`get` also returns where the owner comes from; see
[Who may set a service owner](owner-setting.md):

| Field or code | Meaning |
|---|---|
| `ownerProvenance` | `declared` (a revision's `pacto.yaml`), `attested`, `declared-via-target` (the running contract the operator read) or `inferred`, in that precedence |
| `ownerSource` | For `attested` only: the file and line, the commit and the CI identity that signed it, such as `CODEOWNERS:7 at a1b2c3, signed by github.com/acme/invoices/.github/workflows/ci.yml` |
| `provenance.signature.signerRepository`, `signerCommit` | On a revision, the repository and commit an accepted CI signer's certificate names |
| `provenance.ownerRight` | On a revision, the owner rule's verdict on the owner it sets: `allowed`, `code` and `message` |
| `OWNER_CONFLICT` | Also raised when attested revisions of the service were signed from different repositories; no attested owner is set |
| `OWNER_DECLARATION_REFUSED` | A registry revision sets an owner its signer may not set, so its owner is not used |
| `OWNER_DECLARATION_UNVERIFIED` | A registry revision sets an owner the rule could not check (no verified signer or policy, or no earlier owner read), so its owner is not used and the answer is `partial` |

## Business domains

A service's business domain is the [`service.domain`](contract-reference/sections.md#business-domain)
of its newest revision by semver that is valid. An older revision that declares
another domain is a move.
When no revision declares a domain, the service takes the domain the operator
read from the contract each target runs (`status.contract.domain`), as it does
for owners. A value that is not a lowercase slug is never a domain.

Each service is in exactly one group:

- a named domain;
- **No domain**, when a valid revision or the operator says the contract
  declares none;
- **Domain unknown**, when Pacto cannot tell. Pacto has no valid revision of
  the service and no source that reports the domain, such as an operator that
  writes a `status.statusFormat` below 2, or the operators observing its
  targets report different domains. A disagreement also raises a
  `DOMAIN_CONFLICT` limitation.

No domain and Domain unknown are listed and counted like any other domain and
never read as healthy. The API filters them with `businessDomain=(none)` and
`businessDomain=(unknown)`, and marks an unknown domain with
`businessDomainUnknown`. A contract revision's own row shows the domain that
revision declares, while the filter groups it by its service's domain.
Domains are returned only to a query that asks for the `domain` kind.

The registry path in the service key, such as `ghcr.io/acme`, is a different
value: `domain` in the API and Registry on the dashboard.

## Golden paths

See the [golden paths reference](golden-paths-reference.md).

## Platform offerings

See the [platform offerings reference](platform-offerings.md) and
[offering availability and lifecycle](offering-availability.md).

| `AvailabilityState` | Meaning |
|---------------------|---------|
| `available` | Fresh provider targets serve and no consumer reports the provider unreachable. |
| `degraded` | Some provider targets report `INTERFACE_ABSENT`, or some consumer targets cannot reach it. |
| `unavailable` | Every fresh provider target serves none of its interfaces, or with no fresh provider target every consumer target that declares it cannot reach it. |
| `unknown` | Targets are hidden, a target source is incomplete, no fresh evidence exists or no interface was seen answering. |

| Limitation | Raised when |
|------------|-------------|
| `SUCCESSOR_UNRESOLVED` | A provider's declared successor names no revision in the fleet. |
| `HIDDEN_PROVIDERS` | An offering event has providers the viewer cannot see. |
| `OFFERING_NOT_IN_FLEET` | No visible service provides the offering an event names. |

| A service's `consumedOfferings` item | Detail |
|---|---|
| Rule | Every declared dependency or configuration reference on a provider with an offering, plus one on a provider with no offering once a provider reports on it (`offering` empty) |
| `providerOwner` | Who owns the provider, as its offering page names it |
| `providerAvailability` | Whether the provider answers where it runs; separate from the resource's phase |
| `requested` | On the `configuration_reference` row only: each entry, configuration, provider `revision` and `values` the current revision declares, with `provenance: declared`; `requestedTruncated` when the configuration preview was cut |
| `consumption` | On every use that provisions: one entry, `state: not-reported`, `provenance: observed` and a `reason`. A policy reference carries none |
| `hiddenProviders` | On the service detail: at least this many resources come from providers you may not see, counted from the service's own contract and reports and never named; omitted at zero |

The `capability` filter and facet count each declared capability by its key:
`health`, `metrics` or, for an extension, its ref, such as `acme.io/cost`.

## External systems

A ref-less `external: true` dependency joins an entity of kind `external`, one
per canonical identity. The [external catalog reference](external-catalog-reference.md)
covers identities, the join order and catalogs.

| Surface | Behaviour |
|---|---|
| Relationship | `toExternal` names the identity and `resolved` stays `false`, because no fleet service delivers it. It never gets `resolvedRevision` and never counts in impact, dependents or delivery. Only a dependency whose ref names `pacto-inferred/<identity>` gets `rangeStatus: unknown` with `rangeReason: INFERRED_PROVIDER` |
| Provider | `provider` on the record holds the system's [inferred contract](inferred-contracts.md): `repository`, `revisions` (operator-signed first, each group newest first, each with `version`, `digest`, `ref`, `surface` and `signature`), `revisionsTotal`, and `rangeStatus: unknown` with `rangeReason: INFERRED_PROVIDER`. A restricted view that hides any source or consumer drops every `surface`. A contract under `pacto-inferred` whose `service.external` disagrees with its path is attached nowhere and raises `INFERRED_IDENTITY_MISMATCH` |
| Attention | One `INFERRED_PROVIDER` item in the `knowledge` category per inferred contract that ranges check against, counting only visible consumers |
| Snapshot | `externals`, one record per identity, and `externalCatalogs`, the catalogs used with `digest`, `origin`, `copy`, `signature` and `reason`; every answer's meta repeats `externalCatalogs` |
| Record | `key`, `label`, `class`, `aliases`, `sources` (one per consumer and dependency position, each with `provenance`, `match` and `superseded` when a newer consumer revision no longer declares it), `sourcesTotal`, `catalog` (origin, digest and entry; absent when no entry classifies it), `joinedBy` (`identity`, `catalog-alias`, `host` or `normalized-name`), `consumers`, `consumersTotal`, `hiddenConsumers` |
| Entities | `--kind external` (MCP `kind: external`) lists them, `--class` keeps one class; the default listing leaves them out |
| Get | `pacto fleet get external:<identity>` and `pacto_fleet_get` with `service: external:<identity>` |
| Search | A search on text alone, first page, adds up to 20 matching `externals`, by identity, label or alias, with `externalsTotal`; any other filter or a later page lists none |
| Explain | `EXTERNAL_DECLARED` per source, then `EXTERNAL_CATALOG_MATCH`, `EXTERNAL_UNCLASSIFIED` (joined by identity or host) or `EXTERNAL_NAME_ONLY`, `EXTERNAL_SOURCES_TRUNCATED` when consumers are cut or hidden, and `EXTERNAL_CAP` when folded; status is always `Unknown` |
| Graph | Rooted at `external:<identity>`, it walks the consumers and their dependents (an unset direction means `dependents`); an explicit `direction: dependencies` is an invalid query |
| Neighborhood | External systems are leaves beside the services that declare them; focusing one shows its consumers |
| Overview | `externals` and `hiddenExternals` |
| Repository | An external system a revision's [inferred record](contract-reference/inferred-record.md#external-dependencies) names joins as an inferred edge whose sources have kind `repository` and name that `revision`, never as a service dependency, impact or gate input |
| Cap | An answer that names more than 200 external systems folds the rest into `other-egress` and is partial with `EXTERNAL_CAP`; see [the cap](external-catalog-reference.md#the-cap) |
