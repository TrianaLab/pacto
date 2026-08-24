# Example Contracts
This section provides ready-to-use Pacto contracts for common infrastructure services. Use these as references when writing your own contracts, or as dependencies. New to authoring? Start with the [developer guide](../developers.md).

To see the whole dashboard in your browser with nothing to install, open the [live dashboard demo](dashboard-demo.md) — its source and curated contract set live in [`examples/demo`](https://github.com/TrianaLab/pacto/tree/main/examples/demo). For a real fleet on your own machine — a registry, an Evidence Server and the dashboard, pulled as one OCI artifact — see the [Docker Compose demo](compose-demo.md).

!!! tip
    These contracts represent the **operational interface** of each service — not a deployment recipe. They describe what a service exposes and how it behaves — not how to deploy it. Each one composes schemas you already have — an OpenAPI or AsyncAPI document, a gRPC service descriptor, JSON Schema for `configurations` — rather than inventing a new format; the contract is the relational layer Pacto adds around them: ownership, dependencies, compatibility, lifecycle. Every referenced spec file must parse as JSON or YAML (see [interface types](../contract-reference/sections.md#interface-types)).

## Available examples

| Service | Type | State | Description |
|---------|------|-------|-------------|
| [PostgreSQL](postgresql.md) | service | stateful/persistent | Relational database |
| [Redis](redis.md) | service | stateful/persistent | In-memory data store |
| [RabbitMQ](rabbitmq.md) | service | stateful/persistent | Message broker |
| [NGINX](nginx.md) | service | stateless/ephemeral | Reverse proxy / web server |
| [Cron Worker](cron-worker.md) | scheduled | stateless/ephemeral | Scheduled batch job |
| [Event Processor](event-processor.md) | service | stateless/ephemeral | Event-driven message consumer |
| [gRPC Service](grpc-service.md) | service | stateless/ephemeral | Microservice exposing a gRPC service descriptor |
| [Hybrid Cache API](hybrid-cache.md) | service | hybrid/persistent | API with local cache and upstream rebuild |

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

See the [contract reference](../contract-reference/sections.md#dependencies) for the full dependency schema.

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
rest of this site is that loop in detail: [`pacto explain` and MCP](../mcp-integration.md#agent-capabilities)
read it, the [Kubernetes collector](../integrations/kubernetes/runtime-observations.md)
observes against it, and [compliance scenarios](compliance-scenarios.md) show
what each verdict is proven by.
