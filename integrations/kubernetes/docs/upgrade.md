# Upgrade the Kubernetes operator

Most upgrades are one `helm upgrade`. A release that changes or adds a CRD
needs one extra step first, and this page shows it.

The Kubernetes integration has its own version, separate from Pacto core: the
operator image, Helm chart, Go module and these docs move together.

## Upgrade with Helm

For an upgrade that does not change the CRD schema, a plain `helm upgrade` is
enough. The currently published chart:

--8<-- "integrations/kubernetes/docs/generated/_upgrade-command.md"

!!! warning "An upgrade reverts hand-patched controller flags"
    `helm upgrade` rebuilds the Deployment's `args` from the chart, so a flag
    you patched in by hand disappears without an error. Set opt-in features
    through chart values such as `controller.enableProbing`,
    `controller.enableMetricsObservation` and `nodeAgent.enabled`, which
    survive an upgrade. A flag with no chart value, such as name-match
    discovery, has to be patched again after every upgrade; see
    [Limitations: opt-in features](limitations.md#opt-in-features).

## Upgrading across a major version (CRD migration)

A CRD (CustomResourceDefinition) tells Kubernetes what a `Pacto` object looks
like. Helm installs the CRDs in a chart's `crds/` directory on the first
`helm install` and never touches them again: `helm upgrade` neither updates a
changed CRD nor adds a new one. A release that changes the schema or ships a
new CRD therefore has one extra, ordered step: **apply the new CRDs before you
run `helm upgrade`**.

**Step 1: apply the new CRDs yourself.** Use server-side apply
(`--server-side`): these CRDs are too large for the annotation a client-side
apply writes, and `--force-conflicts` takes over the fields the previous
install set.
The URLs are pinned to the release tag these docs describe, so the schema you
apply is the one the chart in step 2 expects:

--8<-- "integrations/kubernetes/docs/generated/_crd-apply.md"

**Step 2: upgrade the release to the new chart version**, exactly the command
from [Upgrade with Helm](#upgrade-with-helm) above:

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
    lands. A `kubectl get pacto -o yaml` taken between step 1 and step 2
    therefore shows a resource whose `spec` is intact and whose `status` looks
    half-empty.
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

If a future release ever ships an incompatible schema change, the server-side
apply or the resource read fails with an error, so no resource disappears silently.
See the [CRD reference](crd-reference.md) for the current field set.

This exact flow is exercised against a real cluster by
`tests/acceptance/kind/upgrade-v4-v5.sh`, the `upgrade` leg of CI's
`ci-e2e-kind` job. It installs the real v4 chart with its v4 CRDs, server-side
applies the new CRDs, then `helm upgrade`s to the current chart and asserts the
pre-existing resource survives and reconciles.

## Upgrading to v6

v6 adds the `NodeRuntimeObservation` CRD, so apply the CRDs first as
[above](#upgrading-across-a-major-version-crd-migration). Then check these
before `helm upgrade`:

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
major it takes more work. Helm does not manage `crds/` in either direction, so
a rollback leaves the new CRD in place. The old controller then writes v4-only
status fields that the API server drops while still reporting success. A real return to the
previous major means rolling the chart back *and* server-side applying the
previous release's CRDs, the mirror image of step 1.

## Turning on the Evidence Server during an upgrade

The Evidence Server arrived in chart 5.2.0 and is off by default, so an upgrade
changes nothing until you set `evidence.enabled`. Its requirements, a subject
list and a registry serving the native Referrers API, are on
[The Evidence Server](evidence-server.md). Turning it back off later removes the
whole footprint and loses nothing: every accepted record already lives in your
contract registry.

--8<-- "integrations/kubernetes/docs/generated/_compatibility.md"
