# Cron Worker

A Pacto contract for a scheduled batch job — a stateless worker that runs on a cron schedule.

```yaml
pactoVersion: "2.0"

service:
  name: report-generator
  version: 1.2.0
  owner:
    team: analytics

interfaces:
  - name: health
    type: openapi
    ref: interfaces/health.yaml
    visibility: internal

workload: scheduled

state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low

capabilities:
  - type: health
    binding:
      type: http
      interface: health
      path: /ready

metadata:
  schedule: "0 2 * * *"
  timeout: 3600
```

## Key decisions

- **`workload: scheduled`** — runs on a cron schedule, not continuously
- **No replica scaling** — v2 has no `scaling` section; batch workloads scale as a deployment concern, outside the contract
- **Schedule in `metadata`** — the cron expression is platform-specific, so it lives in `metadata`, not the contract's core fields
- **Health as a capability** — the readiness probe binds to the `health` interface (see [capabilities](../contract-reference/sections.md#capabilities))

## Variant: One-shot job

For a job that runs once (e.g., a database migration):

```yaml
workload: job

state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low

capabilities:
  - type: health
    binding:
      type: http
      interface: health
      path: /ready
```
