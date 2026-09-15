# Example Contracts

This section provides ready-to-use Pacto contracts for common infrastructure services. Use these as references when writing your own contracts, or as dependencies. New to authoring? Start with the [developer guide](../developers.md).

To see the whole dashboard in your browser with nothing to install, open the [live dashboard demo](dashboard-demo.md) — its source and curated contract set live in [`examples/demo`](https://github.com/TrianaLab/pacto/tree/main/examples/demo). For a real fleet on your own machine — a registry, an Evidence Server and the dashboard, pulled as one OCI artifact — see the [Docker Compose demo](compose-demo.md). To drive one from the command line instead, the [guided tour](demo-tour.md) walks six user stories over a fixture fleet, offline. That same fixture opens in the [terminal UI](../fleet-tools.md#the-terminal-ui) if you would rather browse it than type paths, and `pacto tui --root oci://ghcr.io/trianalab/pacto/pacto-demo:1.0.0 --local ""` gets most of it out of the registry with no clone.

!!! tip
    These contracts represent the **operational interface** of each service — not a deployment recipe. They describe what a service exposes and how it behaves — not how to deploy it. Each one composes schemas you already have — an OpenAPI or AsyncAPI document, a gRPC service descriptor, JSON Schema for `configurations` — rather than inventing a new format; the contract is the relational layer Pacto adds around them: ownership, dependencies, compatibility, lifecycle. Every referenced spec file must parse as JSON or YAML (see [interface types](../contract-reference/sections.md#interface-types)).

## Available examples

| Service | Type | State | Description |
|---------|------|-------|-------------|
| [PostgreSQL](#postgresql) | service | stateful/persistent | Relational database |
| [Redis](#redis) | service | stateful/persistent | In-memory data store |
| [RabbitMQ](#rabbitmq) | service | stateful/persistent | Message broker |
| [NGINX](#nginx) | service | stateless/ephemeral | Reverse proxy / web server |
| [Cron Worker](#cron-worker) | scheduled | stateless/ephemeral | Scheduled batch job |
| [Event Processor](#event-processor) | service | stateless/ephemeral | Event-driven message consumer |
| [gRPC Service](#grpc-service) | service | stateless/ephemeral | Microservice exposing a gRPC service descriptor |
| [Hybrid Cache API](#hybrid-cache-api) | service | hybrid/persistent | API with local cache and upstream rebuild |

## PostgreSQL

A stateful, persistent relational database with high data criticality.

```yaml
pactoVersion: "2.0"

service:
  name: postgresql
  version: 16.4.0
  owner:
    team: databases

interfaces:
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
      interface: metrics
      path: /health
  - type: metrics
    binding:
      type: http
      interface: metrics
      path: /metrics

metadata:
  tier: critical
  backup: required
  storage-class: ssd
```

!!! note
    PostgreSQL's binary wire protocol — like Redis's RESP below — is not an `openapi`/`asyncapi`/`grpc` spec, so it is not declared as an interface. In both contracts the only declared interface is the HTTP endpoint the metrics exporter exposes, which the `health` and `metrics` capabilities bind to (see [interface types](../contract-reference/sections.md#interface-types)).

- **`state.type: stateful`** with **`durability: persistent`** — persistent storage survives pod restarts
- **`dataCriticality: high`** — backups and strict disruption budgets expected
- **Single instance** — replication is handled externally; replica counts are a deployment concern, not part of the contract
- **Capabilities over ports** — health and metrics are declared as [capabilities](../contract-reference/sections.md#capabilities) bound to the exporter interface

## Redis

A stateful in-memory data store with persistent durability.

```yaml
pactoVersion: "2.0"

service:
  name: redis
  version: 7.4.0
  owner:
    team: caching

interfaces:
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
  dataCriticality: medium

capabilities:
  - type: health
    binding:
      type: http
      interface: metrics
      path: /health
  - type: metrics
    binding:
      type: http
      interface: metrics
      path: /metrics

metadata:
  tier: high
  eviction-policy: allkeys-lru
```

- **`state.type: stateful`** with **`durability: persistent`** — Redis with AOF/RDB persistence enabled needs durable storage
- **`dataCriticality: medium`** — data is important but can be rebuilt from source if needed
- **Capabilities over ports** — health and metrics are declared as [capabilities](../contract-reference/sections.md#capabilities) bound to the exporter interface, not as ports

For a pure cache without persistence:

```yaml
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
```

## RabbitMQ

A stateful message broker with persistent durability and multiple protocol interfaces.

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

- **`dataCriticality: high`** — message loss can cause data integrity issues across the system
- **`state.type: stateful` with `durability: persistent`** — queues and messages must survive restarts
- **Clustering is external** — quorum-queue cluster sizing is a deployment concern (captured in `metadata`), not a contract `scaling` section
- **Multiple interfaces** — AsyncAPI for messaging, OpenAPI for the management API and metrics

## NGINX

A stateless reverse proxy and web server.

```yaml
pactoVersion: "2.0"

service:
  name: nginx
  version: 1.27.3
  owner:
    team: networking

interfaces:
  - name: proxy
    type: openapi
    ref: interfaces/openapi.yaml
    visibility: public

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
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low

capabilities:
  - type: health
    binding:
      type: http
      interface: proxy
      path: /health
  - type: metrics
    binding:
      type: http
      interface: metrics
      path: /metrics

metadata:
  tier: critical
  edge: true
```

- **`state.type: stateless`** — any instance can serve any request
- **`durability: ephemeral`** — no persistent storage needed
- **`visibility: public`** — the `proxy` interface is externally reachable; HTTP/HTTPS termination and ports are a deployment concern, not part of the contract
- **Capabilities over ports** — health and metrics are declared as [capabilities](../contract-reference/sections.md#capabilities) bound to the declared interfaces
- **No replica scaling** — v2 has no `scaling` section; high availability and autoscaling are deployment concerns

## Cron Worker

A scheduled batch job — a stateless worker that runs on a cron schedule.

```yaml
pactoVersion: "2.0"

service:
  name: report-generator
  version: 1.2.0
  owner:
    team: analytics

interfaces:
  - name: health
    type: openapi
    ref: interfaces/health.yaml
    visibility: internal

workload: scheduled

state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low

capabilities:
  - type: health
    binding:
      type: http
      interface: health
      path: /ready

metadata:
  schedule: "0 2 * * *"
  timeout: 3600
```

- **`workload: scheduled`** — runs on a cron schedule, not continuously
- **No replica scaling** — v2 has no `scaling` section; batch workloads scale as a deployment concern, outside the contract
- **Schedule in `metadata`** — the cron expression is platform-specific, so it lives in `metadata`, not the contract's core fields
- **Health as a capability** — the readiness probe binds to the `health` interface (see [capabilities](../contract-reference/sections.md#capabilities))

For a job that runs once (e.g., a database migration):

```yaml
workload: job

state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low

capabilities:
  - type: health
    binding:
      type: http
      interface: health
      path: /ready
```

## Event Processor

An event-driven service — a stateless consumer that processes messages from a message broker and exposes an HTTP health endpoint.

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

- **`type: asyncapi`** — declares an event-driven interface; the service consumes messages rather than serving HTTP/gRPC requests
- **`ref: interfaces/order-events.yaml`** — the AsyncAPI event schema is bundled and versioned with the service
- **`dataCriticality: medium`** — event processing failures have moderate impact; dead-letter queues provide a safety net
- **Capabilities on the HTTP interface** — health and metrics bind to the `health` OpenAPI interface (see [capabilities](../contract-reference/sections.md#capabilities))
- **Dependency target** — the `rabbitmq` dependency points to the [RabbitMQ example](#rabbitmq), which publishes version `3.13.7` and satisfies the `^3.13.0` compatibility range
- **Secret reference** — broker credentials use `secret://`, an opaque convention the platform resolves at deployment time (see [Secret references](../contract-reference/configuration-and-policy.md#secret-references))

## gRPC Service

A gRPC microservice — a user service exposing a gRPC API with internal visibility.

```yaml
pactoVersion: "2.0"

service:
  name: user-service
  version: 3.2.0
  owner:
    team: identity

interfaces:
  - name: grpc-api
    type: grpc
    ref: interfaces/user-service.yaml
    visibility: internal

  - name: metrics
    type: openapi
    ref: interfaces/metrics.yaml
    visibility: internal

configurations:
  - name: default
    required: true
    schema: configuration/schema.json
    values:
      DB_HOST: user-db.internal
      DB_PORT: 5432
      DB_PASSWORD: secret://vault/user-service/db-password
      CACHE_TTL_SECONDS: 300

dependencies:
  - name: postgres
    ref: oci://ghcr.io/acme/postgres-pacto@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
    required: true
    compatibility: "^16.0.0"

workload: service

state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low

capabilities:
  - type: health
  - type: metrics
    binding:
      type: http
      interface: metrics
      path: /metrics

metadata:
  tier: critical
```

- **`type: grpc`** — the gRPC service descriptor is bundled in the OCI artifact, so the API contract travels with the service version. The `ref` file must parse as JSON or YAML (see [interface types](../contract-reference/sections.md#interface-types))
- **Health as a capability with no binding** — a `health` capability without an HTTP binding declares the service implements the [gRPC Health Checking Protocol](https://github.com/grpc/grpc/blob/master/doc/health-checking.md) rather than an HTTP probe (see [capabilities](../contract-reference/sections.md#capabilities))
- **Metrics over HTTP** — the gRPC interface serves application traffic while the `metrics` capability binds to a separate HTTP interface exposing Prometheus metrics
- **`stateless`** — the service itself holds no state; data lives in PostgreSQL (the `postgres` dependency resolves to the [PostgreSQL example](#postgresql))

## Hybrid Cache API

A service with hybrid state — an API that caches data locally for performance but can rebuild its cache from an upstream source. Loss of local state degrades performance but does not break the service.

```yaml
pactoVersion: "2.0"

service:
  name: product-catalog
  version: 2.0.1
  owner:
    team: catalog

interfaces:
  - name: rest-api
    type: openapi
    ref: interfaces/openapi.yaml
    visibility: public

  - name: metrics
    type: openapi
    ref: interfaces/metrics.yaml
    visibility: internal

configurations:
  - name: default
    required: true
    schema: configuration/schema.json
    values:
      UPSTREAM_API: https://inventory.internal/api
      CACHE_MAX_SIZE_MB: 512
      CACHE_TTL_SECONDS: 3600
      WARMUP_ON_START: true
      API_KEY: secret://vault/product-catalog/upstream-api-key

dependencies:
  - name: inventory
    ref: oci://ghcr.io/acme/inventory-pacto@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
    required: true
    compatibility: "^1.0.0"

workload: service

state:
  type: hybrid
  persistence:
    scope: local
    durability: persistent
  dataCriticality: low

capabilities:
  - type: health
    binding:
      type: http
      interface: rest-api
      path: /health
  - type: metrics
    binding:
      type: http
      interface: metrics
      path: /metrics

metadata:
  tier: standard
  cache-strategy: write-through
```

- **`state.type: hybrid`** — the service caches product data locally for fast reads, but can reconstruct the cache from the upstream inventory service on restart
- **`durability: persistent`** — persisting the cache across restarts avoids cold-start latency, but the service still works without it (it just warms up first)
- **`dataCriticality: low`** — the cache is reconstructible; losing it has no business impact beyond temporary performance degradation
- **Capabilities over ports** — health binds to the public `rest-api` interface and metrics to a separate internal interface (see [capabilities](../contract-reference/sections.md#capabilities))
- **Secret reference** — the API key for the upstream service uses `secret://` so credentials never appear in the contract

### When to use `hybrid`

Choose `hybrid` over `stateless` when local state persists across restarts to avoid warm-up but the service still functions after losing it; a purely in-memory cache rebuilt on every start is `stateless`/`ephemeral`. The [state.type table](../contract-reference/dependencies-and-state.md#state) has the full breakdown.

## Using examples as dependencies

You can reference these contracts (once published to a registry) as dependencies in your own `pacto.yaml`:

```yaml
dependencies:
  - name: postgres
    ref: oci://ghcr.io/acme/postgres-pacto@sha256:abc1230000000000000000000000000000000000000000000000000000000000
    required: true
    compatibility: "^16.0.0"

  - name: redis
    ref: oci://ghcr.io/acme/redis-pacto@sha256:def4560000000000000000000000000000000000000000000000000000000000
    required: false
    compatibility: "^7.0.0"
```

The `ghcr.io/acme/…` refs above are placeholders: nothing is published there. The one Pacto contract anyone can pull is the dashboard's own, and `pacto explain` prints it:

```bash
pacto explain oci://ghcr.io/trianalab/pacto/dashboard-contract
```

See the [contract reference](../contract-reference/dependencies-and-state.md#dependencies) for the full dependency schema.

Then run `pacto graph` from the bundle directory to see the resolved tree:

```bash
pacto graph .
```

It prints the service and one line per dependency. A ref it cannot resolve — the placeholders above included — becomes an error node in the tree rather than a failure; the command still exits 0.

## One contract, many sections

The examples above each show a contract shaped by one kind of service. This one
is shaped by nothing: it declares most of the optional sections at once, so it
reads as a field checklist rather than a recommendation. Nothing here is required
beyond `pactoVersion` and `service`, and the three sections it leaves out —
`readiness`, `metadata` and `extensions` — are in
[Contract sections](../contract-reference/sections.md), which is the complete list.

```yaml
pactoVersion: "2.0"

service:
  name: payments-api
  version: 2.1.0
  owner:
    team: payments
    dri: alice

interfaces:
  - name: rest-api
    type: openapi
    ref: interfaces/openapi.yaml
    visibility: public

capabilities:
  - type: health
    binding:
      type: http
      interface: rest-api
      path: /health
  - type: metrics
    binding:
      type: http
      interface: rest-api
      path: /metrics

configurations:
  - name: default
    schema: configuration/schema.json
    required: true

policies:
  - name: platform-policy
    schema: policy/schema.json

dependencies:
  - name: auth
    ref: oci://ghcr.io/acme/auth-pacto:2.0.0
    required: true
    compatibility: "^2.0.0"

workload: service

state:
  type: stateful
  persistence:
    scope: shared
    durability: persistent
  dataCriticality: high
```

That contract is step 1 of the [operational control loop](../model.md#the-operational-control-loop)
— declare, read, constrain, act, observe, evaluate — which the model page states
once, including which two steps external systems perform rather than Pacto. The
rest of this site is that loop in detail: [`pacto explain` and MCP](../mcp-agent-capabilities.md)
read it, the [Kubernetes collector](../integrations/kubernetes/runtime-observations.md)
observes against it, and [compliance scenarios](compliance-scenarios.md) show
what each verdict is proven by.
