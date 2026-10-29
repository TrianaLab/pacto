# Contract Reference (v2.2)

A Pacto contract is one YAML file, `pacto.yaml`, that says what a service is and
what it needs to run: its interfaces, dependencies, configuration, capabilities
and readiness. This section is the field-by-field reference. The smallest valid
contract is four lines:

```yaml
pactoVersion: "2.0"
service:
  name: my-library
  version: 1.0.0
```

The fields are split across [contract sections](sections.md), [configuration and policy](configuration-and-policy.md), [dependencies and state](dependencies-and-state.md), [readiness](readiness.md), [publish](publish.md) and [checks](checks.md). The rules are in [validation layers](validation.md), [overrides](overrides.md) and [change classification](diff.md). This page covers the bundle layout all of them assume.

Each `interfaces`, `configurations` and `policies` entry points at a schema you already have: an OpenAPI document, an AsyncAPI event definition, a gRPC service descriptor or a JSON Schema. Every referenced file must parse as JSON or YAML; see [interface types](sections.md#interface-types).

[The model](../model.md#declaration-versus-observation) explains what the
contract states and what it leaves to the platform.

---

The canonical JSON Schemas are
[`pacto-v2.0.schema.json`](https://github.com/TrianaLab/pacto/blob/main/pkg/validation/schema/pacto-v2.0.schema.json)
,
[`pacto-v2.1.schema.json`](https://github.com/TrianaLab/pacto/blob/main/pkg/validation/schema/pacto-v2.1.schema.json),
which adds `publish`, `checks`, external dependencies and the `mcp` interface type,
and
[`pacto-v2.2.schema.json`](https://github.com/TrianaLab/pacto/blob/main/pkg/validation/schema/pacto-v2.2.schema.json),
which adds the interface lifecycle. Every contract declares [`pactoVersion`](sections.md#pactoversion) and is validated against that version's schema.

---

## Bundle structure

A Pacto bundle is a self-contained directory (or OCI artifact) with the following layout:

```text
/
├── pacto.yaml
├── interfaces/              ← optional
│   ├── openapi.yaml
│   ├── service.yaml
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

Only `pacto.yaml` is required. All other directories are optional. Include a directory when your contract references files in it. Validation checks that every file referenced by `pacto.yaml` exists within the bundle.

When you run `pacto push`, Pacto packages the bundle as a versioned, content-addressed OCI artifact. You can store it in any OCI registry and pull it from any team, service or environment.

### `docs/`: optional documentation

Files in `docs/` are **informational metadata**. They travel with the contract and are ignored by validation and diff classification.

Typical content:

- Service overview: what the service does and its purpose within the platform
- Architecture notes: internal design, data flow and key dependencies
- Operational runbooks: incident response, scaling procedures and known failure modes
- Integration guides: how consumers use the service's interfaces

### `sbom/`: optional Software Bill of Materials

Files in `sbom/` are also **informational metadata**. They travel with the contract and are ignored by validation.

`pacto diff` includes SBOM files in its output. When both the old and new bundles contain an SBOM, `pacto diff` reports package-level changes (added, removed, version, license or supplier modified). These changes are informational and leave the overall diff classification unchanged.

**Supported formats:**

| Format | File extension | Spec version |
|--------|---------------|--------------|
| [SPDX](https://spdx.dev/) | `.spdx.json` | 2.3 |
| [CycloneDX](https://cyclonedx.org/) | `.cdx.json` | 1.5 |

Pacto auto-detects the format by file extension. Place one or more SBOM files directly in the `sbom/` directory. Pacto reads only the top level of `sbom/`.

**Generating an SBOM:**

Use any standard tool ([Syft](https://github.com/anchore/syft), [Trivy](https://github.com/aquasecurity/trivy), [cdxgen](https://github.com/CycloneDX/cdxgen)) writing SPDX/CycloneDX JSON into `sbom/`.

---

## Minimal contract

Only `pactoVersion` and `service` are required, as in the four-line example at the top of this page. All other sections are optional: `workload`, `state`, `interfaces`, `configurations`, `policies`, `dependencies`, `capabilities`, `readiness`, `metadata` and `extensions`. A minimal contract is useful for lightweight dependency declarations, shared libraries or contracts where runtime semantics are managed externally.

---
