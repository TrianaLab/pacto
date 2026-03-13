---
title: CLI Reference
layout: default
nav_order: 7
---

# CLI Reference
{: .no_toc }

All commands support `--output-format json` for programmatic consumption, `--output-format markdown` for rich markdown output (e.g. CI comments), `-v` / `--verbose` for debug-level logging, and `--help` for detailed usage.

---

<details open markdown="block">
  <summary>Table of contents</summary>
- TOC
{:toc}
</details>

---

## Global flags

| Flag | Description |
|------|-------------|
| `--output-format` | Output format: `text` (default), `json`, or `markdown` |
| `-v, --verbose` | Enable verbose output (debug-level logging to stderr) |
| `--config` | Path to config file (searches `./pacto.yaml` and `~/.config/pacto/` if not set) |
| `--no-cache` | Disable OCI bundle caching (bypasses `~/.cache/pacto/oci/`) |

## OCI version resolution

All commands that accept `oci://` references support automatic version resolution. When a reference omits the tag (e.g. `oci://ghcr.io/acme/svc-pacto` instead of `oci://ghcr.io/acme/svc-pacto:1.0.0`), pacto queries the registry for available tags and selects the **highest semver version**.

For dependency references declared with a `compatibility` constraint, only tags satisfying the constraint are considered. For example, a dependency with `compatibility: "^2.0.0"` and available tags `1.0.0`, `2.0.0`, `2.3.0`, `3.0.0` resolves to `2.3.0`.

Non-semver tags (e.g. `latest`, `main`) are ignored during resolution. Digest-pinned references (`@sha256:...`) and explicitly tagged references are used as-is.

---

## `pacto init`

Scaffold a new service contract project.

```bash
pacto init <name>
```

**Arguments:**

| Argument | Required | Description |
|----------|----------|-------------|
| `name` | Yes | Service name (used as directory name) |

**Example:**

```bash
$ pacto init my-service
Created my-service/
  my-service/pacto.yaml
  my-service/interfaces/
  my-service/configuration/
```

Creates a complete bundle with a valid `pacto.yaml`, a placeholder OpenAPI spec, and a configuration JSON Schema.

---

## `pacto validate`

Validate a contract through all three validation layers.

```bash
pacto validate [dir | oci://ref]
```

**Arguments:**

| Argument | Required | Description |
|----------|----------|-------------|
| `dir` | No | Directory containing `pacto.yaml` or `oci://` reference (default: current directory) |

**Exit code:** Non-zero if validation fails.

**Examples:**

```bash
# Validate a local contract (directory containing pacto.yaml)
pacto validate my-service

# Validate from current directory
pacto validate

# Validate from an OCI registry
pacto validate oci://ghcr.io/acme/my-service-pacto:1.0.0

# JSON output
pacto validate --output-format json my-service
```

---

## `pacto pack`

Create a tar.gz archive of the bundle directory.

```bash
pacto pack [dir] [-o output]
```

**Arguments & flags:**

| Argument/Flag | Required | Description |
|----------|----------|-------------|
| `dir` | No | Directory containing `pacto.yaml` (default: current directory) |
| `-o, --output` | No | Output file path (default: `<name>-<version>.tar.gz`) |

**Example:**

```bash
$ pacto pack my-service
Packed my-service@0.1.0 -> my-service-0.1.0.tar.gz
```

The contract is validated before packing. If validation fails, no archive is created.

---

## `pacto push`

Push a validated contract bundle to an OCI registry.

```bash
pacto push <ref> [-p dir] [-f]
```

**Arguments & flags:**

| Argument/Flag | Required | Description |
|----------|----------|-------------|
| `ref` | Yes | OCI reference (e.g., `oci://ghcr.io/org/name:tag`). If no tag is specified, the contract version is used automatically. |
| `-p, --path` | No | Path to contract directory (default: current directory) |
| `-f, --force` | No | Overwrite existing artifact in registry |

If the artifact already exists in the registry, `pacto push` prints a warning and exits successfully without pushing. Use `--force` to overwrite.

**Examples:**

```bash
# Push with auto-tag (uses contract version)
$ pacto push oci://ghcr.io/acme/my-service-pacto -p my-service
Pushed my-service@1.0.0 -> ghcr.io/acme/my-service-pacto:1.0.0
Digest: sha256:a1b2c3d4...

# Push with explicit tag
$ pacto push oci://ghcr.io/acme/my-service-pacto:latest -p my-service
Pushed my-service@1.0.0 -> ghcr.io/acme/my-service-pacto:latest
Digest: sha256:a1b2c3d4...

# Attempting to push an already-published version
$ pacto push oci://ghcr.io/acme/my-service-pacto -p my-service
Warning: artifact already exists: ghcr.io/acme/my-service-pacto:1.0.0 (use --force to overwrite)

# Force overwrite an existing artifact
$ pacto push oci://ghcr.io/acme/my-service-pacto -p my-service --force
Pushed my-service@1.0.0 -> ghcr.io/acme/my-service-pacto:1.0.0
Digest: sha256:e5f6g7h8...
```

---

## `pacto pull`

Pull a contract bundle from an OCI registry.

```bash
pacto pull <ref> [-o output]
```

**Arguments & flags:**

| Argument/Flag | Required | Description |
|----------|----------|-------------|
| `ref` | Yes | OCI reference to pull (tag resolved automatically if omitted) |
| `-o, --output` | No | Output directory (default: service name) |

**Examples:**

```bash
# Pull a specific version
$ pacto pull oci://ghcr.io/acme/my-service-pacto:1.0.0
Pulled my-service@1.0.0 -> my-service/

# Pull the latest available version
$ pacto pull oci://ghcr.io/acme/my-service-pacto
Pulled my-service@2.3.0 -> my-service/
```

---

## `pacto diff`

Compare two contracts and classify every change.

```bash
pacto diff <old> <new>
```

**Arguments:**

| Argument | Required | Description |
|----------|----------|-------------|
| `old` | Yes | Directory or `oci://` reference to the old contract |
| `new` | Yes | Directory or `oci://` reference to the new contract |

**Exit code:** Non-zero if breaking changes are detected.

**Example:**

```bash
$ pacto diff oci://ghcr.io/acme/svc-pacto:1.0.0 my-service
Classification: BREAKING
Changes (5):
  [BREAKING] openapi.paths[/users].methods[DELETE] (removed): DELETE /users method removed [- DELETE /users]
  [POTENTIAL_BREAKING] openapi.paths[/users].methods[GET].responses[200] (modified): GET /users response 200 modified [GET /users 200 -> GET /users 200]
  [POTENTIAL_BREAKING] openapi.paths[/users].methods[GET].parameters[filter:query] (added): GET /users query param 'filter' added [+ GET /users filter:query]
  [NON_BREAKING] openapi.paths[/users].methods[POST] (added): POST /users method added [+ POST /users]
  [NON_BREAKING] openapi.paths[/orders] (added): API path /orders added [+ /orders]

Dependency graph changes:
  my-service
  ├─ cache         1.0.0 → 2.0.0
  └─ old-dep       -1.0.0
```

**Markdown output** (`--output-format markdown`) renders the same information as tables, suitable for CI comments and PR summaries:

```bash
$ pacto diff --output-format markdown oci://ghcr.io/acme/svc-pacto:1.0.0 my-service
## Contract Diff

**Classification:** `BREAKING`

### Changes (5)

| Classification | Path | Type | Reason | Old | New |
|---|---|---|---|---|---|
| BREAKING | `openapi.paths[/users].methods[DELETE]` | removed | ... | `DELETE /users` | |
| NON_BREAKING | `openapi.paths[/orders]` | added | ... | | `/orders` |
...
```

The diff engine performs deep comparison of referenced OpenAPI specs, detecting changes at the path, method, parameter, request body, and response level. The optional `docs/` directory is ignored entirely — documentation changes never produce diff entries or affect compatibility classification.

When both bundles include an `sbom/` directory with recognized SBOM files (`.spdx.json` or `.cdx.json`), `pacto diff` reports package-level changes — added, removed, or modified packages (version and license). SBOM changes are informational and do not affect the overall classification or exit code.

When dependencies change between the old and new contracts (version upgrades, additions, or removals), a dependency graph diff section is displayed showing the tree of affected nodes.

See [Change Classification]({{ site.baseurl }}{% link contract-reference.md %}#change-classification-rules) for the full rules.

---

## `pacto graph`

Resolve and display the dependency graph.

```bash
pacto graph [dir | oci://ref]
```

**Arguments:**

| Argument | Required | Description |
|----------|----------|-------------|
| `dir` | No | Directory containing `pacto.yaml` or `oci://` reference (default: current directory) |

**Example:**

```bash
$ pacto graph my-service
my-service@1.0.0
├─ auth-service@2.3.0
│  └─ user-store@1.0.0
└─ cache@1.0.0 (shared)
```

Dependencies resolved from local paths are annotated with `[local]`. Shared dependencies (referenced by multiple parents) are annotated with `(shared)`.

Reports cycles, version conflicts, and unreachable dependencies.

Sibling dependencies are resolved in parallel. OCI bundles are cached locally in `~/.cache/pacto/oci/` for faster subsequent operations. Use `--no-cache` to bypass the cache.

---

## `pacto explain`

Produce a human-readable summary of a contract.

```bash
pacto explain [dir | oci://ref]
```

**Arguments:**

| Argument | Required | Description |
|----------|----------|-------------|
| `dir` | No | Directory containing `pacto.yaml` or `oci://` reference (default: current directory) |

---

## `pacto doc`

Generate rich Markdown documentation from a contract.

```bash
pacto doc [dir | oci://ref]
```

**Arguments & flags:**

| Argument/Flag | Required | Description |
|----------|----------|-------------|
| `dir` | No | Directory containing `pacto.yaml` or `oci://` reference (default: current directory) |
| `-o, --output` | No | Output directory for generated Markdown file |
| `--serve` | No | Start a local HTTP server to view documentation in the browser |
| `--port` | No | Port for the documentation server (default: `8484`, used with `--serve`) |

`--serve` and `--output` are mutually exclusive.

**Examples:**

```bash
# Print documentation to stdout
pacto doc my-service

# Write documentation to a file
pacto doc my-service -o docs/

# Serve documentation in the browser
pacto doc my-service --serve

# Serve on a custom port
pacto doc my-service --serve --port 9090

# Generate from an OCI reference
pacto doc oci://ghcr.io/acme/my-service-pacto:1.0.0
```

Sibling dependencies are resolved in parallel. OCI bundles are cached locally in `~/.cache/pacto/oci/` for faster subsequent operations. Use `--no-cache` to bypass the cache.

---

## `pacto generate`

Generate artifacts from a contract using a plugin.

```bash
pacto generate <plugin> [dir | oci://ref] [-o output]
```

**Arguments & flags:**

| Argument/Flag | Required | Description |
|----------|----------|-------------|
| `plugin` | Yes | Plugin name (looks for `pacto-plugin-<name>`) |
| `dir` | No | Directory containing `pacto.yaml` or `oci://` reference (default: current directory) |
| `-o, --output` | No | Output directory (default: `<plugin>-output/`) |

**Example:**

```bash
$ pacto generate helm my-service -o manifests/
Generated 3 file(s) using helm
Output: manifests/
```

---

## `pacto login`

Store credentials for an OCI registry.

```bash
pacto login <registry> -u <username> [-p <password>]
```

**Arguments & flags:**

| Argument/Flag | Required | Description |
|----------|----------|-------------|
| `registry` | Yes | Registry hostname (e.g., `ghcr.io`) |
| `-u, --username` | Yes | Registry username |
| `-p, --password` | No | Registry password (prompted securely if omitted) |

**Example:**

```bash
$ pacto login ghcr.io -u my-username
Password: ********
Login successful
```

Credentials are stored in `~/.config/pacto/config.json` (or `$XDG_CONFIG_HOME/pacto/config.json`), keeping them separate from Docker's configuration.

### GitHub Container Registry (ghcr.io)

For GitHub registries (`ghcr.io` and `docker.pkg.github.com`), pacto can automatically reuse credentials from the [GitHub CLI](https://cli.github.com/) — no `pacto login` required.

If you already have `gh` installed and authenticated, pacto will use `gh auth token` to obtain a token transparently. To verify your setup:

```bash
# Check if gh is authenticated
gh auth status

# Verify the token is available
gh auth token
```

To push container images or packages, your token needs the `write:packages` scope. If you authenticated `gh` without it, refresh your scopes:

```bash
gh auth refresh --scopes write:packages
```

After this, `pacto push oci://ghcr.io/...` will work without any additional login step.

If `gh` is not installed or not authenticated, pacto silently falls back to the next credential source in the chain.

---

## `pacto update`

Update pacto to a newer version.

```bash
pacto update [version]
```

**Arguments:**

| Argument | Required | Description |
|----------|----------|-------------|
| `version` | No | Target version (default: latest release). Accepts with or without `v` prefix. |

**Examples:**

```bash
# Update to the latest release
$ pacto update
Checking for updates...
Updated pacto v1.0.0 -> v1.2.0

# Update to a specific version
$ pacto update v1.1.0
Checking for updates...
Updated pacto v1.0.0 -> v1.1.0

# Version prefix is optional
$ pacto update 1.1.0
```

{: .note }
`pacto update` is not available on dev builds. If you built from source without version injection, install a release build first.

### Update notifications

When a newer version is available, pacto displays a notification after any command:

```
A new version of pacto is available: v1.0.0 -> v1.2.0
Run 'pacto update' to update.
```

The check runs asynchronously and adds no latency. Results are cached for 24 hours in `~/.config/pacto/update-check.json`.

Notifications are suppressed when:
- Running a dev build
- Using `--output-format json`
- The `PACTO_NO_UPDATE_CHECK=1` environment variable is set

---

## `pacto mcp`

Start a [Model Context Protocol](https://modelcontextprotocol.io) (MCP) server that exposes Pacto contract operations as structured tools. AI assistants (Claude, Cursor, GitHub Copilot) connect to this server and can then validate contracts, resolve dependency graphs, run diff analysis, and generate contract scaffolding — all through standardized tool calls.

```bash
pacto mcp [-t transport] [--port port]
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `-t, --transport` | `stdio` | Transport type: `stdio` or `http` |
| `--port` | `8585` | Port for HTTP transport |

The server exposes the following tools:

| Tool | Description |
|------|-------------|
| `pacto_validate` | Validate a contract and return errors/warnings |
| `pacto_inspect` | Return the full structured contract representation |
| `pacto_resolve_dependencies` | Resolve the dependency graph, detecting cycles and conflicts |
| `pacto_list_interfaces` | List interfaces exposed by a service |
| `pacto_generate_docs` | Generate Markdown documentation from a contract |
| `pacto_explain` | Return a human-readable summary of a contract |
| `pacto_generate_contract` | Generate a new contract YAML from structured inputs |
| `pacto_suggest_dependencies` | Suggest likely dependencies based on service characteristics |
| `pacto_schema` | Return the Pacto JSON Schema and documentation link |

All tools accept both local directory paths and `oci://` references.

**Examples:**

```bash
# Start MCP server over stdio (default, used by Claude Code / Cursor)
$ pacto mcp
MCP server running on stdio

# Start MCP server over HTTP
$ pacto mcp -t http
MCP server listening on http://127.0.0.1:8585/mcp

# Start MCP server on a custom port
$ pacto mcp -t http --port 9090
MCP server listening on http://127.0.0.1:9090/mcp
```

See [MCP Integration]({{ site.baseurl }}{% link mcp-integration.md %}) for detailed setup instructions with Claude and other AI tools.

---

## `pacto version`

Print version information.

```bash
$ pacto version
pacto version 1.0.0
```

---

## Environment variables

| Variable | Description |
|----------|-------------|
| `PACTO_NO_CACHE` | Set to `1` to disable OCI bundle caching (equivalent to `--no-cache`) |
| `PACTO_NO_UPDATE_CHECK` | Set to `1` to disable automatic update checks |
| `PACTO_REGISTRY_USERNAME` | Registry username for authentication |
| `PACTO_REGISTRY_PASSWORD` | Registry password for authentication |
| `PACTO_REGISTRY_TOKEN` | Registry token for authentication |

---

## Authentication

Pacto follows this credential resolution chain:

1. Explicit CLI flags (`--username`, `--password`)
2. Environment variables (`PACTO_REGISTRY_USERNAME`, `PACTO_REGISTRY_PASSWORD`, `PACTO_REGISTRY_TOKEN`)
3. Pacto config (`~/.config/pacto/config.json`, written by `pacto login`)
4. GitHub CLI (`gh auth token`, for `ghcr.io` and `docker.pkg.github.com` only)
5. Docker config (`~/.docker/config.json`) and credential helpers
6. Cloud auto-detection (ECR, GCR, ACR)
7. Anonymous fallback

For GitHub registries, step 4 means you can skip `pacto login` entirely if you have `gh` authenticated with the `write:packages` scope (see [`pacto login`](#pacto-login) above).

No credentials are ever stored in contract files.
