---
title: CLI Reference
layout: default
nav_order: 7
---

# CLI Reference
{: .no_toc }

All commands support `--output-format json` for programmatic consumption and `--help` for detailed usage.

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
| `--output-format` | Output format: `text` (default) or `json` |
| `--config` | Path to config file (searches `./pacto.yaml` and `~/.config/pacto/` if not set) |

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
pacto push <ref> [-p dir]
```

**Arguments & flags:**

| Argument/Flag | Required | Description |
|----------|----------|-------------|
| `ref` | Yes | OCI reference (e.g., `ghcr.io/org/name:tag`). If no tag is specified, the contract version is used automatically. |
| `-p, --path` | No | Path to contract directory (default: current directory) |

**Examples:**

```bash
# Push with auto-tag (uses contract version)
$ pacto push ghcr.io/acme/my-service-pacto -p my-service
Pushed my-service@1.0.0 -> ghcr.io/acme/my-service-pacto:1.0.0
Digest: sha256:a1b2c3d4...

# Push with explicit tag
$ pacto push ghcr.io/acme/my-service-pacto:latest -p my-service
Pushed my-service@1.0.0 -> ghcr.io/acme/my-service-pacto:latest
Digest: sha256:a1b2c3d4...
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
| `ref` | Yes | OCI reference to pull |
| `-o, --output` | No | Output directory (default: service name) |

**Example:**

```bash
$ pacto pull ghcr.io/acme/my-service-pacto:1.0.0
Pulled my-service@1.0.0 -> my-service/
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
Classification: POTENTIAL_BREAKING
Changes (2):
  [NON_BREAKING] service.version (modified): service.version modified
  [POTENTIAL_BREAKING] scaling.min (modified): scaling.min modified
```

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
  - auth-service@2.3.0 (ghcr.io/acme/auth-pacto@sha256:abc)
    - user-store@1.0.0 (ghcr.io/acme/user-store-pacto:1.0.0)

Cycles (0)
Conflicts (0)
```

Reports cycles, version conflicts, and unreachable dependencies.

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

After this, `pacto push ghcr.io/...` will work without any additional login step.

If `gh` is not installed or not authenticated, pacto silently falls back to the next credential source in the chain.

---

## `pacto version`

Print version information.

```bash
$ pacto version
pacto version 1.0.0
```

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
