# Configurations as composable claims

**Problem.** A single deployable needs several distinct configuration inputs — Helm values for the chart, claim values for a database, declared keys for a secret store. Each has a different schema. You want all of them validated together at CI time, supplied through one file per environment, with no parallel `claims/` or `values/` directories to keep in sync.

**Primitives.**

- **`configurations` is an array.** Each entry is independently resolved against its own schema (`schema:` local file, or `ref:` another contract)
- **Override files** ([Contract overrides](../contract-reference/overrides.md#contract-overrides)) replace the array wholesale per environment

## One file, multiple typed outputs

```yaml
# pactos/my-service-api/overrides/values.stg.yaml
# Value-carrying entries use a local (vendored) schema — `ref:` and `values:`
# are mutually exclusive, so a schema you supply values for must live in the bundle.
configurations:
  - name: deployment
    required: true
    schema: configuration/deployment/schema.json
    values:
      replicas: 3
      resources:
        requests:
          cpu: 1000m
          memory: 1Gi

  - name: postgres
    required: true
    schema: configuration/postgres/schema.json
    values:
      instances: 2
      size: medium
      backups:
        enabled: true
        schedule: "0 */6 * * *"

  - name: secrets
    required: true
    schema: configuration/secrets/schema.json
    values:
      secrets:
        - key: api-key
        - key: openai-token
```

`pacto validate -f overrides/values.stg.yaml` validates each entry's `values` against its local `schema`. Your deployment tooling reads the same file and produces:

- Helm values (`deployment` entry → values nested under the component's chart alias)
- A Postgres claim (`postgres` entry → fields land in the claim's `spec`)
- A secret-store claim (`secrets` entry → declared keys provisioned)

**Each value is written once** — no drift between a chart's `values.yaml` and a separate `claims/postgres.yaml`.

!!! info
    Override files use **Helm-style array replacement** for `configurations` — the override's array replaces the contract's array entirely, not merged by name (see [Contract overrides](../contract-reference/overrides.md#contract-overrides)). Each override file must therefore include every configuration it cares about, each with its `name` and `required` plus either a `schema` (with `values`) or a `ref` (schema-only) — omitting `required` is rejected with a `PARSE_ERROR` naming the missing property. Inline `values` require a local `schema`; a `ref`-based entry carries neither.

## One file per environment

The same component runs in dev, staging and production with different replica counts, resource limits, database sizes and secret sets. One override file per component per environment keeps each environment self-contained and validated against the same schemas, without a parallel Helm `values.yaml` to maintain:

```text
pactos/my-service-api/
├── pacto.yaml
└── overrides/
    ├── values.dev.yaml
    ├── values.stg.yaml
    └── values.prod.yaml
```

Each file lists every configuration the component cares about for that environment:

```yaml
# overrides/values.prod.yaml
configurations:
  - name: deployment
    required: true
    schema: configuration/deployment/schema.json
    values:
      replicas: 5
      resources:
        requests: { cpu: 2000m, memory: 2Gi }

  - name: postgres
    required: true
    schema: configuration/postgres/schema.json
    values:
      instances: 3
      size: large
      backups:
        enabled: true
        schedule: "0 */4 * * *"
```

Each file is validated independently by `pacto validate -f overrides/values.<env>.yaml`, so a change scopes to one component — a typo in staging Postgres can't break an unrelated one, and reviewers see a small diff.

## Precedence

Contract inline `values` → `-f overrides/values.<env>.yaml` → `--set` (wins). Use inline values for cross-environment defaults, override files for environment-specific values and reserve `--set` for values your platform tooling controls (image tag, namespace, deploy-time labels). See [Precedence](../contract-reference/overrides.md#precedence) for the full chain.

**Cross-links:** [`configurations`](../contract-reference/configuration-and-policy.md#configurations) · [Contract overrides](../contract-reference/overrides.md#contract-overrides) · [Precedence](../contract-reference/overrides.md#precedence) · [Environment-specific values files](../contract-reference/overrides.md#environment-specific-values-files)

---
