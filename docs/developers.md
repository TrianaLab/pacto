---
title: For Developers
layout: default
nav_order: 5
---

# Pacto for Developers
{: .no_toc }

You own the service — and you own the contract. Pacto gives you a structured way to declare your service's operational interface alongside your code, so platform engineers, CI systems, and other teams have an accurate, machine-readable description of what your service needs to run.

No forms. No tickets. No wiki pages that go stale. One YAML file, validated by tooling, versioned in a registry.

---

<details open markdown="block">
  <summary>Table of contents</summary>
- TOC
{:toc}
</details>

---

## Your workflow

```mermaid
flowchart LR
    A[Write code] --> B[Infer schemas]
    B --> C[Define pacto.yaml]
    C --> D[pacto validate]
    D --> E[pacto pack]
    E --> F[pacto push]
    F --> G[CI / Platform picks it up]
```

### 1. Initialize your contract

```bash
pacto init my-service
```

This scaffolds a contract with sensible defaults. Edit `pacto.yaml` to match your service.

### 2. Infer schemas from your code (optional)

If your service has a configuration file, use the `schema-infer` plugin to generate a JSON Schema from it. Use `-o` to write the output directly into your bundle:

```bash
pacto generate schema-infer my-service --option file=config.yaml -o my-service
```

This generates `config.schema.json`. Reference it in your contract:

```yaml
configuration:
  schema: config.schema.json
```

When you define your own configuration schema, you are declaring **what your service requires** to run. This is the most common model for services that need to be portable across environments. If your platform team provides a shared schema instead, you can either vendor it into your bundle or reference it via OCI:

```yaml
configuration:
  ref: oci://ghcr.io/acme/platform-config-pacto:1.0.0
```

See [Configuration Schema Ownership Models]({{ site.baseurl }}{% link contract-reference.md %}#configuration-schema-ownership-models) for details.

If your service exposes an HTTP API using FastAPI or Huma, use the `openapi-infer` plugin to extract an OpenAPI 3.1 spec from your source code:

```bash
# Auto-detect framework (generates interfaces/openapi.yaml)
pacto generate openapi-infer my-service -o my-service

# Override framework detection
pacto generate openapi-infer my-service -o my-service --option framework=fastapi

# Custom output path (format inferred from extension)
pacto generate openapi-infer my-service -o my-service --option output=interfaces/openapi.json
```

Then reference the generated spec in your contract:

```yaml
interfaces:
  - name: api
    type: http
    port: 8080
    visibility: public
    contract: interfaces/openapi.yaml
```

Both plugins are installed automatically with Pacto. See the [Official plugins]({{ site.baseurl }}{% link plugins.md %}#official-plugins) section for details.

### 3. Declare your interfaces (optional)

List every boundary your service exposes. Services with no network interfaces (e.g. batch jobs or shared libraries) may omit this section:

```yaml
interfaces:
  - name: api
    type: http
    port: 8080
    visibility: public
    contract: interfaces/openapi.yaml

  - name: events
    type: event
    visibility: internal
    contract: interfaces/events.yaml
```

Include the actual interface files (OpenAPI specs, protobuf definitions, event schemas) in the bundle.

### 4. Define your runtime semantics (optional)

This is where you tell the platform *how* your service behaves — not how to deploy it, but what it *is*:

```yaml
runtime:
  workload: service

  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low

  health:
    interface: api
    path: /health
```

**Ask yourself:**
- Is my service long-running (`service`) or does it run to completion (`job`)?
- Does it hold local state that survives restarts (`stateful`) or not (`stateless`)?
- Does it keep optional in-memory state like caches (`hybrid`)?
- How critical is the data it handles?

The answers determine how platforms provision infrastructure for your service. See [runtime.state]({{ site.baseurl }}{% link contract-reference.md %}#runtimestate) in the Contract Reference for the full explanation.

### 5. Declare dependencies

If your service depends on other Pacto-enabled services:

```yaml
dependencies:
  - ref: oci://ghcr.io/acme/auth-pacto@sha256:abc123
    required: true
    compatibility: "^2.0.0"

  - ref: oci://ghcr.io/acme/cache-pacto:1.0.0
    required: false
    compatibility: "~1.0.0"

  # Tag omitted — resolves to the highest version matching ^3.0.0
  - ref: oci://ghcr.io/acme/utils-pacto
    required: true
    compatibility: "^3.0.0"
```

During development, you can reference local contracts:

```yaml
dependencies:
  - ref: file://../shared-db
    required: true
    compatibility: "^1.0.0"
```

{: .warning }
Local refs are rejected by `pacto push`. Switch all dependencies to `oci://` references before publishing.

If your service depends on a cloud-managed resource (e.g. a database or message queue), create a minimal Pacto contract representing it and reference it as a dependency. This keeps cloud dependencies explicit and version-tracked.

Use `pacto graph` to visualize your dependency tree. Pass `--with-references` to also see config/policy reference edges alongside dependencies, or `--only-references` to show only reference edges.

### 6. Adopt a policy (optional)

If your platform team publishes a policy contract, reference it in your contract:

```yaml
policy:
  ref: oci://ghcr.io/acme/platform-policy-pacto:1.0.0
```

A policy is a JSON Schema that validates the contract itself — enforcing organizational standards like requiring health endpoints or mandating specific ports. See [policy]({{ site.baseurl }}{% link contract-reference.md %}#policy) in the Contract Reference for details.

### 7. Reference your Helm chart (optional)

If your service is deployed via a Helm chart, reference it in the contract:

```yaml
service:
  name: my-service
  version: 1.0.0
  chart:
    ref: oci://ghcr.io/acme/my-chart
    version: 1.0.0
```

During development, you can use a local chart path:

```yaml
service:
  chart:
    ref: ./charts/my-chart
    version: 1.0.0
```

{: .warning }
Local chart references are rejected by `pacto push`. Switch to an OCI reference before publishing.

### 8. Validate before pushing

```bash
pacto validate my-service
```

Validation catches errors in three layers:

1. **Structural** — missing fields, wrong types, invalid enum values
2. **Cross-field** — interface references match, state invariants hold, files exist
3. **Semantic** — strategy consistency warnings

### 9. Pack and push

```bash
pacto pack my-service
pacto push oci://ghcr.io/your-org/my-service-pacto -p my-service
```

If the artifact already exists in the registry, `pacto push` prints a warning and exits without pushing. Use `--force` to overwrite:

```bash
pacto push oci://ghcr.io/your-org/my-service-pacto -p my-service --force
```

---

## Using contract overrides

Pacto supports Helm-style overrides to modify contract values without editing `pacto.yaml`. This is useful for environment-specific values, CI pipelines, or quick experimentation.

```bash
# Override a value inline
pacto validate my-service --set service.version=2.0.0

# Use a values file
pacto validate my-service -f staging-values.yaml

# Combine both (--set takes precedence)
pacto validate my-service -f staging-values.yaml --set service.version=3.0.0

# Set configuration values
pacto validate my-service --set configuration.values.DB_HOST=localhost
```

Overrides work on all commands that take a contract reference. For `diff`, use `--old-set`/`--old-values` and `--new-set`/`--new-values` to override each contract independently.

See the [Contract Reference — Contract overrides]({{ site.baseurl }}{% link contract-reference.md %}#contract-overrides) section for full details.

---

## Common patterns

### Stateless HTTP API

```yaml
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: api
    path: /health
scaling:
  min: 2
  max: 10
```

### Stateful service (database proxy, cache)

```yaml
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
    gracefulShutdownSeconds: 60
  health:
    interface: api
    path: /health
scaling:
  min: 3
  max: 5
```

### API with local cache (hybrid)

```yaml
runtime:
  workload: service
  state:
    type: hybrid
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: api
    path: /health
scaling:
  min: 2
  max: 8
```

A `hybrid` service handles requests statelessly but keeps a local cache or session store. The platform knows it can scale horizontally, but might account for cache warm-up time.

### Fixed-replica service

Use `replicas` instead of `min`/`max` when the service should always run an exact number of instances:

```yaml
scaling:
  replicas: 1
```

### Scheduled job

```yaml
runtime:
  workload: scheduled
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: api
    path: /health
# No scaling — jobs don't scale horizontally
```

---

## Detecting breaking changes

Before releasing a new version, diff against the previous one:

```bash
$ pacto diff oci://ghcr.io/acme/my-service-pacto:1.0.0 my-service
Classification: BREAKING
Changes (2):
  [BREAKING] interfaces (removed): metrics [- metrics]
  [NON_BREAKING] service.version (modified): service.version modified [1.0.0 -> 1.1.0]
```

Integrate `pacto diff` into your CI pipeline to block merges that introduce breaking changes.

{: .tip }
Using GitHub Actions? Check out the official [Pacto CLI action]({{ site.baseurl }}{% link github-actions.md %}).

---

## AI-assisted workflow

If you use an AI assistant that supports [MCP](https://modelcontextprotocol.io) (Claude Code, Cursor, GitHub Copilot), you can connect it to Pacto so it can validate, inspect, and generate contracts on your behalf.

### Setup

Add Pacto as an MCP server in your project. For Claude Code, create `.mcp.json` in your project root:

```json
{
  "mcpServers": {
    "pacto": {
      "command": "pacto",
      "args": ["mcp"]
    }
  }
}
```

For other tools, see the full [MCP Integration]({{ site.baseurl }}{% link mcp-integration.md %}) guide.

### What you can do

Once connected, the AI assistant can use Pacto tools directly in your conversation:

- **Validate** — *"Validate my contract in ./my-service"* — catches structural, cross-field, and semantic errors without leaving your editor
- **Inspect** — *"Show me the full contract for oci://ghcr.io/acme/auth-pacto:2.0.0"* — explore contracts from your registry
- **Explain** — *"Explain what this service does"* — get a human-readable summary of interfaces, dependencies, and runtime behavior
- **Generate** — *"Generate a contract for a stateless Go HTTP API called user-service"* — scaffold new contracts from a description
- **Dependencies** — *"What does my service depend on?"* — resolve and explore the dependency graph
- **Documentation** — *"Generate docs for this contract"* — produce Markdown documentation without running CLI commands

This is particularly useful when writing a new contract from scratch — describe your service to the assistant and let it generate the initial `pacto.yaml`, then iterate with validation feedback in the same conversation.

---

## Including documentation

You can include an optional `docs/` directory in your bundle to ship human-readable documentation alongside the contract:

```
my-service/
  pacto.yaml
  interfaces/
    openapi.yaml
  docs/
    README.md
    architecture.md
    runbook.md
    integration.md
```

Documentation travels with the contract as part of the OCI artifact, so it is versioned and distributed alongside the contract it describes. It has no effect on validation, diffing, or compatibility checks — changes to `docs/` never produce diff entries or affect classification.

Good candidates for `docs/`:

- **Service overview** — what the service does and its purpose
- **Architecture notes** — internal design and data flow
- **Operational runbooks** — incident response and scaling procedures
- **Integration guides** — how consumers should interact with the service

---

## Including an SBOM

You can include an optional `sbom/` directory in your bundle to ship a Software Bill of Materials alongside the contract:

```
my-service/
  pacto.yaml
  interfaces/
    openapi.yaml
  sbom/
    sbom.spdx.json
```

Pacto supports [SPDX 2.3](https://spdx.dev/) (`.spdx.json`) and [CycloneDX 1.5](https://cyclonedx.org/) (`.cdx.json`) formats. The recommended tool for generating SBOMs is [Syft](https://github.com/anchore/syft):

```bash
# Generate an SPDX SBOM
syft . -o spdx-json=sbom/sbom.spdx.json

# Or generate a CycloneDX SBOM
syft . -o cyclonedx-json=sbom/bom.cdx.json
```

Other supported generators include [Trivy](https://github.com/aquasecurity/trivy) and [cdxgen](https://github.com/CycloneDX/cdxgen).

The SBOM travels with the contract as part of the OCI artifact. When both the old and new versions of a contract include an SBOM, `pacto diff` reports package-level changes (added, removed, version or license modified). These changes are informational — they never affect the overall breaking/non-breaking classification.

No contract-level field references the SBOM. Pacto discovers it automatically by scanning the `sbom/` directory for recognized file extensions — the same convention-based approach used for `docs/`.

---

## Tips

- **Version your contract alongside your code.** The `pacto.yaml` lives in your repository.
- **Pin dependency digests in production.** Tags are mutable; digests are not.
- **Keep interface contracts up to date.** OpenAPI specs and protobuf definitions in the bundle should match what your service actually serves.
- **Include documentation in the bundle.** Add a `docs/` directory with runbooks, architecture notes, and integration guides. It ships with the contract but doesn't affect diffing or validation.
- **Include an SBOM.** Add an SBOM to `sbom/` using Syft, Trivy, or cdxgen. `pacto diff` will report package-level changes between versions.
- **Use `pacto explain` to review.** It produces a human-readable summary of your contract.
- **Use `pacto doc` for rich documentation.** It generates Markdown with architecture diagrams and interface tables. Use `--serve` to view it in the browser.
- **Leverage caching.** OCI bundles are cached locally in `~/.cache/pacto/oci/` and tag listings are cached in memory per command, so repeated `graph`, `doc`, and `diff` commands resolve instantly. Use `--no-cache` to force a fresh pull.
- **Use `--verbose` for debugging.** Pass `-v` to any command to see debug-level logs (OCI operations, resolution steps, cache hits/misses) on stderr.
- **Use metadata for organizational context.** Team ownership, on-call channels, and service tiers go in `metadata`.
- **Explore contracts visually.** Run `pacto dashboard` to launch a local web UI that auto-detects contracts from Kubernetes, OCI cache, and local directories, with an interactive dependency graph, status filtering, and diff viewer.
