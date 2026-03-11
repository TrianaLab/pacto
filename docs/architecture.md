---
title: Architecture
layout: default
nav_order: 11
---

# Architecture
{: .no_toc }

Pacto follows a clean, layered architecture with strict dependency direction. This page describes the internal design for contributors and plugin authors.

---

<details open markdown="block">
  <summary>Table of contents</summary>
- TOC
{:toc}
</details>

---

## Dependency graph

```mermaid
graph TD
    MAIN[cmd/pacto/main.go<br/>Composition Root] --> CLI[internal/cli<br/>Cobra Commands]
    CLI --> LOG[internal/logger<br/>Logger Setup]
    CLI --> MCP[internal/mcp<br/>MCP Server]
    MCP --> APP
    CLI --> APP[internal/app<br/>Application Services]
    APP --> VAL[internal/validation<br/>Three-Layer Validator]
    APP --> DIFF[internal/diff<br/>Change Classifier]
    APP --> GRAPH[internal/graph<br/>Dependency Resolver]
    APP --> OCI[internal/oci<br/>OCI Adapter]
    APP --> PLUG[internal/plugin<br/>Plugin Runner]
    APP --> DOC[internal/doc<br/>Doc Generator]
    VAL --> CONTRACT[pkg/contract<br/>Domain Model]
    DOC --> CONTRACT
    DIFF --> CONTRACT
    GRAPH --> CONTRACT
    OCI --> CONTRACT
    PLUG --> CONTRACT
```

Dependencies flow **downward only**. No package imports a package above it.

---

## Package responsibilities

### `pkg/contract` — Domain model

The only public package. Contains pure Go types and logic with **zero I/O and zero framework dependencies**.

- `Contract`, `ServiceIdentity`, `Interface`, `Runtime`, `State`, etc.
- `Parse()` — YAML deserialization
- `OCIReference` — OCI reference parsing
- `Range` — Semver constraint evaluation
- `Bundle` — Contract + file system

### `internal/app` — Application services

Each CLI command maps to exactly one service method. This layer orchestrates domain logic and infrastructure.

- `Init()`, `Validate()`, `Pack()`, `Push()`, `Pull()`
- `Diff()`, `Graph()`, `Explain()`, `Generate()`, `Doc()`
- Shared helpers: `resolveBundle()`, `loadAndValidateLocal()`

### `internal/logger` — Logger setup

Configures Go's standard `log/slog` default logger based on the `--verbose` flag. When verbose mode is enabled, debug-level messages are emitted to stderr; otherwise only warnings and above are shown. Called once during CLI initialization via `PersistentPreRunE` — all packages use `slog.Debug()` directly with no wrappers.

### `internal/cli` — CLI layer

Cobra command handlers and Viper configuration. **Zero business logic** — only input parsing, orchestration, and output formatting.

### `internal/validation` — Validation engine

Three-layer, short-circuit validation:

```mermaid
flowchart LR
    A[Layer 1<br/>Structural<br/>JSON Schema] --> B[Layer 2<br/>Cross-Field<br/>Reference Validation]
    B --> C[Layer 3<br/>Semantic<br/>Consistency Checks]
```

Each layer short-circuits — if it produces errors, subsequent layers are skipped.

### `internal/diff` — Change classifier

Compares two contracts and classifies every change using a deterministic rule table. Sub-analyzers handle specific sections:

- `contract.go` — service identity, scaling
- `runtime.go` — workload, state, lifecycle, health
- `interfaces.go` — interface additions/removals/changes
- `dependency.go` — dependency list changes
- `openapi.go` — OpenAPI path-level diff
- `schema.go` — JSON Schema property-level diff

### `internal/graph` — Dependency resolver

Builds a dependency graph by recursively fetching contracts from OCI registries and local paths. Sibling dependencies at each level are resolved concurrently. Detects cycles and version conflicts.

- `ParseDependencyRef()` — centralized dependency reference parser (`oci://`, `file://`, bare paths)
- `RenderTree()` / `RenderDiffTree()` — tree-style rendering with `├─`/`└─` connectors
- `DiffGraphs()` — structural diff between two dependency graphs

### `internal/oci` — OCI adapter

Thin wrapper over `go-containerregistry`. Handles bundle-to-image translation, credential resolution, error mapping, and local disk caching of pulled bundles (`~/.cache/pacto/oci/`).

Pacto distributes contracts as OCI artifacts — the same standard behind container images. This means contracts work with any OCI-compliant registry (GHCR, ECR, ACR, Docker Hub, Harbor) without new infrastructure. Every pushed contract is content-addressed with a digest, making it immutable and verifiable.

### `internal/doc` — Documentation generator

Generates rich Markdown documentation from a contract. Reads OpenAPI specs, event contracts, and JSON Schema configuration to produce a comprehensive service document with architecture diagrams, interface tables, and configuration details. Includes an HTTP server for browser-based viewing.

### `internal/mcp` — MCP server

Thin adapter layer that exposes Pacto operations as [Model Context Protocol](https://modelcontextprotocol.io) tools. Each MCP tool handler delegates to an `internal/app` service method — no business logic lives here. The server communicates over stdio and is started via `pacto mcp`. Used by AI tools such as Claude, Cursor, and Copilot.

### `internal/plugin` — Plugin system

Out-of-process plugin execution via JSON stdin/stdout. Discovers plugin binaries and manages the communication protocol. See the [Plugin Development]({{ site.baseurl }}{% link plugins.md %}) guide.

---

## Design principles

1. **Pure core** — `pkg/contract` has zero I/O and zero framework dependencies
2. **Strict layering** — CLI → App → Engines → Domain
3. **No global state** — all instances created in the composition root (`main.go`); the only global is `slog.SetDefault()` configured once at startup
4. **Interface-based** — engines depend on interfaces, not concrete implementations
5. **Out-of-process plugins** — language-agnostic, version-independent
6. **Embedded schemas** — JSON Schema compiled into the binary
7. **Deterministic validation** — no configurable rules; same input, same result
