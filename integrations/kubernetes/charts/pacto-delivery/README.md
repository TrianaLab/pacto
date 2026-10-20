# pacto-delivery

![Version: 6.0.1](https://img.shields.io/badge/Version-6.0.1-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 6.0.1](https://img.shields.io/badge/AppVersion-6.0.1-informational?style=flat-square)

Opt-in Pacto delivery promoter that decides which version each environment runs

## Overview

This chart installs Pacto delivery, the opt-in part of Pacto that decides which
version each environment runs. Everything else Pacto ships (the CLI, the
operator, the dashboard and the Evidence Server) never changes what runs, and
installing them grants no right to. This chart is the only one that does, so it
is separate, with its own CRDs, service account, RBAC and admission policies.

What the chart installs:

- The promoter `Deployment` (the `pacto-delivery` image, subcommand `promoter`), leader-elected
- CRDs: `Stage`, `Operation` and `Approval` in `delivery.pacto.trianalab.io/v1alpha1` (from the `crds/` directory)
- RBAC: the promoter's `ServiceAccount`, `ClusterRole` and leader-election `Role`, plus two roles to bind: `<release>-agent` (propose, read and cancel operations) and `<release>-approver` (the same, plus approvals)
- `ValidatingAdmissionPolicy` objects (Kubernetes 1.30 or later, `admissionPolicies.enabled`)

It needs Flux (source and kustomize controllers) and the Pacto operator. The
promoter writes one field of one object it does not own: `spec.ref.digest` on
the `OCIRepository` a `Stage` names. Label that object
`delivery.pacto.trianalab.io/apply-ref` so admission keeps everyone else off it.

See [Pacto delivery](https://pacto.run/latest/integrations/kubernetes/delivery/).

## Installation

```bash
helm install pacto-delivery oci://ghcr.io/trianalab/pacto/charts/pacto-delivery \
  --version 6.0.1 \
  --namespace pacto-delivery --create-namespace
```

The chart version tracks the operator version, and the default image is
`ghcr.io/trianalab/pacto/delivery` at the chart's `appVersion`. Install the
same version as your Pacto operator.

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| admissionPolicies | object | `{"enabled":true}` | Install the ValidatingAdmissionPolicies (Kubernetes 1.30 or later): an operation's actor is the requesting user, only the promoter may set witnessed, override and unfreeze are limited to their raisers, cancel is refused once the pointer moved, approvals come from people and spec.ref.digest is edited only by the promoter or break-glass. |
| affinity | object | `{}` | Affinity for the promoter pod |
| fullnameOverride | string | `""` | Override the full release name |
| gates.providers | list | `[]` | Health gate providers a Stage may name in `spec.gates.verify.checks[].provider`, each `{name, type, url}` with type `mira` (Mira's query API) or `http` (any tool behind the plug-in format). The URL is operator configuration: a Stage only names a provider and an agent names nothing. `pacto.contract` and `pacto.runtime` are built in. |
| identities.agentGroups | list | `["pacto:agents"]` | Groups whose members are agents. Agents may propose, never approve or unfreeze. Service accounts other than the promoter are always agents. |
| identities.breakGlassGroups | list | `["pacto:break-glass"]` | Groups that may raise an override and edit an OCIRepository's spec.ref.digest directly |
| identities.teamGroupPrefix | string | `""` | Only members of the teams a held question names may answer it. A contract owner team `T` is the identity-provider group `T`, or this prefix followed by `T` (for example `acme:`). |
| image.pullPolicy | string | `"IfNotPresent"` | Image pull policy |
| image.repository | string | `"ghcr.io/trianalab/pacto/delivery"` | Promoter image repository (the pacto-delivery image) |
| image.tag | string | `""` | Overrides the image tag (default is the chart appVersion) |
| imagePullSecrets | list | `[]` | Image pull secrets for private registries |
| insecureRegistries | list | `[]` | Registry hosts (`host:port`) the promoter reaches over plain HTTP when it reads contracts. An OCIRepository with `spec.insecure: true` is read over HTTP anyway. |
| nameOverride | string | `""` | Override the chart name |
| nodeSelector | object | `{}` | Node selector for the promoter pod |
| podSecurityContext.runAsNonRoot | bool | `true` | Run pod as non-root |
| podSecurityContext.seccompProfile.type | string | `"RuntimeDefault"` | Seccomp profile type |
| promoter.leaderElect | bool | `true` | Elect one active promoter |
| promoter.poll | string | `"3s"` | How often an operation waiting on Flux, Pacto or a person is looked at (a Go duration) |
| replicaCount | int | `1` | Number of promoter replicas. Only the elected leader acts. |
| resources.limits.cpu | string | `"500m"` | CPU limit |
| resources.limits.memory | string | `"256Mi"` | Memory limit |
| resources.requests.cpu | string | `"10m"` | CPU request |
| resources.requests.memory | string | `"64Mi"` | Memory request |
| securityContext.allowPrivilegeEscalation | bool | `false` | Disallow privilege escalation |
| securityContext.capabilities.drop | list | `["ALL"]` | Drop all capabilities |
| securityContext.readOnlyRootFilesystem | bool | `true` | Read-only root filesystem |
| securityContext.runAsNonRoot | bool | `true` | Run as non-root |
| securityContext.runAsUser | int | `65532` | Run as UID 65532 (nonroot in distroless) |
| serviceAccount.annotations | object | `{}` | Annotations to add to the ServiceAccount |
| serviceAccount.create | bool | `true` | Create a ServiceAccount for the promoter |
| serviceAccount.name | string | `""` | Name override (defaults to fullname) |
| tolerations | list | `[]` | Tolerations for the promoter pod |

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| edu-diaz | <edudiazasencio@gmail.com> | <https://edudiaz.dev> |
