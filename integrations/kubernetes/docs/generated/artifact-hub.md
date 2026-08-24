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
| Controller image | oci-image | `ghcr.io/trianalab/pacto/operator` | `5.2.3` |
| Helm chart | helm-chart | `ghcr.io/trianalab/pacto/charts/pacto-operator` | `5.2.3` |
| Go module | go-module | `github.com/trianalab/pacto/integrations/kubernetes/v5` | `5.2.3` |
| Documentation | docs | `mkdocs:integrations/kubernetes` | `5.2.3` |

## Verify a published artifact

The controller image and the Helm chart are signed keylessly by the release workflow through GitHub's OIDC issuer. Verify either before installing it:

```bash
cosign verify \
  --certificate-identity-regexp '^https://github\.com/TrianaLab/pacto/\.github/workflows/release\.yml@' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/trianalab/pacto/operator:5.2.3
```

Anything other than a successful verification -- including `no signatures found` -- means do not deploy it. Not every Pacto artifact is signed; see [what is signed and what is not](../../installation.md#supply-chain-what-is-signed-and-what-is-not).

## Artifact Hub repository

- **Repository ID**: `4d7aef48-84d5-447f-bd73-8590a6801d0e`
- **Chart image annotation** (`artifacthub.io/images`, from `Chart.yaml`):

```yaml
- name: pacto-controller
  image: ghcr.io/trianalab/pacto/operator:5.2.3
```

## Install from the published chart

```bash
helm install pacto-operator \
  oci://ghcr.io/trianalab/pacto/charts/pacto-operator \
  --version 5.2.3 \
  --namespace pacto-operator-system --create-namespace
```
