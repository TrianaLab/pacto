# Example Contracts
This section provides ready-to-use Pacto contracts for common infrastructure services. Use these as references when writing your own contracts, or as dependencies.

For a complete, runnable demo, see the [live dashboard demo](dashboard-demo.md) — its source and curated contract set live in [`examples/demo`](https://github.com/TrianaLab/pacto/tree/main/examples/demo).

!!! tip
    These contracts represent the **operational interface** of each service — not a deployment recipe. They describe what a service exposes and how it behaves — not how to deploy it. Each one composes schemas you already have — Protocol Buffers for gRPC APIs, JSON Schema for `configurations` — rather than inventing a new format; the contract is the relational layer Pacto adds around them: ownership, dependencies, compatibility, lifecycle.

## Available examples

| Service | Type | State | Description |
|---------|------|-------|-------------|
| [PostgreSQL](postgresql.md) | service | stateful/persistent | Relational database |
| [Redis](redis.md) | service | stateful/persistent | In-memory data store |
| [RabbitMQ](rabbitmq.md) | service | stateful/persistent | Message broker |
| [NGINX](nginx.md) | service | stateless/ephemeral | Reverse proxy / web server |
| [Cron Worker](cron-worker.md) | scheduled | stateless/ephemeral | Scheduled batch job |
| [Event Processor](event-processor.md) | service | stateless/ephemeral | Event-driven message consumer |
| [gRPC Service](grpc-service.md) | service | stateless/ephemeral | gRPC microservice with Proto contract |
| [Hybrid Cache API](hybrid-cache.md) | service | hybrid/persistent | API with local cache and upstream rebuild |

## Using examples as dependencies

A JSON Schema or `.proto` describes one interface in isolation; wiring these contracts together as `dependencies` — with compatibility ranges — is Pacto describing the relationships between interfaces and how they change.

You can reference these contracts (once published to a registry) as dependencies in your own `pacto.yaml`:

```yaml
dependencies:
  - name: postgres
    ref: oci://ghcr.io/acme/postgres-pacto@sha256:abc123
    required: true
    compatibility: "^16.0.0"

  - name: redis
    ref: oci://ghcr.io/acme/redis-pacto@sha256:def456
    required: false
    compatibility: "^7.0.0"
```

See the [contract reference](../contract-reference.md#dependencies) for the full dependency schema.

Then use `pacto graph` to visualize the full dependency tree:

```bash
pacto graph .
```
