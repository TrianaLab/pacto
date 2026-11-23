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
| `INFERRED_REPLACEMENT_AMBIGUOUS` | More than one declared service targets the workload or names it in its declaration, a target of unknown cluster names it in a snapshot of several clusters, or a declaration names it with no cluster in a fleet of several clusters. The workload stays an inferred service; see [When a contract takes over](#when-a-contract-takes-over) |
| `INFERRED_LINK_IGNORED` | A declaration names the workload but hands nothing over: no accepted person signed it or the source checks no signatures, a Pacto target runs as the workload, the workload is in a namespace where the service has no target, its cluster contradicts the fleet's, or a newer revision ships an inferred record Pacto cannot read. The workload stays an inferred service; see [When a declaration replaces an inferred service](#when-a-declaration-replaces-an-inferred-service) |
| `LINK_VERSION_AMBIGUOUS` | Several release versions of a service record the images a linked workload runs, or its pods run the images of different versions. The newest is linked and `pacto impact` reads the workload as an unverified consumer. A `pacto` older than this release reads a target snapshot's linked workload as a consumer at the newest version; see [When a running image links a contract](#when-a-running-image-links-a-contract) |
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

## Contributed services

A tool that [writes a source](write-a-source.md) contributes inferred services
as signed records.

- A contributed service is `declaration: inferred` too, keyed under its
  producer's repository, such as `ghcr.io/acme/pacto-inferred/my-idp/checkout`.
  Its record carries `contribution`: the producer, the record's ref and key,
  when the producer made it (`asOf`), when Pacto read it and whether it is
  `stale`.

- `pacto fleet get` prints `Contributed by my-idp from <ref>, recorded 3h ago,
  read 2m ago; inferred, never clears a gate`. The dashboard opens it on the
  service page with one discreet mark beside its name and only the
  `open-graph` action.
- Its `dependencies[<name>]` fields resolve against the services declared in
  the producer's registry. They are inferred edges with `compatibility:
  unknown`: impact lists the service as an inferred consumer and the verdict
  never moves.
- It has no target and its status is `Unknown`, so it never reads Compliant.
- A record older than the freshness window, one that says no creation time or
  is dated in the future, and one read because a newer version was refused,
  reads stale.
- When a contract declares the same name in the producer's registry, the
  contract wins: the record is dropped and `CONTRIBUTION_SUPERSEDED` counts it.
  A record whose `pacto.yaml` declares more than its name and version, carries
  a `declaration` or sits in another producer's repository is refused with
  `CONTRIBUTION_REFUSED`.

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
`pacto_fleet_entities` and `GET /api/fleet/entities?group` take the key, which
`pacto fleet entity` prints on its `group key` line. The
dashboard shows the group and repository in the Workload section of the
inferred service page, and the Services list shows the group under the image.

### Ambiguous groups

When two claims of one rule name different owners, such as a tracking id and
an instance label naming two Applications, Pacto merges nothing. The workload
keeps its own `workload` group, reads `Ungrouped` and says in `ambiguous`
which claims disagree. A lower rule never decides instead. The service carries
`SERVICE_GROUP_AMBIGUOUS`, and one limitation per source counts those
workloads. An Argo CD instance with no namespace that matches several
Applications stays ungrouped the same way, and the reason says neither how
many match nor where.

### Repositories

`workload.repositories` lists at most five source repositories, each with
`url`, `path`, `from`, `namespace` and `kind`; `dropped` names each one past
five. An Argo CD Application gives
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
- `the cluster does not serve Applications`;
- `Pacto read only the first 10000 entries Kustomization flux-system/apps lists`;
- `Pacto could not discover the Argo CD and Flux APIs`.

A restricted view shows a repository only to a viewer whom Kubernetes lets
list its `kind`, such as `applications.argoproj.io` or
`gitrepositories.source.toolkit.fluxcd.io`, in its `namespace`, whether or not
they can read Pactos there. Anyone else
reads `hidden by your permissions`. A `repositoryUnknown` reason read from a
GitOps object follows the same rule; one that names no object, such as `the
cluster does not serve Applications`, shows to everyone. The group of a workload you can see always
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

Calls Istio, Linkerd or Cilium Hubble record join inferred services the same
way; see [Calls a service mesh records](operational-graph-mesh.md).

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

## When a declaration replaces an inferred service

When a person declares a service from its inferred twin with
[`pacto declare`](publishing.md#declare-a-running-service), the declared
bundle's `pacto.inferred.yaml` names that twin in `declaration.inferredFrom`.
The fleet then shows one service: the inferred service stops being listed, its
observed calls as caller and as callee resolve to the declared service, and
`replacements` records `link: inferred-from` with the `revision`, `by` (the
person who signed it) and `workload` (what the twin showed, when a source
reported it).

- **Signer rule.** A hand-over needs a signature the fleet verified whose
  subject and issuer are those of the declaration's asker signing as a person.
  The installation signer rule must also allow that person
  ([`delivery.identities.signers`](integrations/kubernetes/delivery-signed-approvals.md)).
  Only a registry read checks signatures, so a hand-over never happens in a
  `--local` fleet or from the Kubernetes source alone. Otherwise the twin stays
  inferred and `INFERRED_LINK_IGNORED` says why and names the fix.
- **A Pacto target wins.** When a Pacto target runs as the workload, or may run
  as it (two targets name it, or its cluster is unknown), the
  [Pacto target link](#when-a-contract-takes-over) decides and the declaration
  hands nothing over.
- **Ambiguity.** When the declarations of two services name one workload,
  nothing merges: the workload stays inferred with
  `INFERRED_REPLACEMENT_AMBIGUOUS`. In a fleet of several clusters, a claim with
  no cluster merges nothing either.
- **One claim per service.** Only the newest declared revision of a service
  counts. A newer declaration that names no workload retracts the claim; a CI
  publish without a declaration keeps it. A newer revision whose inferred
  record Pacto cannot read may have retracted it, so the claim hands nothing
  over. A service with Kubernetes targets
  claims a workload only in a namespace where one of its targets runs.
- **Attention.** Each hand-over raises an `INFERRED_REPLACED` item in the
  `knowledge` category that asks you to confirm the workload is the service.
  It carries no time and stays while the declaration holds.
- **Fields.** `pacto fleet get`, `pacto fleet explain`, their MCP tools, the
  TUI and the dashboard read `fields` for a service whose current revision
  carries a declaration or that replaced an inferred service through one: each
  field of the current revision as
  `declared`, `accepted` (with `by`, who signed the accepted declaration),
  `attested` or `inferred`, with its source and `asOf`. A proposed range stays
  `unknown` beside `proposed`. Inferred fields never clear a gate.
- **Restricted viewers.** A viewer sees the replacement only when they may see
  both the declared service and the workload. Otherwise `replacesHidden`
  counts it and no workload key, image, port, probe or cluster source reaches
  them.
- **Dashboard.** The service page keeps the **Replaces** line and adds one
  closed fold, **Where the values come from**; see
  [The service page](dashboard-service-page.md#owner-and-contract).

```text
$ pacto fleet get invoices
Service: invoices
Declared by ana@acme.com from inferred k8s:billing/Deployment/invoices (revision invoices@sha256:...)
Owner: billing
Fields:
  owner         billing    declared
  ledger range  ^2.3.0     accepted by ana@acme.com
  audit range   unknown    proposed ^1.4.0 (fleet, partial)
```

## When a running image links a contract

The operator [links a workload](integrations/kubernetes/linked-workloads.md)
no Pacto targets to the published contract it runs, by its image digest or its
`pacto.trianalab.io/contract` annotation. With `--detected`, the fleet reads
each such workload as a target of that contract's revision, with a `link`, and
hands its inferred twin over to the declared service.

- **Order.** A Pacto target that runs as the workload, or may run as it, wins
  and the linked target is dropped. A declaration that names the workload
  wins next. A link to another service then adds
  `INFERRED_REPLACEMENT_AMBIGUOUS`.
- **Replacement.** When the workload's `creationTimestamp` is more than 10
  minutes before the operator first linked it, `replacements` records
  `link: observed` with `signal` (`image-digest` or `annotation`), `value`,
  the `target`, the `revision` and `at`, when it was linked. An
  `INFERRED_REPLACED` item names it for 24 hours.
- **Restricted viewers.** A viewer who may not list the workload's namespace
  sees neither the linked target, its link nor its limitations.

```text
$ pacto fleet get payments --k8s --detected k8s:pacto-system/pacto-operator-detected
...
k8s:shop/Deployment/payments is linked to payments by its running image digest sha256:ab12cd34ef56… (observed, 3m ago)
```
