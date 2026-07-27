<!--
  GENERATED FILE -- DO NOT EDIT.
  Produced by release/scripts/gen_integration_docs.py from release/release-manifest.json + artifacthub-repo.yml + Chart.yaml.
  Regenerate with `make docs-generate`; drift is a CI failure (`make docs-check`).
-->

# Artifact Hub

Published artifact coordinates for the Kubernetes integration. All coordinates and versions are generated from `release/release-manifest.json` -- the single source of truth for what has been published.

## Artifact coordinates

| Artifact | Kind | Coordinate | Version |
| --- | --- | --- | --- |
| Controller image | oci-image | `ghcr.io/trianalab/pacto-operator/pacto-controller` | `4.7.0` |
| Helm chart | helm-chart | `ghcr.io/trianalab/pacto-operator/charts/pacto-operator` | `4.7.0` |
| Go module | go-module | `github.com/trianalab/pacto/integrations/kubernetes/v5` | `4.7.0` |
| Documentation | docs | `mkdocs:integrations/kubernetes` | `4.7.0` |

## Artifact Hub repository

- **Repository ID**: `4d7aef48-84d5-447f-bd73-8590a6801d0e`
- **Chart image annotation** (`artifacthub.io/images`, from `Chart.yaml`):

```yaml
- name: pacto-controller
  image: ghcr.io/trianalab/pacto-operator/pacto-controller:4.7.0
```

## Install from the published chart

```bash
helm install pacto-operator \
  oci://ghcr.io/trianalab/pacto-operator/charts/pacto-operator \
  --version 4.7.0 \
  --namespace pacto-operator-system --create-namespace
```
