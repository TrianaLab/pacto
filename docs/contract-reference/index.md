# Contract Reference (v2.0)
A Pacto contract is a YAML file (`pacto.yaml`) that describes a service's operational interface — interfaces, dependencies, runtime behavior, configuration, capabilities and readiness. This page covers every section, field, validation rule and change classification rule.

Each `interfaces`, `configurations` and `policies` entry points at a schema you already have — an OpenAPI document, an AsyncAPI event definition, a gRPC service descriptor, a JSON Schema — so a contract composes the interfaces you already own rather than inventing a new configuration language. Every referenced file must parse as JSON or YAML; see [interface types](sections.md#interface-types). On top of that it adds what no single schema can express: ownership, dependencies, compatibility, readiness and how they change over time.

Every section below contributes one piece of a service's machine-readable operational meaning. The contract states stable *intent* — what the service is, independent of any orchestrator. What deliberately stays **outside** the contract: how the service is built, scheduled, scaled and wired (delivery concerns owned by the platform — which is why there is no port, scaling, image or lifecycle field), and what the service looks like at runtime (an observation, gathered as [evidence](../architecture.md#declaration-versus-observation) and evaluated against the contract, never baked into the declaration). This separation is what lets one contract be validated at authoring time, diffed in CI and verified against a running system without depending on how any of those systems work.

---

The canonical JSON Schema is
[`schema/pacto-v2.0.schema.json`](https://github.com/TrianaLab/pacto/blob/main/pkg/validation/schema/pacto-v2.0.schema.json).
It is the single tracked schema and applies to every contract, which must declare [`pactoVersion: "2.0"`](sections.md#pactoversion).

---

## Bundle structure

A Pacto bundle is a self-contained directory (or OCI artifact) with the following layout:

```
/
├── pacto.yaml
├── interfaces/              ← optional
│   ├── openapi.yaml
│   ├── service.grpc.yaml
│   └── events.yaml
├── configuration/           ← optional
│   └── schema.json
├── policy/                  ← optional
│   └── schema.json
├── docs/                    ← optional
│   ├── README.md
│   ├── architecture.md
│   ├── runbook.md
│   └── integration.md
└── sbom/                    ← optional
    └── sbom.spdx.json
```

Only `pacto.yaml` is required. All other directories are optional — include them when your contract references files in them. Validation enforces that every file referenced by `pacto.yaml` exists within the bundle.

When you run `pacto push`, the bundle is packaged as an OCI artifact — versioned, content-addressed, and distributable through any OCI registry. This is how contracts travel between teams, services, and environments.

### `docs/` — Optional documentation

The `docs/` directory is an optional convention for including human-readable documentation alongside the contract. Its contents are treated as **informational metadata** — they travel with the contract but have no effect on contract semantics, validation, or diff classification.

**Key properties:**

- **Optional.** Bundles without `docs/` are fully valid. No existing workflow breaks.
- **No contract semantics.** Documentation is not part of the contract. It does not influence validation, diffing, or compatibility checks.
- **Self-contained.** Documentation lives inside the OCI artifact, so it is versioned, distributed, and cached alongside the contract it describes.
- **Format-flexible.** Markdown is the natural default, but there are no restrictions on file formats inside `docs/`.

**What documentation could include:**

- Service overview — what the service does, its purpose within the platform
- Architecture notes — internal design, data flow, key dependencies
- Operational runbooks — incident response, scaling procedures, known failure modes
- Integration guides — how consumers should interact with the service's interfaces

### `sbom/` — Optional Software Bill of Materials

The `sbom/` directory is an optional convention for including a Software Bill of Materials alongside the contract. Like `docs/`, its contents are treated as **informational metadata** — they travel with the contract but have no effect on contract semantics or validation.

Unlike `docs/`, SBOM files **are included in diff output**. When both the old and new bundles contain an SBOM, `pacto diff` reports package-level changes (added, removed, version, license or supplier modified). These changes are informational only — they never affect the overall diff classification.

**Supported formats:**

| Format | File extension | Spec version |
|--------|---------------|--------------|
| [SPDX](https://spdx.dev/) | `.spdx.json` | 2.3 |
| [CycloneDX](https://cyclonedx.org/) | `.cdx.json` | 1.5 |

Pacto auto-detects the format by file extension. Place one or more SBOM files directly in the `sbom/` directory (subdirectories are not scanned).

**Key properties:**

- **Optional.** Bundles without `sbom/` are fully valid.
- **Convention-based.** No contract-level field references the SBOM — Pacto discovers it automatically, just like `docs/`.
- **Informational diffing.** `pacto diff` reports SBOM package changes but they don't affect breaking/non-breaking classification.
- **Self-contained.** The SBOM lives inside the OCI artifact, versioned and distributed alongside the contract.

**Generating an SBOM:**

Generate with any standard tool ([Syft](https://github.com/anchore/syft), [Trivy](https://github.com/aquasecurity/trivy), [cdxgen](https://github.com/CycloneDX/cdxgen)) writing SPDX/CycloneDX JSON into `sbom/`.

---

## Full example

```yaml
pactoVersion: "2.0"

service:
  name: payments-api
  version: 2.1.0
  owner:
    team: payments
    dri: alice

interfaces:
  - name: rest-api
    type: openapi
    ref: interfaces/openapi.yaml
    visibility: public

  - name: grpc-api
    type: grpc
    ref: interfaces/service.yaml
    visibility: internal

  - name: order-events
    type: asyncapi
    ref: interfaces/events.yaml
    visibility: internal

configurations:
  - name: default
    schema: configuration/schema.json
    required: true

policies:
  - name: platform-policy
    schema: policy/schema.json

dependencies:
  - name: auth
    ref: oci://ghcr.io/acme/auth-pacto@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
    required: true
    compatibility: "^2.0.0"

  - name: notifications
    ref: oci://ghcr.io/acme/notifications-pacto:1.0.0
    required: false
    compatibility: "~1.0.0"

workload: service

state:
  type: stateful
  persistence:
    scope: shared
    durability: persistent
  dataCriticality: high

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

metadata:
  team: payments
  tier: critical
```

---

## Minimal contract

Only `pactoVersion` and `service` are required. All other sections — `interfaces`, `configurations`, `policies`, `dependencies`, `workload`, `state`, `capabilities`, `readiness`, `verification`, and `metadata` — are optional:

```yaml
pactoVersion: "2.0"

service:
  name: my-library
  version: 1.0.0
```

This is useful for lightweight dependency declarations, shared libraries, or contracts where runtime semantics are managed externally.

---

