# Upgrade

The Kubernetes integration is versioned independently from Pacto core. The
operator image, Helm chart, Go module and this documentation set bump together as
one release group on their own cadence.

## Upgrade with Helm

```bash
helm upgrade pacto-operator \
  oci://ghcr.io/trianalab/pacto-operator/charts/pacto-operator \
  --version 4.7.0 \
  --namespace pacto-operator-system
```

Helm does not upgrade CRDs bundled under a chart's `crds/` directory. When a
release changes the CRD schema, apply the new CRDs before upgrading the release:

```bash
kubectl apply -f https://raw.githubusercontent.com/TrianaLab/pacto/main/integrations/kubernetes/config/crd/bases/pacto.trianalab.io_pactos.yaml
kubectl apply -f https://raw.githubusercontent.com/TrianaLab/pacto/main/integrations/kubernetes/config/crd/bases/pacto.trianalab.io_pactorevisions.yaml
```

CRD changes in this integration are additive within the `v1alpha1` API version, so
existing `Pacto` resources keep working across upgrades. Check the
[CRD reference](crd-reference.md) for the current field set.

## Compatibility

--8<-- "integrations/kubernetes/docs/generated/_compatibility.md"

## Documentation versions

The documentation site's version selector (top of every page) tracks Pacto core
releases. A Kubernetes-only release does not add a new core version to the selector;
it republishes the current core version in place with regenerated integration docs.
The compatibility table above always reflects the integration version the docs you
are reading describe. Pick a core version from the selector to read the docs that
shipped with that release.
