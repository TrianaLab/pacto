# @pacto/k8s-module

## 6.6.0

### Minor Changes

- e250ce8: An empty `pacto.yaml`, or one line of it, is now a valid contract
  (pactoVersion 2.5). An absent `pactoVersion` reads as the newest version the
  CLI supports, and `service`, `service.name` and `service.version` may be left
  out. Pacto infers the service name from `Chart.yaml`, a Kustomization or a
  single-service Compose file, the `org.opencontainers.image.title` label, the
  `app.kubernetes.io/name` label or the folder name, and `pacto validate`, the
  fleet and the dashboard say where each inferred value came from. An inferred
  name that another service also claims is dropped from the operational graph
  with a `SERVICE_NAME_COLLISION` limitation, and an inferred name never clears
  an impact gate (`SERVICE_NAME_INFERRED`).
  
  `pacto pack`, `pacto push` and `pacto publish` refuse a contract that does not
  declare `service.name` (`PUBLISH_NEEDS_SERVICE_NAME`) or `service.version`
  (`PUBLISH_NEEDS_SERVICE_VERSION`) and write `pactoVersion` into the bundled
  `pacto.yaml`. A bundle pulled from a registry, or an inline contract in a Pacto
  resource, that does not declare all three is rejected; the operator reports
  `ContractUndeclared`.
  
  A dependency may leave out `name`, which is derived from its `ref` and resolves
  only against a provider published at that repository, and `required`, which
  defaults to `true`. `compatibility: unknown` records a range nobody decided
  yet: it is never read as compatible and never clears a gate or a promotion.
  An interface can declare its `port`, and `breaks[]` announces a deliberate
  breaking change to the consumers it names. Validation, `pacto diff`, the fleet,
  the dashboard, MCP and the operator status (`status.interfaces[].port`) read
  the new fields.
- ca50460: Every workload a cluster runs shows up in the fleet as an inferred service,
  with its image, ports and probes; inferred services never satisfy a dependency
  or clear a gate. A new `kubernetes-workloads` source lists Deployments,
  StatefulSets, DaemonSets and CronJobs (`list` only) and reads no environment
  variable, Secret or ConfigMap. Service records carry `declaration`
  (`declared` or `inferred`); `pacto fleet entities` and `pacto fleet search`
  take `--declaration`, the fleet MCP tools take `declaration`, the Overview
  counts `inferredServices` apart and the Services list shows inferred services
  muted with a filter. An inferred caller of a declared service stays an
  uncontracted caller and links to its entity. The dashboard ClusterRole and the
  namespace watch Role gain `list` on the four workload kinds, and the operator
  ClusterRole gains `daemonsets`.
- bd0bdc4: Group inferred services the way the cluster already does: by Argo CD
  Application, Flux Kustomization or HelmRelease, Helm release or
  app.kubernetes.io labels, with the source repository URL and where Pacto read
  it. `pacto fleet entities --group`, `pacto_fleet_entities.group` and
  `GET /api/fleet/entities?group` filter by group; Data sources lists Argo CD,
  Flux and Helm. The dashboard role lists Argo CD Applications and Flux
  Kustomizations, HelmReleases and GitRepositories. Data sources also lists who
  promotes and who reports, from an optional `fingerprints` chart value and
  `--fingerprints` flag that name an installation's own workload managers and
  promoters; a workload one of those managers applies reads `external: <product>`
  in the Stage applier verdict, and the promoter writes no pointer for it.
- bd0bdc4: Probe each registry once and record the mode Pacto uses. Delivery records now
  work on registries without the OCI Referrers API, such as GHCR and CNCF
  distribution, as one write-once, content-addressed tag per record in the
  streams repository; every reader reads both forms. Stages gain
  `status.stream.registryMode` and the `RecordStoreReady` reason `WriteOnceTags`;
  the `ReferrersUnsupported` reason is removed, and a Stage that recorded
  `native` is never moved to write-once tags on its own. `pacto fleet providers`
  shows a `Registry <host>` row for every registry an OCI source reads.
- bd0bdc4: `pacto impact` and `pacto ci` find the consumers that run without cluster
  credentials. From the change's `oci://` ref they try
  `<host>/<first path segment>/pacto-targets`, then `<host>/pacto-targets`, read
  every `cluster-<uid>` snapshot of the first that exists and print a `Registry:`
  line and one `Targets:` line per snapshot, pinned by digest. `--target-snapshot`
  (repeatable) names a repository or pins cluster tags or digests, and `off`
  reads none; `--delivery-trust` or `$PACTO_DELIVERY_TRUST` names the trust
  config. A snapshot counts only when a key whose `streams` name its cluster's
  UID verified it and its `asOf` is less than 15 minutes old; otherwise its
  source reads `partial`, `stale` or `unavailable` with one of
  `TARGET_SNAPSHOT_UNVERIFIED`, `TARGET_SNAPSHOT_UNSCOPED`,
  `TARGET_SNAPSHOT_STALE`, `TARGET_SNAPSHOT_INVALID` or `TARGET_SNAPSHOT_BOUND`,
  and a `BREAKING` change with consumers never clears. No `pacto-targets`
  repository leaves today's answer and exit code. `--root` in the CI recipe is
  now optional, for consumers that run nowhere yet.
  
  A snapshot becomes up to three fleet sources per cluster: `target-snapshot`
  for its Pacto resources, `kubernetes-workloads` for its inferred services,
  which never count as consumers, and `evidence-http` for the targets its
  Evidence Server accepted. Source states gain `ref`, `signer`, `publisher` and
  `cluster`, and `impact` answers gain `registry` and `targetSnapshots`, also on
  the `pacto_impact` MCP tool over stdio. `pacto mcp` over HTTP refuses
  `--target-snapshot`. `pacto delivery targets publish` signs and pushes a
  snapshot document with a PEM key or a `pacto evidence keygen` seed.
  
  The operator publishes the cluster's signed snapshot to
  `<registry>/pacto-targets:cluster-<uid>` every 5 minutes when the chart's new
  top-level `registry` value is set: the cluster's Pactos, the workloads no
  Pacto targets (without env values or probes) and, with `evidence.enabled`, the
  Evidence Server's targets, each with its own `asOf` and completeness. Give
  `pacto-targets` its own read ACL: pull on it reads every namespace's Pacto
  status and workload shape.
  
  The Compose demo runs a `targets` service that publishes a signed snapshot of
  the demo cluster to `registry:5000/demo/pacto-targets` every 5 minutes.
- e250ce8: Probing and metrics observation take `auto`, `on` or `off` and default to
  `auto`, which turns each one on only in namespaces whose owner labels them
  `pacto.trianalab.io/observe` with `probing`, `metrics` or `probing.metrics`.
  A namespace owner opts in with `kubectl label ns payments
  pacto.trianalab.io/observe=probing`, with no chart change, and every other
  namespace stays passive. The chart values are `controller.probing` and
  `controller.metricsObservation`; `controller.enableProbing` and
  `controller.enableMetricsObservation` are deprecated and still honoured, so an
  upgrade that reuses the earlier release's values renders the same arguments. An
  explicit `auto` overrides a deprecated `true`. ServiceMonitor and PodMonitor
  read access still comes with `metricsObservation: on` only.
  
  `controller.interfaceNameMatchDiscovery` and `nodeAgent.image` are new, so
  every operator flag has a chart value or a documented reason, and a test keeps
  it that way. `controller.kernelEvidence.enabled` takes `auto`, `on` or `"off"`;
  `false` keeps following `nodeAgent.enabled`.
  
  The default install gains a `namespaces get` grant for the label read (in each
  watched namespace's Role under `controller.watchNamespaces`). The health probe
  no longer follows ExternalName or selector-less Services. The `RuntimeObserved`
  message names where each setting came from, and a transient namespace read
  failure marks it `False`.
- e250ce8: The operator reads no Secret across the cluster. Its ClusterRole drops
  `secrets`, and it reads pull secrets and the Secrets behind contract
  configurations only in the release namespace, in each watched namespace and
  where the new `controller.secretGrants` names them as `namespace/name` (one Role
  limited to each named Secret). A Secret-backed configuration it may not read
  reads `OBSERVATION_UNSUPPORTED`, and the finding names the grant to add; the
  collection does not fail. A pull secret outside every grant sets
  `ContractValid=Unknown` with reason `PullSecretNotGranted`. The operator no
  longer watches Secrets and reads a rotated pull secret within one check
  interval.
  
  `controller.watchNamespaces` takes a list of namespaces and the operator takes
  `--watch-namespaces`. With a list, the chart renders Roles and RoleBindings in
  each namespace and no ClusterRole, unless the node agent or the dashboard's
  delivery views need one. `controller.watchNamespace` and `--watch-namespace`
  still work as deprecated aliases that join the list, also in an upgrade that
  reuses the earlier release's values. Moving a cluster-wide install to a list leaves the
  `pacto-dashboard` ClusterRole and binding behind: the upgrade notes and a
  `StaleClusterRBAC` Warning event name the `kubectl delete` command. The chart
  refuses an `ed25519.secretName` recorder key without
  `allowOperatorReadableKey` whenever a grant reaches the delivery namespace.
  
  `--namespace` on `pacto fleet`, `pacto dashboard` and `pacto mcp` and
  `PACTO_DASHBOARD_NAMESPACE` accept a comma-separated list; one value keeps
  working. A source that reads a list is always `partial`, names its scope in a
  `K8S_NAMESPACE_SCOPED` limitation and in `namespaces` on its source state, and
  names a namespace it could not read in `K8S_NAMESPACE_UNREADABLE`. A restricted
  view never names a watched namespace the reader may not read. The dashboard
  Overview says which namespaces the fleet watches. `--review=local` (or
  `PACTO_DASHBOARD_REVIEW=local`) reviews people with LocalSubjectAccessReviews
  in the watched namespaces only; the managed dashboard of a namespace-list
  install without delivery uses it.
  
  Upgrade: list each pull secret outside the release namespace in
  `controller.secretGrants`, or add its namespace to `controller.watchNamespaces`,
  before upgrading.
- e250ce8: Delivery tells people from agents by how they sign in and lets whoever may
  update a Stage's workload decide its held changes, with no identity values.
  
  Upgrade effect first. An upgrade that takes the new chart's defaults turns
  owner-team narrowing and the ownerless protection off unless the install set
  `delivery.identities.teamGroupPrefix` explicitly: a values file through Argo
  CD, a Flux HelmRelease or `helm upgrade -f`, and the documented
  `helm upgrade --reset-then-reuse-values`. To keep the old behaviour, set
  `delivery.identities.teamGroupPrefix: ""` and
  `delivery.identities.requireOwnerTeam: true`. Plain `--reuse-values` keeps the
  old defaults (`""` and `true`) and so keeps narrowing. The promoter emits a
  `TeamNarrowingOff` Event on each Operation it holds while narrowing is off, so
  the effect shows on the first held change.
  
  - A ServiceAccount, any other `system:` user and an unauthenticated request
    are agents; every other signed-in identity is a person. `agentGroups` marks
    automation that signs in through OIDC. The chart no longer refuses delivery
    with `humanGroups` empty: `allowEmptyHumanGroups` is ignored (the schema
    still accepts it) and `humanGroups` is a deprecated override.
  - Approvers need `update` on the Stage's workload, asked as a
    SubjectAccessReview by the promoter (it gains `create subjectaccessreviews`)
    and as a SelfSubjectAccessReview by the dashboard and `pacto delivery
    approve`. The same rule decides a person's unfreeze and acknowledge. Refusals
    read `CannotUpdateWorkload` and `ApproverUnverified`; a failed review never
    allows. With delivery on, the chart aggregates delivery reads into the
    built-in `view`, `edit` and `admin` roles and creating Approvals and
    Operations into `edit` and `admin`, so people with `edit` or `admin` in a
    namespace may decide there without an `-approver` binding.
  - `teamGroupPrefix` defaults to `null`: set, even to `""`, it narrows who may
    answer to the owner teams. `requireOwnerTeam` defaults to `false`.
  - Operation and Approval records, `pacto delivery blame` and `history`, the
    dashboard and MCP delivery answers carry how each actor was classified
    (`classifiedBy`) and, on each approval, the access phrase, its source and
    when it was answered (`access`, `accessSource`, `accessAt`). Records stay
    `operation/v1`, but signed records are parsed strictly: upgrade a promoter
    that follows another cluster's records (`upstreamTrust`), and the CLI, no
    later than the recording cluster.
  - Go API: `delivery.ApprovalRefusal` takes a last `WorkloadAccess` argument,
    so no caller can skip the rule; `delivery.ApprovalRefusalAsking` asks it only
    for an approval every other check lets through. `delivery.ApprovalEligible`
    is removed: use `ApprovalRefusal`, whose empty reason means the approval
    counts.
- ca50460: The dashboard asks people to sign in by default, and the install notes print
  a short-lived sign-in link.
  
  Upgrade effect first. After any upgrade, one that reuses the earlier
  release's values included, the dashboard asks people to sign in.
  To keep serving without sign-in, set `dashboard.auth.tokenSignIn.enabled=false`
  and `dashboard.auth.allowAnonymous=true`; the install notes say so when the
  previous release served without sign-in.
  
  - The new chart value `dashboard.auth.tokenSignIn.enabled` defaults to `true`.
    `dashboard.auth.allowAnonymous` defaults to `false` and passes
    `--dashboard-allow-anonymous` only when set to `true`.
    `dashboard.auth.tokenLogin.enabled` is deprecated and ignored; the schema
    still accepts it.
  - The install notes print a port-forward and a sign-in link carrying a
    10-minute token for the chart's read-only `pacto-viewer` ServiceAccount,
    minted with `--audience pacto-dashboard` and passed after `#`, so the
    browser sends it to no server in the URL and it reaches no access log or
    proxy. An expired link says "link expired, run the command again".
  - A ServiceAccount token signs in only when a TokenReview proves it was minted
    for the `pacto-dashboard` audience, and its session reads the fleet only:
    the Delivery tab is hidden and delivery reads answer `SignInLinkReadsFleet`.
    A ServiceAccount token without the audience is refused with
    `TokenAudienceRequired`, and a sign-in link with any other token with
    `SignInLinkNeedsDashboardToken`.
  - A person's own token (an OIDC ID token, an EKS or static token) signs in as
    them, and their token session may now approve held changes under the
    approver rule. ServiceAccount tokens never approve. `TokenCannotDecide` is
    gone.
  - The dashboard's ServiceAccount gains `create` on `tokenreviews` through the
    ClusterRole `<fullname>-<namespace>-dashboard-tokenreview`, also with
    `controller.watchNamespaces`. `pacto-viewer` gets `get`, `list` and `watch`
    on Pactos and PactoRevisions through the ClusterRole `<fullname>-viewer`, or a
    Role in each watched namespace.
- 73ce984: One chart value, `registry`, turns on the registry-backed features. The operator publishes the cluster's signed target snapshot with its installation key, advertises the registry, snapshot, index and inferred refs, and pushes only under them. Delivery stays opt-in. Pacto reaches GAR, GCR, ECR and ACR with cloud workload identity, and uses a pull Secret only once `registryPullSecret` names it.
- e250ce8: Pacto detects the manager that owns each Stage's workloads and never promotes
  through another one. Stages can apply through a Flux HelmRelease.
  
  Upgrade effect first. `delivery.argocd.enabled` and `delivery.flux.enabled`
  default to `auto`. An upgrade that takes the new chart's defaults on a cluster
  that serves Argo CD Applications grants the promoter `patch` on Applications, limited by
  admission to `spec.source.targetRevision` of labelled Applications; set
  `delivery.argocd.enabled=false` to keep it off. Flux access renders as before,
  plus HelmRelease read, `--flux-helm` and the `applier-helmrelease` admission
  policy when the cluster serves Flux HelmReleases; the agent, approver and
  recorder roles gain HelmRelease `get` there too, which includes inline Helm
  values.
  
  - The promoter reads, and never writes, the labels, annotations, field
    managers and owner references of each workload a Stage's Pactos report, and
    records the manager in the Stage's new `status.applier` (observed, with
    evidence, `asOf` and `completeness`) and the `Applier` print column. A
    manager that contradicts `spec.applyRef` reads `Ready` `False`
    `ApplierConflict`; an unsupported or ambiguous manager, a workload without
    its applier's marks or an unreadable one reads `ApplierUnknown`, with the
    reason a Kustomization could not be read. So does a Pacto the operator has
    not reconciled, or one that evaluated another workload than the Stage runs.
    Detection reads a Kustomization outside the Stage namespace only when it
    applies from the Stage's OCIRepository, and gives one fixed answer for any
    other the workload's labels name. `status.applier.staleAt` is twice the
    promoter's resync after `asOf`, so a longer `--resync` never reads stale.
  - The new `pacto.applier` promote gate fails on a conflict and is Unknown when
    Pacto cannot tell; both refuse the promote, and nobody can accept them.
  - `spec.applyRef.kind` accepts `HelmRelease`. The promoter writes the chart
    digest to the OCIRepository the HelmRelease's `spec.chartRef` names, in the
    Stage's namespace, and counts a move applied once the HelmRelease is Ready at
    that digest. `spec.chart` reads `ApplierNotChartRef`.
  - `pacto delivery status`, `pacto delivery plan`, MCP `pacto_delivery_status`
    and the dashboard Delivery views print one applier line per service, with the
    reason when the Stage does not adopt it. `pacto delivery plan` and the
    candidate list read versions from an Argo CD Application's or a
    HelmRelease's repository, as the promoter does.
  - An Operation the promoter cannot read the applier for says
    `reading the applier`, and a deadline says `the applier did not confirm`,
    instead of naming an OCIRepository or Flux.
  - An `app.kubernetes.io/instance` label of `<namespace>_<app>` names the
    Application in that namespace, as the tracking id does.
  - The delivery binary gains `--flux` (default `true`) and `--flux-helm`
    (default `false`). Go API: the new `pkg/workloadmanager` package (`Detect`,
    `Describe`), `delivery.AdoptApplier`, `delivery.ApplierStatus` and
    `delivery.GateApplier`.
- bd0bdc4: Keep approvals and delivery records in the registry on every registry. The
  recorder signs an `operation-intent/v1` record for every admitted Operation
  and an `approval/v1` record for every Approval made in the cluster, on behalf
  of the person admission verified. With `delivery.identities.signers` set,
  `pacto delivery approve` signs the decision keyless after a fresh login and
  pushes it to the Stage's approvals repository
  (`delivery.recorder.approvalsRepository`); it refuses while
  `SIGSTORE_ID_TOKEN` or a CI token is set. With the recorder on, an approval
  counts only once its record is written, and a person's signed approval only
  once the promoter verified it. The recorder anchors the approvals repository
  only once a signer is set, and a failure there never stops operation
  records. `pacto delivery record
  get/verify <ref>` and `approve --intent <ref>` read records with no
  kubeconfig, and the stdio MCP record tools take `ref`; MCP still has no
  approve tool. On registries without the OCI Referrers API, approval and
  intent records use write-once tags, and publisher signatures and SBOMs also
  get write-once `pacto-sig-<subject hex>-<12 hex>` tags that Pacto reads in
  place of the shared `sha256-<hex>` index. A recorded `native` registry mode
  can no longer narrow at admission.
  
  Upgrade order: operation records now name each approval's record, and earlier
  readers decode records strictly. Upgrade the CLIs and every promoter that
  follows these streams before the recorder.
- bd0bdc4: Accept a version only from a publisher Pacto knows. A first Stage with
  `spec.candidates.registry` watches the registry, verifies each new version's
  Sigstore signature and accepts it only when the signer is the CI the
  installation's registry names (`delivery.publishers.registry`, a rule derived
  for `ghcr.io` and `registry.gitlab.com`), an identity a person confirmed for
  that service or a rule of a signed delivery policy
  (`delivery.publishers.policy`). It needs no Flux, so an Argo CD-only cluster
  promotes its first Stage. A new CI signer raises one "Confirm identity" item
  in the delivery inbox; confirming it writes a signed `identity-pin/v1` record
  to the registry. An unsigned or unreadable version, or a deploy digest a
  person signed, never passes. Keyless signers are classified as `human`, `agent` or `ci`, and an
  approval signed by anything but a person is a proposal that never counts.
  `pacto delivery trust`, the `pacto_delivery_trust` MCP tool and
  `GET /api/delivery/trust` show which publishers each service accepts.
  
  A fleet signature `status` is now `verified` only when a publisher rule, a
  pin or a signed policy accepts the signer, and the new `source` field says
  which. A signer the contract's own `publish.sign` declares is no longer
  `verified`. `pacto publish` stamps `org.opencontainers.image.source` on the
  artifacts it builds in CI.
- 73ce984: Only a member of a service's current owner or a default approver may set,
  change or remove its service.owner through a declaration, on pacto declare,
  pacto_declare and (once it can declare) the dashboard; agents and CI signers
  never do, and on_behalf_of grants nothing. A refused owner change stops before
  anything is pushed with OWNER_SIGNER_NOT_PERSON, OWNER_SIGNER_NOT_ALLOWED,
  OWNER_POLICY_UNVERIFIED or OWNER_SIGNER_UNVERIFIED, and an agent's plan lists
  who may accept it in mayAccept. An owner read from CODEOWNERS or
  catalog-info.yaml is attested when a CI identity the publisher rule accepts
  signed the bundle at the commit it was read at: it routes reviews and never
  clears a gate. The fleet adds ownerSource on services, source on ownership details,
  signerRepository and signerCommit on bundle signatures and ownerRight on
  revision provenance, and ignores a refused or unchecked owner with the
  limitations OWNER_DECLARATION_REFUSED and OWNER_DECLARATION_UNVERIFIED.
  pacto fleet get and the TUI print where an attested owner was read. The
  operator chart gains the declare values, rendered into an immutable,
  content-named ConfigMap the operator mounts, passes to its dashboard and
  publishes in its signed target snapshot when registry is set; the operator
  sets the OwnerVerified condition on a running bundle that changes the owner
  and withholds that owner from status unless its signer is allowed.
- e250ce8: Every target, caller and record names the cluster it is in. A snapshot that
  reads a cluster now lists it in `clusters`, and each target carries `cluster`,
  `clusterName`, `clusterNameSource`, `clusterNameReportedBy` and
  `clusterRelation`. The live Kubernetes source's id is the cluster UID (its
  `kube-system` namespace UID) when it can get `namespaces/kube-system`, and the
  context name otherwise. The display name comes from a node label that names the
  cluster, then the context name, then the first 8 characters of the UID, and its
  source is always shown.
  
  Target keys and `k8s:` callers gain a leading cluster component only when the
  snapshot holds two or more clusters, so single-cluster keys, hrefs, callers and
  MCP answers keep their form. A 3-part key keeps resolving while one cluster
  matches it; when several do, the dashboard API answers `409 Conflict` with the
  candidate keys, the dashboard offers a chooser and the CLI and MCP print the
  candidates. An old dashboard link redirects to the canonical one. Two targets
  in two clusters never merge. A target with no cluster id in a multi-cluster
  snapshot raises `CLUSTER_UNKNOWN` and makes its source partial.
  
  A `--target-state` file takes an optional `cluster` block with `id` and `name`.
  `pacto evidence serve` takes `--cluster-id`, `--cluster-name` and
  `--cluster-name-source`; each accepted record carries the cluster that accepted
  it, and the targets endpoint answers `pacto.dev/evidence-source/v5` when it
  carries a cluster; older readers accept only v2 to v4. Two evidence sources that report one record with two
  clusters raise `CLUSTER_CONFLICT`.
  
  The operator reads its cluster identity at start and passes it to the Evidence
  Server it runs. The operator ClusterRole and the managed dashboard ClusterRole
  gain get on `namespaces` for `kube-system` only.
- 73ce984: - `pacto impact --running oci://<repo>@<digest>` never judges a revision against itself: a target already running the new digest is listed in `running.excluded`, so a gate that runs after a deploy, such as a Kargo verification, judges the new revision against the other revisions that run. A promoter fingerprint can carry an `offer` (`id`, `text`, an `https` `doc`), which its Detected providers row lists under `offers` on `pacto fleet providers`, `pacto_fleet_status` and the dashboard's Data sources. The demo shows the Kargo row with its offer.
  - The chart's default Kargo fingerprints offer "Gate every Kargo promotion with one shared AnalysisTemplate", and the GitOps docs publish that `ClusterAnalysisTemplate`: each Stage adds one `verification` stanza, exit 2 (unverified) fails unless `allow-unverified` is `"true"`, and the template reads the signed target snapshots by default and needs no cluster RBAC.
- e250ce8: Every vendor API, managed cloud service and legacy host that a contract names
  is one graph node of the new kind `external`, keyed by a canonical identity such
  as `stripe`, `aws/rds/123456789012/eu-west-1/orders-db` or
  `host/corp-legacy.example`. A ref-less `external: true` dependency joins it
  through its `identity`, else a catalog alias, else a hostname's registrable
  domain, else its normalized name, so consumers write nothing new. Its
  relationship carries `toExternal` with `resolved: false`; an external system
  never gets a range status and never enters impact, dependents or delivery.
  
  Each external record lists its sources with their own provenance and match, its
  consumers and the catalog entry and digest that classified it. A snapshot lists
  them in `externals` and the catalogs used in `externalCatalogs`, which every
  answer's meta repeats. One answer names at most 200 external systems and folds
  the rest into `other-egress`, partial with `EXTERNAL_CAP`. A restricted view
  counts hidden consumers and hidden external systems and never names them.
  
  `pacto fleet get`, `graph` and `explain` and the matching MCP tools take
  `external:<identity>`; `pacto fleet entities --kind external --class <class>`
  lists them and a text search adds matching `externals`. The TUI, the dashboard
  Overview, a new External systems list, an external entity page with its
  provenance and a distinct graph node show them. The contract graph labels a
  broken reference "Unresolved ref" and an external system "External system".
  
  Pacto embeds a default external catalog. `--external-catalog
  oci://REPOSITORY@sha256:DIGEST` on `pacto fleet`, `impact`, `mcp`, `stats`,
  `tui` and `dashboard` (or `PACTO_DASHBOARD_EXTERNAL_CATALOG`) merges an
  organization catalog pinned by digest; a tag is refused and a catalog that
  cannot be read makes the answer partial with `EXTERNAL_CATALOG_UNAVAILABLE`.
  The operator chart value `dashboard.externalCatalog` sets it on the managed
  dashboard through the new operator flag `--dashboard-external-catalog`.
  
  pactoVersion 2.5 adds the optional `dependencies[].identity`, validated with
  `INVALID_EXTERNAL_IDENTITY` and `IDENTITY_WITHOUT_EXTERNAL`.
  
  Each core release publishes the default catalog to
  `ghcr.io/trianalab/pacto/external-catalog:<core version>` and signs it with
  Cosign; its digest equals the embedded copy's.
- e250ce8: Pacto recognizes the vendors, managed services and legacy hosts that a
  service's configuration, pod spec and traces already name, with no contract
  edit. Every surface shows the host and port at most: a user name, password,
  path and query are stripped, a value under a sensitive key is never parsed and
  no Secret value is ever read.
  
  - `pacto init` and `pacto_create` with `pactify` list the externals that the
    environment, ConfigMaps, Secret and key names and every chart's
    `values.yaml` name, and write the likely ones as commented entries under
    `dependencies` for a person to uncomment. The parsed contract does not change.
  - The operator writes `status.externals`, `status.externalsCoverage` and
    `status.externalCatalog` at `PactoStatusFormat` 3. It reads the target
    workload's env names and values, Secret names, `secretKeyRef` key names and
    the ConfigMap keys the pod spec references, plus the `ExternalName` Services,
    selectorless Services and Istio `ServiceEntries` of its own namespace that
    the workload names. `status.externals` shows every reader of a Pacto the
    normalized hosts that the referenced ConfigMap keys hold.
  - The operator role gains `get`, `list` and `watch` on
    `networking.istio.io` `serviceentries`, rendered by the chart only when the
    cluster serves that API. Without it the coverage reads `partial`, reason
    `serviceentry-reader-off`.
  - The fleet joins trace callees that the external catalog recognizes to
    external systems as `observed` edges, and reads the operator's externals as
    `inferred` edges with `inferredSources`. Neither enters an index that impact
    or a gate reads. `NeighborhoodEdge.provenance` gains the value `inferred`
    and edges gain `inferred: true`. `rpc.system` is now the last OTel callee
    key.
  - New limitations: `EXTERNALS_UNREPORTED` (an older, partial or stale
    operator status), `EXTERNAL_CATALOG_MISMATCH` and
    `EXTERNAL_USE_UNATTRIBUTED`. `OBSERVED_IDENTITY_UNRESOLVED` stays for names
    that match more than one service or that are neither a service nor an
    external system.
- 73ce984: Pacto publishes an inferred contract for every external system your services call. Once `registry` is set the operator writes `<registry>/pacto-inferred/<identity>` every 5 minutes, signed with the installation key and annotated `run.pacto.contract.provenance: inferred`, and only when the hosts, ports or client majors it sees change; `pacto fleet publish-inferred` does the same where no operator runs. The 2.5 contract field `service.external` names the system and the `pacto.inferred.yaml` section `surface` records what Pacto saw with its sources, catalog digest and first and last seen; it never names a consumer. A range against an inferred contract reads `rangeStatus: unknown` with the new `rangeReason: INFERRED_PROVIDER` and gates as unverified. A revision only the operator signed reads `unverified` with the new signature source `operator` and an `INFERRED_PROVIDER` attention item suggests `pacto declare`. The Compose demo publishes `demo/pacto-inferred/stripe`.

### Patch Changes

- ca50460: Pacto keeps what it infers in pacto.inferred.yaml, a separate bundle file with
  each value's source, for pactoVersion 2.5 contracts. pacto init and
  pacto_create complete an existing pacto.yaml without changing it, pacto explain
  lists each inferred field and the YAML to declare it, a publish that changes
  only the inferred record is not republished, the diff classifies inferred-only
  changes as non-breaking and impact never clears a gate on an inferred
  consumer. The dashboard shows inferred values apart from declared ones.
- e250ce8: pacto init and pacto_create no longer copy environment values into the
  inferred configuration schema as defaults; sensitive keys are marked
  writeOnly, cluster drafts read sensitive literals from a Secret and every
  schema summary (dashboard, fleet, consume snippet, operator status) shows
  writeOnly keys as (sensitive). Schema diffs print (sensitive) in place of a
  sensitive key's schema values; values declared under configurations[].values
  or in an environment file are shown as written. Existing configuration/schema.json files are left as they are:
  remove any default that holds a secret and republish.
- bd0bdc4: `pacto ci --publish` publishes every changed service of a repository on the merged commit, providers first. An undeclared `service.version` is derived from the newest verified version in the registry and the change since it (`1.0.0` for a first publish), and a contract with no `publish` section gets one derived in memory: the image is built with the one tool the folder shows (Skaffold, ko, Jib, buildpacks or a Dockerfile), with no shell, and Pacto assembles the deploy artifact from the folder's manifests, Kustomization or Helm chart, with Secrets left out and the image pinned by digest. Signing is keyless by default, as the identity the GitHub Actions or GitLab `id_tokens` token names; a declared key or KMS key and `--sign-key` still sign with a key. Pull request pipelines and stale checkouts are refused, every tag is read back after it is written, a version lost to a concurrent run is planned again up to 3 times and a failure blocks only the services of the run that depend on it. Each published bundle carries a signed record of the image digests its deploy pins. In `pactoVersion: "2.5"`, `publish.deploy` names a manifests or chart artifact any applier deploys and `publish.artifacts[].build` is optional and deprecated. `pacto init` and MCP `pacto_create` no longer write the shell publish section or the `service.version` placeholder. `pacto publish` gains `--image`, `--registry`, `--sign-key` and `--no-transparency-log`, and MCP `pacto_publish` and `pacto_publish_plan` take them as `image`, `registry`, `sign_key` and `no_transparency_log`.
- 73ce984: A dependency on a service of your own may leave out ref in a 2.5 contract:
  Pacto resolves it by name in its domain. pacto ci proposes its ref from the
  organization index and, from the target snapshot, a ^MAJOR.MINOR.0 range for a
  dependency whose range is unknown. Both land in pacto.inferred.yaml and
  publish as compatibility: unknown. pacto accept --proposed (or --field for one
  field) signs, after the person's interactive Sigstore login, an acceptance
  record attached to the service's newest published version: a referrer of
  type application/vnd.pacto.acceptance.v1+json, or a write-once
  pacto-accept-<subject>-<manifest> tag on a registry without the Referrers API.
  Only a record signed by the service's declared owner counts; any other
  signer, and every agent through the new MCP tool pacto_accept, writes a
  pacto-accept-draft-<n> a person accepts with --draft. pacto ci --publish
  carries counted ranges into the next version's pacto.yaml and cites each
  record in inputs.acceptances. pacto accept --break <provider>@<version>
  accepts a break announced in breaks[], also before the provider publishes
  (--from); the gate clears with BREAKING_ACCEPTED once every consumer accepted,
  and an announcement without expires expires 30 days after the commit time.
  pacto ci, pacto impact, pacto_impact and Change analysis print a Ranges:
  coverage line, and --policy unknown-range=warn|fail and
  uncontracted=warn|fail:<window> hold a BREAKING change that no range covers.
  A changed spec file that is only inferred prints a possible break (inferred)
  advisory with the YAML that declares the interface: the row reads unverified
  (INFERRED_POSSIBLE_BREAK) and the classification and version never change.
  The dashboard service page shows the plan and the pacto accept command for
  Accept all proposed ranges; it never signs or writes.
- 73ce984: `pacto ci init` writes the CI caller that runs `pacto ci` into a repository's
  working tree: five lines in `.github/workflows/pacto.yml` that call the
  pacto-actions v2 reusable workflow pinned by commit, or one `include` entry
  in `.gitlab-ci.yml` for its GitLab component. It never commits, pushes or
  needs Git write access. The plan job runs `pacto ci` on pull requests,
  annotates each service that does not read as unchanged and writes the job summary; the publish job,
  the only one with `id-token: write`, runs `pacto ci --publish` on the default
  branch, one run per repository at a time. Pacto's own CI runs the caller over
  a demo monorepo with empty contracts.
  
  Keyless publishes through the shared reusable workflow keep verifying across
  pin bumps and stay trusted by the operator's derived publisher rule: Pacto
  matches the caller repository and branch that Fulcio certifies, never the
  reusable workflow's commit. A repository moving from its own keyless release
  workflow keeps its earlier versions verifying. The operator picks the rule up
  through the core module.
  
  The CI guide, a new guide "From an imperative pipeline to one step" and the
  GitHub Actions reference teach `pacto ci` in place of `pacto push`,
  `COSIGN_KEY` and hand-raised versions.
  
  Until the pacto-actions release that pins a Pacto with `pacto ci`, a caller
  written by `pacto ci init` adds `with: {pacto-version: <this release>}`.
- e250ce8: pacto validate and pacto_check report `CONTRACT_BEHIND`, an info finding that
  never fails a run, when a contract declares an older pactoVersion. Its message
  says whether raising the version keeps what the contract means, names the
  finding the raise would add or says Pacto cannot tell until the errors are
  fixed. pacto_check adds it as a suggestion.
  
  pacto_edit raises pactoVersion only when that keeps the contract's meaning and
  otherwise fails with the finding the raise would add. Its change line now
  names the old version: `set pactoVersion to "2.4" (was "2.3")`, comes first
  and replaces the `set pactoVersion "2.1" for the external dependency` line. An
  edit that needs a raise now fails when the contract already fails validation,
  does not parse or declares an unknown or 1.x pactoVersion, where it used to
  overwrite the version.
  
  The fleet revision detail gains `pactoVersionCurrent` and
  `pactoVersionUnknown`, and `pacto fleet entity revision`, the TUI and the
  dashboard revision page show a behind or unknown version.
  
  The operator reads its `ContractOverrides` rule for ref configurations from
  validation, with no behaviour change, and its finding reference lists
  `CONTRACT_BEHIND`.
- bd0bdc4: `pacto ci` publishes a contract that declares an older `pactoVersion` as the current version, raised in memory, when that keeps what the contract means: only the version token of the bundled `pacto.yaml` changes, the bundle records the raise as a `migrations` entry in `pacto.inferred.yaml` and the plan adds one line per migrated or kept contract without changing the exit code. A raise alone does not republish an unchanged service, and the file in the repository is never rewritten. The fleet revision detail reads `2.5 (migrated from 2.4, inferred)`, with the inferred `migratedFrom` JSON key read only from a verified record, in `pacto fleet`, the TUI, MCP `pacto_fleet_get` and the dashboard revision page. The `CONTRACT_BEHIND` message now says when `pacto ci` publishes the contract as the current version.
- e250ce8: Release CLI binaries, `checksums.txt` and the dashboard, operator and delivery
  images now carry signed SLSA build-provenance attestations, verifiable with
  `gh attestation verify`. The images no longer carry BuildKit's unsigned
  per-platform provenance manifests.
- e250ce8: pacto init and pacto_create propose dependency ranges and write
  `compatibility: unknown` instead of declaring a guessed range. The proposal
  goes in a trailing comment: `^MAJOR.MINOR.0` of the lowest version the
  reconciled Pactos describing the called Service report, or of the dependency's
  contract in `--fleet`, and none when nothing was read or those Pactos disagree
  on the contract. A draft that writes a dependency declares pactoVersion 2.5.
  pacto_create and pacto_edit write `compatibility: unknown` when an agent gives
  no range. The CLI, the TUI, `Used by` and the dashboard say "range not
  declared" for such a dependency.
  
  The delivery controller's gate reason says range not declared for a consumer
  with compatibility unknown.
- bd0bdc4: pacto init --cluster and pacto_create with cluster complete each draft with
  the engine pacto ci uses and write a pacto.inferred.yaml into the draft
  folder. Each value names its source (manifest, label, runtime or fleet), when
  it was read and, for runtime and fleet facts, whether every node was observed.
  The running image digest is recorded and never declared, and a proposed range
  stays a proposal. The cluster read lists each kind of object in pages up to
  --max-items (max_items), takes --call-window (call_window) and resumes a read
  the bound stopped with --continue (continue). A kind of object you may not
  list reads as failed and the run goes on. A literal secret in a container's
  command or args moves to the draft's Secret. A published bundle whose
  inferred record carries cluster facts fails validation.
- 73ce984: pacto declare publishes a contract for a workload the cluster already runs as
  a contract-only version: 0.1.0 for a first declaration, else the published
  baseline raised by the change, with the baseline's deploy artifact reused and
  nothing built. Only a person's interactive Sigstore login (a browser, or a
  device code with --device) publishes a declared version; a key, an ambient
  OIDC token or a CI identity writes a write-once draft-<n> tag that a person
  accepts with --from-draft. The bundle's pacto.inferred.yaml gains a
  declaration block naming the signer, the inferred workload, the baseline and
  each proposed range accepted with --accept. pacto pull gains --verify-identity,
  --verify-issuer and --verify-key. The MCP tool pacto_declare is the first tool
  of the agent write contract (the existing pacto_publish_plan and pacto_publish
  stay outside it): it plans by default with an intent digest, always writes a
  draft carrying on_behalf_of and refuses with a stable code and the command a
  person runs. The fleet, explain, diff and the dashboard show a declaration's
  cluster facts, coverage and signer only when the bundle carries a verified
  signature by that person and the installation signer policy allows them.
- 89eedf2: The operator no longer writes a Pacto's status on every reconcile that lands in a
  new second. Each finding's evidence times (`observedAt`, `detectedAt`) are stamped
  when the evidence is read, so comparing them made almost every reconcile look like a
  change. They now refresh with the `lastReconciledAt` heartbeat, at least once every
  half check interval, and a status that is otherwise unchanged is not rewritten.

## 6.5.0

### Minor Changes

- da4494d: Add `pactoVersion: "2.4"` with `service.domain`, `service.offering`,
  `service.summary`, `service.lifecycle`, and `values` and a `configuration`
  selector beside a configuration `ref`. Every 2.3 contract is schema-valid under
  2.4, but 2.4 checks configuration refs strictly: a ref that cannot be resolved
  or that matches more than one provider configuration is an error, and a
  required ref configuration needs values in every environment. Raising a
  contract to 2.4 can therefore turn those warnings into errors. A `"2.0"` to
  `"2.3"` contract that sets any of the new fields reports `NEEDS_PACTO_2_4`.
  
  `service.domain` is the business domain a service belongs to, and `pacto diff`
  reports an added, changed or removed domain as `NON_BREAKING`. The operator reports the
  domain in `status.contract.domain` and writes `status.statusFormat` 2, so a
  service seen only through Kubernetes is still grouped by it. A status from an
  older operator leaves that service's domain unknown.
  
  The dashboard groups services by domain: domains have a list and a page with
  their services, owners, operational targets and attention, and the overview
  breaks services down by domain, with services that declare none under No domain
  and services whose domain Pacto cannot tell under Domain unknown. Sources that
  disagree on a domain raise `DOMAIN_CONFLICT`.
  The registry path that used to read "domain" now reads "Registry". Services with
  no owner from any source appear in attention as `unowned`. An operational target shows
  the environment it runs in when its Pacto carries the
  `delivery.pacto.trianalab.io/environment` label, and whether the revision it runs
  declares that environment; without the label it reads Unknown. The services list
  filters by declared capability. `pacto fleet search --business-domain`, the
  `pacto_fleet_search` MCP tool and the TUI accept a domain, `(none)` or
  `(unknown)`, and `pacto_edit`
  sets one.
- a6c189d: A delivery plan or proposal can carry a correlation id, such as a ticket or
  incident id: `pacto delivery plan|propose --correlation-id`, `correlation_id`
  on `pacto_delivery_operation_plan` and `pacto_delivery_operation_propose`, and
  `correlationID` in the dashboard's plan and propose bodies, and `pacto publish
  --propose --correlation-id`. The id is at most
  128 printable ASCII characters, checked by the client and by the operation
  admission policy. The automatic rollback, freeze and step-down the promoter
  raises after a change inherit its id, so `pacto delivery history
  --correlation-id` lists the changes of one incident.
  
  The intent digest now binds the cause a proposal cites (`--cause-verdict`,
  `cause_verdict`), never the correlation id, and `PlanStale` names a changed
  cause. Proposals that cite a cause need the CLI, the dashboard and the
  operator at the same release, in both directions: across releases the client
  and the promoter compute different digests, and the promoter finishes the
  Operation `Rejected` with `PlanStale`. Proposals without a cause are
  unaffected.
  
  An agent must name the person it acts for (`onBehalfOf`, `--on-behalf-of`,
  `on_behalf_of`), as the Kubernetes username the cluster authenticates them as,
  when it proposes a change, never itself, a service account or
  the promoter. The client, the promoter and the operation admission policy
  refuse an agent's proposal without one, with the reasons `NoPrincipal`,
  `SelfPrincipal` and `NotAPerson`; planning needs none, and changes the
  promoter raises itself need none. Existing agent automations that propose
  without naming a person must add it. The named person may not approve the
  change, and an approval of an agent's change that names nobody is refused.
  The name is asserted, never verified: it records who an agent says it acts
  for, and it does not stop a person who controls an agent's credentials from
  naming someone else and then approving the change.
  
  The dashboard's propose panel proposes the plan it shows when a reason or an
  on-behalf-of person is typed, with no false "Something changed" warning. Its delivery
  errors carry `differs`, the field that made a plan stale. The propose panel
  asks an agent who it acts for and takes a ticket or incident id.
  
  Fleet attention items whose fix is a contract edit carry `nextAction`
  (`{"verb": "edit-contract", "tool": "pacto_edit"}`), inferred from the item's
  code beside `nextStep`; it grants nothing, and unknown, stale, unresolved or
  non-compliant items carry none. `pacto_edit` returns `impact` (who the edit
  affects, from the session's fleet snapshot), `chain` (publish and promote,
  each with its tool and CLI command) and `executor` (who acts on the published
  revision), also on a dry run; `pacto_create` returns `chain`. The dashboard's
  target, revision and Change analysis pages offer **Plan…** for the operations
  the caller may take there, and a `correlationID` in the page URL fills the
  propose panel's ticket field.
- a6c189d: The dashboard's Metrics page, under Change and delivery, draws the run records
  in a stats repository as daily or weekly trends: breaking changes caught,
  consumers protected, gates, delivery rates, lead time per environment and
  coverage of contracts and observations. Every number is labelled unsigned statistics
  and links to the records behind it. Start the dashboard with
  `pacto dashboard --stats-registry` (or `PACTO_STATS_REGISTRY`); without one the
  page explains how to turn recording on. A signed-in viewer sees only the
  services their Kubernetes RBAC allows: hidden services are never named or
  counted. `pacto fleet status --stats-registry` (or `--stats-file`) writes a
  coverage record of the snapshot it read. The operator chart value
  `dashboard.stats.registry` points the managed dashboard at a stats repository.
- 1e7624b: An environment in `environments[]` can now declare its `lifetime`
  (`temporary` or `persistent`), an `expires` time (RFC 3339, only with
  `temporary`) and an `owner` in the shape of `service.owner` (pactoVersion 2.4).
  A lifetime nobody declared reads "not declared", never persistent. Once a
  temporary environment's `expires` has passed, `pacto validate` and
  `pacto_check` warn `ENVIRONMENT_EXPIRED`; Pacto deletes nothing for it.
  `pacto diff` classifies every change to these fields, and to an environment's
  `file`, as non-breaking.
  
  `pacto init --delivery --env` and `pacto_create` with `delivery` also declare
  each environment in `pacto.yaml` with an empty values file.
  `pacto_edit add_environments` takes `lifetime`, `expires` and `owner`, and
  `pacto_edit remove_environments` retires an environment and deletes the values
  file no other environment names. `pacto explain --environments a,b` and
  `pacto_check environments` list the configuration values two or more
  environments disagree on.
  
  A service's environments are one list in `pacto fleet entity service`,
  `pacto_fleet_entity` and the dashboard's service page: each environment the
  current revision declares or a visible target reports, with its declaration,
  its owner (declared, or the service owner labelled inferred), the targets
  observed there, its provisioning tally, the differences between environments
  and how the declarations changed between versions. On a delivery host the
  service page also shows the Stages for each environment.
  `pacto fleet entities --consumes <offering> --phase <phase> --environment <env>`
  and the matching `pacto_fleet_entities` arguments list the consumers in a
  provisioning phase in one environment.
  
  The delivery promoter emits the Kubernetes Events `EnvironmentCreated` and
  `EnvironmentDeleted` when a Stage is first reconciled and when it is deleted,
  and the recorder posts the CDEvents `environment.created` and
  `environment.deleted` to its CDEvents sink. Each names one Stage.
- 9cdf40f: `service.template` (pactoVersion 2.4) marks a contract as a golden path and
  names the generate plugins it offers, and `service.origin` (pactoVersion 2.4)
  records the golden path a service was created from as declared provenance
  (`ref` and the path's content `digest`). A `"2.0"` to `"2.3"` contract that
  sets either field reports `NEEDS_PACTO_2_4`; a repeated plugin reports
  `DUPLICATE_TEMPLATE_PLUGIN` and an origin that is neither an `oci://` reference
  nor a directory reports `INVALID_ORIGIN_REF`. `pacto diff` classifies every
  change to either field as non-breaking.
  
  `pacto init <dir> --from <oci-ref|dir> --owner <team>` creates a service from a
  golden path: it resets the identity, applies `--values` and `--set`, records
  `service.origin`, validates the result with its references resolved and only
  adds files. A path read from a registry is pulled by digest and, when the
  trust config (`--trust` or `$PACTO_DELIVERY_TRUST`) names a `role: template`
  key, must be signed by one; otherwise the signature step reads Unknown.
  `--run-plugins` runs only the plugins of the path you name. `--delivery`, with
  `--env`, `--registry-secret` and `--insecure-registry`, plans the cluster pieces
  from the created contract when the path publishes manifests under
  `publish.deploy`. The step list says what was checked and what Pacto cannot
  tell yet. The MCP `pacto_create` tool gains `from`, `values`, `set`,
  `run_plugins` and `domain`, and takes `delivery` and its options with `from`;
  `from` serves stdio only. `pacto_edit` gains `template`.
  
  A blank `pacto init` now writes `--owner`, `--domain` and a publish section,
  and no placeholder readiness block. `pacto_create` without `pactify` refuses a
  folder that already holds `pacto.yaml` and keeps any interface or schema file
  already there.
  
  The operational graph treats a service whose newest readable revision declares
  `service.template` and that runs nowhere as a golden path. Its dependencies and
  references are requirements, never consumption: impact, dependents, offering
  consumers, attention and the Overview population counts leave it out, and the
  Overview counts it in `goldenPaths` and `invalidGoldenPaths`. A running
  service that declares `service.template` stays a service and raises
  `GOLDEN_PATH_DEPLOYED`. A declared origin links to the golden path revision
  whose content digest matches. Entity lists take `template` (`only` or
  `exclude`) and `createdFrom` (MCP `created_from`), search takes `template` and
  marks golden path hits, and a service created from a path carries a creation
  checklist derived from what the fleet observes. A restricted viewer sees a
  golden path with no Kubernetes target, with the services created from it that
  they may not read counted. The CLI, MCP tools, dashboard (a Golden paths tab)
  and TUI show all of it. Consume snippets carry structured `entries`, and a
  values skeleton leaves out and names in `missingValues` every required value
  no candidate satisfies.
  
  The operator chart's `dashboard.repositories` (operator flag
  `--dashboard-repositories`) lists `oci://` references the operator-managed
  dashboard reads beside the contracts it discovers, so golden paths, which are
  never deployed, show on its Golden paths tab.
- a6c189d: People and agents now see the same organization and meet the same policy on
  every surface.
  
  Changes that callers notice:
  
  - An MCP server over HTTP serves only `pacto_schema` of the contract tools.
    `pacto_create`, `pacto_edit` and `pacto_check` read and write the server's
    folders and follow refs with its credentials, so they run beside the agent
    with `pacto mcp` over stdio.
  - A remote `pacto mcp --fleet` with `--auth-issuer` needs token exchange
    (`--exchange-client-id`): it asks the API server who each caller is, the way
    remote delivery does, in place of reading the token's claims.
  - `pacto mcp -t http` on loopback without `--auth-issuer` prints a bearer at
    launch (`pmcp_...`), and every request must carry it.
  - `pacto dashboard` bound beyond loopback without sign-in refuses to start
    unless `--allow-anonymous` (`PACTO_DASHBOARD_ALLOW_ANONYMOUS=true`) allows
    it. The operator passes `--dashboard-allow-anonymous` from the new chart
    value `dashboard.auth.allowAnonymous`, `true` by default with a warning in
    the install notes, so existing installs keep starting.
  - A bundle operation whose name starts with `pacto_` is skipped, so a bundle
    never takes a Pacto tool's name.
  - `pacto_impact` over HTTP, and the impact view of a dashboard that is not on
    loopback, accept only refs of revisions in the caller's view and read each
    at its digest-pinned ref.
  
  Additions:
  
  - Every MCP tool carries annotations (`readOnlyHint`, `destructiveHint`,
    `idempotentHint`, `openWorldHint`); a generated tool takes them from its
    HTTP method. `--allow-writes` and the stdio-only rules still decide what
    runs.
  - A bundle, `--fleet` and `--delivery` combine in one `pacto mcp` process,
    whose instructions describe only the modes it serves. `pacto_capabilities`
    reports the modes, the contract tools, the caller and how Pacto knows them,
    whether writes are exposed and the transport.
  - Six delivery read tools answer what the dashboard's delivery views show,
    with the caller's identity: `pacto_delivery_caller`, `pacto_delivery_inbox`,
    `pacto_delivery_environments`, `pacto_delivery_autonomy`,
    `pacto_delivery_published` and `pacto_delivery_refusals`. The CLI gains
    `pacto delivery caller`, `inbox`, `environments`, `autonomy`, `published`
    and `refusals` over the same route code.
  - `pacto fleet entities` and `pacto_fleet_entities` list any entity kind and
    take every filter the dashboard's entity list takes; a filter of one kind,
    such as `--source-kind`, lists that kind. `pacto fleet attention` and
    `pacto_fleet_attention` take `kind`, `key`, `source`, `status` and
    `stale-only`, `pacto_fleet_neighborhood` takes `max_edges` and
    `pacto_fleet_search` takes `label`. `pacto mcp --publish` serves
    `pacto_capabilities` too.
  - `pacto_fleet_revision_document` reads one document of a revision's bundle,
    as the dashboard's revision page does.
  - With `--verbose`, every MCP tool call logs one `mcp tool call` line with the
    tool, the caller and how Pacto knows them, the outcome, the duration and a
    keyed digest of the arguments.
  - Generated tool calls send `User-Agent: pacto-mcp/<version>`.
  - [Fleet views](https://pacto.run/latest/fleet-tools/) maps each question to
    its command, MCP tool and dashboard route.
- af4f27d: `service.lifecycle` (pactoVersion 2.4) deprecates a whole service with a
  sunset and a successor, and a configuration `ref` takes `values` and a
  `configuration` selector beside it (pactoVersion 2.4). Consumers of a deprecated service are warned with
  `DEPENDENCY_SERVICE_DEPRECATED` or `DEPENDENCY_SERVICE_SUNSET`, and `pacto diff`
  classifies the schedule like an interface lifecycle.
  
  Configuration references are now resolved and their values validated against
  the referenced contract's selected configuration schema in a new
  `configuration-refs` validation stage, in the fleet, the delivery gate,
  `pacto_check`, `pacto_edit` and the operator. `pacto validate`, `pacto push` and
  `pacto publish` fail with `CONFIG_REF_UNRESOLVED` on an unreachable
  configuration ref whose values or selection depend on it, like a policy ref; a
  `"2.0"` to `"2.3"` ref with no values anywhere only warns. Environment values
  of `"2.0"` to `"2.3"` ref configurations are now checked, and only 2.4 reports
  missing required values for a ref configuration. A failed configuration-refs
  stage does not skip environments. Impact, graph traversal and dependents count
  resolved configuration-reference consumers, and impact checks their values
  against the new provider schema. Neighborhood edges gain the
  `configuration_reference` relation.
  
  Provider adapters report provisioning status as `ResourceProvisioned`
  evidence, written with `pacto evidence provisioned`. A trust entry needs
  `allowedObservationKinds` listing `ResourceProvisioned` to report it. The
  Evidence Server's targets endpoint answers `pacto.dev/evidence-source/v3` with
  a `provisioning` list only when it holds provisioning reports, and v2
  otherwise, and target-state files accept `pacto.dev/fleet-targets/v2` with
  unsigned provisioning reports. Offering uses carry a provisioning status per
  environment that reads Unknown without a current report. `pacto fleet
  versions`, `pacto_fleet_versions` and the dashboard list each provider's
  versions with their lifecycle and range status, `--consumes` and `consumes`
  list an offering's consumers, and `pacto_edit` gains
  `remove_configuration_refs`, `add_environments`, `set_environment_values` and
  `lifecycle`. A provider with no Kubernetes target that declares its offering
  is visible to every signed-in viewer, with its targets hidden and its hidden
  consumers counted. `OfferingProvider.revisions` and `activeRevisions` become
  `versions`. `pacto explain` lists each configuration with its
  `environmentValues`, the effective values per environment an adapter
  provisions from.
  
  The operator resolves configuration references with host-scoped credentials
  and validates their values, `spec.overrides` and bound ConfigMaps against the
  resolved schema. `status.metadata` renders non-string values as JSON.
  The chart's `evidence.trust.configKey` (operator flag
  `--evidence-trust-config-key`) names a structured trust config inside the
  trust Secret, so the operator's Evidence Server can grant an adapter key
  `ResourceProvisioned`.
- a6c189d: A failing health check is reported as `HEALTH_FAILING` (category
  `Inconclusive`, severity `unknown`), and compliance stays `Unknown`: a failing
  endpoint does not contradict the contract. The operator reports it when the
  declared health endpoint answers with a 5xx other than 501, or when the
  readiness probe on the declared health path keeps every pod out of Ready;
  `pacto run` and `pacto agent` report it on a 5xx other than 501. A 501 or a
  405 stays `EVIDENCE_INSUFFICIENT`, as do a workload scaled to zero and
  endpoints the operator cannot map to the health port. `COLLECTION_FAILED` now
  means only that a collector query errored, an EndpointSlice read included. The Evidence Server re-evaluates the same evidence
  and reaches the same finding. The `CapabilityObserved` evidence payload gains
  the optional `healthy` key, which only a present capability may carry. Strict
  decoders reject unknown keys, so upgrade the Evidence Server with or before the
  operator.
  
  `pacto fleet`, `pacto mcp`, `pacto tui` and `pacto impact` now mark target
  evidence older than 24 hours as stale by default, the window the dashboard
  already uses. `--freshness 0` still skips the check, and every answer then
  carries the `FRESHNESS_NOT_EVALUATED` limitation, so a stale count of zero is
  never read as "nothing is stale"; the CLI also prints it as a note on stderr.
  The limitation does not change completeness.
  
  `pacto fleet overview`, the MCP tool `pacto_fleet_overview` and the dashboard
  overview answer "how healthy is the organization and how much do we know" in
  one shape. The overview summary gains `operationalHealth` (targets that are
  healthy, failing their own health check, unknown or declare no health check),
  `knowledge` with its gap kinds (`noSummary`, `noDomain`, `noInterfaceDocs`,
  `noReadiness`, `notDeployed`, `unverifiedCapability`) and an `unknown` bucket,
  `knowledgeItems`, `coverage` (assertions evaluated and required, and the
  targets that report none) and `staleCompliantTargets`. Every compliance tally
  gains `staleCompliant`, the Compliant targets or services that rest on stale
  evidence: a subset, never subtracted from `compliant`, drawn hatched. The
  entity list aggregate gains `targetHealth` and `knowledge`. The dashboard draws
  an Operational health band and a Knowledge band, and a target that is unknown
  is never counted as healthy. There is still no composite score.
  
  Ownership splits a service whose contract Pacto cannot read from a service
  that declares no owner: `ownership.unknown` and the filter value
  `ownership=unknown` are new, labelled Owner unknown. Each business domain row
  gains `failingHealth`, `ownerUnknown`, `notDeployed` and `withGaps`; owner and
  domain pages gain operational health, knowledge, readiness, services that run
  nowhere, services whose placement is unknown and unresolved and undeclared
  dependencies; owner and domain list rows
  gain `unknown`, `stale`, `failingHealth` and `withGaps` counts, and
  `sort=health` ranks them by those counts. New filters and sorts: `gap`
  (`--gap`), `sort=gaps` and `sourceHealth=incomplete`, which the new
  Incomplete sources entry point opens. The attention categories `health` and
  `knowledge` are new, and the dashboard's Attention view lists every category
  with each item's reason.
  
  Changed meanings: `ownership.unowned` and a domain row's `unowned` no longer
  count services whose contract Pacto cannot read; the lead "Services needing
  attention" tile and `servicesNeedingAttention` count every service with at
  least one attention item, and the tile's `itemCount` carries the item total;
  an `unknown` attention item names its cause in `code` (for example
  `EVIDENCE_STALE` or `EVIDENCE_MISSING`) and keeps `UNKNOWN` only when no
  cause is known; the CLI and MCP mark targets stale after 24 hours by default;
  and the evidence payload gains `healthy`. The dashboard's sidebar badge counts
  only attention items above `info`.
- 78fd229: `service.offering` (pactoVersion 2.4) names the platform offering a service
  provides to other teams such as `postgresql`, and `service.summary` a one-line
  purpose; `pacto diff` reports an added, changed or removed offering or summary as `NON_BREAKING`.
  The operator reports both in `status.contract.offering` and
  `status.contract.summary`.
  
  The dashboard gains an Offerings page. Each offering lists its providers with
  their versions, owners, interfaces, configuration and policies, the services
  that consume it and a snippet to consume it from a contract. A service page
  lists the platform offerings the service uses and, marked inferred, the
  providers its unresolved dependencies may mean. The capability filter and chips
  now read "Declared capability" and count each extension by its ref.
  `pacto fleet search --offering`, the `offering` argument of
  `pacto_fleet_search` and the TUI Offerings tab find providers, and `pacto_edit`
  gains `offering`, `summary`, `add_configuration_refs` and `add_policy_refs`.
- a6c189d: The delivery promoter can publish an unsigned run record of every operation it
  concludes to a repository you name with `delivery.stats.repository` (Helm) or
  `--stats-repository`. Records hold the type, outcome, verification, gate
  results, how long each operation was held, the class of who asked, the Stage
  and the digests, never a name. Publishing is best effort: a full queue or a
  registry error is counted in `pacto_delivery_stats_dropped_total` and never
  changes an outcome. `pkg/runrecord` describes `kind: operation` records.

### Patch Changes

- a6c189d: Provisioning keys can be scoped to provider repositories with
  `allowedProviderRepos` in the structured trust config. The Evidence Server
  refuses a provisioning report whose declared configuration or dependency ref
  names a repository outside the scope with HTTP 403 and the code
  `provider_not_allowed`, and it logs a warning at startup for every provisioning
  key with no scope. Older binaries reject a trust config that uses the field.
  
  An offering answer names who reports on it on the CLI, TUI, MCP and dashboard:
  `adapters` lists each adapter slug, producer and signature with the
  environments and phases it covers, `conflicting` counts requests whose adapters
  disagree and `unattributed` counts requests no adapter reported on. A report
  with no slug reads "unnamed adapter". `unresolvedUses` counts the declared
  references that may name a provider of the offering but did not resolve, and
  above 0 the answer carries `OFFERING_USES_UNRESOLVED` and is `partial`.
  
  Phase tallies count an unsigned Ready apart in `readyUnsigned`, and the CLI
  prints "N ready (M unsigned)". The dashboard shows an unsigned Ready as
  "Ready (unsigned)" in the info tone, so only a signed Ready reads positive.
  
  Impact's `CONFIG_REF_CONSUMERS_UNRESOLVED` and
  `POLICY_REF_CONSUMERS_UNRESOLVED` now count hidden consumers and count each
  consumer once instead of once per revision, so their numbers can change.
  
  The demo adds a `security-scanning` offering, provided by `platform-security`,
  with a signed and an unsigned report from the `scanner` adapter.
- 1e7624b: Follow a service through delivery on every surface. `pacto delivery status`,
  the `pacto_delivery_status` MCP tool and the dashboard show each Stage of a
  service in promotion order with its upstream, the one state the board counts,
  the version, contract and source revision it runs, its newest re-check with
  `Watched` and what you may ask for (`offers`: promote, unfreeze, freeze and
  undo, each with why or why not). `--why` and `/api/delivery/progress` say why
  a candidate cannot move yet, with each reason's fix and a dry-run plan that
  proposes nothing. Offers go unavailable when RBAC denies or cannot answer, and
  when the delivery identity policy cannot be read, with a `--delivery-namespace`
  hint when it is not found. A Stage whose admission policies are missing (Ready
  False `Unguarded`) and a Stage that observes (including one that reports no
  level) offer no promote, and none appears in `actions`.
  
  `pacto delivery lifecycle`, `pacto_delivery_lifecycle` and
  `/api/delivery/lifecycle` follow one version through every Stage, and
  `pacto delivery record get`, `pacto_delivery_record_get` and
  `/api/delivery/record` read one signed record by its id. History takes
  `--cause` and `--correlation-id` (`cause` and `correlation_id` in MCP, which
  also accepts durations for `since` and `until`); both narrow the timeline too.
  The CLI history shows applied, cause and correlation columns and marks lines
  that are not signed. `/api/delivery/plan` and propose return the full plan
  answer (`next`, `precedent` and `order`). Published versions show the source
  revision `pacto publish` stamps, an annotation, not signed, and link their
  fleet revision; a service whose Stages name two domains is listed once per
  domain. Delivery service filters match the service name exactly. A Stage
  waiting on exactly one held change names it (`heldOperation`). A fleet service detail carries
  `placement`, where its targets report it runs. The CLI labels an evidence
  gate's Pass as Checked, `pacto.contract` verdicts cite each Pacto's target,
  status, evaluation time and findings, a held promote on a frozen Stage names
  the `StageFrozen` conflict and the `CandidateUnverified` fix points at
  `spec.verify`. The operator uses the shared Ready reasons and auto-promote
  rule, and a held shadow promote keeps the person's question.
- 8517f96: Bump Go dependencies (sigstore, sigstore-go, go-jose, oras-go, prometheus,
  pflag, ginkgo and k8s.io/apiextensions-apiserver), dashboard frontend
  dependencies (jsdom 31, vite, vitest, playwright, mermaid, marked, d3 and
  svelte-check) and the cosign-installer, docker/login-action and CodeQL
  GitHub Actions.
- a39e7dc: Build with Go 1.27.4 (was 1.27.3) everywhere it's pinned (both modules,
  `go.work`, both Dockerfiles) for GO-2026-6859, a `net/http` vulnerability fixed
  in Go 1.27.4 and reachable from Pacto's code. No source changes.
- a6c189d: The demos ship generated run records, labelled unsigned statistics: the browser
  demo opens Metrics with about 90 days of records that follow the payments story,
  the Compose demo seeds CI records into its own registry and the Kubernetes demo
  records its real publishes and promoter operations on top of a seeded history.
  The delivery tour visits Metrics before its recap. The new guide "Measure what
  Pacto does" explains what is recorded, where it goes, how to turn it on in CI
  and in delivery, who sees which records and how to prune them, and the run
  record reference says for every field whether it can hold personal data.
- a6c189d: A service's detail now carries its `changes`: every declared change between
  the revisions Pacto holds (owner, business domain, offering, dependency,
  lifecycle and environment) and each validation finding that appeared or
  resolved, newest first in version order, each with the revision that shows it,
  the one it was compared with, its `provenance` and its `source`. `changes`
  replaces the unreleased `environmentHistory`. `pacto fleet entity service
  <key> --reached`, `reached` on a stdio `pacto_fleet_entity` and the service
  page add when each change's revision reached a Stage, from signed delivery
  records read as the caller; a change with no record reads "time not recorded",
  and "time hidden" when the caller may not read delivery. The TUI and the
  dashboard's service page show the same changes.
  
  A sequential owner handover is now an owner change, never `OWNER_CONFLICT`.
  `OWNER_CONFLICT` remains for targets that run revisions declaring different
  owners, or for two revisions of one version that disagree, and says when the
  link is inferred. Rankings, owner filters and owner pages count the current
  owner, and a former owner's page still lists the revisions it declared.
  
  `pacto delivery history`, `pacto_delivery_history` and Delivery history name
  the fleet revision of every artifact and the `pacto.impact` verdict each
  record signed, apart from a re-analysis against today's graph; blame names the
  revision too. `pacto delivery history --at`, `at` on `pacto_delivery_history`
  and `?at=` on the dashboard answer which revision each Stage ran at a past
  time, from signed records, and read `unknown` where no record holds.
- a6c189d: A service's detail lists every resource it consumes. A use of a provider that
  declares no offering now shows once a provider reports on it, and the service's
  `hiddenProviders` says at least how many resources come from providers the
  viewer may not see. Each use carries `providerOwner`, `providerAvailability`,
  `requested` (the values the contract declares, on the configuration-reference
  row) and `consumption`, which reads `not-reported` until a provider reports
  usage or cost. A new `provisioning` attention category raises
  `PROVISIONING_FAILED` and `PROVISIONING_STALE`; a resource's phase never changes
  the service's compliance. `pacto fleet entity service` and the TUI's service
  page print a Resources block with provenance, and the dashboard's service page gains a Resources section
  that joins each use with its requested configuration.
  
  Provider adapters can report a resource's usage and cost with the new
  `ResourceUsage` observation kind, written by `pacto evidence usage`. Only a key
  that lists `ResourceUsage` in `allowedObservationKinds` may report it, scoped by
  `allowedProviderRepos` like `ResourceProvisioned`, and an EvidenceSet carries
  one provider-reported kind only. The Evidence Server's targets endpoint lists
  usage under schema `pacto.dev/evidence-source/v4`, written only when it holds
  usage. `consumption` reads `reported`, with the figures, only for a signed,
  fresh, uncontested report about the current revision; otherwise it reads
  `unknown` and keeps the raw figures apart under `reported`. The CLI, MCP and the
  dashboard's Resources section show both, and Pacto never sums or converts a cost.

## 6.4.1

### Patch Changes

- d44ced1: Dependency bumps from Dependabot in one go: `bubbletea` 2.1.0 → 2.1.1, `huma`
  2.39.1 → 2.40.0, `prometheus/common` 0.72.1 → 0.73.0 and `sigstore` 1.10.9 →
  1.11.1 in the engine, and `k8s.io/api`, `apimachinery` and `client-go` 0.37.1 →
  0.37.2 in both modules. No source changes.
  
  `sigstore` 1.11 needs Go 1.27, so Go goes 1.26.10 → 1.27.3 everywhere it's
  pinned (both modules, `go.work`, both Dockerfiles), and golangci-lint goes
  v2.13.0 → v2.14.0 because v2.13 can't load Go 1.27 code.
  
  Heads-up: Go 1.27's gzip writes different bytes, so `pacto push` now produces different OCI bundle digests for the same content than older binaries did. The published demo bundles get re-published once via `release/demo-bundle-migration.txt`.

## 6.4.0

### Minor Changes

- 0813b6e: Delivery matches contract environments to Stages. A promote runs a new
  `pacto.environment` check that compares the Stage's `spec.environment` with
  the environments the version's contract declares, using the same
  `environments` stage as `pacto validate` on the version's bundle.
  
  - A contract that declares no `environments` passes the check, which says it
    does not apply, so services that do not use environments are unaffected.
  - A contract that declares environments but none named like the Stage's
    `spec.environment` waits for the service's team to approve it, for example
    `version 1.1.0 declares no environment prod for Stage payments-prod (it
    declares dev, staging)`. So does one whose values for that environment fail
    with `ENVIRONMENT_VALUES_INVALID`, `ENVIRONMENT_VALUES_MISSING` or
    `ENVIRONMENT_UNKNOWN_CONFIGURATION`, each located in the values file. When
    the version also waits for a consumer's team, the service's team is asked
    too, and the question names every team.
  - Values Pacto cannot check (`ENVIRONMENT_VALUES_UNVERIFIED`) wait for a
    person to accept the unknown.
  
  The promoter also holds a promote that leaves a consumer with a required
  guarantee unmet, the consumers `pacto impact` lists for the change. Like a
  consumer whose range refuses the version, it holds wherever the consumer runs,
  including when Pacto cannot place it. The consumer's team is asked, and the
  question names consumer, provider and guarantee. A rollback to such a version
  waits the same way. An unmet guarantee of an optional dependency holds
  nothing.
  
  The dashboard labels the check "Environment values" and shows N/A when it
  does not apply. The Stage's `spec.environment` field documents the match.
- 0813b6e: Add `pactoVersion: "2.3"` and `environments`, so `pacto validate` checks the
  configuration values each environment uses against the schema of every
  configuration they name. Every 2.2 contract is valid under 2.3. A 2.3 contract
  needs Pacto core 4.4.0 or later and operator 6.4.0 or later. A `"2.0"`, `"2.1"`
  or `"2.2"` contract that declares `environments` reports `NEEDS_PACTO_2_3`.
  
  ```yaml
  pactoVersion: "2.3"
  configurations:
    - name: chart
      schema: chart/values.schema.json
      required: true
  environments:
    - name: staging
      file: environments/staging.yaml
    - name: production
      file: environments/production.yaml
  ```
  
  An environment's file maps each configuration name to that environment's
  values, for example `chart: {replicaCount: 5, ingress: {host: shop.example.com}}`.
  It ships in the bundle like any other referenced file. Values hold references
  to secrets, such as a Secret name; keep secret material out of the file.
  
  - `pacto validate` reports a duplicate environment name
    (`DUPLICATE_ENVIRONMENT_NAME`), and a file that is missing or whose path
    leaves the bundle (`FILE_NOT_FOUND`). A new `environments` stage, after
    `policy`, merges each environment's values over the configuration's default
    `values` and checks the result against its schema
    (`ENVIRONMENT_VALUES_INVALID`). A `null` in an environment's file removes
    that key from the merged values. The stage reports values for a
    configuration the contract does not declare
    (`ENVIRONMENT_UNKNOWN_CONFIGURATION`) and a required configuration whose
    defaults alone fail its schema when an environment omits it
    (`ENVIRONMENT_VALUES_MISSING`). Each error names the environment, the
    configuration and the JSON Pointer of the value, and carries the
    environment's file. A schema violation and an unknown configuration also
    carry the line and column of the value, and a file that does not parse
    carries the line the parser names. A configuration whose schema is behind a
    `ref`, or a run with no bundle files, reports `ENVIRONMENT_VALUES_UNVERIFIED`,
    a warning, and the stage is skipped with a reason naming it. Without
    `environments` the stage is skipped.
  - `pack`, `push`, `publish`, the operator, the dashboard and `pacto_check` run
    the environments stage too, so a version whose values break an environment
    is refused there as well.
  - `pacto diff` reports an environment added as non-breaking, one removed as
    potentially breaking and every changed value in its file as non-breaking, at
    `environments[<name>].<configuration>.<path>`.
  - `pacto explain` and the MCP contract summary list the environments.
  - A validation error carries `file` when it points into a file other than
    `pacto.yaml`, in `pacto validate` and in `pacto_check`. The text output of
    `pacto validate` widens its stage column by one character.
- 0813b6e: The Pacto status records the guarantees of the contract, so the fleet the
  promoter builds from Pacto status evaluates them as a fleet built from
  published bundles does.
  
  - `status.interfaces[]` adds `guarantees`, `lifecycle` and
    `compatibilityEpoch`, under the contract's own field names.
  - `status.dependencies[]` adds `requires`, one entry per interface of the
    provider with the guarantees the dependency relies on.
  - The promoter's fleet rebuilds each Pacto's interfaces from its status. A
    dependency whose provider runs in the cluster reports its guarantees as met
    or unmet, and as unknown when no Pacto of the provider runs there. The
    impact analysis lists a guarantee the candidate does not meet under each consumer
    whose status records the requirement.
  
  - `status.statusFormat` is `1` on every status this operator writes. It says
    the status records guarantees, so a missing one means none is offered.
  
  The fields are optional and the CRD version stays `v1alpha1`. Pactos last
  reconciled by an older operator carry none of them until the operator
  reconciles them again, which it does for every Pacto when it starts. In that
  window the promoter reads a status without `statusFormat` as guarantees
  unknown: a dependency on that provider reports its guarantees as unknown,
  never unmet, and a breaking or possibly breaking change to a service whose
  consumer has such a status waits for a person to accept the unknown.

## 6.3.0

### Minor Changes

- 039d160: Add `pactoVersion: "2.2"` and an interface lifecycle, so a provider can retire
  an interface on a schedule its consumers can see. Every 2.1 contract is valid
  under 2.2, which also carries an interface's `compatibilityEpoch` and
  `guarantees` and a dependency's `requires`. A 2.2 contract needs Pacto core
  4.3.0 or later and operator 6.3.0 or later. A `"2.0"` or `"2.1"` contract that
  uses a 2.2 field reports `NEEDS_PACTO_2_2` for each.
  
  ```yaml
  pactoVersion: "2.2"
  interfaces:
    - name: orders-v1
      type: openapi
      ref: interfaces/orders-v1.yaml
      lifecycle:
        deprecated: "2026-10-01"
        sunset: "2027-04-01"
        successor: orders-v2
  ```
  
  - `pacto validate` checks that both dates are `YYYY-MM-DD`, that the sunset is
    not before the deprecation (`INVALID_INTERFACE_LIFECYCLE`) and that the
    successor names another interface or is an absolute URL
    (`INTERFACE_SUCCESSOR_UNKNOWN`). A 2.0 or 2.1 contract with a lifecycle
    reports `NEEDS_PACTO_2_2`.
  - `pacto validate --closure` warns a consumer about each deprecated interface
    of a service it depends on directly, naming the interface, its sunset and its
    successor (`DEPENDENCY_INTERFACE_DEPRECATED`). From the sunset date the code
    is `DEPENDENCY_INTERFACE_SUNSET`.
  - `pacto diff` reports deprecating an interface as non-breaking and moving its
    sunset earlier as potentially breaking. Removing an interface before its
    sunset stays breaking, and the reason says so.
  - `pacto explain`, the MCP contract summary and the dashboard API show an
    interface's lifecycle.

## 6.2.0

### Minor Changes

- 67de328: Delivery always installs its admission policies, and a promoter started
  without `--admission-policies` admits no operation and counts no approval: its
  Operations and Stages show `Unguarded`.
  
  Breaking for delivery (alpha): the chart removes
  `delivery.admissionPolicies.enabled` and
  `delivery.admissionPolicies.allowUnguarded`, and fails to render while either
  key is set. Delete both from your values. To render offline for `helm lint`
  or a `helm template` without `--api-versions`, set
  `delivery.admissionPolicies.skipCapabilityCheck=true`, which skips only the
  Kubernetes 1.30 and ValidatingAdmissionPolicy API check. An upgrade with
  `--reuse-values` carries the old `enabled: true` over, so use
  `--reset-then-reuse-values`.

### Patch Changes

- 9d4ec34: Make Pacto easier for an agent to drive from the CLI help and the MCP tool
  text alone.
  
  **`pacto impact` has a new exit status.** It now exits `2` when a breaking
  change cannot be shown to be safe. A CI job that treated any non-zero exit as
  a failure fails on these runs too; one that gates on deployed impact alone can
  treat `2` as a pass.
  
  `pacto impact` and the fleet tools no longer give answers that read as safe
  when they are not.
  
  - `pacto impact` adds the new revision to the snapshot when no fleet source
    holds the changed service, so `--local ./fleet` with only the consumers
    finds them instead of answering "No affected consumers".
  - Every impact answer ends in a release gate, `gate` and `gateReason`, and the
    exit status follows it: `0` clear, `1` blocked (a breaking change reaches an
    incompatible consumer on a known target) and the new `2` unverified (a
    breaking change on a partial answer, or with consumers none of which is shown
    to be safe). A declared-only run of a breaking change with consumers now
    exits `2` instead of `0`. The `pacto_impact` MCP tool, the dashboard's Change analysis
    view and the terminal UI show the same gate and its reason.
  - On a breaking change, a consumer whose range accepts the new version carries
    a `note`: the range match does not show that it avoids what the change
    removes. An open range such as `>=1.0.0` is named as open.
  - A declared dependency carries `rangeStatus` (`satisfied`, `unsatisfied` or
    `unknown`) beside `resolved`. A provider in the fleet at a version outside the
    range is reported by `pacto fleet status`, `pacto_fleet_status`, `explain`
    and the dashboard's attention list as `DEPENDENCY_RANGE_UNSATISFIED`, in the
    new `out-of-range` category (`--unsatisfied-ranges`, `unsatisfied_ranges`).
  - `pacto_fleet_status` with no category selected, including empty input,
    reports every category, as `pacto fleet status` does. It returned
    `items: null` before.
  - `owner` has one shape in `search` and `get`, the contract's
    `{team, dri, contacts}`, and is left out when no contract declares one. A
    service read only from Kubernetes takes its owner from the operator's
    `status.contract.owner`, where it was empty before.
  
  Delivery plans are readable to an agent from the tools alone. The CLI and the
  MCP delivery tools now say the same thing. The operator's promoter shares this
  code, so its messages and the Rejected-plan behaviour change with it.
  
  - `pacto delivery candidates` and the `pacto_delivery_candidates` tool list the
    versions a Stage may take, with their digests: what its candidate source
    offers that `pacto publish` published. The `to` of plan and propose takes a
    version (`3.0.0`), a tag or a digest. A version or a tag must name exactly one
    candidate; Pacto refuses one that names none or several, or that it cannot
    match because a candidate cannot be read, and says why. A missing `to` says
    where to find a valid value.
  - A plan carries `next`: which teams must sign (and that their approval also
    meets a Stage's rule that a person approves every promote), that an agent
    cannot answer and the operation is then Held, and that an operation nobody
    decides before its approval deadline concludes Expired with nothing changed.
    `pacto delivery get`, `pacto_delivery_get` and `pacto delivery explain` say
    what happens at the deadline of a held operation.
  - A plan predicted Rejected asks nobody anything, and its failing verdict says
    what to do first, such as promoting the version on the upstream Stage.
  - The message for a consumer that must move first names the Stage to promote it
    on in plain words.
  - `PlanStale` names the reason or the on-behalf-of person when a propose adds
    one the plan did not have, and otherwise lists what the intent digest binds
    instead of saying the Stage moved.
  
  `pacto init` and `pacto_create` with `pactify` read more of a service into its
  contract. The workload's environment, including the ConfigMaps it reads,
  becomes a configuration schema with each value as its default. A URL in it
  that names another service in the cluster becomes a dependency: with that
  service's contract in `--fleet`, its bundle and a range from its version;
  without it, a placeholder range that the contract comment and Unknown name. A
  readiness, liveness or startup probe becomes a health check.
  
  `pacto init --describe-only` (`describe_only` in `pacto_create`) writes a
  contract that only describes the service: no publish section, no signing key
  and no `.pactoignore`. Without it, `init` still writes the publish section and
  now says how to leave it out.
  
  `pacto init`, `pacto_create` and `pacto_edit` flag an owner written as
  `team/billing`, and `pacto_check` suggests the `pacto_edit` call that writes
  `billing`. `pacto_check` also explains how to declare a service of
  your own whose contract is not published yet, so it shows as unresolved until it
  is. The contract schema and the `DEPENDENCY_REF_REQUIRED` message say the same.
  
  The MCP server instructions, `pacto_schema` and `pacto --help` now describe
  what a contract holds and say delivery is opt-in, and `pacto_schema` returns
  how to describe an AI agent as a service.

## 6.1.0

### Minor Changes

- 407c06e: The `pacto-operator` chart renders every CRD as a template from
  `files/crds/`, which `make sync-crds` fills from `config/crd/bases`. `helm
  upgrade` now updates the `Pacto`, `PactoRevision` and `NodeRuntimeObservation`
  CRDs and adds new ones, and `helm uninstall` keeps every CRD
  (`helm.sh/resource-policy: keep`). The delivery CRDs still follow
  `delivery.enabled`. Set `crds.install=false` to manage the CRDs yourself.
  
  **Upgrading:** CRDs an older chart installed from its `crds/` directory are not
  part of the release, so `helm upgrade` stops with `invalid ownership metadata`.
  Label and annotate them for your release once, as
  [Upgrade the operator](https://pacto.run/latest/integrations/kubernetes/upgrade/#hand-older-crds-to-helm)
  shows, then upgrade.
- 407c06e: The `pacto-operator` chart has a `global` block shared by every workload it
  runs (the controller, the delivery promoter and the recorder):
  `global.imageRegistry` (default `ghcr.io`), `global.imagePullSecrets`,
  `global.commonLabels`, `global.podSecurityContext` and
  `global.securityContext`. A component's own `podSecurityContext` or
  `securityContext` replaces the global one, and its `imagePullSecrets` follow
  the global ones. The delivery pods take `delivery.podAnnotations` and
  `delivery.podLabels`.
  
  **Changed values:** `image.repository` and `delivery.image.repository` no
  longer carry the registry (`trianalab/pacto/operator`,
  `trianalab/pacto/delivery`). Set `image.registry` or
  `delivery.image.registry` for one image, `global.imageRegistry` for all, or
  `global.imageRegistry=""` to give each repository as a whole reference. A
  repository that already starts with a registry host (`ghcr.io/...`,
  `localhost:5001/...`) stays the whole reference, so older values keep working. The
  top-level `podSecurityContext` and `securityContext`, and their `delivery.`
  counterparts, default to empty and fall back to the global ones.
- 407c06e: The `pacto-operator` chart can install [zot](https://zotregistry.dev) and
  [Dex](https://dexidp.io) from their official charts (`zot.enabled`,
  `dex.enabled`, both off by default). The chart ships the profile
  `values-quickstart.yaml`, which turns both on for an evaluation cluster: zot
  holds the delivery record streams, Dex signs people in to the dashboard and
  the chart generates the dashboard's client secret, the first sign-in password
  and the recorder's signing key (`delivery.recorder.signing.ed25519.generate`)
  at install. See
  [Quickstart with a bundled registry and sign-in](https://pacto.run/latest/integrations/kubernetes/quickstart/).
- 407c06e: A dashboard people sign in to now reads delivery Stages and Operations from one
  shared cache, which its ServiceAccount lists and watches, instead of listing
  them again with every person's token. Each person sees only what their own
  RBAC allows: the dashboard asks the API server who their token is and, with a
  SubjectAccessReview, whether they may make the same list or get, keeping each
  answer 10 seconds; a list across every namespace needs a cluster-wide allow.
  Every action (propose, approve, cancel, roll back, unfreeze) and every read it
  is built on still uses the person's own token, and the dashboard reads with
  that token until the cache has loaded and for a moment after the person
  changes something. With 2,000 Stages and 10,000 Operations, fifty people
  opening the inbox at once wait about a second instead of 12. The operator
  grants the dashboard's ServiceAccount get, list and watch on Stages and
  Operations, nothing more in the delivery group, and holds that right itself
  while the dashboard is enabled.
- 407c06e: A shared dashboard can sign people in, and then shows nothing until they do.
  `pacto dashboard` takes an OpenID Connect issuer (`--oidc-*`, the client secret
  in `PACTO_DASHBOARD_OIDC_CLIENT_SECRET`), a private issuer CA
  (`--oidc-ca-file`), an internal issuer address (`--oidc-internal-issuer-url`),
  the API server's claim mapping, session lifetimes (`--session-idle-timeout`,
  `--session-max-age`) and `--token-login`, which takes a pasted Kubernetes token
  for clusters that trust no identity provider. The API server names each
  signed-in person (SelfSubjectReview), and every view and action runs with
  their token and RBAC. `/api/session` says who is signed in and how; every
  other endpoint answers `401` with the code `LoginRequired` until they sign in.
  The header names who they are, and the sign-in button says Sign in with OIDC
  or names the provider (`--oidc-display-name`). A remote `pacto mcp` reads the
  same claim mapping flags and logs each caller as the mapping names them.
  
  The `pacto-operator` chart gains `dashboard.auth`: `oidc` (`issuerURL`,
  `internalIssuerURL`, `caBundle`, `clientID`, `clientSecretRef`, `redirectURL`,
  `extraScopes`, `displayName` and the claim mapping `usernameClaim`,
  `usernamePrefix`, `groupsClaim`, `groupsPrefix`), `session` (`idleTimeout`,
  `maxAge`) and `tokenLogin.enabled`. See
  [Dashboard authentication](https://pacto.run/latest/integrations/kubernetes/dashboard-authentication/).
- 407c06e: A dashboard people sign in to now shows each person only what Kubernetes RBAC
  lets them read. A namespace's services, revisions, targets, edges and evidence
  appear only if the person may list Pactos there, checked with a
  SubjectAccessReview for their username and groups, as the API server names
  them, and kept a minute. One filter covers every fleet view, counts, search, the graph, change
  analysis and a remote `pacto mcp --fleet` with `--auth-issuer`. The dashboard
  says when services are hidden without naming them, and a service page counts
  the hidden services on each side. A service known only from a registry, with no
  namespace, shows only to people who may list Pactos in every namespace. Local
  and sign-in-free dashboards are unchanged.
  The operator grants the dashboard's ServiceAccount create on
  `subjectaccessreviews` and holds that right itself while the dashboard is enabled.
- 407c06e: Add the Pacto delivery promoter and recorder, an opt-in component with its own
  image and API group, installed by the `pacto-operator` chart under
  `delivery.enabled` into its own namespace, and tie every Pacto check result to
  the version it evaluated. The operator holds no role in the delivery namespace,
  so it cannot run a pod as the promoter or the recorder or mint their tokens.
  
  **Pacto status.** `Pacto.status.evaluatedRevision` names the contract digest
  and the workload object and generation an evaluation read, and its
  `kernelCoverage` says how far the node agent's view could be trusted
  (`Complete`, `Partial`, `Failed` or `Stale`). A consumer acting on
  `contractStatus` must check `evaluatedRevision` first, as the promoter does.
  The Pacto status also accepts `mcp` interfaces, so a service that exposes tools reconciles.
  `status.observedCallers` lists the workloads the node agent saw calling the
  target's Service that no Pacto describes, and `status.observedCallersCoverage`
  says whether that list is complete, partial (with a reason such as
  `shared-netns` or `stale-node`) or unavailable; delivery holds a breaking
  change on either. Host-network pods in kube-system and the other cluster
  namespaces and in the node agent's own namespace do not make that coverage
  partial; `controller.kernelEvidence.callerIgnoreNamespaces` adds more.
  
  **Check results change for every operator user**, delivery on or off. A
  dependency that resolves to the same service in several namespaces now
  resolves to the one in the consumer's own namespace when there is one (a
  service in test calls the provider in test), where before the match was
  ambiguous (Insufficient) unless exactly one of them satisfied the declared
  range. The node agent's evidence leaves out pods being deleted, so a
  pod still shutting down no longer counts toward kernel coverage or findings,
  and a workload with no running pod has `Partial` kernel coverage.
  
  **The API.** `delivery.pacto.trianalab.io/v1alpha1` (Alpha, in the kubectl
  category `pacto-delivery`) adds `Stage` (one service
  in one environment, whose pointer is the artifact digest it runs), `Operation`
  (one decision, with an immutable spec) and `Approval` (a person's decision
  bound to an operation's uid and intent digest and to the approver's groups).
  
  **The promoter** (`ghcr.io/trianalab/pacto/delivery`, subcommand `promoter`)
  admits each operation and promotes only a version `pacto publish` published:
  the Stage's candidate `OCIRepository` must set `spec.verify` naming the
  publisher, and a downstream Stage takes only what its upstream Stage verified
  and offered (`status.published`, `status.offered`). Anything else is refused as
  `NotPublished`, and a Stage that cannot check its candidates says why in its
  `Ready` condition. It then compares the contracts, checks every consumer's
  declared range, holds for a person when a consumer in the Stage's environment
  would break or Pacto cannot see enough and writes to `status.plan.order` which
  consumer must move first, planned again while the change waits. It accepts an
  approval only from a person in a team the question names (chart
  `delivery.identities.teamGroupPrefix`); a question that names no team is answered by
  nobody unless `delivery.identities.requireOwnerTeam` is false, and with
  `delivery.identities.humanGroups` set only those groups count as people. The chart
  publishes these identities in the ConfigMap `pacto-delivery-identities`, which
  the promoter runs with and the CLI, the MCP server and the dashboard read, with
  the name and type of each gate provider (never its URL or signers) so a
  client's plan predicts an eval check's hold as the promoter does. It takes the Stage lock with one
  compare-and-swap, moves the pointer, waits for the apply and runs the verify
  checks the Stage lists in `spec.gates.verify.checks`: the Pacto contract check,
  providers from `delivery.gates.providers` (`mira`, `http` or `eval`, whose
  check names the contract suite it runs in `suite`) and the built-in
  `pacto.runtime`. Checks run again every `spec.gates.verify.recheck` after
  release. Unknown never passes and never rolls back. A confirmed failure is
  never promoted again (the Stage keeps its last 50) and gets one bound rollback;
  a second failure, a version a person chose, nothing safe to go back to, a
  refused automatic rollback, an outcome
  that cannot be established or an edit of the pointer the promoter did not make
  freezes the Stage. A Stage starts on `spec.startLevel` (`observe` by default;
  an admin creating a Stage may pick any level) and only an `autonomy` operation moves `status.level` after that;
  on `auto-nonbreaking` and `autonomous` the promoter promotes the newest
  eligible candidate itself. It records what an observe Stage would have done in
  `status.shadow` (the newest 20), proposes the next level in `status.proposal`
  after `spec.autonomy.proposeAfter` verified changes (10 by default) and, after
  an automatic rollback of a version it shipped on its own, lowers the Stage one
  level through an operation named `<failed operation>-stepdown`. `kubectl get
  stages` shows the level. `spec.auto` is gone. A breaking change no contract depends on also waits for a person,
  since a service without a contract may call it, unless the Stage sets
  `gates.impact.declaredConsumersOnly`.
  
  The promoter emits Events when it holds, concludes, rolls back or freezes and
  exports `pacto_delivery_operations_total`, `pacto_delivery_verdicts_total`,
  `pacto_delivery_stage_frozen` and `pacto_delivery_stage_watched` (chart
  `delivery.metrics.*`). Each Stage says in `Watched` whether its running version passed
  its latest re-check and in `Mediated` whether its pointer alone decides what
  runs, gives every condition an observed generation and publishes the
  promoter's login freshness in `status.authFreshness`. The freshness is off by
  default, so a person decides with the sign-in they have; set
  `delivery.promoter.authFreshness` (a duration such as `15m`) to make deciding
  need a recent sign-in. An approval the promoter
  passes over is named on the operation with the reason.
  A Stage reconciles when its operations, its upstream Stage, its
  OCIRepositories, the Kustomizations that apply them or its Application change,
  and every `delivery.promoter.resync` (a minute) otherwise. The promoter reads
  the apply OCIRepository's digest from the apiserver wherever it decides on it,
  and its client allows 50 requests per second, bursting to 100
  (`--kube-api-qps`, `--kube-api-burst`).
  
  **Reach (experimental).** A Stage applies through a Flux `OCIRepository` by
  default, or, as an experimental option, through an Argo CD Application with an
  OCI source (`applyRef.kind: Application`, chart `delivery.argocd.enabled`). `candidates.upstreamRecords` lets a
  Stage follow a Stage in another cluster from that cluster's signed records,
  read with `delivery.promoter.upstreamTrust`; without it the Stage is not Ready
  (`UpstreamUntrusted`), and while those records cannot be read or lost their
  newest entries it is not Ready either (`UpstreamUnreadable`). An unrecordable
  stand-in upstream stops the follower too, until a person of the service's team
  checks it and raises an `acknowledge` naming its record id with a reason
  (`status.upstreamAcknowledged`), or break-glass overrides. Ready also names
  an applier the promoter cannot write through. Only a Flux `OCIRepository` with
  `spec.verify` admits a new version, so the first Stage of a service needs Flux
  even when Argo CD applies it. A freeze is local: it does not stop followers. Argo CD and cross-cluster reach
  may change more than the rest of delivery.
  
  **The recorder** (subcommand `recorder`, chart `delivery.recorder.*`, off by default)
  is the only delivery component that holds the signing key and the registry
  push credentials. With an Ed25519 key the operator, which reads every Secret,
  can read the key too, so prefer a KMS key or keyless signing. It
  writes a signed record of every operation that ends, in order per Stage,
  attaches it to the versions and contract involved, confirms the Referrers API
  lists it and only then advances the Stage's stream. A record can be late, and every
  operation that ends gets one. It signs with an Ed25519 key, a KMS key or keyless through a projected
  service account token, verifies each stream against its trust config before
  extending it, freezes a Stage whose stream is in conflict and can post a
  CDEvent per record. A record of an autonomy operation carries `outcome.level`
  (`from` and `to`). `RecordStoreReady` says whether records are being written,
  and `pacto_delivery_records_pending` shows the lag. With the recorder on, the
  promoter deletes recorded operations after `delivery.recorder.operationTTL` (a week),
  never the one a Stage runs.
  
  **The `delivery` section of the `pacto-operator` chart** (`delivery.enabled`,
  off by default; the default render is unchanged) installs both in
  `delivery.namespace` (`pacto-delivery`, created by the chart and never the
  release namespace) with the
  Stage, Operation and Approval CRDs, their own ServiceAccounts and RBAC, the
  `<release>-delivery-agent` and `<release>-delivery-approver` roles and
  ValidatingAdmissionPolicies that bind an operation's actor to the requesting
  user, keep approvals to people (with `delivery.promoter.authFreshness` set,
  people with a login time, which the apiserver maps to
  `userInfo.extra["delivery.pacto.trianalab.io/auth-time"]`), keep the
  pointer to the promoter or break-glass, keep the promoter itself to the pointer
  field and keep the record fields to the recorder. Every delivery object is
  named `<release>-delivery[-part]`. It refuses to render below Kubernetes 1.30,
  without the ValidatingAdmissionPolicy API or without the policies unless
  `delivery.admissionPolicies.allowUnguarded` is set; the promoter admits no
  operation and counts no approval while they are missing.
  `delivery.promoter.registryCredentials.secretName` lets the promoter read a
  private contract or deploy repository. The delivery CRDs are chart templates
  with `helm.sh/resource-policy: keep`: `helm upgrade` updates them and
  `helm uninstall` leaves them and their history in place. The delivery image is
  published, signed and versioned with the operator.
  
  **The chart's images** gain `image.digest` and `delivery.image.digest`: set
  one to a `sha256:` digest and that image is pulled as `tag@digest`, so the pod
  runs exactly that image whatever the tag later points at.
  
  **The `pacto-operator` chart** gains `dashboard.delivery` for the managed
  dashboard's delivery views: `namespace` (where delivery publishes its identity
  policy), `trust` (the ConfigMap holding the trust config History verifies
  records with) and `miraURL`. The dashboard's role gains no delivery right and
  no impersonation. The operator now passes the managed dashboard its namespace
  scope in `PACTO_DASHBOARD_NAMESPACE`, the variable the dashboard reads.

### Patch Changes

- 9f5f638: Update `github.com/go-jose/go-jose/v4` to v4.1.6 to fix CVE-2026-116587, a
  denial of service when parsing JWKs that contain RSA keys. It reaches the
  `pacto` binary and the operator through OIDC sign-in and Sigstore signing.
  
  `golang.org/x/net` moves to v0.62.0 (GO-2026-6860, GO-2026-6866) and the
  operator's `github.com/google/cel-go` to v0.30.0 (GO-2026-6094). govulncheck
  finds none of the three reachable, so this is hygiene rather than an exposure fix.
  
  The dashboard frontend's dependencies are updated too, which fixes the known
  advisories in the bundle the `pacto` binary and the dashboard image serve:
  DOMPurify 3.4.17, KaTeX 0.18 (used by Mermaid diagrams), devalue 5.9.4 and
  source-map-js 1.2.2. The build and test tooling moves to Vite 8.3 and Vitest
  4.1.12.

## 6.0.1

### Patch Changes

- fe07e42: Fix the operator's `go.sum` checksum for `github.com/trianalab/pacto/v4` v4.0.0.
  The operator v6.0.0 tag recorded a checksum for a core tree that was never
  tagged, so a standalone build of `integrations/kubernetes` failed with a
  checksum mismatch and the v6.0.0 operator image and chart never published.

## 6.0.0

### Major Changes

- 9b84eda: Fixes from a whole-repo architecture review of the operator. Two of them change what you see.
  
  **Behaviour changes**
  
  - Controller logs default to JSON (production mode). Pass `--zap-devel` to get the old console format back.
  - The `RuntimeObserved` condition is now `False` with reason `ObservationFailed` whenever any runtime read failed during collection, and the message lists what failed. Before, it could read `True` while parts of the collection had failed.
  
  **Fixes**
  
  - PactoRevision names and version labels are valid for every semver version, including build metadata (`1.0.0+build.1`), uppercase pre-releases and very long versions. Names that were already valid do not change.
  - ServiceMonitor and PodMonitor endpoints that name their port (the usual case in real clusters) are now resolved, so metrics discovery through them works.
  - An unset numeric `targetPort` now means the Service port everywhere, as the API server defaults it.
  - Configuration schemas are compiled once per contract revision instead of on every reconcile.
  
  **Breaking changes and migration (operator v6)**
  
  - The Go module path becomes `github.com/trianalab/pacto/integrations/kubernetes/v6` (was `.../v5`) and requires `github.com/trianalab/pacto/v4`. Go code importing `api/v1alpha1` updates its imports; `go get github.com/trianalab/pacto/integrations/kubernetes/v6@v6.0.0`.
  - Apply the v6 CRDs before `helm upgrade`, which never touches them: `kubectl apply --server-side --force-conflicts -f charts/pacto-operator/crds/`. v6 adds the `NodeRuntimeObservation` CRD and the operator exits at startup without it (`no matches for kind "NodeRuntimeObservation"`), whether or not the node agent is enabled.
  - Removed `--webhook-cert-path`, `--webhook-cert-name` and `--webhook-cert-key`. The operator registers no webhook and starts no webhook server; drop the flags from any manifest that still passes them, or the operator refuses to start on an unknown flag.
  - Removed `--dashboard-oci-secret` and the `dashboard.ociSecret` chart value, superseded by `--dashboard-oci-secrets` and `dashboard.ociSecrets`. Migrate with `dashboard.ociSecrets: [<name>]`; the chart schema rejects the old key.
  - Removed the condition reasons no reconciler emitted (`NotFound`, `AllPortsMatch`, `MissingPorts`, the seven endpoint probe reasons `OK`, `ConnectionFailed`, `InvalidStatusCode`, `EmptyResponse`, `NotDeclared`, `InterfaceHasNoPort`, `InterfaceNotFound`, and `Match`, `Mismatch`, `Missing`, `Skipped`) and the `SeverityError` and `SeverityWarning` constants from `api/v1alpha1`. Nothing in a cluster carries them; Go code importing them uses `finding.Severity` and the reasons in `troubleshooting.md`.
  
  **Samples**
  
  - The scenario samples under `config/samples/scenarios/` use `pactoVersion: "2.0"` contracts. The duplicate showcase samples were removed in favour of `config/samples/demo/`.
- 9b84eda: Add an opt-in eBPF node agent and the runtime checks it and the Kubernetes API
  make possible.
  
  `nodeAgent.enabled` deploys a DaemonSet (`pacto agent --sink kubernetes` from
  the pacto runtime image the operator already runs the Evidence Server from;
  `--node-agent-image` overrides it) that
  watches TCP socket state through one BTF tracepoint and
  publishes each node's listeners and connections as a `NodeRuntimeObservation`
  object, a new CRD. It reads no payloads, files or Secrets and opens no port. It
  needs Linux 5.8 or later with BTF, `hostPID`, the node's `/sys/fs/cgroup`
  mounted read-only, root and the `BPF`, `PERFMON` and `SYS_PTRACE`
  capabilities, so it runs in its own namespace (`nodeAgent.namespace`, default
  `pacto-node-agent`), which the chart creates with the privileged Pod Security
  level; the release namespace keeps whatever level you set. Each agent renews a
  `coordination.k8s.io` Lease named after its node every publish interval and
  rewrites its `NodeRuntimeObservation` only when the view changes (or every tenth
  interval); the operator reads the node's heartbeat from the Lease. Where the
  API serves `ValidatingAdmissionPolicy` (Kubernetes 1.30 or later), the chart
  binds each agent's writes to the node named in its token, so one agent cannot
  speak for another node. The operator merges the evidence into interface and dependency
  observations: a ready pod with nothing listening on a TCP port becomes
  `INTERFACE_ABSENT`, the latest connection success or failure within the
  staleness bound decides dependency reachability (never against a provider with
  no ready endpoint) and a stale or failed node reads `Unknown`, never a
  violation. The agent's Role is `get`, `create` and `patch` on its observations
  and `get`, `create`, `patch` and `update` on its Leases, the DaemonSet runs on
  Linux nodes only and the operator deletes the objects of a node that no longer
  exists.
  
  Findings carry `evidenceRefs[].collector` and `detectedAt`;
  `status.evaluationCoverage.unverified` lists facets with no usable fact.
  
  **Behaviour change:** the complementary checks run with or without the node
  agent, because none of them depends on it: the interface protocol (with
  `controller.enableProbing`), the interface exposure, the provider version for
  `dependencies[].compatibility` (which also picks the target when several
  providers match the ref) and the persistence scope (read-only claims ignored).
  Exposure is `external` only on positive evidence of a public address: a public
  `externalIPs` entry, or a `LoadBalancer` Service without an internal load
  balancer annotation or an Ingress whose status carries a public IP or
  hostname. A `NodePort` Service or an HTTPRoute leaves it unknown. An interface
  with no `visibility` is `internal`, so one behind such a public address now
  reports `INTERFACE_VISIBILITY_MISMATCH`. Declare `visibility: public` for it.
  
  Fixes: interface availability compared the Service port with the EndpointSlice
  port, which carries the target port, so a Service with `port` different from
  `targetPort` read absent; `oci://` dependencies never matched a sibling Pacto
  and read `Unsupported`; passive health ignored the declared path. The chart now
  exposes `controller.enableProbing` and `controller.enableMetricsObservation`,
  grants `list` and `watch` on ConfigMaps, which the cached reads needed, and
  adds read access to pods, Ingresses, HTTPRoutes and `NodeRuntimeObservation`
  objects. Cached pods are trimmed to the fields the operator reads.
  
  The kernel merge and the probes now come from core `pkg/runtimefacts` and
  `pkg/probe`, so the same verdicts hold on Kubernetes and on any host.
  
  The operator uses evidence types from this release's core, so ship it in the
  same release as `@pacto/core`: the core tag is created first and the operator
  `go.mod` must pin it.
  
  **Breaking changes and migration (operator v6)**
  
  - The node agent moved to a dedicated namespace. Set `nodeAgent.namespace` if
    `pacto-node-agent` does not suit you; it must not be the release namespace and
    must not already exist outside this release. Remove the
    `pod-security.kubernetes.io/enforce=privileged` label you may have put on the
    release namespace for the agent. The operator deletes the agent it ran in the
    release namespace (DaemonSet, RBAC and observations) at its first sync. A
    private `imagePullSecrets` entry must also exist in the agent namespace.
  - `--kernel-evidence-namespace` (and `controller.kernelEvidence.namespace`)
    now defaults to the agent namespace, not the operator's. New flag:
    `--node-agent-namespace`.
  - The managed components' lifecycle permissions moved out of the ClusterRole:
    a `Role` in the release namespace (dashboard and Evidence Server, plus the
    teardown of the old agent) and a `Role` in the agent namespace (the agent's
    DaemonSet, RBAC, observations and Leases). The ClusterRole keeps the reads,
    the dashboard's ClusterRole and binding, node reads, Lease reads and a by-name
    read and relabel of the agent namespace. The operator no longer creates
    namespaces at all, and no longer needs `namespaces` list or watch. An operator
    installed without the chart needs the new Roles (`config/rbac/role.yaml`
    generates them).
  - Objects the operator creates for the agent no longer carry an owner
    reference to the operator Deployment: it lives in another namespace, and
    Kubernetes garbage-collects a dependent whose owner is in another namespace.

## 5.4.3

### Patch Changes

- 79de1ba: This week's dependency bumps in one go: `bubbletea` 2.0.10 → 2.1.0 and
  `x/sync` 0.23.0 → 0.24.0 in the engine, OpenTelemetry 1.46.0 → 1.47.0 (and the
  Prometheus exporter 0.68.0 → 0.69.0) in the operator module. No source changes.
  
  Go goes 1.26.6 → 1.26.9 and `x/net` 0.59.0 → 0.60.0 in both modules, because
  govulncheck flagged reachable `net/http` and `x/net/http2` vulns
  (GO-2026-6607 to GO-2026-6617) and wouldn't let any of the bot's PRs through.
  Both shipped binaries were exposed, so it's worth the patch release.

## 5.4.2

### Patch Changes

- fa6d27a: Take the week's dependency bumps in one go: `modelcontextprotocol/go-sdk`
  1.7.0 → 1.8.0 in the engine, and `controller-runtime` 0.25.0 → 0.25.1,
  `ginkgo` 2.32.2 → 2.33.0 and `gomega` 1.43.0 → 1.43.1 in the operator module.
  No source changes — all four are drop-in.
  
  Dependabot also learns to group: Kubernetes libraries move as one train
  because client-go, apimachinery and controller-runtime pass each other's
  types, Charm moves as one because bubbletea, bubbles and lipgloss share the v2
  renderer, everything else minor-or-patch lands in a single weekly PR. Five
  open PRs for one afternoon of bumps is how a bot teaches you to stop reading
  it.

## 5.4.1

### Patch Changes

- 35c37f6: Carry the checksums for the pinned engine core in the operator's `go.sum`.
  
  The published `v5.4.0` module requires `github.com/trianalab/pacto/v3 v3.3.0`
  while its `go.sum` only covers `v3.2.7`, so building the operator module on
  its own — outside the workspace, which is how a consumer or a fresh clone
  builds it — fails with `missing go.sum entry` for every `pacto/v3` package.
  The fix landed on main in #369; this release ships it, and moves the
  published pin to `v3.3.1`.

## 5.4.0

### Minor Changes

- 771574b: Make the operator's status tell the truth about overrides, force-pushed tags and
  per-tag failures, and stop the registry fan-out riding on the reconcile loop.
  
  **`spec.overrides` are now validated.** An override patched the parsed contract
  struct only, so validation layers 1 and 3 — the JSON Schema check and policy
  enforcement — still ran against the original document. An override that violated
  a policy reported `ContractValid=True`. The patch now applies to the raw YAML as
  well, so every layer sees the contract the operator actually reconciles.
  
  **Force-push detection works on the cases it was missing.** Two of them. A tag
  force-pushed twice compared the registry digest against an arbitrary
  `PactoRevision` rather than the newest one, so `TagOverwritten` re-fired on every
  reconcile forever. And a registry tag of `v1.2.0` for a contract declaring
  `service.version: 1.2.0` missed the label lookup entirely, so the drift check
  never ran for any v-prefixed tag.
  
  **A tag that fails to load is now reported.** Every per-tag failure during
  mirroring was a `log.V(1)` and a `continue`: invisible at default verbosity, and
  one bad tag aborted nothing but told nobody. Failures are aggregated and surface
  as the same events the main path already uses — `ContractUnavailable` for a
  transient obtain failure, `ContractInvalid` for everything else.
  
  **`.status.contractStatus` and the dashboard now agree.** The operator derived
  the status ladder from findings with its own local copy of the rule. It calls
  `validation.DeriveStatus` instead, so a contract cannot read `Warning` in
  `kubectl` and `Compliant` in the dashboard.
  
  **A configuration's schema is read from the bundle.** The runtime config
  dimension treated `configuration.schema` as inline JSON when it is a
  bundle-relative path, so a real schema never compiled and the dimension reported
  insufficient evidence instead of a verdict.
  
  **Tag mirroring runs on its own schedule.** Enumerating a registry's tags and
  loading each one used to happen inline in `Reconcile`, which every watched
  workload write triggers — so a busy namespace turned into registry traffic
  proportional to unrelated churn. It is now a manager runnable on a five-minute
  tick, independent of reconciliation.
  
  **Pull Secrets are read straight from the API server.** A cached typed `Get` on
  a Secret builds an informer for every Secret in scope and parks its `.data` in
  the operator's memory. Secret reads go through the uncached reader and the Secret
  watch carries metadata only, so the operator holds no credential it is not using
  right now.
  
  The dashboard and Evidence Server components moved to the same runnable shape
  behind a shared `component` lifecycle, with the same five-minute tick and an
  ownership check before any delete.
  
  Deprecated, with no replacement needed: the condition reasons no reconciler
  emits — `NotFound`, `AllPortsMatch`, `MissingPorts`, every `ReasonEndpoint*`,
  every runtime-reconciliation reason and the three severity constants. The
  outcomes they used to name are reported as evidence and findings now. They stay
  through v5 for API compatibility and are removed at v6.

## 5.3.0

### Minor Changes

- 92a064f: Make the contract verdict usable as a GitOps promotion gate, and emit the event
  that says a contract recovered.
  
  The operator has always reached a verdict and written it to
  `status.contractStatus`. Neither Flux nor Argo CD reads that field. Flux decides
  health with kstatus, which recognises `Ready`, `Reconciling` and `Stalled` and
  discards every condition Pacto publishes, so a Kustomization holding a
  `NonCompliant` Pacto reports healthy. Argo picks health checks from a closed list
  of built-in kinds and returns nothing for the rest, and the roll-up ignores
  nothing — a violated contract is not unhealthy to Argo, it is invisible. Both
  tools have an extension point for this; neither ships one for Pacto.
  
  The new [GitOps promotion gates](integrations/kubernetes/gitops.md) page is those
  two snippets, plus the timing they depend on. The Flux one is a
  `spec.healthCheckExprs` entry with no `inProgress` expression, so an unrecognised
  verdict holds the deploy instead of going falsely green. The Argo one is a Lua
  health customization written for the sandbox Argo actually runs it in, which has
  the string library disabled.
  
  Neither snippet is an illustration. Both live under
  `tests/acceptance/kind/fixtures/gitops/`, the page includes those files rather
  than copies of them, and each has a kind acceptance shard that applies it to a
  real cluster running the tool it targets.
  
  `gitops-flux.sh` proves the Flux gate changes what ships: a contract that
  contradicts the workload must leave the dependent Kustomization's manifest out of
  the cluster entirely, and correcting the contract must let it through. It runs at
  the operator's default stabilization window on purpose, because the page claims a
  mismatch does not wait one out.
  
  `gitops-argocd.sh` runs in two passes, because the Argo snippet's failure mode is
  silence — a `data` key Argo does not recognise is ignored, and an ignored key
  looks exactly like having configured nothing. The first pass needs no cluster:
  the `argocd` CLI evaluates the customization in the same Lua sandbox the
  controller uses, which is what gives every contract status an assertion,
  including the states a running cluster passes through too quickly to catch — no
  status yet, a verdict behind the contract, a status added in some future release.
  The second pass serves an Application from an OCI source in kind and requires it
  to go `Degraded` naming the finding, then `Healthy` once the contract is
  corrected. The page's read-back recipe is that same CLI command, and the shard
  runs it against the live cluster rather than only publishing it.
  
  Building that second pass turned up something the page has to say out loud: the
  merge patch alone is not enough. Argo's application controller caches health
  customizations at startup and compares that cached verdict to decide whether a
  changed object is worth re-examining, so a controller that started without the
  customization treats every verdict the operator writes as no change and only
  catches up on the next periodic resync, minutes later. The page now pairs the
  patch with a controller restart, and says why the two read-back checks cannot
  detect the difference — both read the ConfigMap, not the controller.
  
  `ContractRecovered` is new. The three contract warnings — `ValidationFailed`,
  `ContractInvalid`, `ContractUnavailable` — are transition-gated, so a contract
  that goes back to `Compliant` used to fall silent with no event marking the
  recovery. It now has one, matching the pair that `ReadinessGateUnmet` and
  `ReadinessRecovered` already formed. `ValidationFailed` also fired only when
  `status.summary` happened to be set; it now fires on the transition itself.
  
  Two documentation corrections ride along, both load-bearing for the timing advice
  on the new page. The stabilization window applies to **absences only** —
  `INTERFACE_ABSENT`, `DEPENDENCY_UNREACHABLE`, `CAPABILITY_ABSENT` and
  `CONFIGURATION_ABSENT`. A **mismatch** — `WORKLOAD_MISMATCH`,
  `PERSISTENCE_MISMATCH`, `CONFIGURATION_MISMATCH` — is `NonCompliant` on the first
  reconcile that observes it. The events table said seven events and listed seven;
  there are eight.

## 5.2.4

### Patch Changes

- 840a183: Move both Go modules onto the Kubernetes 0.37.0 library line and patch the
  runtime image's OpenSSL.
  
  `k8s.io/api`, `k8s.io/apimachinery` and `k8s.io/client-go` are now v0.37.0 in
  `go.mod` and `integrations/kubernetes/go.mod`, together with the transitive
  `k8s.io/kube-openapi`, `k8s.io/utils`, `k8s.io/streaming`,
  `sigs.k8s.io/structured-merge-diff/v6` and `go-openapi/swag` moves the line
  pulls in. The three library modules only work in lockstep, so bumping them
  one at a time — as the individual Dependabot pull requests did — leaves
  `k8s.io/api` behind and fails to compile.
  
  The runtime stage of the CLI/dashboard image now runs `apk upgrade` before
  installing its packages, so the image picks up the fixed `libssl3`/`libcrypto3`
  3.5.8-r0 instead of the 3.5.7-r0 baked into the `alpine:3.22` tag (CVE-2026-14456,
  HIGH).
  
  No API or behaviour change.

## 5.2.3

### Patch Changes

- b11de31: Move both Go modules onto the Kubernetes 0.36.4 library line.
  
  `k8s.io/client-go`, `k8s.io/api` and `k8s.io/apimachinery` are now v0.36.4 in
  `go.mod` and `integrations/kubernetes/go.mod`. The bumps landed on `main`
  without a changeset, so neither the core line nor the kubernetes line would
  have shipped them — this patch is what actually publishes a core module and an
  operator image built against 0.36.4.
  
  No API or behaviour change: the 0.36.4 patch releases only refresh the
  `golang.org/x` dependencies underneath.

## 5.2.2

### Patch Changes

- 2ea7dbe: Rebuild the operator on pacto core v3.2.2.
  
  The 3.2.2 Version PR bumped `integrations/kubernetes/go.mod` to
  `github.com/trianalab/pacto/v3 v3.2.2`, but a core-line release does not
  republish the kubernetes line, so the operator image and chart stayed on the
  code built against v3.2.1. This changeset moves the kubernetes fixed group to
  5.2.2 so the published operator actually carries:
  
  - one `FILE_NOT_FOUND` finding per missing interface spec file, and a directory
    at a spec path no longer passing as a file
  - date-like scalars kept verbatim through generic YAML round-trips, so an
    unquoted timestamp is validated as the text the contract author wrote

## 5.2.1

### Patch Changes

- c230de9: Make a demo-fixture edit unable to half-ship a release.
  
  Release run 32560058692 published four irreversible units and then died. Two
  independent defects had to line up for that, and both are closed here.
  
  The demo bundles publish to immutable tags. `payments-service` 2.1.0 was edited
  in place — a mermaid diagram added to a version already published — so the
  byte-exact gate correctly refused the tag, but it refused it mid-release,
  because nothing ran that gate before the release. The fixture is restored to its
  published bytes and the diagram ships as a new `payments-service` 2.1.1, and
  `publish-demo-bundles.sh --check` now runs the identical gate read-only at PR
  time as the `demo-bundle-immutability` CI leg.
  
  Separately, the `demo-compose` job lost its ORAS install when the unit moved to
  `docker compose publish`, on the reasoning that ORAS stayed where the ledger
  used it — while that job still read and wrote the ledger, which *is* the ORAS
  user. `ledger.sh` returned the empty string for a missing binary, the empty
  string means "nothing recorded", and the unit failed closed. `ledger.sh` now
  refuses to run without its tools and distinguishes a 404 from an unreadable
  registry; the two `if [ "$(ledger.sh …)" ]` call sites that discarded its exit
  status now assign first; and a new gate walks every job's shell through its make
  targets and scripts and fails when a job can reach a CLI it never installed.
  That gate found a second, quieter instance: the release dry run was rehearsing
  without `syft`, silently skipping the SBOM the real release produces.

## 5.2.0

### Minor Changes

- 8352060: Run the operational graph in a cluster: an operator-managed Evidence Server and observed-dependency input for the dashboard.
  
  - **Managed Evidence Server.** Set `evidence.enabled` and the operator reconciles
    a separate Evidence Server Deployment and an internal Service, with optional
    Ingress or Gateway API `HTTPRoute` exposure. It is single-writer, so its replica
    count is fixed at one, and it runs the same runtime image as the managed
    dashboard. `evidence.registry.subjects` names the exact immutable contract
    revisions evidence may be reported against; the registry holding them is the
    durable store, so the chart installs no volume, database or bucket of its own.
  - **Observed dependencies.** `dashboard.observation.sources` mounts offline
    OTLP/JSON trace exports read-only into the managed dashboard, so its
    operational graph can reconcile declared dependencies against observed ones.
    Each entry is one named data source with a stable identity, read through a root
    the process cannot follow a symlink out of, never written to and never scanned.
    This is offline input only: no OTLP receiver ships and no collector is deployed.
  - **`insecureRegistries`.** Reach named `host:port` registries over plain HTTP for
    a controlled in-cluster registry, scoped per host so every other registry stays
    HTTPS-only. The controller, the managed dashboard and the managed Evidence
    Server all inherit it.
  
  Also adds a `pacto-dev-gateway` chart that installs Envoy Gateway and a
  `GatewayClass` for local development, so the Gateway API path can be exercised on
  a laptop cluster.
  
  Backwards compatible: every new capability is off by default and no existing
  value, CRD field or status shape changes.

## 5.1.2

### Patch Changes

- bbc7b9c: Rebuild the operator and dashboard container images through the new native per-arch
  build pipeline: each architecture builds on its own runner (no QEMU emulation) and is
  merged into the multi-arch manifest. No functional change to the engine, operator, or
  dashboard — this release ships and validates the faster image pipeline.

## 5.1.1

### Patch Changes

- dd4dab1: Repo-wide audit remediation (engine, CLI, dashboard). Closes the OpenAPI
  breaking-change diff false-negatives — path-item-level parameters are now diffed,
  the request body is deep-diffed so a newly required property is BREAKING, and
  optional→required / added-required parameters are BREAKING — so a BREAKING-only
  release gate can no longer be bypassed. Further security and dashboard fixes land
  in the same PR.

  As the first release since v3.1.0, this also ships the previously-merged but
  unreleased dashboard **Ctrl+C shutdown fix** and the demo version-label fix.

## 5.1.0

### Minor Changes

- b58778a: Unify all published OCI artifacts under the monorepo `ghcr.io/trianalab/pacto/*` namespace.

  - operator image → `ghcr.io/trianalab/pacto/operator`
  - operator chart → `ghcr.io/trianalab/pacto/charts/pacto-operator`
  - dashboard image → `ghcr.io/trianalab/pacto/dashboard`
  - dashboard contract bundle → `ghcr.io/trianalab/pacto/dashboard-contract`
  - demo bundles already live under `ghcr.io/trianalab/pacto/*`

  All packages are now created and owned by this repository. The chart name
  `pacto-operator` and the Artifact Hub repository are preserved (re-point the AH
  repository URL to the new chart coordinate). The previous coordinates remain as
  historical — their already-published versions are unaffected. Go module paths
  (`/v3`, `/v5`) are unchanged.

## 5.0.0

### Major Changes

- 045f11e: The Kubernetes integration moves into the monorepo — breaking for consumers.

  - The Go module path becomes `github.com/trianalab/pacto/integrations/kubernetes/v5`
    (was `github.com/trianalab/pacto-operator`).
  - It pins the published core module `github.com/trianalab/pacto/v3` at release
    time; the `go.work` workspace resolution is development-only.
  - The integration continues the operator `v4` line as `v5` (image + chart +
    module); public OCI/chart coordinates are preserved.
