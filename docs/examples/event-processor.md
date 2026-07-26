# Event Processor

A Pacto contract for an event-driven service — a stateless consumer that processes messages from a message broker and exposes an HTTP health endpoint.

```yaml
pactoVersion: "2.0"

service:
  name: order-processor
  version: 1.4.0
  owner:
    team: orders

interfaces:
  - name: order-events
    type: asyncapi
    ref: interfaces/order-events.yaml
    visibility: internal

  - name: health
    type: openapi
    ref: interfaces/health.yaml
    visibility: internal

configurations:
  - name: default
    required: true
    schema: configuration/schema.json
    values:
      BROKER_HOST: rabbitmq.internal
      BROKER_PORT: 5672
      BROKER_CREDENTIALS: secret://vault/order-processor/broker-credentials
      DEAD_LETTER_QUEUE: orders.dlq
      MAX_RETRIES: 3

dependencies:
  - name: rabbitmq
    ref: oci://ghcr.io/acme/rabbitmq-pacto@sha256:0000000000000000000000000000000000000000000000000000000000000000
    required: true
    compatibility: "^3.13.0"

workload: service

state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: medium

capabilities:
  - type: health
    binding:
      type: http
      interface: health
      path: /health
  - type: metrics
    binding:
      type: http
      interface: health
      path: /metrics

metadata:
  tier: standard
  consumer-group: order-processing
```

### Key decisions

- **`type: asyncapi`** — declares an event-driven interface; the service consumes messages rather than serving HTTP/gRPC requests
- **`ref: interfaces/order-events.yaml`** — the AsyncAPI event schema is bundled and versioned with the service
- **`dataCriticality: medium`** — event processing failures have moderate impact; dead-letter queues provide a safety net
- **Capabilities on the HTTP interface** — health and metrics bind to the `health` openapi interface (see [capabilities](../contract-reference/sections.md#capabilities))
- **Dependency target** — the `rabbitmq` dependency points to the [RabbitMQ example](rabbitmq.md), which publishes version `3.13.7` and satisfies the `^3.13.0` compatibility range
- **Secret reference** — broker credentials use `secret://`, an opaque convention the platform resolves at deployment time (see [Secret references](../contract-reference/sections.md#secret-references))
