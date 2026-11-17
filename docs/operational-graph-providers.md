# Detected providers

This page describes the providers Pacto detected: the systems each
[source](operational-graph-reference.md#sources) read evidence from, what
Pacto uses each one for and how sure it is.

## The rows

`pacto fleet providers`, `GET /api/fleet/providers`, the `providers` field of
`pacto_fleet_status` and the Detected providers block on Data sources show the
same rows. Each row carries `id`, `source`, `cluster`, `name`, `evidence`,
`usedFor`, `status` and `limitations`, plus `objects` and `objectsTotal` when
the evidence names objects. The evidence never names a namespace.

Each cluster reports two rows, from two sources: the Kubernetes source (such
as `prod`) and its workload source (`prod-workloads`).

- **Pacto CRD**: `served (pacto.trianalab.io/v1alpha1)` or `not installed`.
  Pacto uses it for declared targets.
- **Kubernetes workloads**: how many Deployments, StatefulSets, DaemonSets and
  CronJobs the [workload source](operational-graph-inferred.md) listed. A kind
  it could not list is `unreadable`, never 0, and one it listed in only some
  namespaces reads `at least 12 Deployments (some unreadable)`. Pacto uses it
  for inferred services.

```bash
$ pacto fleet providers --k8s
PROVIDER              EVIDENCE                                             USED FOR
Pacto CRD             served (pacto.trianalab.io/v1alpha1)                 declared targets
Kubernetes workloads  412 Deployments, DaemonSets unreadable, 31 CronJobs  inferred services (partial: source prod-workloads could not list DaemonSets in any namespace: forbidden)
```

With two or more clusters the table gains a leading `CLUSTER` column. With no
Kubernetes source or workload source it prints `No providers detected: this
view reads no Kubernetes cluster (add --k8s)`. When no source reported a service or
a target, the answer's completeness is `empty`, so the CLI also prints
`warning: answer is empty` on stderr while it still lists the rows.

## Detected rows

With `--detected`, the rows the operator
[detected in a cluster](integrations/kubernetes/detection.md)
join from a source of kind `detection`: Registry, Cloud, Sign-in, Exposure,
Metrics and Nodes. Each carries five more fields:

| Field | Meaning |
|---|---|
| `provenance` | Always `inferred` |
| `asOf` | When the operator read it; older than 15 minutes is `stale` |
| `hint` | A command to review, set only when one candidate holds most namespaces. MCP omits it |
| `details` | Other candidates or facts, at most 5 |
| `scope` | `cluster` for a row read cluster-wide |

Detected rows list after the rows a source reported. A detected row never
clears a gate and never changes the answer's completeness. An entry the operator could not read is `unavailable` with a
`DETECTION_<KIND>_UNKNOWN` limitation, and a partial one carries
`DETECTION_<KIND>_PARTIAL`. A row naming a cluster no Kubernetes source of
the snapshot reads carries `DETECTION_CLUSTER_MISMATCH`, and its source
reads `stale`. A restricted
view clears the evidence, hint and details of a row that names a namespace
you cannot read, or of a cluster-wide row when you cannot read the cluster
scope.

## Not read and not installed

A cluster Pacto could not reach contributes no row. Every surface names its
source as `not read` with the error, such as `prod  not read (connection
refused)`. Only a cluster that answered without the Pacto CRD says `not
installed`.

A row is never healthier than its source. A row that is not available always
carries a limitation that says why. `PROVIDER_SOURCE_DEGRADED` says so when the
source itself is partial or stale, when the source marked the row partial
without a reason or when a restricted view hid every other reason.

## Restricted views

A restricted view keeps every row. Objects in namespaces you cannot read and
cluster-scoped objects are hidden, as are limitations that name them, and so is
the name of a cluster where you can see no target. A count stays only where you
can read the namespace and list the workload kind. The rest becomes one hidden
total, such as `12 Deployments (+30 hidden by your permissions)`. Hiding
objects or counts never makes the answer partial.
