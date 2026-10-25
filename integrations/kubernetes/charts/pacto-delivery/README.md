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
- RBAC: the promoter's `ServiceAccount`, `ClusterRole` and leader-election `Role`, plus two roles to bind: `<release>-agent` (propose and read operations, and cancel its own) and `<release>-approver` (the same, plus approvals)
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
| admissionPolicies | object | `{"allowUnguarded":false,"enabled":true}` | Install the ValidatingAdmissionPolicies (Kubernetes 1.30 or later): an operation's actor is the requesting user, only the promoter may set witnessed, override and unfreeze are limited to their raisers, cancel is refused once the pointer moved and an agent cancels only its own operations, approvals come from people and spec.ref.digest is edited only by the promoter or break-glass. The promoter admits no operation and counts no approval while its operation and approval policies are missing. |
| admissionPolicies.allowUnguarded | bool | `false` | Render without the policies (enabled=false) anyway. Without them anyone who may create an Operation or an Approval can claim to be anyone, so the chart refuses enabled=false unless this is set. |
| affinity | object | `{}` | Affinity for the promoter pod |
| argocd.enabled | bool | `false` | Let Stages name an Argo CD Application as their applier (`spec.applyRef.kind: Application`). Grants the promoter patch on `argoproj.io` Applications (it only sets `spec.source.targetRevision`, and the pointer admission policy holds it to that) and read access to the agent, approver and recorder roles. Off by default. |
| fullnameOverride | string | `""` | Override the full release name |
| gates.providers | list | `[]` | Health gate providers a Stage may name in `spec.gates.verify.checks[].provider`, each `{name, type, url}` with type `mira` (Mira's query API), `http` (any tool behind the plug-in format) or `eval` (an eval runner for an AI agent's eval suite). The URL is operator configuration: a Stage only names a provider and an agent names nothing. `pacto.contract` and `pacto.runtime` are built in. |
| identities.agentGroups | list | `["pacto:agents"]` | Groups whose members are agents. Agents may propose, never approve or unfreeze. Service accounts other than the promoter are always agents. |
| identities.breakGlassGroups | list | `["pacto:break-glass"]` | Groups that may raise an override and edit an OCIRepository's spec.ref.digest directly |
| identities.teamGroupPrefix | string | `""` | Only members of the teams a held question names may answer it. A contract owner team `T` is the identity-provider group `T`, or once this prefix is set only this prefix followed by `T` (for example `acme:`). A `system:` group is never a team. |
| image.pullPolicy | string | `"IfNotPresent"` | Image pull policy |
| image.repository | string | `"ghcr.io/trianalab/pacto/delivery"` | Promoter image repository (the pacto-delivery image) |
| image.tag | string | `""` | Overrides the image tag (default is the chart appVersion) |
| imagePullSecrets | list | `[]` | Image pull secrets for private registries |
| insecureRegistries | list | `[]` | Registry hosts (`host:port`) the promoter and the recorder reach over plain HTTP. An OCIRepository with `spec.insecure: true` is read over HTTP anyway. |
| nameOverride | string | `""` | Override the chart name |
| nodeSelector | object | `{}` | Node selector for the promoter pod |
| podSecurityContext.runAsNonRoot | bool | `true` | Run pod as non-root |
| podSecurityContext.seccompProfile.type | string | `"RuntimeDefault"` | Seccomp profile type |
| promoter.authFreshness | string | `"15m"` | How old a person's login may be when their approval is given; an older one is ignored (a Go duration) |
| promoter.leaderElect | bool | `true` | Elect one active promoter |
| promoter.poll | string | `"3s"` | How often an operation waiting on Flux, Pacto or a person is looked at (a Go duration) |
| promoter.registryCredentials.secretName | string | `""` | A `kubernetes.io/dockerconfigjson` Secret with read access to the contract and deploy repositories, for a private registry. Without it the promoter reads only public ones. |
| promoter.upstreamTrust | string | `""` | The recorder keys of other clusters, as a trust config in YAML (keys per stream glob). A Stage with `candidates.upstreamRecords` promotes only on records one of these keys signed for that stream; without it such a Stage is not Ready. Public keys only. |
| recorder.cdeventsSink | string | `""` | A URL the recorder POSTs a CDEvent (CloudEvents, structured mode) to for every record, best effort. Empty sends none. |
| recorder.enabled | bool | `false` | Run the recorder, which writes a signed record of every concluded operation to the registry as OCI referrers. Off by default. When on, the promoter leaves the record finalizer to it. The registry must support the OCI Referrers API natively (GHCR and released `distribution` do not). |
| recorder.extraEnv | list | `[]` | Extra environment for the recorder (for example the credentials a KMS client reads) |
| recorder.leaderElect | bool | `true` | Elect one active recorder |
| recorder.operationTTL | string | `"168h"` | Keep a recorded operation this long after it concluded, then delete it: its record holds what happened. The operation a Stage runs is never deleted. `0s` keeps them all (a Go duration). |
| recorder.poll | string | `"30s"` | How long a registry or signer failure backs off (a Go duration) |
| recorder.registryCredentials.secretName | string | `""` | A `kubernetes.io/dockerconfigjson` Secret with push access to the streams, apply and contract repositories. Only the recorder mounts it. |
| recorder.resources.limits.cpu | string | `"500m"` | CPU limit |
| recorder.resources.limits.memory | string | `"256Mi"` | Memory limit |
| recorder.resources.requests.cpu | string | `"10m"` | CPU request |
| recorder.resources.requests.memory | string | `"64Mi"` | Memory request |
| recorder.serviceAccount.annotations | object | `{}` | Annotations to add to the recorder's ServiceAccount (workload identity for KMS or keyless) |
| recorder.serviceAccount.create | bool | `true` | Create a ServiceAccount for the recorder |
| recorder.serviceAccount.name | string | `""` | Name override (defaults to fullname-recorder) |
| recorder.signing.ed25519.key | string | `"key.pem"` | The key in that Secret |
| recorder.signing.ed25519.secretName | string | `""` | A Secret holding a PKCS#8 PEM Ed25519 private key (for kind and tests) |
| recorder.signing.keyless.enabled | bool | `false` | Sign keyless with a Fulcio certificate for the recorder's workload identity, presented as a projected service account token for the `sigstore` audience. Rekor upload is off; a timestamp authority dates each signature. |
| recorder.signing.keyless.fulcioURL | string | `""` | The Fulcio that issues the certificate |
| recorder.signing.keyless.identity | string | `""` | The certificate identity keyless signing produces |
| recorder.signing.keyless.issuer | string | `""` | The OIDC issuer keyless signing uses |
| recorder.signing.keyless.timestampURL | string | `""` | The timestamp authority whose signed time a short-lived certificate verifies at |
| recorder.signing.keyless.trustedRoot | string | `""` | The Sigstore trusted root (`trusted_root.json`) naming that Fulcio and timestamp authority. Required with keyless: without it a self-signed certificate could name the recorder's identity. |
| recorder.signing.kms | string | `""` | A KMS key URI (`awskms://`, `gcpkms://`, `azurekms://`, `hashivault://`). The default image includes every provider; an image built with `--build-arg GO_TAGS=` has none, and one built with `GO_TAGS=kms_aws` (or `kms_gcp`, `kms_azure`, `kms_hashivault`) has only that one |
| recorder.streamsRepository | string | `""` | Repository (`host/path`) that holds one anchor per Stage stream. Required when enabled. |
| recorder.trust | string | `""` | Other keys whose records the recorder accepts, as a trust config in YAML (`keys: [{publicKey: <PEM>, streams: ["*/prod/*"]}]`): list the old key here while you rotate to a new one. The recorder's own key is always trusted. A record no listed key signed stops the stream. |
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
