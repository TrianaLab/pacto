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

configuration:
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

scaling:
  min: 1
  max: 1

metadata:
  tier: critical
  backup: required
  storage-class: ssd
```

### Key decisions

- **`state.type: stateful`** with **`durability: persistent`** — PostgreSQL needs persistent storage that survives pod restarts
- **`dataCriticality: high`** — data loss is unacceptable; the platform should enable backups and strict disruption budgets
- **`upgradeStrategy: ordered`** — replicas must be updated one at a time (primary before replicas)
- **`scaling: min 1, max 1`** — single-instance; replication is handled externally
- **`gracefulShutdownSeconds: 60`** — allow time for connections to drain and WAL to flush
