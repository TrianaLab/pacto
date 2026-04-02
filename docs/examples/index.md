---
title: Examples
layout: default
nav_order: 14
has_children: true
---

# Example Contracts
{: .no_toc }

This section provides ready-to-use Pacto contracts for common infrastructure services. Use these as references when writing your own contracts or as dependencies in your service contracts.

For a complete working demo repository, see [pacto-demo](https://github.com/TrianaLab/pacto-demo).

---

<details open markdown="block">
  <summary>Table of contents</summary>
- TOC
{:toc}
</details>

{: .tip }
These contracts represent the **operational interface** of each service — not a deployment recipe. They describe what the service exposes, how it behaves, and what the platform needs to know.

---

## Available examples

| Service | Type | State | Description |
|---------|------|-------|-------------|
| [PostgreSQL]({{ site.baseurl }}{% link examples/postgresql.md %}) | service | stateful/persistent | Relational database |
| [Redis]({{ site.baseurl }}{% link examples/redis.md %}) | service | stateful/persistent | In-memory data store |
| [RabbitMQ]({{ site.baseurl }}{% link examples/rabbitmq.md %}) | service | stateful/persistent | Message broker |
| [NGINX]({{ site.baseurl }}{% link examples/nginx.md %}) | service | stateless/ephemeral | Reverse proxy / web server |
| [Cron Worker]({{ site.baseurl }}{% link examples/cron-worker.md %}) | scheduled | stateless/ephemeral | Scheduled batch job |
| [Event Processor]({{ site.baseurl }}{% link examples/event-processor.md %}) | service | stateless/ephemeral | Event-driven message consumer |
| [gRPC Service]({{ site.baseurl }}{% link examples/grpc-service.md %}) | service | stateless/ephemeral | gRPC microservice with Proto contract |
| [Hybrid Cache API]({{ site.baseurl }}{% link examples/hybrid-cache.md %}) | service | hybrid/persistent | API with local cache and upstream rebuild |

---

## Using examples as dependencies

You can reference these contracts (once published to a registry) as dependencies in your own `pacto.yaml`:

```yaml
dependencies:
  - ref: oci://ghcr.io/acme/postgres-pacto@sha256:abc123
    required: true
    compatibility: "^16.0.0"

  - ref: oci://ghcr.io/acme/redis-pacto@sha256:def456
    required: false
    compatibility: "^7.0.0"
```

Then use `pacto graph` to visualize the full dependency tree:

```bash
pacto graph .
```
