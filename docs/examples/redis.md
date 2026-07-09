# Redis

A Pacto contract for Redis — a stateful in-memory data store with persistent durability.

```yaml
pactoVersion: "1.2"

service:
  name: redis
  version: 7.4.0
  owner:
    team: caching
  image:
    ref: docker.io/library/redis:7.4
    private: false

interfaces:
  - name: resp
    type: grpc
    port: 6379
    visibility: internal
    contract: interfaces/redis-resp.proto

  - name: metrics
    type: http
    port: 9121
    visibility: internal

configurations:
  - name: default
    schema: configuration/schema.json

runtime:
  workload: service

  state:
    type: stateful
    persistence:
      scope: local
      durability: persistent
    dataCriticality: medium

  lifecycle:
    upgradeStrategy: ordered
    gracefulShutdownSeconds: 30

  health:
    interface: metrics
    path: /health

  metrics:
    interface: metrics
    path: /metrics

scaling:
  replicas: 1

metadata:
  tier: high
  eviction-policy: allkeys-lru
```

!!! note
    The `resp` interface uses `type: grpc` as the closest available protocol type for Redis's RESP binary protocol — Pacto has no dedicated `tcp` type (see [interface fields](../contract-reference.md#interfaces) for the supported types). The `.proto` here is illustrative; omit the whole interface if you have no schema to publish.

### Key decisions

- **`state.type: stateful`** with **`durability: persistent`** — Redis with AOF/RDB persistence enabled needs durable storage
- **`dataCriticality: medium`** — data is important but can be rebuilt from source if needed
- **`upgradeStrategy: ordered`** — prevents data loss during upgrades

### Variant: Ephemeral cache

For a pure cache without persistence:

```yaml
runtime:
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
```
