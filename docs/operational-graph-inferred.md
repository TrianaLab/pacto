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
the `apps` group and on `cronjobs` in the `batch` group, plus the
[grouping](#what-pacto-reads-to-group) kinds.
Listing a workload also returns every inline `env[].value` of its pod
template, `kube-system` included. Pacto reads no environment variable, Secret,
ConfigMap, command, argument or header, and no annotation other than the
[grouping](#groups-and-repositories) ones, and drops those values before
anything is stored or served. It cuts a probe path at the first `?` or
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
| `SERVICE_GROUP_AMBIGUOUS` | Two groups of one rule claim the workload, so it stays ungrouped; see [Groups and repositories](#groups-and-repositories) |

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

## Groups and repositories

Every inferred service carries `workload.group`, the group the cluster already
puts its workload in. Pacto reads it from the workload's own labels,
annotations and field managers, and the first rule with a claim wins:

| Rule | Read from | Label |
|---|---|---|
| `argocd` | The `argocd.argoproj.io/tracking-id` annotation or the `argocd.argoproj.io/instance` label; with neither, `app.kubernetes.io/instance` written by an `argocd` field manager | `Argo CD Application argocd/cart` |
| `flux-kustomization` | The `kustomize.toolkit.fluxcd.io/name` and `/namespace` labels | `Flux Kustomization flux-system/apps` |
| `flux-helmrelease` | The `helm.toolkit.fluxcd.io/name` and `/namespace` labels | `Flux HelmRelease observability/log-shipper` |
| `helm` | The `meta.helm.sh/release-name` and `/release-namespace` annotations, unless a Flux HelmRelease claims the workload | `Helm release shop/exporter` |
| `part-of` | The `app.kubernetes.io/part-of` label, in the workload's namespace | `part-of checkout in shop` |
| `instance` | The `app.kubernetes.io/instance` label, in the workload's namespace | `instance cart in shop` |
| `workload` | None of the above | `This workload only` |

The group carries `rule`, `namespace`, `name`, `from` (what Pacto read),
`label` and `key`, `rule/namespace/name`, with the cluster first in a snapshot
of several clusters. `pacto fleet entities --group`, `group` on
`pacto_fleet_entities` and `GET /api/fleet/entities?group` take the key. The
dashboard shows the group and repository in the Workload section of the
inferred service page, and the Services list shows the group under the image.

### Ambiguous groups

When two claims of one rule name different owners, such as a tracking id and
an instance label naming two Applications, Pacto merges nothing. The workload
keeps its own `workload` group, reads `Ungrouped` and says in `ambiguous`
which claims disagree. A lower rule never decides instead. The service carries
`SERVICE_GROUP_AMBIGUOUS`, and one limitation per source counts those
workloads. An Argo CD instance with no namespace that matches several
Applications stays ungrouped the same way.

### Repositories

`workload.repositories` lists at most five source repositories, each with
`url`, `path`, `from`, `namespace` and `kind`. An Argo CD Application gives
`spec.source.repoURL` and each `spec.sources[].repoURL`, read as in
`Application argocd/cart spec.source.repoURL`. A Flux Kustomization or
HelmRelease gives the `spec.url` of the GitRepository it deploys from. Every
grouping label is metadata the workload's writer controls, so a repository
attaches only when the object's own status lists the workload: the
Application's `status.resources`, the Kustomization's inventory or a
HelmRelease that installs into the workload's namespace. Helm releases and
label groups name no repository. Pacto drops the credentials, query and
fragment of every URL and never writes to Git.

When a group names a repository Pacto cannot show, `repositoryUnknown` says
why, for example:

- `Application payments/payments-api does not list this workload`;
- `the source is Helm chart redis, not a Git repository`;
- `the source is OCIRepository manifests, not a GitRepository`;
- `no Application named cart was read`;
- `Applications outside the namespaces this source reads`;
- `Pacto could not discover the Argo CD and Flux APIs`.

A restricted view shows a repository only to a viewer whom Kubernetes lets
list its `kind`, such as `applications.argoproj.io` or
`gitrepositories.source.toolkit.fluxcd.io`, in its `namespace`. Anyone else
reads `hidden by your permissions`. The group of a workload you can see always
shows.

### What Pacto reads to group

To group, Pacto reads only the labels and annotations in the table and the
names of the workload's field managers. From Applications, Kustomizations,
HelmReleases and GitRepositories it decodes only names, source fields, Argo CD
`status.resources` and Flux inventory entries, never inline Helm values or
substitutions. The source needs `list` on those four kinds and reports Argo
CD, Flux and Helm as [detected providers](operational-graph-providers.md).

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
  `replacedFrom`. `pacto impact --event` and `pacto_impact` answer for the
  declared service and add `SUBJECT_REPLACED`. Other queries, the dashboard's
  impact event among them, answer not found for it.
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
