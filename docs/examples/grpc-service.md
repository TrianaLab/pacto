# gRPC Service

A Pacto contract for a gRPC microservice — a user service exposing a gRPC API with internal visibility.

```yaml
pactoVersion: "2.0"

service:
  name: user-service
  version: 3.2.0
  owner:
    team: identity

interfaces:
  - name: grpc-api
    type: grpc
    ref: interfaces/user-service.yaml
    visibility: internal

  - name: metrics
    type: openapi
    ref: interfaces/metrics.yaml
    visibility: internal

configurations:
  - name: default
    required: true
    schema: configuration/schema.json
    values:
      DB_HOST: user-db.internal
      DB_PORT: 5432
      DB_PASSWORD: secret://vault/user-service/db-password
      CACHE_TTL_SECONDS: 300

dependencies:
  - name: postgres
    ref: oci://ghcr.io/acme/postgres-pacto@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
    required: true
    compatibility: "^16.0.0"

workload: service

state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low

capabilities:
  - type: health
  - type: metrics
    binding:
      type: http
      interface: metrics
      path: /metrics

metadata:
  tier: critical
```

### Key decisions

- **`type: grpc`** — the gRPC service descriptor is bundled in the OCI artifact, so the API contract travels with the service version. The `ref` file must parse as JSON or YAML (see [interface types](../contract-reference/sections.md#interface-types))
- **Health as a capability with no binding** — a `health` capability without an HTTP binding declares the service implements the [gRPC Health Checking Protocol](https://github.com/grpc/grpc/blob/master/doc/health-checking.md) rather than an HTTP probe (see [capabilities](../contract-reference/sections.md#capabilities))
- **Metrics over HTTP** — the gRPC interface serves application traffic while the `metrics` capability binds to a separate HTTP interface exposing Prometheus metrics
- **`stateless`** — the service itself holds no state; data lives in PostgreSQL (the `postgres` dependency resolves to the [PostgreSQL example](postgresql.md))
