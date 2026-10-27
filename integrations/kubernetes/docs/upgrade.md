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
    through chart values such as `controller.enableProbing`,
    `controller.enableMetricsObservation` and `nodeAgent.enabled`, which
    survive an upgrade. A flag with no chart value, such as name-match
    discovery, has to be patched again after every upgrade; see
    [Limitations: opt-in features](limitations.md#opt-in-features).

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
CRDS=$(kubectl get crd -o name | grep -E '/[a-z]+\.pacto\.trianalab\.io$')
kubectl label $CRDS app.kubernetes.io/managed-by=Helm --overwrite
kubectl annotate $CRDS --overwrite \
  meta.helm.sh/release-name=pacto-operator \
  meta.helm.sh/release-namespace=pacto-operator-system
```

The `grep` matches only the operator CRDs. The delivery CRDs, group
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
`localhost`. The [Helm reference](generated/helm-reference.md) lists every
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
