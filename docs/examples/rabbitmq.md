# RabbitMQ

A Pacto contract for RabbitMQ — a stateful message broker with persistent durability and multiple protocol interfaces.

```yaml
pactoVersion: "2.0"

service:
  name: rabbitmq
  version: 3.13.7
  owner:
    team: messaging

interfaces:
  - name: amqp
    type: asyncapi
    ref: interfaces/amqp.yaml
    visibility: internal

  - name: management
    type: openapi
    ref: interfaces/management-api.yaml
    visibility: internal

  - name: metrics
    type: openapi
    ref: interfaces/metrics.yaml
    visibility: internal

configurations:
  - name: default
    schema: configuration/schema.json
    required: true

workload: service

state:
  type: stateful
  persistence:
    scope: local
    durability: persistent
  dataCriticality: high

capabilities:
  - type: health
    binding:
      type: http
      interface: management
      path: /api/health/checks/alarms
  - type: metrics
    binding:
      type: http
      interface: metrics
      path: /metrics

metadata:
  tier: critical
  cluster: required
  quorum-queues: enabled
```

!!! note
    RabbitMQ's AMQP is a message-oriented protocol, so the `amqp` interface uses `type: asyncapi` with an AsyncAPI document (see [interface types](../contract-reference/sections.md#interface-types)). The management REST API is `openapi`, and the Prometheus metrics endpoint is a separate `openapi` interface the `metrics` capability binds to.

### Key decisions

- **`dataCriticality: high`** — message loss can cause data integrity issues across the system
- **`state.type: stateful` with `durability: persistent`** — queues and messages must survive restarts
- **Clustering is external** — quorum-queue cluster sizing is a deployment concern (captured in `metadata`), not a contract `scaling` section
- **Multiple interfaces** — AsyncAPI for messaging, OpenAPI for the management API and metrics
