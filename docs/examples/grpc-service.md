# gRPC Service

A Pacto contract for a gRPC microservice — a user service exposing a Protocol Buffer API with internal visibility.

```yaml
pactoVersion: "1.2"

service:
  name: user-service
  version: 3.2.0
  owner:
    team: identity
  image:
    ref: ghcr.io/acme/user-service:3.2.0
    private: true

interfaces:
  - name: grpc-api
    type: grpc
    port: 9090
    visibility: internal
    contract: interfaces/user-service.proto

  - name: metrics
    type: http
    port: 9102
    visibility: internal

configurations:
  - name: default
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

runtime:
  workload: service

  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low

  lifecycle:
    upgradeStrategy: rolling
    gracefulShutdownSeconds: 15

  health:
    interface: grpc-api

  metrics:
    interface: metrics
    path: /metrics

scaling:
  min: 3
  max: 12

metadata:
  tier: critical
```

### Key decisions

- **`type: grpc` with `contract`** — the `.proto` file is bundled in the OCI artifact, so the API contract travels with the service version (see [interfaces](../contract-reference/sections.md#interfaces) for the supported types)
- **Health on gRPC** — a `grpc` health interface needs no `path`; the service is expected to implement the [gRPC Health Checking Protocol](https://github.com/grpc/grpc/blob/master/doc/health-checking.md) (see [runtime.health](../contract-reference/sections.md#runtimehealth))
- **Separate metrics port** — the gRPC port serves application traffic while a separate HTTP port exposes Prometheus metrics
- **`stateless`** — the service itself holds no state; data lives in PostgreSQL (the `postgres` dependency resolves to the [PostgreSQL example](postgresql.md))
