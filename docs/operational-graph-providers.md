# Detected providers

This page describes the providers Pacto detected: the systems each
[source](operational-graph-reference.md#sources) read evidence from, what
Pacto uses each one for and how sure it is.

## The rows

`pacto fleet providers`, `GET /api/fleet/providers`, `pacto_fleet_status`
(`providers`) and the Detected providers block on Data sources show the same
rows. Each row carries `id`, `source`, `cluster`, `name`, `evidence`,
`usedFor`, `status` and `limitations`, plus `objects` and `objectsTotal` when
the evidence names objects. The evidence never names a namespace.

Each cluster reports at least two rows, from two sources: the Kubernetes
source (such as `prod`) and its workload source (`prod-workloads`).

- **Pacto CRD**: `served (pacto.trianalab.io/v1)` or `not installed`.
  Pacto uses it for declared targets.
- **Kubernetes workloads**: how many Deployments, StatefulSets, DaemonSets and
  CronJobs the [workload source](operational-graph-inferred.md) listed. A kind
  it could not list is `unreadable`, never 0, and one it listed in only some
  namespaces reads `at least 12 Deployments (some unreadable)`. Pacto uses it
  for inferred services.
- **Argo CD** and **Flux**: when the cluster serves their APIs, how many
  Applications (Argo CD) or Kustomizations, HelmReleases and GitRepositories
  (Flux) the workload source listed, such as `1 GitRepository, 1 HelmRelease`
  or `no Applications found`. A kind it could not list is `unreadable` and the
  row `partial`. Pacto uses them for service grouping and repository
  provenance; see [Groups and repositories](operational-graph-inferred.md#groups-and-repositories).
  A cluster that serves neither shows no row.
- **Helm**: how many Helm releases group at least one workload. Pacto reads no
  release Secret, so it uses them for service grouping only. A workload read
  cut short or an unreadable workload kind makes the row `partial`.
- **Promotion**: one row per promoter the installation names in its
  [fingerprints](#fingerprints) and the cluster shows, by the API group of a
  CRD it serves or a workload label or annotation, such as
  `promotion owned by Kargo (API group kargo.akuity.io served)`, which tells
  delivery another tool promotes. When
  discovery fails, a promoter named only by its CRD shows a `partial` row
  that says Pacto could not check. A restricted view lists only the workloads
  you may list.
- **Reporting adapters**: one row per adapter, signed or unsigned, that
  reports provisioning in the target-state file or signed envelopes. A
  producer the Evidence Server's trust config allows to report
  `ResourceProvisioned` or `ResourceUsage` shows in its
  [producer row](#producers) instead.
- **Istio**, **Linkerd** and **Cilium Hubble** (ids `istio`, `linkerd` and
  `hubble`): one row per mesh the cluster's API discovery shows, from its
  mesh source (`prod-mesh`), such as `serves networking.istio.io; calls read
  from Prometheus` or `serves cilium.io; no Hubble Relay found`. Pacto uses
  them for observed edges and external egress. Uncovered namespaces, an
  unreadable Prometheus or Relay and unattributed calls make the row `partial`;
  see [Calls a service mesh records](operational-graph-mesh.md).
- **Kernel egress** (`kernel-egress`): when the node agent runs, how many
  nodes that run target pods have one, such as `node agent on 3/4 nodes`.
  Pacto uses it for external egress. Its status is the worst egress coverage
  of the Pactos it read; it names those services, so a view hiding all of
  them shows no row. It is cluster-wide; see
  [Calls that leave the cluster](operational-graph-egress.md).

```bash
$ pacto fleet providers --k8s
PROVIDER              EVIDENCE                                             USED FOR
Pacto CRD             served (pacto.trianalab.io/v1)                       declared targets
Kubernetes workloads  412 Deployments, DaemonSets unreadable, 31 CronJobs  inferred services (partial: source prod-workloads could not list DaemonSets in any namespace: forbidden)
```

With two or more clusters the table gains a leading `CLUSTER` column. With no
row and no Kubernetes source it prints `No providers detected: this
view reads no Kubernetes cluster (add --k8s)`. When no source reported a
service or a target, completeness is `empty` and the CLI also prints
`warning: answer is empty` on stderr.

A row can carry `offers`: Pacto integrations that fit the detected provider,
each with an `id`, a one-sentence `text` and a setup guide in `doc`. The Argo CD row offers "Gate every Argo CD sync with one
repo-server plugin" ([setup](integrations/kubernetes/gitops.md#gate-every-argo-cd-sync)),
which the CLI prints as an `offer:` line under the row and the dashboard as a
**Set up** link.

## Fingerprints

`--fingerprints <file>` on `pacto fleet`, `pacto dashboard`, `pacto mcp` and
the other fleet commands, and the chart's `fingerprints` value, name the
workload managers and promoters an installation runs. Each entry names a
`product` and exactly one matcher: a `fieldManager`, `label` or `annotation`
for a manager, and a `crd` (`<plural>.<group>`), `label` or `annotation` for a
promoter. A file holds at most 64 entries. Pacto names no product in code:
the chart's three defaults are entries of an open list, and a private IDP is
one more. A promoter entry can also carry an `offer` (an `id`, a `text` that
starts with "Gate every " and an `https` `doc`), which its row lists under
`offers`; the Kargo entries offer
"Gate every Kargo promotion with one shared AnalysisTemplate"
([setup](integrations/kubernetes/gitops-kargo.md#kargo)).

```yaml
managers:
  - product: Internal Deployer
    fieldManager: acme-deployer
promoters:
  - product: Kargo
    crd: stages.kargo.akuity.io
    offer:
      id: kargo-analysis-template
      text: Gate every Kargo promotion with one shared AnalysisTemplate
      doc: https://pacto.run/latest/integrations/kubernetes/gitops-kargo/#kargo
  - product: Kargo
    crd: warehouses.kargo.akuity.io
    offer:
      id: kargo-analysis-template
      text: Gate every Kargo promotion with one shared AnalysisTemplate
      doc: https://pacto.run/latest/integrations/kubernetes/gitops-kargo/#kargo
  - product: Argo CD Image Updater
    annotation: argocd-image-updater.argoproj.io/image-list
  - product: Flux ImageUpdateAutomation
    crd: imageupdateautomations.image.toolkit.fluxcd.io
  - product: Acme Release Train
    annotation: release-train.acme.example/stage
```

A workload one of the managers applies reads `external: <product>` in the
[Stage applier verdict](integrations/kubernetes/delivery-reach.md#which-manager-applies-a-stage).

## Producers

An [Evidence Server source](evidence.md) adds one `producer/<id>` row per
producer that reported a target, may report provisioning or usage, or may
contribute records (see [Write a source](write-a-source.md)). Its `usedFor`
lists `reported-targets`, `reporting` and `contributed-records`, and its
`asOf` is the newest report or record. The evidence names the repository a
producer contributes from and counts no service, so the row reads the same in
a restricted view.

- **available**: everything it said was read.
- **stale**: its newest target is older than the freshness window (`last
  reported 26h ago`), or its newest record is (`recorded 40d ago`) or says no
  creation time.
- **partial**: its repository holds no record yet, or some records were
  refused, unsigned, tagged with another name or version, or over the bound
  of 200 services.
- **unavailable**: its repository could not be listed, so the evidence source
  reads `partial` with `CONTRIBUTION_UNREAD`.

A producer the server lists with nothing to show gets no row. One read takes
at most 32 contributing producers.

## Registry rows

An [OCI source](operational-graph-reference.md#sources) adds one
`Registry <host>` row per registry host it read a contract from. Pacto probes
each host once per process, with the first contract it resolved there, and
keeps the answer until the process restarts.

- **Evidence**: what the probe saw, such as `no Referrers, no _catalog` or
  `Referrers, _catalog`.
- **Used for**: `native-referrers` or `write-once-tags`, then
  `index-listed-by-catalog` or `index-listed-by-tag`. The CLI prints them with
  spaces.
- **Details**: `mode: referrers=<mode> listing=<mode>`, the `auth` scheme of
  the `/v2/` challenge, `artifactType: not checked (read-only probe)` and
  `cosign signatures: not checked`. `asOf` is when the probe ran.

```bash
$ pacto fleet providers --oci oci://ghcr.io/trianalab/pacto/postgresql:17.0.0
PROVIDER          EVIDENCE                   USED FOR
Registry ghcr.io  no Referrers, no _catalog  write once tags, index listed by tag
```

The `_catalog` answer depends on the probe's credentials; the output above is
anonymous. With a GHCR token, which Pacto finds through `gh auth`,
GHCR serves `_catalog` with every public repository on ghcr.io, and the row
reads `no Referrers, _catalog` and `write once tags, index listed by catalog`.

A probe that cannot tell (a network or server error, an answer that is not an
OCI index) reads `capabilities unknown`, is `partial` and carries
`REGISTRY_PROBE_FAILED`. A host whose contracts all
failed to resolve has no row. A registry row never changes the answer's
completeness. A restricted view drops a registry row when it hides every
service read from that host. The
[delivery records reference](integrations/kubernetes/delivery-records.md#write-once-tags)
explains what each mode means for records.

## Detected rows

With `--detected`, the rows the operator
[detected in a cluster](integrations/kubernetes/detection.md)
join from a source of kind `detection`: Registry, Cloud, Sign-in, Exposure,
Metrics, Nodes, the Applier, the [trace backends](#trace-backends), the egress readers and the managed-resource readers. Each carries five more fields:

| Field | Meaning |
|---|---|
| `provenance` | Always `inferred` |
| `asOf` | When the operator read it; older than 15 minutes is `stale` |
| `hint` | A command to review, set only when one candidate holds most namespaces, or the Applier proposal. MCP omits it |
| `details` | Other candidates or facts, at most 5 |
| `scope` | `cluster` for a row read cluster-wide |

Detected rows list after the rows a source reported and never clear a gate or
change the answer's completeness. An entry the operator could not read is `unavailable` with a
`DETECTION_<KIND>_UNKNOWN` limitation, and a partial one carries
`DETECTION_<KIND>_PARTIAL`. A row naming a cluster no Kubernetes source of
the snapshot reads carries `DETECTION_CLUSTER_MISMATCH`, and its source
reads `stale`. A restricted
view clears the evidence, hint and details of a row that names a namespace
you cannot read, or of a cluster-wide row when you cannot read the cluster
scope.

The **Applier** row (`applier`) appears only when the cluster serves none of
`kustomize.toolkit.fluxcd.io/v1`, `helm.toolkit.fluxcd.io/v2` and
`argoproj.io/v1alpha1`: it reads
`none found`, since nothing would apply what Pacto delivery promotes. A failed
discovery reads `unavailable`, never `none found`. Under profile idp without hardened, its hint
proposes one value of the Pacto release you run, at the chart version it
runs, and nothing else:

```text
Set idp.flux.install=true on this Pacto release, at the chart version it runs: helm upgrade <release> oci://ghcr.io/trianalab/pacto/charts/pacto-operator --version <version> -n <namespace> --reset-then-reuse-values --set idp.flux.install=true
```

Every reader shows this text from Pacto itself, whatever the detected row
holds. Pacto installs nothing: you set the value on your release
([IDP mode](integrations/kubernetes/delivery-setup.md#idp-mode)).

The three egress readers are cluster-wide and read calls leaving the cluster
([Calls that leave the cluster](operational-graph-egress.md)):

- **Istio egress** (`istio-egress`): `ServiceEntry CRD present` or
  `no ServiceEntry CRD`.
- **Cilium FQDN** (`cilium-fqdn`): `CiliumNetworkPolicy CRD present` or
  `no CiliumNetworkPolicy CRD`.
- **Egress gateway** (`egress-gateway`): only when Istio egress gateways run,
  such as `1 Istio egress gateway found; logs: auto`. With `auto` the row is
  `partial` and its hint is the command that sets
  `controller.egressGatewayLogs=on`.

- **Managed resources** (`managed-resources`): how many Crossplane, ACK,
  Config Connector and ASO objects of the catalog kinds the operator listed,
  such as `14 managed resources`, or `none found`. A kind the cluster began to
  serve after install is `unread <group>/<plural>` in the details, makes the
  row `partial` with a `helm upgrade` hint and raises `MANAGED_GROUP_UNREAD`.
  A namespace-scoped install reads `unknown`.
- **Terraform state** (`terraform`): only when `controller.terraformStates`
  lists a state, such as `2 listed states read`. A state it could not read
  makes the row `partial`, or `unknown` when it read none.
- **Cloud inventory** (`cloud-inventory`): the cloud
  `controller.cloudInventory` names, read with the operator's workload
  identity. With the value `"off"` and an identity bound, the row reads `off`
  and names the value to set; with a cloud and no identity, it names the
  missing identity. Beyond 500 resources per cluster the managed rows are
  `partial` with `MANAGED_RESOURCES_CAPPED`. A resource whose kind or type no
  catalog row names is skipped and counted in the note
  `MANAGED_RESOURCES_UNCLASSIFIED`. See
  [Managed resources](integrations/kubernetes/detection.md#managed-resources)
  for the grants and the identity.

## Trace backends

The operator detects four trace backends from the pods and Services it
already reads, with a row only for each one it found. A failed read is
one `Traces` row that is `unavailable`. It reads no pod in `kube-system`,
`kube-public`, `kube-node-lease` or the namespaces of the operator and its node
agent, so a backend running there is not detected.

- **OTel Collector** (`otel-collector`): a running
  `opentelemetry-collector` image.
- **Tempo** (`tempo`): a running `grafana/tempo` image.
- **Jaeger** (`jaeger`): a Service with port 16686 in a namespace that runs a
  `jaegertracing/` image. With several candidates the details name the others
  and the dashboard never reads them as complete coverage.
- **Datadog** (`datadog`): the Agent DaemonSet, its `DD_SITE` and the name of
  the Secret its `DD_API_KEY` comes from. Pacto reads no Secret and holds no
  Datadog client, so the row stays `partial`: Pacto reads Datadog calls
  through an OpenTelemetry Collector, or a script maps its service
  dependencies export to records; see [Write a source](write-a-source.md).

`pacto fleet` and `pacto mcp` show these as detected rows used for `observed
edges, read by the dashboard`. On `pacto dashboard` each backend the dashboard
reads becomes the row of its live source instead, dated by the last read and
saying how many edges it read:

- **Jaeger** (source `detected-jaeger`) is `available` once Jaeger returned a
  dependency. With none it is `partial`: Cassandra and Elasticsearch storage
  need the spark-dependencies job.
- **Service graph in Prometheus** (source `detected-service-graph`) reads the
  series Tempo's metrics-generator and the Collector's `servicegraph`
  connector write. With no series it is `partial` and names the processor or
  connector to turn on. The operator picks the Prometheus Service by its labels
  and only when it is the one Prometheus of its kind; with several the row is
  `partial`.
- **OTel Collector** (source `otlp-receiver`) is `available` once Collectors
  sent calls in the last 15 minutes, and counts the senders. Otherwise it is
  `partial`, such as `not exporting to pacto-dashboard.pacto-system:4318`, and
  its hint is one line of Collector configuration. With no Collector
  detected, an **OTLP receiver** row appears once something was sent.

A live row that is not available carries `EDGE_SOURCE_PARTIAL` with the
reason. When the service graph or Jaeger reported complete coverage
in the last minute, its row says `won: the OTLP receiver is off` and the
receiver's row says which source won. If the receiver saw calls the winner
misses, the receiver turns back on and both rows say how many.

When the Collector has a Prometheus, prefer the service graph: add the
connector, keep the other exporters and let Prometheus scrape the new one.

```yaml
connectors:
  servicegraph: {}
exporters:
  prometheus:
    endpoint: 0.0.0.0:8889
service:
  pipelines:
    traces:
      exporters: [servicegraph]  # add to the exporters already listed
    metrics/servicegraph:
      receivers: [servicegraph]
      exporters: [prometheus]
```

Otherwise export traces to the receiver as JSON:

```yaml
exporters:
  otlphttp/pacto:
    endpoint: http://pacto-dashboard.pacto-system:4318
    encoding: json
service:
  pipelines:
    traces:
      exporters: [otlphttp/pacto]  # add to the exporters already listed
```

## Not read and not installed

A cluster Pacto could not reach contributes no row; every surface names its
source `not read` with the error, such as `prod  not read (connection
refused)`. Only a cluster that answered without the Pacto CRD says `not
installed`.

A row is never healthier than its source, and one that is not available
always carries a limitation that says why. `PROVIDER_SOURCE_DEGRADED` says so when the
source itself is partial or stale, when the source marked the row partial
without a reason or when a restricted view hid every other reason.

## Restricted views

A restricted view keeps every row except a [registry row](#registry-rows) whose every service it hides. Objects in namespaces you cannot read and
cluster-scoped objects are hidden, as are limitations that name them, and so is
the name of a cluster where you can see no target. A count stays only where you
can read the namespace and list its kind there; a Helm release needs every
workload kind. The rest becomes one hidden
total, such as `12 Deployments (+30 hidden by your permissions)`, and never
makes the answer partial.
