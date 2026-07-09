# PostgreSQL

A Pacto contract for PostgreSQL — a stateful, persistent relational database with high data criticality.

```yaml
pactoVersion: "1.2"

service:
  name: postgresql
  version: 16.4.0
  owner:
    team: databases
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

!!! note
    The `sql` interface uses `type: grpc` as the closest available protocol type for PostgreSQL's binary wire protocol — there is no dedicated `tcp` type (see [Interfaces](../contract-reference.md#interfaces) for the full list of types). The `.proto` contract file is illustrative; in practice you may omit the interface or point `contract` at your own protobuf file (the file must exist in the bundle or `pacto validate` fails with `FILE_NOT_FOUND`).

### Key decisions

- **`state.type: stateful`** with **`durability: persistent`** — persistent storage survives pod restarts
- **`dataCriticality: high`** — backups and strict disruption budgets expected
- **`upgradeStrategy: ordered`** — replicas updated one at a time (primary first)
- **`scaling: replicas 1`** — single instance; replication handled externally
- **`gracefulShutdownSeconds: 60`** — time for connections to drain and WAL to flush
