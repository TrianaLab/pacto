# Observation sources

How to configure the offline trace exports the dashboard reads observed
dependencies from, and how a configured source reports its own health.

Observed edges come from OTLP/JSON trace files you mount and name. What an
observed edge *is*, and how it reconciles against a declared one, is in
[Observed dependencies and reconciliation](operational-graph.md#observed-dependencies-and-reconciliation).

## Named observation sources

`pacto dashboard --traces <file>` (repeatable, or the `PACTO_DASHBOARD_TRACES`
environment variable) is the ad-hoc form: it names its sources by position
(`observation`, `observation-1`, ...), which is honest for a one-off command line
and wrong for anything written down. A configuration that is written down uses
`pacto dashboard --trace-source NAME=PATH` (repeatable, or
`PACTO_DASHBOARD_TRACE_SOURCES`), where `NAME` is the source's **identity**: it is
what the fleet, the API and the dashboard's Data Source list call it.

Identity and location are deliberately separate. Reordering the configuration
never renames a source; moving the file never renames it either; and two sources
whose files happen to share a basename stay two sources.

A name must be unique across **every** Data Source the dashboard assembles, not
just among the trace sources. The live Kubernetes source, the OCI and local-cache
sources, local bundle roots, target-state fixtures, evidence stores and Evidence
Servers all claim names in the same namespace — so a trace source called `k8s`,
`local` or `oci` collides with one of them, and inside a pod (where there is no
kubeconfig context to name the cluster) the live Kubernetes source is called
exactly `k8s`. A collision is refused **before a snapshot is built**, with an
error naming both claimants. Neither source is renamed: an identity two sources
share is not an identity, and picking a winner would make the dashboard's answer
depend on assembly order.

What that refusal costs depends on who asked for the snapshot. A command that
builds one and exits fails with that error. The long-running dashboard is not
killed by it — the HTTP host keeps serving, and the refused build is an ordinary
refresh failure: a snapshot that was already published stays published and served
while the failure is recorded as degraded, and if no build has ever succeeded
there is no snapshot to serve, so the operational-graph endpoints answer with the
collision error until the names are distinct and a refresh succeeds. The one
outcome that never happens is the ambiguous snapshot: no snapshot ever publishes a
Data Source key owned by two sources.

A named source may read **only inside the directory its file sits in**. That
directory is the source's root, and the read is resolved through it, so a symlink
placed in the storage — by whoever produces the export, or by anyone who can
write to it — cannot walk out to the container's own filesystem. Symlinks
themselves are fine: the internal indirection a projected Kubernetes ConfigMap
volume is built from resolves normally. Only leaving the root is refused, and it
is refused as a source failure, so the Data Source becomes explicitly
unavailable rather than quietly reading something else.

## Operator-managed observation sources

The operator-managed dashboard declares its sources in Helm values:

```yaml
dashboard:
  enabled: true
  observation:
    sources:
      - name: orders            # the stable Data Source identity
        file: traces.json       # a file name, directly inside this source's mount
        existingClaim: orders-trace-export
```

The operator mounts each declared backing **read-only** at
`/var/lib/pacto/observation/<name>/` and configures the dashboard to read exactly
`<mount>/<file>` under the name `<name>`. Nothing is scanned: Pacto opens the
files you declared and no others, never recursively, and never writes to them.
Changing a source changes the pod template, so Kubernetes rolls the dashboard;
reordering the list does not, because order is not identity.

`file` is a plain file name, not a path: no `/`, no whitespace and no comma
(the character that separates fields on the controller's flag). Give a source
its own backing and mount its export at the top of it, rather than reaching into
a subdirectory — which also makes the mount the read root, with nothing above it
in reach. `existingClaim` and `configMap` must be valid Kubernetes object names,
checked when the values are read rather than left to fail at admission after the
Deployment is already being applied. Every value the chart accepts survives the
trip through the controller's flag unchanged; a
[Helm-rendering test](https://github.com/TrianaLab/pacto/blob/main/integrations/kubernetes/internal/dashboard/observation_wire_test.go)
parses the actual rendered argument rather than a second copy of the grammar.

Exactly one backing supplies each source:

| Backing | Use for | Limits |
|---|---|---|
| `existingClaim` | Real exports. Some other workload writes the file into a PVC; the dashboard only reads it. | The claim must exist; a missing PVC blocks pod scheduling, as it would for any workload. |
| `configMap` | Small, static exports — fixtures and deterministic tests. | A ConfigMap caps near 1 MiB. Mounted optional, so a missing ConfigMap degrades that Data Source instead of wedging the pod. |

Storage ownership stays outside Pacto. Whoever owns the claim or the ConfigMap
owns producing, sizing, rotating and deleting the trace export; Pacto is a reader
with no retention policy and no opinion about how the file got there.

This is configuration of **offline** input. Pacto still ships **no live OTLP
receiver**: nothing listens on 4317 or 4318, there is no `/v1/traces` endpoint,
and no collector is deployed as part of the dashboard. If you need live
collection, run a Collector you own and point one of these sources at whatever
file it exports. Two architecture gates hold the line —
`TestOTelObserverStaysOffline` on the analyzer and
`TestOperatorObservationPackagingStaysOffline` on the packaging.

## Source health is not evidence freshness

A configured trace source is a Data Source like any other, and it answers two
independent questions:

- **Source health** — could Pacto read and parse the file? A missing file, an
  unreadable mount, a read that would leave the mount or malformed OTLP/JSON
  makes that source explicitly
  `unavailable`, with a `SOURCE_UNAVAILABLE` limitation naming it. It is never
  silently absent, and a failing source never takes the dashboard down: the k8s,
  OCI, local and evidence sources keep answering, and any other healthy trace
  source keeps contributing.
- **Evidence freshness** — how recent is what the file witnessed? A perfectly
  readable export of last month's traffic is an **available** source carrying
  **stale** evidence; the observed edges carry the window they were seen in.

The Evidence Server is a **different** concept and stays separate: it is the
verification boundary for signed evidence envelopes, which the dashboard consumes
read-only over HTTP. Trace exports do not travel through it, and the registry it
publishes to is never reachable from the dashboard.

---

## See also

- [The operational graph](operational-graph.md) — what observed edges mean and
  how they reconcile against declared ones
- [Offline trace sources for the dashboard](integrations/kubernetes/installation.md#offline-trace-sources-for-the-dashboard)
  — the Helm values in their install context
- [Dashboard container](dashboard-docker.md) — the equivalent environment
  variables outside Kubernetes
- [Evidence protocol](evidence-protocol.md) — the separate, signed evidence path
