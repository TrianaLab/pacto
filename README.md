[![CI](https://github.com/TrianaLab/pacto/actions/workflows/ci.yml/badge.svg)](https://github.com/TrianaLab/pacto/actions/workflows/ci.yml)
[![PkgGoDev](https://pkg.go.dev/badge/github.com/trianalab/pacto)](https://pkg.go.dev/github.com/trianalab/pacto)
[![Go Report Card](https://goreportcard.com/badge/github.com/trianalab/pacto)](https://goreportcard.com/report/github.com/trianalab/pacto)
[![codecov](https://codecov.io/github/TrianaLab/pacto/graph/badge.svg?token=p3AJpP3BbO)](https://codecov.io/github/TrianaLab/pacto)
[![GitHub Release](https://img.shields.io/github/v/release/TrianaLab/pacto)](https://github.com/TrianaLab/pacto/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

# Pacto

**Pacto is to service operations what OpenAPI is to HTTP APIs.**

A service's operational behavior — interfaces, dependencies, runtime semantics, configuration, scaling, readiness — is usually scattered across Helm values, wikis, and dashboards, and drifts from what's actually running. Pacto captures it once in a validated, versioned contract (`pacto.yaml`), distributes it through your existing OCI registry, and verifies it against live workloads.

Pacto (/ˈpak.to/ — Spanish for *pact*) is not a replacement for OpenAPI, Helm, Terraform, Backstage, or Kubernetes. It adds the operational contract layer between them.

**[Documentation](https://trianalab.github.io/pacto)** · **[Quickstart](https://trianalab.github.io/pacto/quickstart)** · **[Specification](https://trianalab.github.io/pacto/contract-reference)** · **[Examples](https://trianalab.github.io/pacto/examples)** · **[Live demo](https://trianalab.github.io/pacto/demo/)**

> **Why Pacto exists** — [MANIFEST.md](MANIFEST.md)

---

## Three components

| Component | Role | When it runs |
|-----------|------|--------------|
| **CLI** | Author, validate, diff, publish contracts | Design-time and CI |
| **Dashboard** | Explore services, dependency graphs, versions, diffs, readiness, insights | Anytime — local or deployed |
| **[Operator](https://github.com/TrianaLab/pacto-operator)** | Track contracts in-cluster, link to workloads, verify runtime consistency | Continuously in Kubernetes |

No sidecars and no central control plane required: the CLI uses your existing OCI registry, the operator watches CRDs, and the dashboard reads from all sources.

---

## Try it

```bash
# Install
curl -fsSL https://raw.githubusercontent.com/TrianaLab/pacto/main/scripts/get-pacto.sh | bash

# Author and publish a contract
pacto init                                   # scaffold a pacto.yaml
pacto validate .                             # 4-layer validation
pacto push oci://ghcr.io/acme/svc-pacto      # tag inferred from service.version

# Catch breaking changes in CI
pacto diff oci://ghcr.io/acme/svc:1.0 oci://ghcr.io/acme/svc:2.0

# Explore everything in a browser
pacto dashboard                              # auto-detects local, OCI, and K8s sources
```

Full install options are [below](#installation); the [Quickstart](https://trianalab.github.io/pacto/quickstart) goes from zero to a published contract in two minutes.

---

## How it fits together

1. A developer defines a `pacto.yaml` alongside the service code.
2. The CLI validates it and publishes it to an OCI registry.
3. The operator discovers the contract in-cluster, tracks every version, and checks runtime alignment (ports, replicas, health).
4. The dashboard merges all sources — local, OCI, Kubernetes, cache — into one explorable view.

---

## Breaking change detection

Someone bumped the version, moved a port, removed an API endpoint, and dropped a config property. `pacto diff` catches it before the merge:

| Classification | Path | Change | Old | New |
|---|---|---|---|---|
| NON_BREAKING | `service.version` | modified | `1.0.0` | `2.0.0` |
| BREAKING | `interfaces.port` | modified | `8081` | `9090` |
| BREAKING | `openapi.paths[/predict]` | removed | `/predict` | — |
| BREAKING | `configuration.properties[model_path]` | removed | `model_path` | — |

The table comes from `pacto diff --output-format markdown`. The exit code is non-zero on breaking changes, so it can gate merges in CI.

---

## Dashboard

Run `pacto dashboard` and open your browser. It auto-detects every available source — Kubernetes (via the operator), OCI registries, local directories, and disk cache — and merges them into a single view of your fleet:

- **Fleet overview** — compliance, readiness, and high-blast-radius services at a glance
- **Dependency graph** — interactive service relationships with recursive resolution
- **Ownership views** — compliance and blast radius per owner, with drill-down and owner-filtered graphs
- **Readiness overview** — readiness scores, status, and check gaps (expired or invalid) across services
- **Version history and diffs** — every published version from OCI, with classified change diffs
- **Service details** — interfaces, configuration schemas, policy references, readiness, documentation
- **Runtime status** — with the operator, whether deployed services align with their contracts

Run it locally, or deploy the [container image](https://trianalab.github.io/pacto/dashboard-docker) alongside the operator for a combined view: runtime state from Kubernetes plus contract data from OCI.

---

## What a contract captures

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

Only `pactoVersion` and `service` are required — everything else is opt-in, so a contract can be as minimal or as detailed as your service needs. `pacto init` scaffolds a `1.0` contract; use `1.1` when you want the optional `readiness` section (everything from `1.0` stays valid under `1.1`).

---

## Capabilities

- **One contract per service** — a single `pacto.yaml` captures interfaces, dependencies, runtime semantics, configuration, scaling, and readiness
- **4-layer validation** — structural (JSON Schema), cross-field, semantic, and policy enforcement
- **Breaking change detection** — deep OpenAPI diffing plus dependency-graph diff with full blast radius; non-zero exit gates CI
- **Dependency graph resolution** — recursive transitive resolution from OCI registries, siblings fetched in parallel
- **Readiness contracts** — declare operational readiness evidence (URLs, docs, tickets, reports) with weights and expiry dates; Pacto can derive readiness scores and flag expired or invalid evidence
- **OCI distribution** — push/pull to GHCR, ECR, ACR, Docker Hub, and Harbor with local caching; signable with cosign or Notary; no custom registry required
- **Runtime verification** — the Kubernetes operator tracks every contract version and checks ports, replicas, and health against running workloads
- **Plugin-based generation** — out-of-process plugins produce deployment artifacts from contracts
- **AI integration** — `pacto mcp` exposes contract operations as [MCP](https://modelcontextprotocol.io) tools for Claude, Cursor, and Copilot
- **SBOM diffing** — SPDX / CycloneDX package-level change detection

See the [documentation](https://trianalab.github.io/pacto) for details on each.

---

## When should I use Pacto?

Use Pacto when you need to:

- Know who owns each service and what it depends on
- Catch operational breaking changes before merge
- Compare contract versions across releases
- Track whether deployed workloads still match their declared contract
- Build dependency graphs without scraping dashboards or Helm values
- Surface readiness gaps before production changes

---

## Who is this for?

- **Application developers** — describe your service once; validation catches misconfigurations before CI, and breaking changes are detected automatically across versions.
- **Platform engineers** — consume contracts to generate manifests, enforce policies, and visualize dependency graphs, with a live view of every service in the dashboard.
- **DevOps / infrastructure teams** — distribute contracts through existing OCI registries; the operator tracks what's deployed and whether it matches its contract.

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

Pacto sits alongside these tools, not on top of them — the contract becomes the shared source of truth between your API spec, your deployment tooling, and your cluster.

### What Pacto is NOT

- Not a deployment tool — it describes services, not how to run them
- Not a service mesh — no sidecars, no traffic interception
- Not a replacement for OpenAPI or Helm — it complements them
- Not a service catalog — the dashboard can feed data into one

See [MANIFEST.md](MANIFEST.md) for the full rationale.

---

## Installation

```bash
# Installer script
curl -fsSL https://raw.githubusercontent.com/TrianaLab/pacto/main/scripts/get-pacto.sh | bash

# Go
go install github.com/trianalab/pacto/cmd/pacto@latest

# From source
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
