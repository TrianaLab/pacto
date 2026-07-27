# PostgreSQL

A Pacto contract for PostgreSQL — a stateful, persistent relational database with high data criticality.

```yaml
pactoVersion: "2.0"

service:
  name: postgresql
  version: 16.4.0
  owner:
    team: databases

interfaces:
  - name: metrics
    type: openapi
    ref: interfaces/metrics.yaml
    visibility: internal

configurations:
  - name: default
    schema: configuration/schema.json
    required: true

workload: service

state:
  type: stateful
  persistence:
    scope: local
    durability: persistent
  dataCriticality: high

capabilities:
  - type: health
    binding:
      type: http
      interface: metrics
      path: /health
  - type: metrics
    binding:
      type: http
      interface: metrics
      path: /metrics

metadata:
  tier: critical
  backup: required
  storage-class: ssd
```

!!! note
    PostgreSQL's binary wire protocol is not an `openapi`/`asyncapi`/`grpc` spec, so it is not declared as an interface — the only declared interface is the HTTP endpoint the metrics exporter exposes, which the `health` and `metrics` capabilities bind to (see [interface types](../contract-reference/sections.md#interface-types)).

### Key decisions

- **`state.type: stateful`** with **`durability: persistent`** — persistent storage survives pod restarts
- **`dataCriticality: high`** — backups and strict disruption budgets expected
- **Single instance** — replication is handled externally; replica counts are a deployment concern, not part of the contract
- **Capabilities over ports** — health and metrics are declared as [capabilities](../contract-reference/sections.md#capabilities) bound to the exporter interface
