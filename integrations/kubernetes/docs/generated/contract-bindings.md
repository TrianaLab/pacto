<!--
  GENERATED FILE -- DO NOT EDIT.
  Produced by release/scripts/gen_integration_docs.py from config/crd/bases/pacto.trianalab.io_pactos.yaml (Pacto CRD binding fields).
  Regenerate with `make docs-generate`; drift is a CI failure (`make docs-check`).
-->

# Contract bindings

A `Pacto` resource points at a platform-agnostic contract (inline or in OCI) and binds it to concrete Kubernetes resources to observe. The binding fields below are generated from the `Pacto` CRD schema.

## Contract source (`spec.contractRef`)

| Field | Type | Required | Enum | Description |
| --- | --- | --- | --- | --- |
| `spec.contractRef` | `object` | yes |  | ContractRef specifies where to find the Pacto contract. |
| `spec.contractRef.inline` | `string` | no |  | Inline allows specifying the contract YAML directly (for testing/dev). |
| `spec.contractRef.oci` | `string` | no |  | OCI is the OCI registry reference for the contract bundle. Three forms are supported: - Unversioned (ghcr.io/org/service-pacto): tracks the latest semver tag. - Tagged (ghcr.io/org/service-pacto:1.2.3): pinned to that exact tag. - Digest (ghcr.io/org/service-pacto@sha256:...): immutable, exact reference. |
| `spec.contractRef.pullSecretRef` | `string` | no |  | PullSecretRef is the name of a Secret in the same namespace containing OCI registry credentials. Supported secret types: - Opaque with "token" key (bearer token) - Opaque with "username"+"password" keys (basic auth) - kubernetes.io/dockerconfigjson (standard Docker registry auth) For Opaque secrets, add a "registry" key (e.g. "ghcr.io") to bind the credentials to that host; the operator then refuses to send them to any other registry. Recommended to prevent a contract from redirecting a referenced secret to an attacker-controlled registry. |

## Runtime target (`spec.target`)

When `spec.target` is omitted the Pacto is reference-only: the operator parses and validates the contract but performs no runtime observation.

| Field | Type | Required | Enum | Description |
| --- | --- | --- | --- | --- |
| `spec.target` | `object` | no |  | Target specifies which Kubernetes resources to observe. When omitted, the Pacto acts as a reference-only contract (no runtime validation). |
| `spec.target.configBindings` | `[]object` | no |  | ConfigBindings maps contract configurations to the concrete ConfigMap/Secret backing each (B7). |
| `spec.target.configBindings[].configuration` | `string` | yes |  | Configuration is the contract configurations[].name this binding backs. |
| `spec.target.configBindings[].format` | `string` | no | `yaml`, `json` | Format of the value at Key. Required with Key for ConfigMap conformance. |
| `spec.target.configBindings[].key` | `string` | no |  | Key names the single ConfigMap/Secret key to decode and validate. Omit for existence-only. |
| `spec.target.configBindings[].kind` | `string` | yes | `ConfigMap`, `Secret` | Kind of the backing object. |
| `spec.target.configBindings[].name` | `string` | yes |  | Name of the backing ConfigMap/Secret in the Pacto's namespace. |
| `spec.target.interfaceBindings` | `[]object` | no |  | InterfaceBindings maps contract interfaces to the Service port that serves each (B4). Kubernetes deployment knowledge — lives on the CR, NOT the platform-agnostic contract. |
| `spec.target.interfaceBindings[].interface` | `string` | yes |  | Interface is the contract interfaces[].name this binding resolves. |
| `spec.target.interfaceBindings[].servicePort` | `object` | yes |  | ServicePort is the Service port name or number that serves the interface. |
| `spec.target.serviceName` | `string` | no |  | ServiceName is the name of the Kubernetes Service to observe. |
| `spec.target.workloadRef` | `object` | no |  | WorkloadRef identifies the workload (Deployment, StatefulSet, ReplicaSet, Job, or CronJob). If omitted, defaults to name=serviceName, kind=Deployment. |
| `spec.target.workloadRef.kind` | `string` | no | `Deployment`, `StatefulSet`, `ReplicaSet`, `Job`, `CronJob` | Kind of the workload resource. Left unspecified (empty) unless the author sets it explicitly; WORKLOAD_MISMATCH only fires when BOTH name AND kind were explicit (AR7). No default is applied here so the collector can distinguish "author asserted kind X" from "kind was defaulted for the GET". |
| `spec.target.workloadRef.name` | `string` | yes |  | Name of the workload resource. |

## Overrides (`spec.overrides`)

| Field | Type | Required | Enum | Description |
| --- | --- | --- | --- | --- |
| `spec.overrides` | `object` | no |  | Overrides specifies partial configuration overrides to apply on top of the resolved contract. This enables environment-specific tuning without duplicating the entire contract inline. Semantics mirror the Pacto CLI --set / -f override model. |
| `spec.overrides.configurations` | `[]object` | no |  | Configurations lists per-scope configuration value overrides. Each entry is matched by name to a configurations[] entry in the resolved contract. |
| `spec.overrides.configurations[].name` | `string` | yes |  | Name identifies the configuration scope to override (must match a configurations[] entry in the contract). |
| `spec.overrides.configurations[].values` | `object` | yes |  | Values contains the configuration key-value pairs to merge into the resolved contract. These values take precedence over the values declared in the contract. |

## Example

A `Pacto` binding an OCI contract to a Service, with an interface bound to a named Service port and a configuration bound to a ConfigMap:

```yaml
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata:
  name: orders-api
  namespace: shop
spec:
  contractRef:
    oci: ghcr.io/acme/orders-api-pacto:1.2.0
  target:
    serviceName: orders-api
    interfaceBindings:
      - interface: http
        servicePort: http
    configBindings:
      - configuration: default
        kind: ConfigMap
        name: orders-api-config
        key: app.yaml
        format: yaml
```

## Choosing a reference form

The three `contractRef.oci` forms are not three styles. They decide what the binding *means* over time, and the operator records which one it inferred in `status.resolutionPolicy` -- `PinnedDigest`, `PinnedTag` or `Latest`.

- **Digest** (`...@sha256:<digest>`) is the only form whose meaning cannot change. Every reconcile re-reads the same bytes, so a compliance verdict is reproducible: what the operator asserted last Tuesday is what it asserts today. The cost is real -- publishing a contract revision becomes a change to the `Pacto` resource too.
- **Tag** (`...:1.2.3`) is a name you control, not an immutable one. If someone force-pushes that tag the operator notices rather than drifting silently: it records a new `PactoRevision` and emits a `TagOverwritten` Warning Event naming the old and new digests. It does not refuse the new content. Choose a tag when you want a promotable pointer and will treat that Event as a signal.
- **Unversioned** (`ghcr.io/org/service-pacto`) re-resolves the highest semver tag on every reconcile -- every `spec.checkIntervalSeconds`, 300 by default. It fits an environment whose job is to run whatever is newest, such as a staging namespace or a preview cluster. It fits production badly, because what is being asserted changes without anyone deciding to change it.

With no other constraint: digest in production, unversioned in staging. The middle form is for teams that already run a tag-promotion discipline.

This page covers the binding fields only. The rest of `spec` -- including `checkIntervalSeconds`, which sets how often the operator re-checks compliance (default `300`) -- is in the [CRD reference](crd-reference.md).
