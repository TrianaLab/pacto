[![CI](https://github.com/TrianaLab/pacto/actions/workflows/ci.yml/badge.svg)](https://github.com/TrianaLab/pacto/actions/workflows/ci.yml)
[![PkgGoDev](https://pkg.go.dev/badge/github.com/trianalab/pacto/v2)](https://pkg.go.dev/github.com/trianalab/pacto/v2)
[![Go Report Card](https://goreportcard.com/badge/github.com/trianalab/pacto/v2)](https://goreportcard.com/report/github.com/trianalab/pacto/v2)
[![codecov](https://codecov.io/github/TrianaLab/pacto/graph/badge.svg?token=p3AJpP3BbO)](https://codecov.io/github/TrianaLab/pacto)
[![GitHub Release](https://img.shields.io/github/v/release/TrianaLab/pacto)](https://github.com/TrianaLab/pacto/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

# Pacto

**Pacto is to service operations what OpenAPI is to HTTP APIs.**

A service's operational behavior — interfaces, dependencies, runtime semantics, configuration, scaling, readiness — is usually scattered across Helm values, wikis, and dashboards, and drifts from what's actually running. Pacto captures it once in a validated, versioned contract (`pacto.yaml`), distributes it through your existing OCI registry, and verifies it against live workloads.

Pacto (/ˈpak.to/ — Spanish for *pact*) is not a replacement for OpenAPI, Helm, Terraform, Backstage, or Kubernetes. It adds the operational contract layer between them. It composes the interfaces you already maintain — an OpenAPI spec, a config JSON Schema — and adds the layer no single interface owns: ownership, dependencies, compatibility and readiness. JSON Schema describes an interface; Pacto describes the relationships between interfaces and how they change over time.

**[Documentation](https://trianalab.github.io/pacto)** · **[Quickstart](https://trianalab.github.io/pacto/quickstart)** · **[Specification](https://trianalab.github.io/pacto/contract-reference)** · **[Examples](https://trianalab.github.io/pacto/examples)** · **[Live demo](https://trianalab.github.io/pacto/demo/)**

> **Why Pacto exists** — [MANIFEST.md](MANIFEST.md)

---

## Three components

| Component | Role | When it runs |
|-----------|------|--------------|
| **CLI** | Author, validate, diff, publish contracts | Design-time and CI |
| **Dashboard** | Explore services, dependency graphs, versions, diffs, readiness, insights | Anytime — local or deployed |
| **[Operator](https://github.com/TrianaLab/pacto-operator)** | Track contracts in-cluster, link to workloads, verify runtime consistency | Continuously in Kubernetes |

No sidecars and no central control plane required: the CLI uses your existing OCI registry, the operator watches CRDs and the dashboard merges every source — local, OCI, Kubernetes and cache — into one view.

---

## Try it

```bash
# Author and publish a contract (install is below)
pacto init my-service && cd my-service       # scaffold a contract bundle
pacto validate .                             # 4-layer validation, run from inside the new directory
pacto push oci://ghcr.io/acme/svc-pacto      # tag inferred from service.version

# Catch breaking changes in CI
pacto diff oci://ghcr.io/acme/svc:1.0 oci://ghcr.io/acme/svc:2.0

# Explore everything in a browser
pacto dashboard                              # auto-detects local, OCI and K8s sources
```

Full install options are [below](#installation); the [Quickstart](https://trianalab.github.io/pacto/quickstart) goes from zero to a published contract in two minutes.

---

## Breaking change detection

Someone bumped the version, moved a port, removed an API endpoint, and dropped a config property. `pacto diff` catches it before the merge:

| Classification | Path | Type | Old | New |
|---|---|---|---|---|
| NON_BREAKING | `service.version` | modified | `1.0.0` | `2.0.0` |
| BREAKING | `interfaces.port` | modified | `8081` | `9090` |
| BREAKING | `openapi.paths[/predict]` | removed | `/predict` | — |
| BREAKING | `schema.properties.model_path` | removed | `model_path` | — |

`pacto diff --output-format markdown` emits a similar table (it also includes a Reason column). The exit code is non-zero on breaking changes, so it can gate merges in CI.

---

## Dashboard

`pacto dashboard` merges every detected source into one view of your fleet:

- **Fleet overview** — compliance, readiness and high-blast-radius services at a glance
- **Dependency graph** — interactive service relationships with recursive resolution
- **Version history and diffs** — every published version from OCI, with classified change diffs
- **Runtime status** — with the operator, whether deployed services align with their contracts

Ownership views, readiness scoring and per-service details are covered in the [platform engineer guide](https://trianalab.github.io/pacto/platform-engineers). Run it locally, or deploy the [container image](https://trianalab.github.io/pacto/dashboard-docker) alongside the operator for a combined view: runtime state from Kubernetes plus contract data from OCI.

---

## What a contract captures

```yaml
pactoVersion: "1.2"

service:
  name: payments-api
  version: 2.1.0
  owner:
    team: payments
    dri: alice

interfaces:
  - name: rest-api
    type: http
    port: 8080
    visibility: public
    contract: interfaces/openapi.yaml

dependencies:
  - name: auth
    ref: oci://ghcr.io/acme/auth-pacto@sha256:abc123
    required: true
    compatibility: "^2.0.0"
```

Only `pactoVersion` and `service` are required — everything else (runtime semantics, scaling, configuration, policies and readiness) is opt-in, so a contract can be as minimal or as detailed as your service needs. `pacto init` scaffolds a `1.2` contract; use `1.0` or `1.1` for contracts without readiness (object-only `owner` is enforced across all versions). See the [Contract Reference](https://trianalab.github.io/pacto/contract-reference) for the full field-by-field schema.

Each interface's `contract` field points at a schema you already own — the `interfaces/openapi.yaml` above is your OpenAPI spec, not a Pacto rewrite — so a contract composes your existing interfaces rather than redefining them.

---

## Capabilities

Beyond the diff, dashboard and runtime verification shown above:

- **Lockfiles** — reproducible dependency resolution and supply-chain pinning via `pacto.lock`
- **Packaging ignore** — gitignore-style `.pactoignore` to exclude files from bundles
- **OCI distribution** — push/pull to GHCR, ECR, ACR, Docker Hub and Harbor with local caching; signable with cosign or Notary; no custom registry required
- **Plugin-based generation** — out-of-process plugins produce deployment artifacts from contracts
- **AI integration** — `pacto mcp` exposes contract operations as [MCP](https://modelcontextprotocol.io) tools for Claude, Cursor and Copilot
- **SBOM diffing** — SPDX / CycloneDX package-level change detection

See the [documentation](https://trianalab.github.io/pacto) for details on each.

---

## Who is it for?

- **Application developers** — describe a service once; validation catches misconfigurations before CI and breaking changes are detected automatically across versions.
- **Platform engineers** — consume contracts to generate manifests, enforce policies and visualize dependency graphs, with a live view of every service in the dashboard.
- **DevOps / infrastructure teams** — distribute contracts through existing OCI registries; the operator tracks what's deployed and whether it matches its contract.

See the [documentation](https://trianalab.github.io/pacto) for when to use Pacto and per-audience guides.

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

Pacto sits alongside these tools, not on top of them: it composes the interfaces they already produce — your OpenAPI spec, your config schema — and adds the relational and temporal layer no single one owns. The contract becomes the shared source of truth between your API spec, your deployment tooling and your cluster.

### Against platform-engineering tools

The table above covers the interface tools Pacto composes. It gets compared just as often to the platform-engineering tier — the orchestrators, provisioners and portals that *act on* a service. Pacto is not one of them. Its job is four verbs over a single versioned OCI artifact — **diff** (semantic breaking changes), **graph** (transitive blast radius), **enforce** (recursive policy, fail-closed) and **verify** (the same artifact at design-time via the CLI and at runtime via the operator) — and it makes zero deployment decisions.

| | Versioned artifact | Semantic diff | Dependency graph | Transitive policy | Runtime verify | Orchestrator-agnostic | Deploys? |
|---|---|---|---|---|---|---|---|
| **Score** | — | — | — | — | — | ✅ | No |
| **Crossplane Configuration** | ✅ | — | — | — | — | — | Yes |
| **KubeVela** / OAM | — | — | Partial | — | — | Partial | Yes |
| **Radius** | — | — | ✅ | — | — | Partial | Yes |
| **Kratix** | — | — | — | — | — | Partial | Yes |
| **Backstage** / Port | — | — | Partial | — | — | ✅ | No |
| **Kargo** | ✅ | — | — | — | — | ✅ | Yes |
| **Pacto** | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | **No** |

✅ first-class · Partial adjacent or limited · — not in scope. A 2026 snapshot, and several of these are complementary rather than competing: a contract can gate a Kargo promotion, feed a Backstage card or front a Crossplane provisioner. The point is the combination — Pacto is the only row that is a versioned, diffable, graph-resolved, policy-enforced and runtime-verified contract that stays orchestrator-agnostic and never deploys.

### What Pacto is NOT

- Not a deployment tool — it describes services, not how to run them. It makes zero deployment decisions, which keeps it complementary to deploy engines like KubeVela, Radius and Kratix rather than competing with them
- Not a service mesh — no sidecars, no traffic interception
- Not a replacement for OpenAPI or Helm — it complements them
- Not another configuration language — it composes the schemas you already own instead of inventing one
- Not a service catalog — the dashboard can feed data into one

See [MANIFEST.md](MANIFEST.md) for the full rationale.

---

## Installation

```bash
# Installer script
curl -fsSL https://raw.githubusercontent.com/TrianaLab/pacto/main/scripts/get-pacto.sh | bash

# Go
go install github.com/trianalab/pacto/v2/cmd/pacto@latest

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
