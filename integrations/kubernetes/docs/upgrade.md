# Upgrade the Kubernetes operator

An upgrade is one `helm upgrade`, which updates the CRDs along with the
operator. A cluster whose CRDs came from an older chart's `crds/` directory
needs one extra step the first time, and this page shows it.

The Kubernetes integration has its own version, separate from Pacto core: the
operator image, Helm chart, Go module and these docs move together.

## Upgrade with Helm

The currently published chart:

--8<-- "integrations/kubernetes/docs/generated/_upgrade-command.md"

!!! warning "An upgrade reverts hand-patched controller flags"
    `helm upgrade` rebuilds the Deployment's `args` from the chart, so a flag
    you patched in by hand disappears without an error. Set opt-in features
    through chart values such as `controller.probing`,
    `controller.metricsObservation`, `controller.interfaceNameMatchDiscovery`
    and `nodeAgent.enabled`, which survive an upgrade. Each operator flag
    has a chart value or a documented reason to stay off the chart; see
    [Limitations: opt-in features](limitations.md#opt-in-features).

## Deprecated observation keys

`controller.enableProbing` and `controller.enableMetricsObservation` still
work. Their replacements, `controller.probing` and
`controller.metricsObservation`, default to `auto`, which turns each feature on
only in namespaces labelled `pacto.trianalab.io/observe`.

An upgrade that keeps the previous release's values renders the same
arguments; set the new key, or reset the values, to move to `auto`. The
new key wins, even `auto` over a deprecated `true`:

```yaml
controller:
  enableProbing: true   # deprecated, ignored once probing is set
  probing: auto
```

`controller.kernelEvidence.enabled: false` still follows `nodeAgent.enabled`;
the quoted `"off"` refuses kernel evidence while the node agent runs.

## Hand older CRDs to Helm

A CRD (CustomResourceDefinition) tells Kubernetes what a `Pacto` object looks
like. The chart renders its CRDs as part of the release, so `helm upgrade`
updates a changed CRD and adds a new one, and `helm uninstall` keeps them
(`helm.sh/resource-policy: keep`). Older charts installed the operator CRDs
from a `crds/` directory that Helm never upgrades and never records as part
of the release.

If your CRDs came from such a chart, `helm upgrade` stops with `invalid
ownership metadata`. Label and annotate them for your release once, then run
the upgrade. Change the release name and namespace if yours differ:

```bash
for crd in $(kubectl get crd -o name | grep -E '/[a-z]+\.pacto\.trianalab\.io$'); do
  kubectl label "$crd" app.kubernetes.io/managed-by=Helm --overwrite
  kubectl annotate "$crd" --overwrite \
    meta.helm.sh/release-name=pacto-operator \
    meta.helm.sh/release-namespace=pacto-operator-system
done
```

The loop runs the same in bash and zsh. The `grep` matches only the operator CRDs. The delivery CRDs, group
`delivery.pacto.trianalab.io`, were already part of the release.

## Image values

The chart builds each image from `global.imageRegistry` (default `ghcr.io`)
and a `repository` without a registry, such as `trianalab/pacto/operator`.
An image's own `registry` value replaces the global one for that image.

Values from older charts set the whole reference in `image.repository` or
`delivery.image.repository`, such as
`ghcr.io/trianalab/pacto-operator/pacto-controller`. The chart keeps such a
repository as the whole reference and adds no registry, so these values
upgrade unchanged. The chart treats a repository as already carrying a
registry host when its first path segment holds a `.` or `:` or is
`localhost`. The [Helm reference](helm-reference.md) lists every
image value.

## Upgrading across a major version (CRD migration)

Helm applies the new CRDs ahead of the new operator, so a major upgrade is
the same command as any other:

--8<-- "integrations/kubernetes/docs/generated/_upgrade-command.md"

The API version stays `v1alpha1` across the major bump and the stored version is
unchanged. Every existing `Pacto` resource therefore remains stored and readable
under the new CRD, with no conversion webhook and no decode error, and the upgraded
operator reconciles it in place. That holds because **`spec` is additive**: v5 adds
`spec.target.configBindings` and `spec.target.interfaceBindings` and removes
nothing, so a contract resource written for v4 still validates unchanged.

### Status is redesigned

!!! warning "The upgrade drops v4 status observations on sight"
    v5 redesigns `status`: it removes 51 status paths and adds 35.
    `status.runtime`, `status.endpoints`, `status.ports`, `status.scaling`,
    `status.readiness.checks`, `status.contract.imageRef` and
    `status.summary.{passed,failed,total}` are gone, replaced by
    `status.findings`, `status.evaluationCoverage` and
    `status.summary.{errorCount,warningCount,infoCount,unknownCount}`. That is
    why the printer columns change from `PASSED`/`FAILED` to
    `ERRORS`/`WARNINGS`.

    The apiserver stops serving the removed fields the moment the new CRD
    lands. A `kubectl get pacto -o yaml` taken before the new operator writes
    `status` therefore shows a resource whose `spec` is intact and whose
    `status` looks half-empty.
    The CRD causes this. The old values are still in etcd and are lost for
    good only once something writes `status` again, so that window is a normal
    part of the migration.

### Confirming the migration

Before and after the upgrade:

```bash
kubectl get crd pactos.pacto.trianalab.io \
  -o jsonpath='{.spec.versions[0].additionalPrinterColumns[*].name}'   # reflects the new schema
kubectl get pacto -A                                                    # existing resources still list + reconcile
```

If a future release ever ships an incompatible schema change, the upgrade or
the resource read fails with an error, so no resource disappears silently.
See the [CRD reference](crd-reference.md) for the current field set.

This exact flow is exercised against a real cluster by
`tests/acceptance/kind/upgrade-v4-v5.sh`, the `upgrade` leg of CI's
`ci-e2e-kind` job. It installs the real v4 chart with its v4 CRDs, hands them to
Helm as [above](#hand-older-crds-to-helm), then `helm upgrade`s to the current
chart and asserts the CRDs migrate and the pre-existing resource survives and
reconciles.

## Upgrading to v6

v6 adds the `NodeRuntimeObservation` CRD, which `helm upgrade` creates. Hand
the older CRDs to Helm first as [above](#hand-older-crds-to-helm), then check
these before `helm upgrade`:

- **Removed flags and values.** `--webhook-cert-path`, `--webhook-cert-name`,
  `--webhook-cert-key` and `--dashboard-oci-secret` are gone; the operator
  refuses to start with an unknown flag. Replace `dashboard.ociSecret: <name>`
  with `dashboard.ociSecrets: [<name>]`: the chart schema rejects the old key.
- **The node agent has its own namespace.** It runs in `nodeAgent.namespace`
  (`pacto-node-agent` by default), which the chart creates privileged for Pod
  Security. Drop the `pod-security.kubernetes.io/enforce=privileged` label
  you may have put on the release namespace for it. At its first sync the
  operator deletes the agent it ran in the release namespace. A private pull
  secret for the agent image must exist in the agent namespace too.
- **RBAC is split by namespace.** The managed components' lifecycle permissions
  moved from the ClusterRole to a Role in the release namespace and, with the
  node agent, a Role in the agent namespace. The chart renders both. An
  operator installed without the chart needs them added by hand
  ([RBAC](rbac.md) lists every rule).
- **Kernel evidence namespace.** `controller.kernelEvidence.namespace`
  defaults to the agent namespace instead of the operator's.

## Delivery approvers need update rights

Whoever may update a Stage's workload decides its held changes. Owner
teams narrow who may answer only when `delivery.identities.teamGroupPrefix` is
set, and `requireOwnerTeam` defaults to `false`. To keep the earlier behaviour, set `teamGroupPrefix: ""` and
`requireOwnerTeam: true` before you upgrade
([upgrading](delivery-identity.md#upgrading)).

## Delivery always installs its admission policies

Delivery no longer runs without its admission policies. The chart refuses
`delivery.admissionPolicies.enabled` and
`delivery.admissionPolicies.allowUnguarded` whatever their value, and names
the replacement. Before `helm upgrade`:

- Delete both keys from your values.
- If you set `allowUnguarded` to skip the Kubernetes 1.30 and
  `ValidatingAdmissionPolicy` API check for `helm lint` or `helm template`,
  set `delivery.admissionPolicies.skipCapabilityCheck=true` instead. The
  policies still render.
- An upgrade that reuses the earlier release's values carries `enabled: true`
  over from the earlier chart's defaults, so it fails. Reuse them with
  `--reset-then-reuse-values`.

A promoter started without `--admission-policies` admits no operation and
counts no approval.

## Delivery records work on every registry

- Stages gain `status.stream.registryMode` (`native` or `write-once-tags`),
  which only the recorder may write. `helm upgrade` installs the new field
  with the CRDs. A Stage that was ready before the upgrade shows the mode
  after the recorder writes its next record.
- A streams repository on a registry without the OCI Referrers API, such as
  GHCR, now holds records as
  [write-once tags](delivery-records.md#write-once-tags) and reports the
  `RecordStoreReady` reason `WriteOnceTags`. The earlier reason for such a
  registry is gone: alerts or scripts that match it match nothing.
- A Stage that recorded `native`, or that was ready before the upgrade, and
  then sees no Referrers API stays `RegistryUnavailable` instead of
  switching to tags.

## Approvals are signed records

- With the recorder on, it signs every Approval into the registry, and the
  promoter counts an Approval only once its record is written. Operation
  records then name each approval's record, which an earlier CLI or promoter
  cannot read. Upgrade the CLIs and every promoter that follows these streams
  first, then the recorder
  ([details](delivery-signed-approvals.md#upgrade-approval-records)).
- The recorder signs no Approval made before it first saw the approval
  admission policy. Such an Approval on a held operation waits and does not
  count, so its author approves again.
- Before you set `delivery.identities.signers`, create the approvals
  repository (`delivery.recorder.approvalsRepository`, default
  `<streamsRepository>-approvals`) where a registry needs it, such as ECR,
  and let the recorder push there. The recorder anchors it only once a
  signer is set. Until it can, keyless approvals wait and operation records
  keep flowing.
- Approvals gain a status subresource and the recorder-only fields
  `spec.record` and `spec.signedAt`; `helm upgrade` installs them with the
  CRDs.

## Signatures are checked against publishers

- A revision's signature `status` (`pacto_fleet_get`, the dashboard and
  `pacto fleet`) is `verified` only when a publisher rule, a person's pin, a
  signed policy or an external system's release signer accepts the signer.
  A signer the contract's own `publish.sign` declares is no longer enough:
  scripts that relied on it read `unknown` or `unverified` until the
  installation sets `delivery.publishers`. The new `source` field says what
  decided.
- New chart values: `delivery.publishers.registry`, `defaultBranch`,
  `policy`, `policySigners` and `trustedRoot`, and
  `delivery.recorder.signing.publicKey`. Reused values without a
  `delivery.publishers` block render the promoter arguments they had.
- Stages gain `spec.candidates.registry` and the promoter-only
  `status.publisher`, and Operations gain the type
  `delivery.pacto.trianalab.io/trust` with `spec.params.publisher`; `helm
  upgrade` installs them with the CRDs. Existing `ociRepositoryRef` and
  upstream Stages keep working unchanged
  ([publishers](delivery-setup.md#publishers)).

## Delivery grants Argo CD and Flux access by itself

- `delivery.argocd.enabled` and `delivery.flux.enabled` now default to
  `auto`.
- An upgrade that takes the new chart's defaults, such as
  `--reset-then-reuse-values`, on a cluster that serves Argo CD Applications
  grants the promoter `patch` on Applications. Admission limits
  that write to `spec.source.targetRevision` of Applications labelled
  `delivery.pacto.trianalab.io/apply-ref`. Set `delivery.argocd.enabled=false`
  to keep it off.
- Flux access renders as before. On a cluster that serves Flux HelmReleases
  the promoter also gains HelmRelease read, the `--flux-helm` flag and the
  `applier-helmrelease` admission policy.
- An upgrade that keeps the earlier chart's defaults keeps `enabled: false`
  for Argo CD, and Flux access as it was.
- `auto` reads the APIs at install and upgrade: run `helm upgrade` after you
  install Argo CD or Flux.

## The dashboard asks people to sign in

After every upgrade, one that reuses the earlier release's values included,
the dashboard asks people to sign in
([token sign-in](dashboard-authentication.md#token-sign-in)):

- The new value `dashboard.auth.tokenSignIn.enabled` defaults to `true`, and
  no earlier release has it, so reused values keep it on.
- `dashboard.auth.allowAnonymous` defaults to `false`. To keep serving
  without sign-in, set `dashboard.auth.tokenSignIn.enabled=false` and
  `dashboard.auth.allowAnonymous=true`. The install notes say so when the
  previous release served without sign-in.
- The earlier value that turned token sign-in on is deprecated and ignored.
  It still validates, so reused values keep working.
- A ServiceAccount token signs in only when minted with
  `--audience pacto-dashboard`, and its session reads the fleet only. A
  person's own token signs in as before and may now approve.
- The dashboard's ServiceAccount gains `create` on `tokenreviews` through the
  ClusterRole `<fullname>-<namespace>-dashboard-tokenreview`, also with
  `controller.watchNamespaces`.

A dashboard you run yourself beyond loopback still needs `--token-login`,
`--oidc-issuer-url` or `--allow-anonymous`.

## The dashboard serves MCP at /mcp

After an upgrade, reused values included, the managed dashboard also serves
MCP to AI agents at `/mcp` when token sign-in is on. The new value `dashboard.mcp.enabled` defaults to
`true`, the install notes say so once and print a client snippet, and no RBAC,
Service or port changes. To turn it off, set `dashboard.mcp.enabled=false`; see
[MCP at /mcp](dashboard-authentication.md#mcp-at-mcp).

## Pull secrets outside granted namespaces

The operator no longer reads Secrets across the cluster. It reads a pull
secret in the release namespace, in each of `controller.watchNamespaces` and
wherever `controller.secretGrants` names it. Any other pull secret sets
`ContractValid=Unknown` with reason `PullSecretNotGranted`, and the condition
message names the grant to add. Before `helm upgrade`, list the pull secrets
in use:

```bash
kubectl get pactos -A \
  -o jsonpath='{range .items[*]}{.metadata.namespace}/{.spec.contractRef.pullSecretRef}{"\n"}{end}'
```

- A cluster-wide install lists each one as `namespace/name` in
  `controller.secretGrants`.
- A namespace-list install already reads pull secrets in each listed
  namespace. There the message points at the chart's watch Role and
  RoleBinding in that namespace.

A rotated pull secret is read within one check interval.

`controller.watchNamespace` still works and joins `controller.watchNamespaces`,
also when an upgrade reuses earlier values.

Moving from a cluster-wide install to a namespace list leaves the
`pacto-dashboard` ClusterRole and ClusterRoleBinding behind: the
operator may no longer delete them. The `helm upgrade` notes and a
`StaleClusterRBAC` Warning event on the dashboard Deployment report them.
Delete them:

```bash
kubectl delete clusterrole,clusterrolebinding pacto-dashboard
```

## Rolling back

**Within** a major, `helm rollback pacto-operator` reverts what the chart owns
and leaves your `Pacto` resources untouched; re-apply any
[hand-patched controller flags](#upgrade-with-helm) afterwards. **Across** a
major it takes more work. A chart from before the CRDs moved into the release
does not carry them, so rolling back to it leaves the new CRDs in place. The old
controller then writes status fields the new schema lacks, which the API server
drops while still reporting success. A real return to that major means rolling
the chart back *and* server-side applying the previous release's CRDs from its
`config/crd/bases`.

## Turning on the Evidence Server during an upgrade

The Evidence Server arrived in chart 5.2.0 and is off by default, so an upgrade
changes nothing until you set `evidence.enabled`. Its requirements, a subject
list and a registry serving the native Referrers API, are on
[The Evidence Server](evidence-server.md). Turning it back off later removes the
whole footprint and loses nothing: every accepted record already lives in your
contract registry.

--8<-- "integrations/kubernetes/docs/generated/_compatibility.md"
