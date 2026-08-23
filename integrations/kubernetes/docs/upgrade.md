# Upgrade

The Kubernetes integration is versioned independently from Pacto core. The
operator image, Helm chart, Go module and this documentation set bump together as
one release group on their own cadence.

## Upgrade with Helm

For an upgrade that does not change the CRD schema, a plain `helm upgrade` is
enough. The version below is the currently published chart:

--8<-- "integrations/kubernetes/docs/generated/_upgrade-command.md"

!!! warning "An upgrade reverts hand-patched controller flags"
    If you turned on an opt-in feature by patching the Deployment's `args` —
    `--enable-probing`, `--enable-metrics-observation` or
    `--interface-name-match-discovery`, none of which the chart can render —
    `helm upgrade` re-renders `args` from the template and your addition
    disappears, silently. Nothing fails; the dimension simply goes back to
    `Unsupported` or to passive observation. Re-apply the patch after every
    upgrade, and see
    [Limitations — opt-in features](limitations.md#opt-in-features) for why it
    is not managed state.

## Upgrading across a major version (CRD migration)

Helm never upgrades the CustomResourceDefinitions bundled under a chart's `crds/`
directory: it installs them on the first `helm install` and then leaves them
untouched on every `helm upgrade`. A major release that changes the CRD schema
therefore has one extra, ordered step — **apply the new CRDs before you run
`helm upgrade`**:

**Step 1 — apply the new CRDs out of band.** Server-side apply is required: these
CRDs are larger than the client-side last-applied-configuration annotation limit,
and `--force-conflicts` takes ownership of the fields the previous chart install
set. The URLs are pinned to the release tag these docs describe, so the schema you
apply is the one the chart in step 2 expects — not whatever is on the default
branch today:

--8<-- "integrations/kubernetes/docs/generated/_crd-apply.md"

**Step 2 — upgrade the release to the new chart version**, exactly the command
from [Upgrade with Helm](#upgrade-with-helm) above:

--8<-- "integrations/kubernetes/docs/generated/_upgrade-command.md"

The API version stays `v1alpha1` across the major bump and the stored version is
unchanged, so every existing `Pacto` resource remains stored and readable under
the new CRD — no conversion webhook, no decode error — and the upgraded operator
reconciles it in place. That holds because **`spec` is additive**: v5 adds
`spec.target.configBindings` and `spec.target.interfaceBindings` and removes
nothing, so a contract resource written for v4 still validates unchanged.

`status` is a different story, and it is the one that surprises people.

!!! warning "The upgrade drops v4 status observations on sight"
    `status` was redesigned, not extended: v5 removes 51 status paths and adds 35.
    `status.runtime`, `status.endpoints`, `status.ports`, `status.scaling`,
    `status.readiness.checks`, `status.contract.imageRef` and
    `status.summary.{passed,failed,total}` are gone, replaced by
    `status.findings`, `status.evaluationCoverage` and
    `status.summary.{errorCount,warningCount,infoCount,unknownCount}` — which is
    why the printer columns change from `PASSED`/`FAILED` to
    `ERRORS`/`WARNINGS`.

    The apiserver stops serving the removed fields the moment the new CRD lands,
    before the new operator has reconciled anything, so a `kubectl get pacto -o
    yaml` taken between step 1 and step 2 shows a resource whose `spec` is intact
    and whose `status` looks half-empty. That is the CRD, not data loss: the old
    values are still in etcd and reappear if you put the old CRD back. They are
    lost for good only once something writes `status` again. Nothing you need to
    do — just do not read that window as a failed migration.

Confirm the migration before and after:

```bash
kubectl get crd pactos.pacto.trianalab.io \
  -o jsonpath='{.spec.versions[0].additionalPrinterColumns[*].name}'   # reflects the new schema
kubectl get pacto -A                                                    # existing resources still list + reconcile
```

If a future release ever ships an incompatible schema change, the server-side
apply or the resource read fails loudly rather than silently dropping resources —
resolve it before running `helm upgrade`. See the [CRD reference](crd-reference.md)
for the current field set.

This exact flow is exercised end to end against a real cluster by
`tests/acceptance/kind/upgrade-v4-v5.sh` (the `upgrade` leg of CI's `ci-e2e-kind`
job): it installs the real previous-major (v4) chart with its v4 CRDs,
server-side applies the new CRDs, then `helm upgrade`s to the current chart and
asserts the pre-existing resource survives and reconciles.

## Rolling back

`helm rollback pacto-operator` reverts what the chart owns — the Deployment, the
ServiceAccount, the RBAC, the Services — and that is the whole story **within** a
major. Your `Pacto` resources are untouched: the operator holds no state of its
own, and the `PactoRevision` objects it created are owned by the `Pacto` they
belong to, so nothing is orphaned and nothing is re-derived from scratch. Re-apply
any [hand-patched controller flags](#upgrade-with-helm) afterwards; a rollback
re-renders `args` from the template exactly as an upgrade does.

Rolling back **across** a major is not that clean, and the reason is the same
`crds/` rule that made the upgrade a two-step: Helm does not manage those CRDs in
either direction, so `helm rollback` leaves the **new** CRD in place and gives you
the old controller running against the new schema. The write path fails silently
there — the apiserver accepts a status update carrying v4-only fields, prunes
every one of them and returns success, so the old operator reports healthy writes
that never land, and `kubectl get pacto` stays blank in the columns you are
watching.

So decide which half you actually need:

- **The old controller image, current schema** — `helm rollback` alone gets you
  there, and it is the wrong place to stay. Treat it as a way to stop a bad
  rollout, not as a supported configuration.
- **A real return to the previous major** — roll the chart back *and* server-side
  apply the previous release's CRDs, the mirror image of step 1. Every field only
  the newer schema defines stops being served at that moment, and the values the
  newer operator wrote are lost as soon as the older one writes `status` again.
  `spec` is unaffected either way, so no contract resource needs re-creating.

Neither direction touches published contracts or evidence: both live in your
registry, not in the cluster.

## Turning on the Evidence Server during an upgrade

The Evidence Server arrived in chart 5.2.0 and is off by default, so upgrading
from an earlier release changes nothing until you set `evidence.enabled`. No
published chart before 5.2.0 had an `evidence` section at all — there is no
bucket value, PVC or storage class to migrate from. Enabling it installs nothing
durable either: every accepted record is published to your **contract registry**
as an OCI 1.1 referrer, and the registry is the store.

Two things are required the first time you enable it:

1. **Name your subjects.** `evidence.registry.subjects` is required whenever
   `evidence.enabled` is true: at least one exact
   `oci://<repo>@sha256:<digest>` contract revision. Upgrading without it fails
   at template time rather than starting a server that reports an authoritative
   empty world.
2. **Your registry must serve the native Referrers API.** Pacto refuses the
   legacy referrers-tag fallback, so a registry without the endpoint leaves the
   Evidence Server permanently not-ready. Neither GHCR nor CNCF distribution
   (`registry:2`, `registry:3`) qualifies — see [Evidence in
   OCI](../../evidence-oci-storage.md) for what was checked.

```bash
helm upgrade pacto-operator \
  oci://ghcr.io/trianalab/pacto/charts/pacto-operator \
  --namespace pacto-operator-system \
  --set evidence.enabled=true \
  --set evidence.trust.existingSecret=pacto-evidence-trust \
  --set 'evidence.registry.subjects[0]=oci://registry.example.com/acme/checkout@sha256:<64 hex>'
```

Turning it back off later removes the whole footprint and loses nothing: the
records stay in the registry where they were written.

--8<-- "integrations/kubernetes/docs/generated/_compatibility.md"
