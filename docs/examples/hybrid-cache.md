# Hybrid Cache API

A Pacto contract for a service with hybrid state — an API that caches data locally for performance but can rebuild its cache from an upstream source. Loss of local state degrades performance but does not break the service.

```yaml
pactoVersion: "2.0"

service:
  name: product-catalog
  version: 2.0.1
  owner:
    team: catalog

interfaces:
  - name: rest-api
    type: openapi
    ref: interfaces/openapi.yaml
    visibility: public

  - name: metrics
    type: openapi
    ref: interfaces/metrics.yaml
    visibility: internal

configurations:
  - name: default
    required: true
    schema: configuration/schema.json
    values:
      UPSTREAM_API: https://inventory.internal/api
      CACHE_MAX_SIZE_MB: 512
      CACHE_TTL_SECONDS: 3600
      WARMUP_ON_START: true
      API_KEY: secret://vault/product-catalog/upstream-api-key

dependencies:
  - name: inventory
    ref: oci://ghcr.io/acme/inventory-pacto@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
    required: true
    compatibility: "^1.0.0"

workload: service

state:
  type: hybrid
  persistence:
    scope: local
    durability: persistent
  dataCriticality: low

capabilities:
  - type: health
    binding:
      type: http
      interface: rest-api
      path: /health
  - type: metrics
    binding:
      type: http
      interface: metrics
      path: /metrics

metadata:
  tier: standard
  cache-strategy: write-through
```

### Key decisions

- **`state.type: hybrid`** — the service caches product data locally for fast reads, but can reconstruct the cache from the upstream inventory service on restart
- **`durability: persistent`** — persisting the cache across restarts avoids cold-start latency, but the service still works without it (it just warms up first)
- **`dataCriticality: low`** — the cache is reconstructible; losing it has no business impact beyond temporary performance degradation
- **Capabilities over ports** — health binds to the public `rest-api` interface and metrics to a separate internal interface (see [capabilities](../contract-reference/sections.md#capabilities))
- **Secret reference** — the API key for the upstream service uses `secret://` so credentials never appear in the contract

### When to use `hybrid`

Choose `hybrid` (not `stateless`) when you persist local state across restarts to avoid warm-up but the service still functions after losing it — a purely in-memory cache rebuilt on every start is `stateless`/`ephemeral`. See the [state.type table](../contract-reference/sections.md#state) in the contract reference for the full `stateless` vs `stateful` vs `hybrid` breakdown.
