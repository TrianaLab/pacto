# Contract sections

The fields that name a service, say who owns it and list what it exposes:
`pactoVersion`, `service`, `interfaces`, `capabilities`, `metadata` and
`extensions`. Configuration and policy are in [Configuration and policy](configuration-and-policy.md);
dependencies and state are in
[Dependencies and state](dependencies-and-state.md); readiness is in
[Readiness](readiness.md).

## `pactoVersion`

The contract specification version: `"2.0"` or `"2.1"`. 2.1 adds
[`publish`](publish.md), [`checks`](checks.md), external dependencies and the `mcp` interface type and
nothing else, so a 2.0 contract keeps working unchanged.

```yaml
pactoVersion: "2.0"
```

The version picks the JSON Schema the contract is validated against:
[`pacto-v2.0.schema.json`](https://github.com/TrianaLab/pacto/blob/main/pkg/validation/schema/pacto-v2.0.schema.json)
or
[`pacto-v2.1.schema.json`](https://github.com/TrianaLab/pacto/blob/main/pkg/validation/schema/pacto-v2.1.schema.json).
Any other value is a hard error: the contract fails to load before validation
runs and reports `PARSE_ERROR` (`unsupported pactoVersion "3.0"; supported: 2.0,
2.1`). See [Validation layers](validation.md).

---

## `service`

Identifies the service.

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `name` | string | Yes | Pattern: `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$` |
| `version` | string | Yes | Parses as semver (e.g., `2.1.0`) — see the note below |
| `owner` | [OwnerInfo](#ownerinfo) | No | Object only (string form removed) |

`service` carries identity only — there is no `image` or `chart` field. How a
service is built and deployed is a delivery concern that lives outside the
contract.

!!! note "`version` is parsed leniently, so write it strictly"
    The check is "does [Masterminds/semver](https://github.com/Masterminds/semver)
    parse this", not "is this three dot-separated numbers". That parser fills in
    the parts you leave out and tolerates a leading `v`, so `1`, `0.1` and `v0.1`
    all validate and all mean `0.1.0`-style coerced versions. `1.2.3.4`, `abc`, a
    capital `V1.0.0` and anything with surrounding whitespace are rejected with
    `INVALID_SEMVER`.

    Nothing downstream re-checks the shape, so a coerced version is what gets
    published. `pacto push` tags the artifact with the literal string, and two
    contracts written `1` and `1.0.0` become two different tags of the same
    version. Write the full `MAJOR.MINOR.PATCH` and skip the `v`.

### OwnerInfo

Structured ownership metadata. All fields are optional but at least one must be present. Which field you set decides the canonical owner key the graph aggregates by — see [Ownership and the canonical owner key](../operational-graph-reference.md#ownership-and-the-canonical-owner-key).

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `team` | string | No | Team name |
| `dri` | string | No | Directly Responsible Individual |
| `contacts` | [OwnerContact](#ownercontact)[] | No | Contact points |

### OwnerContact

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `type` | string | Yes | One of: `email`, `url`, `group`, `oncall`, `chat`, `other` |
| `value` | string | Yes | Non-empty |
| `purpose` | string | No | One of: `ownership`, `support`, `escalation`, `oncall`, `notifications` |

**Examples:**

```yaml
# Structured form (object only — string form removed)
service:
  owner:
    team: foundations
    dri: eduardo.diaz
    contacts:
      - type: email
        value: foundations@acme.com
        purpose: ownership
      - type: chat
        value: "#foundations"

# Minimal object form (at least one field required)
service:
  owner:
    team: payments
```

---

## `interfaces`

Declares the service's communication boundaries. Optional — a service with no network interfaces (e.g. a batch job or shared library) may omit this section entirely. The `ref` field points at the spec you already publish — an OpenAPI document, an AsyncAPI document, a gRPC service descriptor or the MCP tools a service exposes — so Pacto references your existing interface rather than redefining it.

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `name` | string | Yes | Non-empty. Must be unique across interfaces |
| `type` | string | Yes | Enum: `openapi`, `asyncapi`, `grpc`, `mcp` (2.1) |
| `ref` | string | Yes | Non-empty. Path to the spec file within the bundle |
| `visibility` | string | No | Enum: `public`, `internal`. Default: `internal` |

### Interface types

| Value | Spec kind |
|---|---|
| `openapi` | OpenAPI document for HTTP/REST traffic |
| `asyncapi` | AsyncAPI document for event-driven communication |
| `grpc` | gRPC service descriptor |
| `mcp` | The tools a service such as an AI agent exposes, as an MCP `tools/list` result. See [an agent is a service](checks.md#an-agent-is-a-service) |

!!! note
    Interface names must be unique within a contract. Every interface requires a non-empty `ref` (`SCHEMA_VIOLATION` otherwise), and the referenced file must exist in the bundle (`FILE_NOT_FOUND` otherwise). A `.json`, `.yaml` or `.yml` ref must also parse (`INVALID_INTERFACE_SPEC` otherwise); any other extension — a gRPC `.proto`, say — carries no format this layer can check, so it is only checked for existence. There is no `port` field — ports are a deployment concern. Health and metrics endpoints are declared as [capabilities](#capabilities), not interfaces.

### Interfaces at runtime

The Kubernetes operator checks two fields of a running interface: `type` and
`visibility`.

- **`type`.** The operator sends a probe to the port and looks at the protocol
  that answers. `openapi` accepts HTTP/1 or HTTP/2 and `grpc` accepts HTTP/2.
  Anything else is `INTERFACE_PROTOCOL_MISMATCH`. A TLS port, no answer and
  `asyncapi` stay unverified.
- **`visibility`** (`internal` when omitted). It is contradicted only when the
  operator sees an address outside private networks: a public `externalIPs`
  entry, or a `LoadBalancer` Service (without an internal load balancer
  annotation) or an Ingress whose status carries a public IP or hostname. That
  is `INTERFACE_VISIBILITY_MISMATCH`. A `NodePort` Service, an internal or
  address-less load balancer and an HTTPRoute leave exposure unknown, so the
  field stays unverified. A `public` interface seen only in-cluster is not a
  contradiction either, because the operator may not see the gateway in front
  of it.

A field the operator could not check never changes the contract status. See
[Runtime conformance](../integrations/kubernetes/runtime-conformance.md).

---

## `capabilities`

Optional. Declares standard observability capabilities (`health`, `metrics`) or custom `extension` capabilities. Health and metrics endpoints are capabilities, not interfaces — this is where you tell the platform a service exposes them.

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `type` | string | Yes | Enum: `health`, `metrics`, `extension` |
| `ref` | string | Conditional | Required for `extension` only. A namespaced identifier (e.g. `example.com/custom`). Not allowed for `health`/`metrics` |
| `binding` | [Binding](#capability-binding) | No | Standard types only (`health`/`metrics`). Binds the endpoint to a declared interface. Not allowed for `extension` |

### Capability binding

Binds a standard capability endpoint (`health`/`metrics`) to a declared interface so a collector can probe it. `binding.type` is a **closed set — `http` is the only supported transport** this release; an unknown transport fails structural validation (the enum does not advertise unimplemented transports).

Semantics — how a binding resolves:

- `binding.type: http` means the capability is probed over **HTTP**.
- `binding.interface` names the declared interface that provides the **address/port anchor** for the probe — the platform-neutral counterpart of the Kubernetes CR's `spec.target.interfaceBindings[].interface`, which resolves to a concrete Kubernetes Service port. It does **not** claim the capability path is part of that interface's OpenAPI/AsyncAPI/gRPC specification.
- `binding.path` is relative to the resolved endpoint.

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `type` | string | Yes | Enum: `http` (only supported transport; any other value fails validation) |
| `interface` | string | Yes | Must match a declared `interfaces[].name` — the interface whose platform binding provides the address/port (`CAPABILITY_INTERFACE_UNKNOWN` otherwise) |
| `path` | string | No | Application path relative to the resolved endpoint. Must start with a single `/` and carry no scheme/host/fragment (`CAPABILITY_PATH_INVALID` otherwise) |

```yaml
capabilities:
  - type: health
    binding:
      type: http
      interface: rest-api
      path: /health
  - type: metrics
    binding:
      type: http
      interface: rest-api
      path: /metrics
  - type: extension
    ref: example.com/tracing
```

A `health` or `metrics` capability may be declared with no binding. That is a valid declaration for another collector to verify, but the current Kubernetes integration cannot actively verify an unbound capability and reports it as Unsupported/Unknown. An `extension` capability requires a namespaced `ref` and must not declare a binding. Pacto has no evaluator for an extension, so it is listed in `coverage.unverified` as `capabilities[ref=<ref>]` and never counts as a required assertion: a contract declaring one can still reach Compliant, and that verdict says nothing about the extension.

---

## `metadata`

Optional. Free-form key-value pairs for organizational use. Not validated beyond type.

```yaml
metadata:
  team: payments
  tier: critical
  on-call: "#payments-oncall"
```

`additionalProperties: false` — no extra fields allowed at any level (except inside `metadata`).

!!! tip
    `metadata` is a deliberate extension point for tooling. Platform teams use it to attach signals their CI or deployment systems can read off a contract — for example, infrastructure contracts can carry `metadata.labels` like `platform/provisioner: crossplane` to drive provisioning generically. See [Composition Patterns — Infrastructure contracts](../patterns/index.md#infrastructure-contracts).

---

## `extensions`

Optional. A free-form object for forward-compatible, namespaced extension data that is not part of the core contract model. Distinct from `metadata` (free-form organizational key-value pairs): `extensions` is reserved for structured data that future Pacto features or third-party tooling may interpret. Keys should be namespaced (e.g. a domain) to avoid collisions. Not validated beyond type this release.

```yaml
extensions:
  example.com/custom-tool:
    enabled: true
```
