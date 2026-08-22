# Override files as the deployment surface

**Problem.** The same component runs in dev, staging and production with different replica counts, resource limits, database sizes and secret sets. You want each environment self-contained, validated against the same schemas and not maintained alongside Helm `values.yaml` files.

**Primitives.** YAML files per environment, applied with `-f` (see [Contract overrides](../contract-reference/overrides.md#contract-overrides)). One file per component per environment.

**Layout.**

```
pactos/my-service-api/
├── pacto.yaml
└── overrides/
    ├── values.dev.yaml
    ├── values.stg.yaml
    └── values.prod.yaml
```

Each file is self-contained and follows the same shape as [pattern 3](composable-configs.md) — it lists every configuration the component cares about for that environment, value-carrying entries using a local `schema:` (a `ref:` entry is schema-only):

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

**Precedence.** Contract inline `values` → `-f overrides/values.<env>.yaml` → `--set` (wins). Use inline values for cross-environment defaults, override files for environment-specific values and reserve `--set` for values your platform tooling controls (image tag, namespace, deploy-time labels). See [Precedence](../contract-reference/overrides.md#precedence) for the full chain.

**Cross-links:** [Contract overrides](../contract-reference/overrides.md#contract-overrides) · [Precedence](../contract-reference/overrides.md#precedence) · [Environment-specific values files](../contract-reference/overrides.md#environment-specific-values-files)

---
