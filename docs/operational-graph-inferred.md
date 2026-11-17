# Inferred services

This page describes how the [operational graph](operational-graph.md) shows
the workloads a cluster runs before anyone writes a contract for them, what
Pacto reads from each one and what an inferred service never does. The other
sources are in the [operational graph reference](operational-graph-reference.md#sources).

## The workload source

With `--k8s`, and in the dashboard next to the operator, a second source reads
every Deployment, StatefulSet, DaemonSet and CronJob of the cluster, or of the
namespaces you name. Its id is the Kubernetes source id followed by
`-workloads`, `k8s-workloads` without one, and its kind is
`kubernetes-workloads`. Each workload no Pacto resource targets joins the graph
as an inferred service with its image, its
container ports and its readiness, liveness and startup probes, each with the
object path Pacto read it from.

The source needs `list` on `deployments`, `statefulsets` and `daemonsets` in
the `apps` group and on `cronjobs` in the `batch` group, and nothing else.
Listing a workload also returns every inline `env[].value` of its pod
template, `kube-system` included. Pacto reads no environment variable, Secret,
ConfigMap, command, argument, header or annotation, and drops those values
before anything is stored or served. It cuts a probe path at the first `?` or
`#`, because probes often carry tokens there, and drops an image or a probe
path that holds a control character or runs past 512 or 256 characters. The
workload lists each such field in `dropped`, and one `SOURCE_RECORD_INVALID`
limitation per source counts those workloads.

| Limitation | Meaning |
|---|---|
| `PACTO_CRD_NOT_INSTALLED` | The cluster serves no Pacto CRD. The Kubernetes source reads no target and is partial; the workloads still show as inferred services |
| `INFERRED_SERVICES_CAPPED` | A source reported more than 5000 workloads. The rest are left out and the source is partial |
| `DECLARATION_UNVERIFIED` | Pacto could not read the Pacto resources of the inferred service's namespace, so a contract may describe it. The service carries `declarationUnverified`, one limitation per source counts them and the Overview counts them as `inferredUnverified` |
| `INFERRED_REPLACEMENT_AMBIGUOUS` | More than one declared service targets the workload, or a target of unknown cluster names it in a snapshot of several clusters. The workload stays an inferred service; see [When a contract takes over](#when-a-contract-takes-over) |
| `SUBJECT_REPLACED` | `pacto impact --event` was asked about the key of an inferred service a declared service replaced, and answers for the declared service |
| `SERVICE_INFERRED` | The answer is about an inferred service. `pacto fleet graph` and `pacto impact --event` answer partial, since no contract can depend on it; `pacto fleet explain` lists it as a reason |

The check is conservative. A Kubernetes source that reads every namespace
verifies its workloads only when it is `available`, so one invalid Pacto
resource leaves every inferred service of that cluster unverified. A source
scoped to a namespace list verifies the namespaces it read.

## Declared and inferred services

Every service record carries `declaration`: `declared` when a contract revision
or a Pacto target describes it, `inferred` when Pacto only sees a
workload. An inferred service's name is the workload's
`k8s:<namespace>/<kind>/<name>`, such as `k8s:shop/Deployment/billing`; with
more than one cluster the cluster id comes first, as in
`k8s:<cluster>/shop/Deployment/billing`. Its domain and summary read unknown
and its status is empty.

- `--declaration inferred` on `pacto fleet entities` and `pacto fleet search`,
  and `declaration` on `pacto_fleet_entities` and `pacto_fleet_search`, keep
  only inferred services; `declared` leaves them out. With no filter the
  listing marks them. Entity and search rows carry `declaration` and inferred
  rows carry `image`.
- An inferred service never satisfies a dependency, never counts as a consumer
  in impact and never clears a gate or a promotion. A dependency named exactly
  like one stays unresolved.
- A workload source reports no consumer, so when it is unavailable or capped
  the snapshot reads partial but impact and promotion answers do not; its
  limitation still shows.
- An inferred caller of a declared service stays an uncontracted caller, so
  `BlockerMissingContract` still fires. The caller carries the inferred
  service's key in `entity` and its `UNCONTRACTED_CALLER` attention item in
  `caller`.
- `services` on the Overview and every tally count declared services only;
  `inferredServices` counts the rest.
- The detail lists the declared services the workload was seen calling.
  `callsObserved` is `false` when no source that reports calls covers its
  cluster, and an empty list then means unknown.
- A restricted view shows an inferred service only to a viewer whom Kubernetes
  lets list both Pactos and that workload kind in its namespace. Hidden ones
  are counted and never named. An uncontracted caller whose inferred service
  is hidden is left out too, and the declared service it calls counts it in
  `hiddenDependents`, even when you may list Pactos in the caller's namespace.

## Calls with no contract on one end

A runtime call between two workloads, from a source that reports calls, joins
the graph as an `inferred` edge when at least one of its ends is an inferred
service. The other end can be a declared service, another inferred service or
an external system. The edge carries the call count, the first and last time it
was seen and each source that saw it.

- Inferred edges live in the snapshot's `inferredRelationships`, apart from
  `relationships`. They never satisfy a dependency, never count as a consumer
  in impact and never clear a gate or a promotion.
- An inferred caller of a declared service is both an inferred edge and an
  uncontracted caller, so `BlockerMissingContract` still fires.
- An end Pacto cannot resolve still raises `OBSERVED_IDENTITY_UNRESOLVED`, and
  a call from a workload to itself is dropped. In a snapshot of several
  clusters, both `k8s:` ends of a call gain the cluster of the source that saw
  it.
- When impact reads trace edges, an inferred caller of the changed service is
  never a consumer: the answer names it in an `OBSERVED_IDENTITY_UNRESOLVED`
  limitation and stays partial.
- `pacto fleet graph` and `pacto_fleet_graph` list these edges with their
  sources, and the dashboard graph draws them in the inferred style. The
  neighborhood shows them in the default expected and observed views.
- A restricted view drops an edge whose inferred end it hides and counts that
  end once in `hiddenDependents` or `hiddenDependencies`, even when the same
  workload is also an uncontracted caller.

## When a contract takes over

When a Pacto resource's target runs as a workload Pacto showed as an inferred
service, that workload stops being one. Its observed calls resolve to the
declared service from then on, even when no source lists the workload. When the workload's `creationTimestamp` is
more than 10 minutes earlier than that of every Pacto of the service that
targets it, the cluster shows the workload ran with no contract first, and the
snapshot records a replacement in `replacements`: `from` the
inferred key, `to` the declared service, `link: pacto-target`, the `target` and
`at`, when the Pacto was created. A Pacto created before its workload, or
within 10 minutes after it as an install that applies the Pacto last does, or
a missing creation time, records nothing. Pacto keeps no snapshot history, so a
Pacto deleted and created again records a fresh replacement.

- The service answer and page list `replaces`, the newest first. For 24 hours after `at`, an
  `INFERRED_REPLACED` attention item in the `knowledge` category names the
  replacement, and the Overview knowledge tile counts it.
- `pacto fleet get`, `pacto fleet graph`, their MCP tools and the dashboard
  entity page answer an old inferred key with the declared service and
  `replacedFrom`. Other queries answer not found for it, and impact adds
  `SUBJECT_REPLACED`.
- When two declared services target the workload (counting a target of
  unknown cluster that names it), or a target's cluster is unknown in a
  snapshot of several clusters, nothing is merged: the workload
  stays inferred and the answer carries `INFERRED_REPLACEMENT_AMBIGUOUS`.
- A restricted view shows a replacement only when it shows both services.

```text
$ pacto fleet graph k8s:shop/Deployment/cart --k8s
k8s:shop/Deployment/cart was replaced by shop/cart 3m ago (linked by Pacto shop/cart target)
dependencies of shop/cart (1 node(s)):
    k8s:shop/StatefulSet/ledger (depth 1, inferred)
  cart -> k8s:shop/StatefulSet/ledger  inferred (observed by kernel, mesh)
```
