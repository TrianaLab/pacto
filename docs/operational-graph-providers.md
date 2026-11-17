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
