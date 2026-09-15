# Day-to-day with Pacto

Working with a contract once it exists: overriding its values, diffing it before
a release, driving it from an AI assistant and shipping `docs/` and `sbom/`
inside the bundle. The eight steps that produce the contract are in
[Pacto for Developers](developers.md).

---

## Using contract overrides

Pacto supports Helm-style overrides to modify contract values without editing `pacto.yaml`. This is useful for environment-specific values, CI pipelines or quick experimentation.

```bash
# Override a value inline
pacto validate my-service --set service.version=2.0.0

# Use a values file (-f is short for --values on most commands)
pacto validate my-service -f staging-values.yaml

# Combine both (--set takes precedence)
pacto validate my-service -f staging-values.yaml --set service.version=3.0.0

# Set configuration values
pacto validate my-service --set configurations[0].values.DB_HOST=localhost
```

Overrides work on every command that takes a contract reference, with two exceptions: `diff` overrides each side with `--old-values`/`--old-set` and `--new-values`/`--new-set` (it has no `-f` or plain `--values`), and `pacto push` reserves `-f` for `--force`, so spell out `--values` there.

For the per-command flag list see the [CLI reference](cli-reference.md); for override precedence and syntax see the [Contract Reference — Contract overrides](contract-reference/overrides.md#contract-overrides).

---

## Detecting breaking changes

Before releasing a new version, diff against the previous one:

```bash
$ pacto diff oci://ghcr.io/acme/my-service-pacto:1.0.0 my-service
Classification: BREAKING
Changes (2):
  [NON_BREAKING] service.version (modified): service.version modified [1.0.0 -> 1.1.0]
  [BREAKING] interfaces (removed): interfaces removed [- metrics]
breaking changes detected
$ echo $?
1
```

`pacto diff` exits non-zero exactly when the classification is `BREAKING` — that exit code is what makes it usable as a CI gate. Wire it in to block merges that introduce breaking changes — see the official [Pacto CLI action](github-actions.md).

---

## AI-assisted workflow

If you use an AI assistant that supports [MCP](https://modelcontextprotocol.io) (Claude Code, Cursor and GitHub Copilot), connect it to Pacto so it can scaffold, edit and validate contracts inside your conversation. Plain `pacto mcp` exposes four authoring tools, and they stay registered when you also point the server at a bundle or pass `--fleet`:

- **`pacto_create`** — scaffold a new contract from a description
- **`pacto_edit`** — modify an existing contract
- **`pacto_check`** — validate a local contract and return a summary plus improvement suggestions
- **`pacto_schema`** — return the full contract JSON Schema reference

Point the server at a bundle (`pacto mcp <bundle-ref>`) and it also exposes that bundle's OpenAPI operations as executable tools plus a `pacto_skill` tool for any bundled `skills/*.md` — see [Agent capabilities](mcp-agent-capabilities.md).

Inspecting a registry contract, resolving dependency graphs and generating Markdown docs are CLI-only (`pacto explain oci://...`, `pacto graph`, `pacto doc`) — they are not MCP tools.

`pacto_create` and `pacto_edit` write contract files, and Pacto advertises no MCP annotations that would let your client tell them apart from the read-only tools — so no confirmation prompt precedes a write. See [The boundary is documented, not machine-advertised](mcp-integration.md#three-tool-families-and-their-boundaries).

See the [MCP Integration](mcp-integration.md) guide for the `.mcp.json` setup across all clients.

---

## Including documentation

You can include an optional `docs/` directory in your bundle to ship human-readable documentation alongside the contract:

```text
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

Documentation ships inside the OCI artifact, versioned and distributed with the contract; it never affects validation or diffing. See the [Contract Reference — `docs/`](contract-reference/index.md#docs-optional-documentation) for the full behavior.

---

## Including an SBOM

You can include an optional `sbom/` directory in your bundle to ship a Software Bill of Materials alongside the contract:

```text
my-service/
  pacto.yaml
  interfaces/
    openapi.yaml
  sbom/
    sbom.spdx.json
```

Generate one with [Syft](https://github.com/anchore/syft) (or [Trivy](https://github.com/aquasecurity/trivy)/[cdxgen](https://github.com/CycloneDX/cdxgen)):

```bash
# Generate an SPDX SBOM
syft . -o spdx-json=sbom/sbom.spdx.json

# Or generate a CycloneDX SBOM
syft . -o cyclonedx-json=sbom/bom.cdx.json
```

Pacto discovers the SBOM by scanning `sbom/` for recognized extensions — no contract field references it. For the supported formats (SPDX 2.3, CycloneDX 1.5) and how `pacto diff` reports package-level changes, see the [Contract Reference — `sbom/`](contract-reference/index.md#sbom-optional-software-bill-of-materials).
