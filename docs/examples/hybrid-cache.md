---
title: Hybrid Cache API
layout: default
parent: Examples
nav_order: 8
---

# Hybrid Cache API

A Pacto contract for a service with hybrid state — an API that caches data locally for performance but can rebuild its cache from an upstream source. Loss of local state degrades performance but does not break the service.

```yaml
pactoVersion: "1.0"

service:
  name: product-catalog
  version: 2.0.1
  owner: team/catalog
  image:
    ref: ghcr.io/acme/product-catalog:2.0.1
    private: true

interfaces:
  - name: rest-api
    type: http
    port: 8080
    visibility: public
    contract: interfaces/openapi.yaml

  - name: metrics
    type: http
    port: 9090
    visibility: internal

configurations:
  - name: default
    schema: configuration/schema.json
    values:
      UPSTREAM_API: https://inventory.internal/api
      CACHE_MAX_SIZE_MB: 512
      CACHE_TTL_SECONDS: 3600
      WARMUP_ON_START: true
      API_KEY: secret://vault/product-catalog/upstream-api-key

dependencies:
  - name: inventory
    ref: oci://ghcr.io/acme/inventory-pacto@sha256:789abc
    required: true
    compatibility: "^1.0.0"

runtime:
  workload: service

  state:
    type: hybrid
    persistence:
      scope: local
      durability: persistent
    dataCriticality: low

  lifecycle:
    upgradeStrategy: rolling
    gracefulShutdownSeconds: 10

  health:
    interface: rest-api
    path: /health

  metrics:
    interface: metrics
    path: /metrics

scaling:
  min: 2
  max: 6

metadata:
  team: catalog
  tier: standard
  cache-strategy: write-through
```

### Key decisions

- **`state.type: hybrid`** — the service caches product data locally for fast reads, but can reconstruct the cache from the upstream inventory service on restart
- **`durability: persistent`** — persisting the cache across restarts avoids cold-start latency, but the service functions correctly without it (it just needs time to warm up)
- **`dataCriticality: low`** — the cache is reconstructible; losing it has no business impact beyond temporary performance degradation
- **`upgradeStrategy: rolling`** — rolling updates prevent all instances from cold-starting simultaneously
- **Secret reference** — the API key for the upstream service uses `secret://` so credentials never appear in the contract

### When to use `hybrid`

Use `hybrid` when your service:

- Maintains local state that **improves** behavior (caches, pre-computed indexes, session stores)
- Can **recover** from state loss by rebuilding from an upstream source
- Would experience **degraded performance** but not **failure** if local state is lost

If state loss would break the service, use `stateful` instead.
