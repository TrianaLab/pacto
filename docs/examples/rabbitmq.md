---
title: RabbitMQ
layout: default
parent: Examples
nav_order: 3
---

# RabbitMQ

A Pacto contract for RabbitMQ — a stateful message broker with persistent durability and multiple protocol interfaces.

```yaml
pactoVersion: "1.0"

service:
  name: rabbitmq
  version: 3.13.7
  owner: infra/messaging
  image:
    ref: docker.io/library/rabbitmq:3.13-management
    private: false

interfaces:
  - name: amqp
    type: grpc
    port: 5672
    visibility: internal
    contract: interfaces/amqp.proto

  - name: management
    type: http
    port: 15672
    visibility: internal
    contract: interfaces/management-api.yaml

  - name: metrics
    type: http
    port: 15692
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
    gracefulShutdownSeconds: 120

  health:
    interface: management
    path: /api/health/checks/alarms

scaling:
  min: 3
  max: 5

metadata:
  tier: critical
  cluster: required
  quorum-queues: enabled
```

### Key decisions

- **`dataCriticality: high`** — message loss can cause data integrity issues across the system
- **`gracefulShutdownSeconds: 120`** — RabbitMQ needs time to drain queues and transfer leadership
- **`scaling: min 3`** — minimum cluster size for quorum queues
- **Multiple interfaces** — AMQP for messaging, HTTP for management API and metrics
