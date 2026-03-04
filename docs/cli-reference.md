---
title: CLI Reference
layout: default
nav_order: 7
---

# CLI Reference

All commands support `--output-format json` for programmatic consumption and `--help` for detailed usage.

---

## Global flags

| Flag | Description |
|------|-------------|
| `--output-format` | Output format: `text` (default) or `json` |
| `--config` | Path to config file (default: `pacto.yaml` or `~/.config/pacto/config.yaml`) |

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
pacto validate [path | oci://ref]
```

**Arguments:**

| Argument | Required | Description |
|----------|----------|-------------|
| `path` | No | Path to `pacto.yaml` or `oci://` reference (default: `pacto.yaml`) |

**Exit code:** Non-zero if validation fails.

**Examples:**

```bash
# Validate a local contract
pacto validate my-service/pacto.yaml

# Validate from an OCI registry
pacto validate oci://ghcr.io/acme/my-service-pacto:1.0.0

# JSON output
pacto validate --output-format json my-service/pacto.yaml
```

---

## `pacto pack`

Create a tar.gz archive of the bundle directory.

```bash
pacto pack [path] [-o output]
```

**Arguments & flags:**

| Argument/Flag | Required | Description |
|----------|----------|-------------|
| `path` | No | Path to `pacto.yaml` (default: `pacto.yaml`) |
| `-o, --output` | No | Output file path (default: `<name>-<version>.tar.gz`) |

**Example:**

```bash
$ pacto pack my-service/pacto.yaml
Packed my-service@0.1.0 -> my-service-0.1.0.tar.gz
```

The contract is validated before packing. If validation fails, no archive is created.

---

## `pacto push`

Push a validated contract bundle to an OCI registry.

```bash
pacto push <ref> [-p path]
```

**Arguments & flags:**

| Argument/Flag | Required | Description |
|----------|----------|-------------|
| `ref` | Yes | OCI reference (e.g., `ghcr.io/org/name:tag`) |
| `-p, --path` | No | Path to `pacto.yaml` (default: `pacto.yaml`) |

**Example:**

```bash
$ pacto push ghcr.io/acme/my-service-pacto:1.0.0 -p my-service/pacto.yaml
Pushed my-service@1.0.0 -> ghcr.io/acme/my-service-pacto:1.0.0
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
| `old` | Yes | Path or `oci://` reference to the old contract |
| `new` | Yes | Path or `oci://` reference to the new contract |

**Exit code:** Non-zero if breaking changes are detected.

**Example:**

```bash
$ pacto diff oci://ghcr.io/acme/svc-pacto:1.0.0 my-service/pacto.yaml
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
pacto graph [path | oci://ref]
```

**Arguments:**

| Argument | Required | Description |
|----------|----------|-------------|
| `path` | No | Path to `pacto.yaml` or `oci://` reference (default: `pacto.yaml`) |

**Example:**

```bash
$ pacto graph my-service/pacto.yaml
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
pacto explain [path | oci://ref]
```

**Arguments:**

| Argument | Required | Description |
|----------|----------|-------------|
| `path` | No | Path to `pacto.yaml` or `oci://` reference (default: `pacto.yaml`) |

---

## `pacto generate`

Generate artifacts from a contract using a plugin.

```bash
pacto generate <plugin> [path | oci://ref] [-o output]
```

**Arguments & flags:**

| Argument/Flag | Required | Description |
|----------|----------|-------------|
| `plugin` | Yes | Plugin name (looks for `pacto-plugin-<name>`) |
| `path` | No | Path to `pacto.yaml` or `oci://` reference (default: `pacto.yaml`) |
| `-o, --output` | No | Output directory (default: `<plugin>-output/`) |

**Example:**

```bash
$ pacto generate helm my-service/pacto.yaml -o manifests/
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

Credentials are stored in `~/.docker/config.json` using Docker's standard format.

---

## `pacto version`

Print version information.

```bash
$ pacto version
pacto version 1.0.0
```

---

## Authentication

Pacto follows the OCI credential resolution chain:

1. Explicit CLI flags (`--username`, `--password`)
2. Environment variables (`PACTO_REGISTRY_USERNAME`, `PACTO_REGISTRY_PASSWORD`, `PACTO_REGISTRY_TOKEN`)
3. Docker config (`~/.docker/config.json`)
4. Docker credential helpers
5. Cloud auto-detection (ECR, GCR, ACR)
6. Anonymous fallback

No credentials are ever stored in contract files.
