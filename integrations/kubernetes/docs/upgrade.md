# Upgrade

The Kubernetes integration is versioned independently from Pacto core. The
operator image, Helm chart, Go module and this documentation set bump together as
one release group on their own cadence.

## Upgrade with Helm

For an upgrade that does not change the CRD schema, a plain `helm upgrade` is
enough:

```bash
helm upgrade pacto-operator \
  oci://ghcr.io/trianalab/pacto/charts/pacto-operator \
  --version 4.7.0 \
  --namespace pacto-operator-system
```

## Upgrading across a major version (CRD migration)

Helm never upgrades the CustomResourceDefinitions bundled under a chart's `crds/`
directory: it installs them on the first `helm install` and then leaves them
untouched on every `helm upgrade`. A major release that changes the CRD schema
therefore has one extra, ordered step — **apply the new CRDs before you run
`helm upgrade`**:

```bash
# 1. Apply the new CRDs out of band. Server-side apply is required: these CRDs are
#    larger than the client-side last-applied-configuration annotation limit, and
#    --force-conflicts takes ownership of the fields the previous chart install set.
kubectl apply --server-side --force-conflicts \
  -f https://raw.githubusercontent.com/TrianaLab/pacto/main/integrations/kubernetes/config/crd/bases/pacto.trianalab.io_pactos.yaml
kubectl apply --server-side --force-conflicts \
  -f https://raw.githubusercontent.com/TrianaLab/pacto/main/integrations/kubernetes/config/crd/bases/pacto.trianalab.io_pactorevisions.yaml

# 2. Upgrade the release to the new chart version.
helm upgrade pacto-operator \
  oci://ghcr.io/trianalab/pacto/charts/pacto-operator \
  --version 5.0.0 \
  --namespace pacto-operator-system
```

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
`tests/acceptance/kind/upgrade-v4-v5.sh` (the `ci-e2e-kind-upgrade` gate): it installs
the real previous-major (v4) chart with its v4 CRDs, server-side applies the new
CRDs, then `helm upgrade`s to the current chart and asserts the pre-existing
resource survives and reconciles.

## Upgrading an installation with the Evidence Server enabled

Evidence is no longer stored in the cluster. The Evidence Server publishes every
accepted record to your **contract registry** as an OCI 1.1 referrer, so the
component now installs nothing durable — no PVC, no data volume — and a fresh
install creates no storage resource at all.

Two things change on upgrade:

1. **Name your subjects.** `evidence.registry.subjects` is required whenever
   `evidence.enabled` is true: at least one exact
   `oci://<repo>@sha256:<digest>` contract revision. Upgrading without it fails
   at template time rather than starting a server that reports an authoritative
   empty world. The bucket values (`evidence.storage.*`) are gone.
2. **Your registry must serve the native Referrers API.** Pacto refuses the
   legacy referrers-tag fallback, so a registry without the endpoint leaves the
   Evidence Server permanently not-ready. CNCF distribution (`registry:2`,
   `registry:3`) does not qualify.

```bash
helm upgrade pacto-operator pacto/pacto-operator -n pacto-system \
  --set evidence.enabled=true \
  --set evidence.trust.existingSecret=pacto-evidence-trust \
  --set 'evidence.registry.subjects[0]=oci://ghcr.io/acme/checkout@sha256:<64 hex>'
```

An existing `pacto-evidence-data` PVC from an earlier release is **not** deleted
by the upgrade — Pacto will not destroy data it no longer manages. Records in it
are not visible to this release; producers re-report current state. Back it up if
you want the history, then retire it manually:
[retiring a legacy bucket or PVC](../../evidence-oci-storage.md#retiring-a-legacy-bucket-or-pvc).

## Compatibility

--8<-- "integrations/kubernetes/docs/generated/_compatibility.md"

## Documentation versions

The documentation site's version selector (top of every page) tracks Pacto core
releases. A Kubernetes-only release does not add a new core version to the selector;
it republishes the current core version in place with regenerated integration docs.
The compatibility table above always reflects the integration version the docs you
are reading describe. Pick a core version from the selector to read the docs that
shipped with that release.
