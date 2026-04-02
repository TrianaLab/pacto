[![CI](https://github.com/TrianaLab/pacto/actions/workflows/ci.yml/badge.svg)](https://github.com/TrianaLab/pacto/actions/workflows/ci.yml)
[![PkgGoDev](https://pkg.go.dev/badge/github.com/trianalab/pacto)](https://pkg.go.dev/github.com/trianalab/pacto)
[![Go Report Card](https://goreportcard.com/badge/github.com/trianalab/pacto)](https://goreportcard.com/report/github.com/trianalab/pacto)
[![codecov](https://codecov.io/github/TrianaLab/pacto/graph/badge.svg?token=p3AJpP3BbO)](https://codecov.io/github/TrianaLab/pacto)
[![GitHub Release](https://img.shields.io/github/v/release/TrianaLab/pacto)](https://github.com/TrianaLab/pacto/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

# Pacto

**Pacto is to service operations what OpenAPI is to HTTP APIs.**

Pacto (/ˈpak.to/ — Spanish for *pact*) is a contract system for cloud-native services. You describe a service's operational behavior once — interfaces, dependencies, runtime semantics, configuration, scaling — and Pacto validates it, distributes it, verifies it at runtime, and lets humans explore it.

The system has three pieces that work together:

| Component | Role | When it runs |
|-----------|------|--------------|
| **CLI** | Author, validate, diff, publish contracts | Design-time and CI |
| **Dashboard** | Explore services, dependency graphs, versions, diffs, insights | Anytime — local or deployed |
| **[Operator](https://github.com/TrianaLab/pacto-operator)** | Track contracts in-cluster, link to workloads, verify runtime consistency | Continuously in Kubernetes |

No sidecars. No new infrastructure. The CLI uses your existing OCI registry. The operator watches CRDs. The dashboard reads from all sources.

**[Documentation](https://trianalab.github.io/pacto)** · **[Quickstart](https://trianalab.github.io/pacto/quickstart)** · **[Specification](https://trianalab.github.io/pacto/contract-reference)** · **[Examples](https://trianalab.github.io/pacto/examples)** · **[Demo](https://github.com/TrianaLab/pacto-demo)**

> **Why Pacto exists** — [MANIFEST.md](MANIFEST.md)

---

## The system

Pacto connects design-time authoring to runtime verification to human exploration:

```
CLI                        Operator                      Dashboard
 │                          │                             │
 ├─ define contracts        ├─ watch Pacto CRs            ├─ auto-detect sources
 ├─ validate (4 layers)     ├─ resolve OCI refs           │  (K8s, OCI, local, cache)
 ├─ diff versions           ├─ track versions             ├─ dependency graph
 ├─ publish to OCI          │  (PactoRevision per ver)    ├─ version history + diffs
 └─ resolve dep graphs      ├─ link to workloads          ├─ service details
                            └─ check runtime alignment    │  (interfaces, config, docs)
                               (ports, replicas, health)  ├─ runtime status
                                                          └─ compliance insights
```

The lifecycle:

```
1. Developer defines a pacto.yaml alongside their code
2. CLI validates and publishes it to an OCI registry
3. Operator discovers the contract in-cluster, tracks every version, checks runtime alignment
4. Dashboard merges all sources and lets humans explore the full contract graph
```

---

## What you get

- **One contract per service** — a single `pacto.yaml` captures interfaces, dependencies, runtime semantics, configuration, and scaling
- **Versioned OCI artifacts** — contracts are pushed to the same registries you already use for container images
- **Runtime state in Kubernetes** — the operator tracks every contract version and checks alignment against running workloads
- **Dependency graph + version history** — the dashboard visualizes relationships, diffs, and compliance across all services
- **Diffable operational changes** — breaking changes are classified and caught in CI before they reach production

---

## Breaking change detection

Someone changed a service — bumped the version, moved the port, removed an API endpoint, and dropped a config property. Pacto caught it before the merge:

| Classification | Path | Change | Old | New |
|---|---|---|---|---|
| NON_BREAKING | `service.version` | modified | `1.0.0` | `2.0.0` |
| BREAKING | `interfaces.port` | modified | `8081` | `9090` |
| BREAKING | `openapi.paths[/predict]` | removed | `/predict` | — |
| BREAKING | `configuration.properties[model_path]` | removed | `model_path` | — |

This output is generated automatically by `pacto diff` (with `--output-format markdown` for the table). The exit code is non-zero on breaking changes, so it can gate merges in CI.

---

## Quick preview

```bash
# CLI
pacto validate .                              # 4-layer contract validation
pacto push oci://ghcr.io/acme/svc-pacto       # push to any OCI registry (skips if exists)
pacto diff oci://registry/svc:1.0 svc:2.0     # detect breaking changes
pacto graph .                                  # resolve dependency tree
pacto doc . --serve                            # generate and serve documentation
pacto mcp                                     # start MCP server for AI assistants

# Dashboard
pacto dashboard                                # auto-detects local contracts
pacto dashboard --namespace production         # auto-detects from K8s + OCI
pacto dashboard oci://ghcr.io/acme/payments   # explicit OCI repos
```

---

## Dashboard

The dashboard is the entry point for humans. It auto-detects available sources — Kubernetes (via the operator), OCI registries, local directories, and disk cache — and merges them into a single view.

What it shows:

- **Dependency graph** — interactive visualization of service relationships, with recursive resolution
- **Ownership views** — aggregated compliance and blast radius per owner, with drill-down to individual services and owner-filtered graphs
- **Version history** — all published versions from OCI, with the ability to fetch and cache every version
- **Diffs between versions** — classified changes (breaking, non-breaking) between any two versions
- **Service details** — interfaces, configuration schemas, policy references, documentation
- **Runtime status** — when paired with the operator, shows whether deployed services align with their contracts

Run it locally with `pacto dashboard`, or deploy the [container image](https://trianalab.github.io/pacto/dashboard-docker) alongside the operator for a combined view: runtime state from Kubernetes + contract data from OCI.

---

## Who is this for?

- **Application developers** — Describe your service once. Validation catches misconfigurations before CI. Breaking changes are detected automatically across versions.
- **Platform engineers** — Consume contracts to generate manifests, enforce policies, and visualize dependency graphs. The dashboard gives you a live view of every service and its relationships.
- **DevOps / infrastructure teams** — Distribute contracts through existing OCI registries. The operator tracks what's deployed and whether it matches its contract.

---

## Contract example

```yaml
pactoVersion: "1.0"

service:
  name: payments-api
  version: 2.1.0
  owner: team/payments

interfaces:
  - name: rest-api
    type: http
    port: 8080
    visibility: public
    contract: interfaces/openapi.yaml
  - name: grpc-internal
    type: grpc
    port: 9090
    visibility: internal
    contract: interfaces/service.proto

dependencies:
  - ref: oci://ghcr.io/acme/auth-pacto@sha256:abc123
    required: true
    compatibility: "^2.0.0"

runtime:
  workload: service
  state:
    type: stateful
    persistence:
      scope: local
      durability: persistent
    dataCriticality: high
  health:
    interface: rest-api
    path: /health

scaling:
  min: 2
  max: 10
```

Only `pactoVersion` and `service` are required — everything else is opt-in, so a contract can be as minimal or as detailed as your service needs.

---

## Key capabilities

- **4-layer validation** — structural (JSON Schema), cross-field, semantic, and policy enforcement
- **Breaking change detection** — deep OpenAPI diffing + dependency graph diff with full blast radius
- **Dependency graph resolution** — recursive transitive resolution from OCI registries with parallel fetching
- **OCI distribution** — push/pull to GHCR, ECR, ACR, Docker Hub, Harbor with local caching
- **Plugin-based generation** — out-of-process plugins produce deployment artifacts from contracts
- **Dashboard** — multi-source exploration UI with dependency graphs, version history, diffs, and runtime compliance
- **Kubernetes Operator** — runtime contract tracking, workload linking, and alignment verification
- **AI integration** — `pacto mcp` exposes contract operations as [MCP](https://modelcontextprotocol.io) tools for Claude, Cursor, and Copilot
- **SBOM diffing** — SPDX / CycloneDX package-level change detection

See the [full documentation](https://trianalab.github.io/pacto) for details on each capability.

---

## Why OCI?

Pacto bundles are distributed as OCI artifacts — versioned, content-addressed, and compatible with GHCR, ECR, ACR, Docker Hub, and Harbor. Same registries, same auth, same tooling you already use for container images. Signable with cosign or Notary. No new infrastructure.

---

## How Pacto compares

| Concern | OpenAPI | Helm | Terraform | Backstage | Pacto |
|---------|---------|------|-----------|-----------|-------|
| API contract | ✅ | — | — | — | ✅ |
| Runtime semantics (state, health, lifecycle) | — | Partial | — | — | ✅ |
| Typed dependencies with version constraints | — | — | — | — | ✅ |
| Configuration schema | — | Partial | — | — | ✅ |
| Breaking change detection | — | — | — | — | ✅ |
| Dependency graph visualization | — | — | — | — | ✅ |
| Runtime consistency verification | — | — | — | — | ✅ |
| OCI-native distribution | — | ✅ | — | — | ✅ |
| Machine validation | ✅ | — | ✅ | — | ✅ |

Pacto does not replace these tools. It provides the operational contract layer between them.

## What Pacto is NOT

- Not a deployment tool — it describes services, not how to run them
- Not a service mesh — no sidecars, no traffic interception
- Not a replacement for OpenAPI or Helm — it complements them
- Not a service catalog — the dashboard can feed data into one

See [MANIFEST.md](MANIFEST.md) for the full rationale.

---

## Installation

### Via installer script

```bash
curl -fsSL https://raw.githubusercontent.com/TrianaLab/pacto/main/scripts/get-pacto.sh | bash
```

### Via Go

```bash
go install github.com/trianalab/pacto/cmd/pacto@latest
```

### Build from source

```bash
git clone https://github.com/TrianaLab/pacto.git && cd pacto && make build
```

---

## Documentation

Full documentation at **[trianalab.github.io/pacto](https://trianalab.github.io/pacto)**.

| Guide | Description |
|-------|-------------|
| [Quickstart](https://trianalab.github.io/pacto/quickstart) | From zero to a published contract in 2 minutes |
| [Contract Reference](https://trianalab.github.io/pacto/contract-reference) | Every field, validation rule, and change classification |
| [For Developers](https://trianalab.github.io/pacto/developers) | Write and maintain contracts alongside your code |
| [For Platform Engineers](https://trianalab.github.io/pacto/platform-engineers) | Consume contracts for deployment, policies, and graphs |
| [CLI Reference](https://trianalab.github.io/pacto/cli-reference) | All commands, flags, and output formats |
| [Dashboard](https://trianalab.github.io/pacto/dashboard-docker) | Deploy the dashboard container alongside the operator |
| [Kubernetes Operator](https://trianalab.github.io/pacto/operator) | Runtime contract tracking and consistency verification |
| [MCP Integration](https://trianalab.github.io/pacto/mcp-integration) | Connect AI tools (Claude, Cursor, Copilot) to Pacto via MCP |
| [Plugin Development](https://trianalab.github.io/pacto/plugins) | Build plugins to generate artifacts from contracts |
| [Examples](https://trianalab.github.io/pacto/examples) | PostgreSQL, Redis, RabbitMQ, NGINX, gRPC, and more |
| [Architecture](https://trianalab.github.io/pacto/architecture) | Internal design for contributors |

---

## License

[MIT](LICENSE)
