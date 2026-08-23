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

The API version stays `v1alpha1` across the major bump and schema changes are
additive, so the stored version is unchanged: every existing `Pacto` resource
remains stored and readable under the new CRD (no conversion webhook, no decode
error) and the upgraded operator reconciles it in place. Confirm the migration
before and after:

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
