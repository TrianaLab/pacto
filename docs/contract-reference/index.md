# Contract Reference (v1.2)
A Pacto contract is a YAML file (`pacto.yaml`) that describes a service's operational interface — interfaces, dependencies, runtime behavior, configuration, scaling and readiness. This page covers every section, field, validation rule and change classification rule.

Each `interfaces`, `configurations` and `policies` entry points at a schema you already have — an OpenAPI spec, a protobuf or event definition, a JSON Schema — so a contract composes the interfaces you already own rather than inventing a new configuration language. On top of that it adds what no single schema can express: ownership, dependencies, compatibility, readiness and how they change over time.

---

The canonical JSON Schemas are available per version:
[`schema/pacto-v1.0.schema.json`](https://github.com/TrianaLab/pacto/blob/main/pkg/validation/schema/pacto-v1.0.schema.json),
[`schema/pacto-v1.1.schema.json`](https://github.com/TrianaLab/pacto/blob/main/pkg/validation/schema/pacto-v1.1.schema.json) and
[`schema/pacto-v1.2.schema.json`](https://github.com/TrianaLab/pacto/blob/main/pkg/validation/schema/pacto-v1.2.schema.json).
The schema is selected by the contract's [`pactoVersion`](sections.md#pactoversion).

---

## Bundle structure

A Pacto bundle is a self-contained directory (or OCI artifact) with the following layout:

```
/
├── pacto.yaml
├── interfaces/              ← optional
│   ├── openapi.yaml
│   ├── service.proto
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
pactoVersion: "1.2"

service:
  name: payments-api
  version: 2.1.0
  owner:
    team: payments
    dri: alice
  image:
    ref: ghcr.io/acme/payments-api:2.1.0
    private: true
  chart:
    ref: oci://ghcr.io/acme/payments-chart
    version: 2.1.0

interfaces:
  - name: rest-api
    type: http
    port: 8080
    visibility: public
    contract: interfaces/openapi.yaml

  - name: grpc-api
    type: grpc
    port: 9090
    visibility: internal
    contract: interfaces/service.proto

  - name: order-events
    type: event
    visibility: internal
    contract: interfaces/events.yaml

configurations:
  - name: default
    schema: configuration/schema.json

policies:
  - name: platform-policy
    ref: oci://ghcr.io/acme/platform-policy-pacto:1.0.0

dependencies:
  - name: auth
    ref: oci://ghcr.io/acme/auth-pacto@sha256:abc123def456
    required: true
    compatibility: "^2.0.0"

  - name: notifications
    ref: oci://ghcr.io/acme/notifications-pacto:1.0.0
    required: false
    compatibility: "~1.0.0"

runtime:
  workload: service

  state:
    type: stateful
    persistence:
      scope: local
      durability: persistent
    dataCriticality: high

  lifecycle:
    upgradeStrategy: ordered
    gracefulShutdownSeconds: 30

  health:
    interface: rest-api
    path: /health
    initialDelaySeconds: 15

  metrics:
    interface: rest-api
    path: /metrics

scaling:
  min: 2
  max: 10

metadata:
  team: payments
  tier: critical
```

---

## Minimal contract

Only `pactoVersion` and `service` are required. All other sections — `interfaces`, `runtime`, `configurations`, `policies`, `dependencies`, `scaling`, and `metadata` — are optional:

```yaml
pactoVersion: "1.0"

service:
  name: my-library
  version: 1.0.0
```

This is useful for lightweight dependency declarations, shared libraries, or contracts where runtime semantics are managed externally.

---

