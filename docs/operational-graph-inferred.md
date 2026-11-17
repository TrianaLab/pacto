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
path that holds a control character or runs past 512 or 256 characters, with a
`SOURCE_RECORD_INVALID` limitation naming the field.

| Limitation | Meaning |
|---|---|
| `PACTO_CRD_NOT_INSTALLED` | The cluster serves no Pacto CRD. The Kubernetes source reads no target and is partial; the workloads still show as inferred services |
| `INFERRED_SERVICES_CAPPED` | A source reported more than 5000 workloads. The rest are left out and the source is partial |
| `DECLARATION_UNVERIFIED` | Pacto could not read the Pacto resources of the inferred service's namespace, so a contract may describe it. The Overview counts these as `inferredUnverified` |

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
