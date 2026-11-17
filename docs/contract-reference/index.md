# Contract Reference (v2.5)

A Pacto contract is one YAML file, `pacto.yaml`, that says what a service is and
what it needs to run: its interfaces, dependencies, configuration, capabilities
and readiness. This section is the field-by-field reference. An empty
`pacto.yaml` is a valid contract; see [the smallest contract](#the-smallest-contract).

The fields are split across [contract sections](sections.md), [configuration and policy](configuration-and-policy.md), [dependencies and state](dependencies-and-state.md), [readiness](readiness.md), [publish](publish.md), [delivery](delivery.md), [checks](checks.md) and [environments](environments.md). The rules are in [validation layers](validation.md), [overrides](overrides.md) and [change classification](diff.md), and what Pacto infers beside the contract in the [inferred record](inferred-record.md). This page covers the bundle layout all of them assume.

Each `interfaces`, `configurations` and `policies` entry points at a schema you already have: an OpenAPI document, an AsyncAPI event definition, a gRPC service descriptor or a JSON Schema. Every referenced file must parse as JSON or YAML; see [interface types](sections.md#interface-types).

[The model](../model.md#declaration-versus-observation) explains what the
contract states and what it leaves to the platform.

---

The canonical JSON Schemas are
[`pacto-v2.0.schema.json`](https://github.com/TrianaLab/pacto/blob/main/pkg/validation/schema/pacto-v2.0.schema.json),
[`pacto-v2.1.schema.json`](https://github.com/TrianaLab/pacto/blob/main/pkg/validation/schema/pacto-v2.1.schema.json),
which adds `publish`, `checks`, external dependencies and the `mcp` interface type,
and
[`pacto-v2.2.schema.json`](https://github.com/TrianaLab/pacto/blob/main/pkg/validation/schema/pacto-v2.2.schema.json),
which adds the interface `lifecycle`, `compatibilityEpoch` and `guarantees` and
dependency `requires`, and
[`pacto-v2.3.schema.json`](https://github.com/TrianaLab/pacto/blob/main/pkg/validation/schema/pacto-v2.3.schema.json),
which adds `environments`, and
[`pacto-v2.4.schema.json`](https://github.com/TrianaLab/pacto/blob/main/pkg/validation/schema/pacto-v2.4.schema.json),
which adds `service.domain`, `service.offering`, `service.summary`,
`service.lifecycle`, `service.template` and `service.origin`, and `values` and a
`configuration` selector beside a configuration `ref`, and
[`pacto-v2.5.schema.json`](https://github.com/TrianaLab/pacto/blob/main/pkg/validation/schema/pacto-v2.5.schema.json),
the newest. A contract is validated against the schema of its [`pactoVersion`](sections.md#pactoversion), or of the newest version when it declares none.

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
├── environments/            ← optional
│   ├── staging.yaml
│   └── production.yaml
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

## The smallest contract

An empty `pacto.yaml` is a valid contract. One line names the service:

```yaml
service: {name: payments}
```

Pacto fills what the file leaves out and says where each value came from. An
empty `pacto.yaml` beside a chart named `payments` reads:

```console
$ pacto validate .
. is valid (pactoVersion 2.5 from this CLI, service.name payments from chart/Chart.yaml:2, service.version not declared)
```

An absent `pactoVersion` reads as the newest version the CLI supports. An
absent `service.name` is read from the first of these that names it:

1. `name` in a `Chart.yaml`
2. `metadata.name` in a `kustomization.yaml`, or the single service of a Compose file
3. the `org.opencontainers.image.title` label in a `Dockerfile`
4. the `app.kubernetes.io/name` label in a Kubernetes manifest
5. the folder name

A file `.pactoignore` leaves out is never read. When two folders end up with
the same name and at least one of them was inferred, the operational graph
drops the inferred ones and reports a `SERVICE_NAME_COLLISION` limitation, so
an inferred name never joins another service.

`pacto pack`, `pacto push` and `pacto publish` need a declared `service.name`
(`PUBLISH_NEEDS_SERVICE_NAME`) and `service.version`
(`PUBLISH_NEEDS_SERVICE_VERSION`), and write `pactoVersion` into the bundled
`pacto.yaml`. Every published contract declares all three. Every other section
is optional.

---
