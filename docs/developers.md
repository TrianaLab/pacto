# Pacto for Developers

You own the service — and you own the contract. Pacto gives you a structured way to declare your service's operational contract alongside your code, so platform engineers, CI systems and other teams have an accurate, machine-readable description of what your service needs to run.

Already have a contract and want a single answer? [Quickstart](quickstart.md) is
the five-minute path, the [CLI reference](cli-reference.md) is every command and
flag, and [contract sections](contract-reference/sections.md) is every field.

---

## Your workflow

```mermaid
flowchart LR
    A[Write code] --> B[Infer schemas]
    B --> C[Define pacto.yaml]
    C --> D[pacto validate]
    D --> F[pacto push]
    F --> G[CI / Platform picks it up]
```

### 1. Initialize your contract

```bash
pacto init my-service
```

This scaffolds a bundle with a valid contract. Edit `pacto.yaml` to match your service.

### 2. Infer schemas from your code (optional)

If your config already ships a JSON Schema — for example your Helm chart's `values.schema.json` — vendor that file into your bundle and point `configurations[].schema` at it. If it doesn't, the `schema-infer` plugin generates one from a config file.

#### Generating a configuration schema

The `--option file=` path is resolved relative to the **bundle directory**, not your shell's working directory, so keep the config file inside the bundle. With `config.yaml` in `my-service/`:

```bash
pacto generate schema-infer my-service --option file=config.yaml -o my-service
```

This generates `my-service/config.schema.json`. Reference it in your contract:

```yaml
configurations:
  - name: default
    required: true
    schema: config.schema.json
```

!!! warning "Secret leak trap"
    The plugin leaves your `config.yaml` sitting in the bundle, and **a bundle
    directory is published whole**: `pacto pack` and `pacto push` upload every file
    under it, so `pacto pull` hands your config values, secrets included, to anyone
    who can read the artifact. The input is not part of the contract — the schema
    inferred from it is. Delete it once the schema exists, or list it in
    [`.pactoignore`](pactoignore.md) if you want to keep it beside the contract.

#### Generating an OpenAPI spec

If your service exposes an HTTP API using FastAPI or Huma, use the `openapi-infer` plugin to extract an OpenAPI 3.1 spec from your source code:

```bash
# Auto-detect framework — FastAPI or Huma only (generates interfaces/openapi.yaml)
pacto generate openapi-infer my-service -o my-service

# Override framework detection
pacto generate openapi-infer my-service -o my-service --option framework=fastapi
```

Both plugins live in a separate repository and are **not** part of the `pacto` binary. The installer script fetches them alongside the CLI; `go install` and `make build` do not. See [Official plugins](plugins.md#official-plugins) for how to get them.

### 3. Define the contract

See [Contract sections](contract-reference/sections.md) for interfaces, workload, state and capabilities; [Dependencies and state](contract-reference/dependencies-and-state.md) for dependencies; and [Configuration and policy](contract-reference/configuration-and-policy.md) for configurations and policies.

### 4. Validate and push

```bash
pacto validate my-service
pacto push oci://ghcr.io/your-org/my-service-pacto -p my-service
```

See [Validation layers](contract-reference/validation.md) for the rules and error codes. `push` validates before uploading; use `--force` to overwrite an existing tag.

---

## See also

- [Contract reference](contract-reference/index.md) — every field, precisely
- [Composition patterns](patterns/index.md) — the compositions these primitives
  are usually assembled into
- [CLI reference](cli-reference.md) — every command and flag
- [For platform engineers](platform-engineers.md) — the other side of the same
  contract
