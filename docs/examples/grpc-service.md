---
title: gRPC Service
layout: default
parent: Examples
nav_order: 7
---

# gRPC Service

A Pacto contract for a gRPC microservice — a user service exposing a Protocol Buffer API with internal visibility.

```yaml
pactoVersion: "1.0"

service:
  name: user-service
  version: 3.2.0
  owner: team/identity
  image:
    ref: ghcr.io/acme/user-service:3.2.0
    private: true

interfaces:
  - name: grpc-api
    type: grpc
    port: 9090
    visibility: internal
    contract: interfaces/user-service.proto

  - name: health
    type: http
    port: 8080
    visibility: internal

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
    ref: oci://ghcr.io/acme/postgres-pacto@sha256:def456
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
  team: identity
  tier: critical
```

### Key decisions

- **`type: grpc` with `contract`** — the `.proto` file is bundled in the OCI artifact, making the API contract portable and versionable
- **Health on gRPC** — when the health interface is `grpc`, Pacto uses the [gRPC Health Checking Protocol](https://github.com/grpc/grpc/blob/master/doc/health-checking.md); no `path` is needed
- **Separate health and metrics** — the gRPC port serves application traffic, while HTTP ports expose health checks and Prometheus metrics independently
- **`stateless`** — the service itself holds no state; data lives in PostgreSQL
