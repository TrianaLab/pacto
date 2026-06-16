# Example Contracts
This section provides ready-to-use Pacto contracts for common infrastructure services. Use these as references when writing your own contracts or as dependencies in your service contracts.

For a complete, runnable demo, see the [live dashboard demo](dashboard-demo.md) — its source and curated contract set live in [`examples/demo`](https://github.com/TrianaLab/pacto/tree/main/examples/demo).

---

!!! tip
    These contracts represent the **operational interface** of each service — not a deployment recipe. They describe what the service exposes, how it behaves, and what the platform needs to know.

---

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

---

## Using examples as dependencies

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

Then use `pacto graph` to visualize the full dependency tree:

```bash
pacto graph .
```
