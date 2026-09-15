# Fleet sources and freshness

Where the [operational graph](operational-graph.md) gets its facts, and how it
reports the parts it could not see.

## Sources

The graph is assembled from **sources** — a framework-neutral ingestion seam. Each
contributes the revisions and targets it can observe right now. (The dashboard
lists these as **Data sources**; they are not an evidence
[collector](collectors.md), which produces compliance evidence *for* a source to
carry.)

- **Local bundles** (`--local`) — the revision a developer is editing, before it
  is pushed. The scan defaults to the working directory, descends 8 levels and
  skips hidden directories, `node_modules` and `vendor`. A directory the
  operating system refuses is reported as a gap and stepped over, so pointing
  `--local` at a home directory still finds the bundles below the privacy-guarded
  paths it meets on the way; past 10 refusals the rest are summarised as a count.
- **A contract root and its closure** (`--root <path|oci://ref>`) — the root you
  name *plus every revision it declares*, followed transitively. The other
  definition sources stop at what someone listed: a local scan finds the bundles
  in a directory and `--oci` pulls exactly the references you typed, so a bundle
  depending on `oci://ghcr.io/acme/payments:2.1.0` leaves a dangling edge unless
  that reference is passed separately. `--root` follows the declaration instead.
  It is the same discovery [`pacto mcp --root`](mcp-integration.md) freezes into
  a catalog, through the same resolver, so the two cannot disagree about what a
  reference means. Roots and dependencies that do not resolve stay visible as
  limitations rather than vanishing.
- **Contracts in OCI** (`--oci <ref>`) — the published revision catalogue,
  resolved cache-first so a pulled ref works offline.
- **Local OCI cache** (`--cache`) — every bundle already pulled to disk, as an
  offline baseline. Opt-in, unlike the dashboard, which picks the cache up while
  it boots. A snapshot is built from the sources you name, and on a machine that
  has been pulling contracts for months the cache is a record of everything
  anyone ever fetched — other fleets, one-off comparisons, test fixtures — which
  is an offline baseline worth asking for and a fleet nobody operates. Reach for
  it when the registry is unreachable, not to fill an empty screen.
- **Live Kubernetes** (`--k8s [--namespace]`) — Pacto CRs read straight from a
  running cluster: which revision runs in which target and its operator-computed
  compliance, findings, coverage and observed runtime.
- **Ingested external evidence** (`--evidence-url <url>`) — a remote
  environment's signed, versioned [EvidenceSet report](evidence-protocol.md),
  verified and evaluated at ingestion by the
  [Evidence Server](evidence-protocol.md#durable-storage-in-the-registry) and
  published to the contract registry, then exposed as an operational target.
  `--evidence-url` consumes a running Evidence Server's read-only contribution
  over HTTP — the only way in, because the store is a registry the CLI has no
  business holding a credential for.
- **Offline target-state fixtures** (`--target-state`) — an unsigned demo and
  test adapter for supplying targets without a cluster.

Offline trace exports are Data Sources too, claiming names in the same namespace,
but they supply observed dependency edges rather than revisions and targets — see
[Observation sources](observation-sources.md).

Every source flag above is shared by `pacto fleet` and the MCP fleet server.
`pacto impact` reads a narrower set — `--local`, `--root` and `--target-state`,
alongside `--freshness`, a single-file `--traces` and `--include-observed` — so a
blast-radius question is answered from an offline graph. Every source implements
the same small interface, so the read model stays free of Kubernetes, MCP and
dashboard code.

**What a source sent is not what it contributed.** Its record counts
(`revisionCount`, `targetCount`) are the raw records it supplied; the product
entities attributable to it (`contributed`, by kind) are a different and usually
larger set, so the two are reported side by side and never reconciled into one
number. A revision two sources both reported is one record in each count and one
shared entity contributed by both. No source ever sends a *service* record —
services are derived from reported revisions and targets — so a source whose
records are entirely revisions still contributes services. Both counts are
computed over the complete population, never over the bounded entity preview
beside them.

### What a target-state fixture looks like

`--target-state` is the only source you author yourself: a single YAML or JSON
document, read by a strict decoder. A second `---` document, an unknown field or a
`schemaVersion` other than `pacto.dev/fleet-targets/v1` is rejected and the whole
file contributes nothing.

```yaml
schemaVersion: pacto.dev/fleet-targets/v1
targets:
  - service: orders-service          # required: the service this target runs
    name: commerce/orders-service    # required: unique within the scope
    scope: production-eu             # the environment, e.g. a cluster
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

Only `service` and `name` are required on a target — everything else may be
omitted, and an omitted `evidenceAt` is exactly how you model a target the
collector could not observe. The enumerations are closed:

| Field | Accepted values |
| --- | --- |
| `compliance` | `Compliant`, `NonCompliant`, `Unknown`, `Warning`, `Invalid`, `Reference`, `NotEvaluated`, or omitted |
| `findings[].severity` | `error`, `warning`, `info`, `unknown`, or omitted |
| `state.status` | `available`, `partial`, `stale`, `unavailable` (anything else reads as `available`) |

A file may declare at most 5000 targets. An individual entry that fails
validation is skipped with a `SOURCE_RECORD_INVALID` limitation and the rest of
the file is kept; a failure of the *file* — missing, unparseable, wrong schema
version, unknown field — drops the whole source with a `SOURCE_UNAVAILABLE`
limitation and marks the snapshot `partial`.

!!! warning "A malformed fixture is reported the same way as a missing one"
    Both produce exactly `SOURCE_UNAVAILABLE`, and the parse error itself is not
    surfaced — `-v` does not add it. If the source drops and the path is right,
    suspect the file: check `schemaVersion` first, then field spelling.

[`examples/demo/fleet-targets.yaml`](https://github.com/TrianaLab/pacto/blob/main/examples/demo/fleet-targets.yaml)
is a complete worked fixture — compliant, non-compliant, unknown and stale
targets across two scopes — and it is the file the live demo runs on.

---

## Freshness and completeness

Incompleteness is always explicit. A source reports its state; a snapshot and
every query answer carry an **as-of** time, a **completeness** and a list of
**limitations**. Two rules are absolute:

> **An unavailable source is never an empty result.** If a registry is unreachable
> or a cluster is disconnected, its records are *missing*, and the answer says so —
> it is never rendered as "nothing is there".
>
> **Absence of telemetry is not evidence of absence.** A missing observation under
> partial coverage is uncertainty, not a confirmed "no".

One case does not carry the envelope. `get`, `graph` and `explain` name a single
subject, and a subject missing from the snapshot is a *failure*: `pacto fleet get
ghost` exits 1 with
`service "ghost" not found in the fleet snapshot` on stderr and nothing on
stdout whatever `--output-format` said, and the
[MCP equivalents](mcp-integration.md#fleet-query-safety) return the same string
as a tool error. With no `meta` there is no `completeness`, so read that from
`search` or `status` on the same snapshot before reading a subject miss as an
absence.

### Source status and snapshot completeness

Every source reports one status, and the snapshot rolls those up into one
completeness value:

| Term | Where it applies | Meaning |
|------|------------------|---------|
| **available** | source | Reachable and current. |
| **partial** | source and snapshot | The source returned some but not all of its records, or at least one source is degraded. Treat the answer as incomplete knowledge. |
| **stale** | source | The most recent data is older than the freshness window. |
| **unavailable** | source | The source could not be observed at all; its records are absent, not empty. |
| **complete** | snapshot | Every source was available and current. |
| **empty** | snapshot | Every source was available and produced no record. A genuine empty, not a hidden failure. |

The dashboard uses a matching per-section vocabulary — `present`, `empty`,
`not_applicable`, `unavailable` — under [Section
provenance](dashboard-architecture.md#section-provenance-sectionmeta). A failing
source has its error sanitized to a category code (`AUTH_FAILED`, `NOT_FOUND`,
`UNAVAILABLE`, `CANCELLED`) and a generic message, so credentials, tokens and host
names never leak.

A query answer's `meta` lists every source. The **product answers** the dashboard
reads (`/api/fleet/*`, schema version `pacto.dev/fleet-product/v1`) cap that list
at 50, least healthy first, flagging the cut with `sourcesTruncated`; they also
carry `sourceCounts`, every source tallied by health state over the complete
population the list was cut from. An unrecognized status is never folded into a
bucket — `total` stays above the sum of the buckets rather than adding up
perfectly and being wrong.
