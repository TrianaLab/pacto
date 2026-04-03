---
title: PostgreSQL
layout: default
parent: Examples
nav_order: 1
---

# PostgreSQL

A Pacto contract for PostgreSQL — a stateful, persistent relational database with high data criticality.

```yaml
pactoVersion: "1.0"

service:
  name: postgresql
  version: 16.4.0
  owner: infra/databases
  image:
    ref: docker.io/library/postgres:16.4
    private: false

interfaces:
  - name: sql
    type: grpc
    port: 5432
    visibility: internal
    contract: interfaces/postgres-wire.proto

  - name: metrics
    type: http
    port: 9187
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
    dataCriticality: high

  lifecycle:
    upgradeStrategy: ordered
    gracefulShutdownSeconds: 60

  health:
    interface: metrics
    path: /health

  metrics:
    interface: metrics
    path: /metrics

scaling:
  replicas: 1

metadata:
  tier: critical
  backup: required
  storage-class: ssd
```

{: .note }
> The `sql` interface uses `type: grpc` as the closest available protocol type for PostgreSQL's binary wire protocol. The Pacto schema currently supports `http`, `grpc`, and `event` — there is no dedicated `tcp` type. The `.proto` contract file is illustrative; in practice you may omit the interface or use a custom schema.

### Key decisions

- **`state.type: stateful`** with **`durability: persistent`** — PostgreSQL needs persistent storage that survives pod restarts
- **`dataCriticality: high`** — data loss is unacceptable; the platform should enable backups and strict disruption budgets
- **`upgradeStrategy: ordered`** — replicas must be updated one at a time (primary before replicas)
- **`scaling: replicas 1`** — single-instance; replication is handled externally
- **`gracefulShutdownSeconds: 60`** — allow time for connections to drain and WAL to flush
