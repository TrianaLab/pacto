# Upgrade the Kubernetes operator

The Kubernetes integration is versioned independently from Pacto core: the
operator image, Helm chart, Go module and this documentation set bump together
on their own cadence.

## Upgrade with Helm

For an upgrade that does not change the CRD schema, a plain `helm upgrade` is
enough. The currently published chart:

--8<-- "integrations/kubernetes/docs/generated/_upgrade-command.md"

!!! warning "An upgrade reverts hand-patched controller flags"
    `helm upgrade` re-renders the Deployment's `args` from the template, so an
    opt-in flag you patched in by hand disappears silently. Nothing fails; the
    dimension goes back to `Unsupported` or to passive observation. Re-apply
    the patch after every upgrade, and see
    [Limitations — opt-in features](limitations.md#opt-in-features) for why it
    is not managed state.

## Upgrading across a major version (CRD migration)

Helm never upgrades the CustomResourceDefinitions bundled under a chart's `crds/`
directory: it installs them on the first `helm install` and then leaves them
untouched on every `helm upgrade`. A major release that changes the CRD schema
therefore has one extra, ordered step — **apply the new CRDs before you run
`helm upgrade`**:

**Step 1 — apply the new CRDs out of band.** Server-side apply is required:
these CRDs exceed the client-side last-applied-configuration annotation limit,
and `--force-conflicts` takes ownership of the fields the previous install set.
The URLs are pinned to the release tag these docs describe, so the schema you
apply is the one the chart in step 2 expects:

--8<-- "integrations/kubernetes/docs/generated/_crd-apply.md"

**Step 2 — upgrade the release to the new chart version**, exactly the command
from [Upgrade with Helm](#upgrade-with-helm) above:

--8<-- "integrations/kubernetes/docs/generated/_upgrade-command.md"

The API version stays `v1alpha1` across the major bump and the stored version is
unchanged. Every existing `Pacto` resource therefore remains stored and readable
under the new CRD — no conversion webhook, no decode error — and the upgraded
operator reconciles it in place. That holds because **`spec` is additive**: v5 adds
`spec.target.configBindings` and `spec.target.interfaceBindings` and removes
nothing, so a contract resource written for v4 still validates unchanged.

### Status is redesigned, not extended

!!! warning "The upgrade drops v4 status observations on sight"
    `status` was redesigned, not extended: v5 removes 51 status paths and adds 35.
    `status.runtime`, `status.endpoints`, `status.ports`, `status.scaling`,
    `status.readiness.checks`, `status.contract.imageRef` and
    `status.summary.{passed,failed,total}` are gone, replaced by
    `status.findings`, `status.evaluationCoverage` and
    `status.summary.{errorCount,warningCount,infoCount,unknownCount}` — which is
    why the printer columns change from `PASSED`/`FAILED` to
    `ERRORS`/`WARNINGS`.

    The apiserver stops serving the removed fields the moment the new CRD
    lands. A `kubectl get pacto -o yaml` taken between step 1 and step 2
    therefore shows a resource whose `spec` is intact and whose `status` looks
    half-empty.
    That is the CRD, not data loss: the old values are still in etcd and are
    lost for good only once something writes `status` again. Do not read that
    window as a failed migration.

### Confirming the migration

Before and after the upgrade:

```bash
kubectl get crd pactos.pacto.trianalab.io \
  -o jsonpath='{.spec.versions[0].additionalPrinterColumns[*].name}'   # reflects the new schema
kubectl get pacto -A                                                    # existing resources still list + reconcile
```

If a future release ever ships an incompatible schema change, the server-side
apply or the resource read fails loudly rather than silently dropping resources.
See the [CRD reference](crd-reference.md) for the current field set.

This exact flow is exercised against a real cluster by
`tests/acceptance/kind/upgrade-v4-v5.sh`, the `upgrade` leg of CI's
`ci-e2e-kind` job. It installs the real v4 chart with its v4 CRDs, server-side
applies the new CRDs, then `helm upgrade`s to the current chart and asserts the
pre-existing resource survives and reconciles.

## Rolling back

**Within** a major, `helm rollback pacto-operator` reverts what the chart owns
and leaves your `Pacto` resources untouched; re-apply any
[hand-patched controller flags](#upgrade-with-helm) afterwards. **Across** a
major it is not that clean. Helm does not manage `crds/` in either direction, so
a rollback leaves the new CRD in place and the old controller writes v4-only
status fields the apiserver prunes while returning success. A real return to the
previous major means rolling the chart back *and* server-side applying the
previous release's CRDs, the mirror image of step 1.

## Turning on the Evidence Server during an upgrade

The Evidence Server arrived in chart 5.2.0 and is off by default, so an upgrade
changes nothing until you set `evidence.enabled`. Its requirements — a subject
list and a registry serving the native Referrers API — are on
[The Evidence Server](evidence-server.md). Turning it back off later removes the
whole footprint and loses nothing: every accepted record already lives in your
contract registry.

--8<-- "integrations/kubernetes/docs/generated/_compatibility.md"
