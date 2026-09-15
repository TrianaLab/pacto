# Tooling and adapters

The rest of [Package responsibilities](architecture.md#package-responsibilities):
overrides and packaging, the generators and servers, the application services and
the adapters to registries, agents and the terminal.

## `pkg/override` -- YAML overrides

Applies value-file and `--set` overrides to raw YAML before parsing. Supports deep merge, dot-separated paths and array index notation.

## `pkg/doc` -- Documentation generator

Renders the dashboard's single-service `ServiceDetails` snapshot as a Markdown document and as a self-contained static HTML site that reuses the embedded dashboard UI (`dashboard.EmbeddedUI()`) with the snapshot injected as `window.__PACTO_STATIC__`. It no longer re-derives content from the raw contract — the dashboard and the doc read the same model, so they cannot drift. `--serve` serves the static site, and `--ui swagger` serves the API explorer.

## `pkg/openapi` -- OpenAPI parser

A leaf package that parses OpenAPI specs into endpoint lists. Used by `pkg/dashboard` (interface endpoint tables), `pkg/capability` (MCP tool generation) and `internal/mcp`.

## `pkg/plugin` -- Plugin system

Out-of-process plugin execution via JSON stdin/stdout. Discovers plugin binaries and manages the communication protocol. See the [Plugin Development](plugins.md) guide.

## `pkg/dashboard` -- Dashboard server

The exploration and observability layer, and the largest core package: an HTTP server, multi-source aggregation, graph, compliance, Kubernetes client and embedded single-page app, which the operator also embeds. Its source model, resolution rules, section provenance and API are documented separately in [Dashboard architecture](dashboard-architecture.md).

## `pkg/lock` -- Lockfile builder and verifier

Deterministic lockfile model for dependency and reference closure tracking. Pure, dependency-light package (stdlib + `gopkg.in/yaml.v3` only).

- `Lock` -- root model with `Root`, `Dependencies[]`, `References[]`
- `Marshal()` -- stable-sorted, byte-identical deterministic serialization so re-marshaling an unchanged closure produces identical output
- `HashFS()` -- content hashing for local bundles (sha256 over sorted file list with length-prefixed paths and data)
- Typed errors: `DriftError` (OCI digest mismatch), `LocalDriftError` (local content changed), `StaleError` (pacto.yaml and pacto.lock disagree), `ConflictError` (conflicting version requirements), `UnresolvedError` (resolution failure), `MissingError` (lock required but absent)

Closure building (transitive dependencies and transitive config/policy references) lives in `internal/app`. Verification is wired into validate, graph, diff and push commands with go.sum-style hard-fail semantics. See the [Lockfile](lockfile.md) reference for the file format and workflow.

## `pkg/ignore` -- Bundle ignore matcher

Gitignore-style pattern matching for `.pactoignore` filtering. Determines which files are excluded from bundle packaging.

- `DefaultPatterns` -- `.git/`, `.pactoignore`, `.DS_Store` (a committed `pacto.lock` is intentionally NOT default-ignored — it ships inside the bundle)
- `alwaysKeep` guard -- ensures `pacto.yaml` is never ignorable regardless of user patterns
- `Matcher.Ignored()` -- ancestor-aware filtering (files inside ignored directories are themselves ignored)
- `FS()` -- filtering `fs.FS` wrapper applied at bundle load so pack, push and validation see one consistent file set

Supports gitignore syntax: comments (`#`), negation (`!`), directory-only (`/`), anchoring (`^/`), globs (`*`, `?`, `[]`) and double-star (`**`) for cross-segment matching. Last matching rule wins. See the [Packaging ignore](pactoignore.md) reference for details.

## `internal/app` -- Application services

Each CLI command maps to exactly one service method. This layer orchestrates `pkg/*` packages and infrastructure adapters. Methods are stateless: they take an options struct and return a result struct, never printing directly.

- `Init()`, `Validate()`, `Pack()`, `Push()`, `Pull()`
- `Diff()`, `Graph()`, `Explain()`, `Generate()`, `Doc()`, `Lock()`
- Shared helpers: `resolveBundle()`, `resolveBundleWithOverrides()`, `loadAndValidateLocal()`, `loadAndValidateFull()`

## `internal/cli` -- CLI layer

Cobra command handlers and Viper configuration. **Zero business logic** -- only input parsing, orchestration and output formatting.

## `pkg/oci` -- OCI adapter

Wraps `go-containerregistry` for OCI registry operations. Public package, imported by the operator. Pushes are content-addressed (immutable digest).

Key components:

- **`BundleStore`** interface -- the core abstraction: `Push()`, `Pull()`, `Resolve()`, `ListTags()`
- **`Client`** -- implements `BundleStore` using `go-containerregistry`. Translates between `contract.Bundle` and OCI images (tar.gz layer with metadata labels)
- **`CachedStore`** -- wraps any `BundleStore` with in-memory and disk caching. An entry is `~/.cache/pacto/oci/_v2/<escaped repo segments>/<escaped tag>/`, holding `bundle.tar.gz` and a `ref.json` sidecar naming the reference that bundle came from. The reserved `_v2/` segment keeps the layout disjoint from the pre-injective one it replaced: that key spelled every `:` as `/`, so `localhost:5000/demo/svc:1.0.0` and `localhost/5000/demo/svc:1.0.0` named one directory and overwrote each other. Entries in the old layout are still read, and only served when their sidecar names the reference asked for; nothing is written there. `--no-cache` stops disk *reads* -- writes continue, so a bundle pulled in the same session is still available for enrichment
- **`Resolver`** -- lazy version resolution with semver filtering. `Resolve()` pulls bundles in `LocalOnly` or `RemoteAllowed` mode. `FetchAllVersions()` pulls every semver tag to populate the cache. `FilterSemverTags()` selects valid semver tags sorted descending
- **Credential chain** -- `NewKeychain()` resolves credentials by priority order; see [CLI reference → Authentication](cli-reference.md#authentication) for the full chain
- **Typed errors** -- `AuthenticationError`, `ArtifactNotFoundError`, `RegistryUnreachableError`, `InvalidRefError`, `InvalidBundleError`, `NoMatchingVersionError`

## `internal/mcp` -- MCP server

Thin adapter layer that exposes Pacto operations as [Model Context Protocol](https://modelcontextprotocol.io) tools and resources. Each handler delegates to an `internal/app` service method or projects an already-built core model -- no business logic lives here. The server communicates over stdio (default) or HTTP (`pacto mcp -t http`) and is started via `pacto mcp`. Used by AI tools such as Claude, Cursor and Copilot. See the [MCP integration](mcp-integration.md) guide for setup.

One invocation selects exactly one server, and the modes cannot be combined:

| Invocation | Surface |
|------------|---------|
| `pacto mcp` | Authoring tools over `internal/app` |
| `pacto mcp <bundle-ref>` | Authoring tools plus one bundle's OpenAPI operations and skills (`pkg/capability`) |
| `pacto mcp --fleet` | Authoring tools plus read-only operational-graph queries (`pkg/fleet`) and `pacto_impact` blast-radius analysis (`pkg/impact`) |
| `pacto mcp --root <ref>` | Read-only contract catalog discovery (`pkg/catalog`) — this surface only |

Catalog mode builds the catalog once, before serving, from the repeated `--root` references. It is mostly MCP *resources* rather than tools, and the served session is frozen: handlers project the immutable `*Catalog` and reach neither a registry nor the filesystem. It is also the one mode that does **not** register the authoring tools: two of them write `pacto.yaml` to disk, so a mode whose whole promise is read-only discovery starts from a bare server and adds only the discovery surface.

## `pkg/logging` -- Contextual logger

Builds one `*slog.Logger` per CLI invocation in the root `PersistentPreRunE` (`internal/cli/root.go`) and carries it on the command context. Call sites obtain it with `logging.LoggerFromContext`, which falls back to `slog.Default()` when no logger is on the context (library callers, tests, the operator). `--verbose` selects debug level and writes to the command's stderr; otherwise only warnings and above are emitted. The logger is deliberately **not** a process global -- nothing calls `slog.SetDefault()` -- so concurrent in-process `Execute` calls never race on a shared logger.

## `internal/update` -- Update checker

Performs async version checking against the GitHub releases API. Started in a background goroutine during CLI initialization, with a 200ms timeout to avoid blocking. Results are cached on disk for 24 hours (`~/.config/pacto/update-check.json`) to minimize API calls. Suppressed for dev builds and JSON output mode.
