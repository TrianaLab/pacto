# Event Processor

A Pacto contract for an event-driven service — a stateless consumer that processes messages from a message broker and exposes an HTTP health endpoint.

```yaml
pactoVersion: "1.2"

service:
  name: order-processor
  version: 1.4.0
  owner:
    team: orders
  image:
    ref: ghcr.io/acme/order-processor:1.4.0
    private: true

interfaces:
  - name: order-events
    type: event
    visibility: internal
    contract: interfaces/order-events.yaml

  - name: health
    type: http
    port: 8080
    visibility: internal

configurations:
  - name: default
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

runtime:
  workload: service

  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: medium

  lifecycle:
    upgradeStrategy: rolling
    gracefulShutdownSeconds: 60

  health:
    interface: health
    path: /health

  metrics:
    interface: health
    path: /metrics

scaling:
  min: 2
  max: 8

metadata:
  tier: standard
  consumer-group: order-processing
```

### Key decisions

- **`type: event`** — declares that this service consumes events rather than serving HTTP/gRPC requests
- **`contract: interfaces/order-events.yaml`** — the event schema (e.g. AsyncAPI) is bundled and versioned with the service
- **`dataCriticality: medium`** — event processing failures have moderate impact; dead-letter queues provide a safety net
- **`gracefulShutdownSeconds: 60`** — allows in-flight messages to complete processing before shutdown
- **Dependency target** — the `rabbitmq` dependency points to the [RabbitMQ example](rabbitmq.md), which publishes version `3.13.7` and satisfies the `^3.13.0` compatibility range
- **Secret reference** — broker credentials use `secret://`, an opaque convention the platform resolves at deployment time (see [Secret references](../contract-reference.md#secret-references))
