# Redis

A Pacto contract for Redis — a stateful in-memory data store with persistent durability.

```yaml
pactoVersion: "2.0"

service:
  name: redis
  version: 7.4.0
  owner:
    team: caching

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
  dataCriticality: medium

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
  tier: high
  eviction-policy: allkeys-lru
```

!!! note
    Redis's RESP wire protocol is not an `openapi`/`asyncapi`/`grpc` spec, so it is not declared as an interface — the only declared interface is the HTTP endpoint the metrics exporter exposes, which the `health` and `metrics` capabilities bind to (see [interface types](../contract-reference/sections.md#interface-types)).

### Key decisions

- **`state.type: stateful`** with **`durability: persistent`** — Redis with AOF/RDB persistence enabled needs durable storage
- **`dataCriticality: medium`** — data is important but can be rebuilt from source if needed
- **Capabilities over ports** — health and metrics are declared as [capabilities](../contract-reference/sections.md#capabilities) bound to the exporter interface, not as ports

### Variant: Ephemeral cache

For a pure cache without persistence:

```yaml
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
```
