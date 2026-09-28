# Plugin Development

Pacto uses an out-of-process plugin architecture for artifact generation. A plugin is a standalone executable that receives a contract via JSON on stdin and writes generated file descriptions to stdout. A plugin can turn a contract into any artifact — Helm charts, Terraform, Kubernetes manifests — in any language.

---

## Official plugins

Pacto has two official plugins:

| Plugin | Description |
|--------|-------------|
| **pacto-plugin-schema-infer** | Infers a JSON Schema from sample configuration files (JSON, YAML, TOML) |
| **pacto-plugin-openapi-infer** | Extracts OpenAPI 3.1 specs from source code (FastAPI, Huma) |

Both run *inward*: they derive interfaces you already have rather than asking you to hand-author a schema. **No deployment-artifact plugin ships with Pacto.** `pacto generate helm` is a plugin you write or install, not one Pacto provides. Without a `pacto-plugin-helm` on your `PATH` it exits 1 with `plugin "helm" not found`.

See [Installation](installation.md#installing-the-official-plugins) for how to install them. Refer to each plugin's README for usage:

- [pacto-plugin-schema-infer](https://github.com/TrianaLab/pacto-plugins/tree/main/plugins/pacto-plugin-schema-infer)
- [pacto-plugin-openapi-infer](https://github.com/TrianaLab/pacto-plugins/tree/main/plugins/pacto-plugin-openapi-infer)

---

## Why plugins?

Pacto describes *what* a service is. Plugins decide *how* to deploy it. The contract is the input; deployment artifacts are the output.

Plugins run in both directions: the `*-infer` plugins run inward (composing interfaces you already have into the contract), while generate plugins run outward (turning the contract into deployment artifacts). One schema describes a single interface; the contract describes how those interfaces relate and change.

The plugin design is:

- **Language-agnostic** — write plugins in Go, Python, Rust, Bash or anything
- **Version-independent** — plugins don't link against Pacto libraries
- **Isolated input** — the plugin gets a serialized, read-only snapshot of the contract on stdin and cannot mutate Pacto's state; Pacto bounds it with a timeout and output cap but does not OS-sandbox the process

---

## How it works

```mermaid
sequenceDiagram
    participant User
    participant Pacto as pacto CLI
    participant Plugin as pacto-plugin-schema-infer

    User->>Pacto: pacto generate schema-infer ./my-service
    Pacto->>Pacto: Load and parse contract
    Pacto->>Plugin: Spawn process, write JSON to stdin
    Plugin->>Plugin: Read contract, generate files
    Plugin->>Pacto: Write JSON response to stdout
    Pacto->>Pacto: Write files to output directory
    Pacto->>User: Generated 1 file(s) using schema-infer
```

1. The user runs [`pacto generate <plugin-name> [dir | oci://ref]`](cli-reference.md#pacto-generate) — see the command reference for flags like `--option key=value` (populates `options`) and `-o/--output`
2. Pacto loads and parses the contract
3. Pacto finds the plugin binary (`pacto-plugin-<name>`)
4. Pacto writes a `GenerateRequest` JSON to the plugin's stdin
5. The plugin reads the request, generates artifacts, and writes a `GenerateResponse` JSON to stdout
6. Pacto reads the response and writes the generated files to disk

---

## Plugin discovery

Pacto searches for plugin binaries in this order:

1. **`$PATH`** — any binary named `pacto-plugin-<name>`
2. **`~/.config/pacto/plugins/`** — user plugin directory

For example, `pacto generate schema-infer` looks for:

- `pacto-plugin-schema-infer` in `$PATH`
- `~/.config/pacto/plugins/pacto-plugin-schema-infer`

Neither found is an error, not a no-op — `pacto generate helm` on a machine with
no `pacto-plugin-helm` exits 1 and says so:

```text
plugin "helm" not found (looked for pacto-plugin-helm in $PATH and ~/.config/pacto/plugins/)
```

---

## Protocol (v1)

### Request (stdin)

Pacto writes a JSON object to the plugin's stdin:

```json
{
  "protocolVersion": "1",
  "contract": {
    "pactoVersion": "2.0",
    "service": {
      "name": "my-service",
      "version": "1.0.0"
    },
    "interfaces": [...],
    "workload": "service",
    "state": {...},
    ...
  },
  "bundleDir": "my-service",
  "outputDir": "helm-output",
  "options": {
    "namespace": "production"
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `protocolVersion` | string | Always `"1"` for the current protocol |
| `contract` | object | The full parsed contract (same structure as `pacto.yaml`) |
| `bundleDir` | string | Path to the bundle directory, **as resolved by the CLI** — for a local contract this is the reference the user typed, so it may be relative; for an `oci://` reference it is an absolute temp directory that Pacto deletes when the plugin exits. Treat as read-only (Pacto does not enforce that). |
| `outputDir` | string | Path where output files should go — `-o/--output` verbatim, or `<plugin-name>-output` when the flag is omitted. **May be relative**, and Pacto creates the directory before spawning the plugin. |
| `options` | object | *Optional.* User-provided key-value options (from `--option key=value`). **Omitted entirely** when no `--option` is passed, so read it defensively: `request.get("options", {})`. |

A relative `bundleDir` or `outputDir` is relative to Pacto's working directory,
which the plugin process inherits. A plugin must therefore not `chdir` before it
resolves either path, and must not assume either one is absolute.

The contract object mirrors [`pacto.yaml`](contract-reference/index.md) exactly.

### Response (stdout)

The plugin writes a JSON object to stdout:

```json
{
  "files": [
    {
      "path": "deployment.yaml",
      "content": "apiVersion: apps/v1\nkind: Deployment\n..."
    },
    {
      "path": "service.yaml",
      "content": "apiVersion: v1\nkind: Service\n..."
    }
  ],
  "message": "Generated Kubernetes manifests for my-service"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `files` | array | List of generated files |
| `files[].path` | string | Relative path within the output directory |
| `files[].content` | string | File content |
| `message` | string | Optional message displayed to the user |

#### Path safety

`files[].path` must be a relative path within the output directory. Absolute paths and paths containing `..` are rejected. Always return clean relative paths.

### Errors

If the plugin encounters an error, it should:

1. Write a message to **stderr**
2. Exit with a non-zero exit code

Pacto captures stderr and presents it to the user.

### Execution limits

Pacto bounds plugin execution defensively: a plugin is killed if it runs longer
than 60 seconds, and its stdout is capped at 64 MB (output beyond the cap is an
error). Keep plugins fast and bounded; long-running work should be split or
streamed differently.

---

## Example: Minimal plugin in Bash

```bash
#!/usr/bin/env bash
# pacto-plugin-readme — Generates a README from a Pacto contract

set -euo pipefail

# Read the full JSON request from stdin
REQUEST=$(cat)

# Extract fields using jq
NAME=$(echo "$REQUEST" | jq -r '.contract.service.name')
VERSION=$(echo "$REQUEST" | jq -r '.contract.service.version')
WORKLOAD=$(echo "$REQUEST" | jq -r '.contract.workload // "n/a"')
STATE=$(echo "$REQUEST" | jq -r '.contract.state.type // "n/a"')

# Generate a README
CONTENT="# ${NAME}

**Version:** ${VERSION}
**Workload:** ${WORKLOAD}
**State:** ${STATE}

This file was auto-generated by pacto-plugin-readme.
"

# Write the response JSON to stdout
jq -n \
  --arg path "README.md" \
  --arg content "$CONTENT" \
  --arg msg "Generated README for ${NAME}" \
  '{files: [{path: $path, content: $content}], message: $msg}'
```

Make it executable and place it in your `$PATH`:

```bash
chmod +x pacto-plugin-readme
mv pacto-plugin-readme /usr/local/bin/
pacto generate readme my-service
```

---

## Guidelines

- **Read only from `bundleDir`.** Do not access files outside the bundle.
- **Write only to stdout.** Do not write files directly; return them in the response. Pacto handles file creation.
- **Follow the protocol.** Return clean relative paths, write errors to stderr and exit non-zero on failure.
- **Be deterministic.** Given the same input, produce the same output.
- **Handle missing optional fields.** Only `pactoVersion` and `service` are required — not all contracts have `workload`, `state`, `configurations`, `dependencies` or `capabilities`.
