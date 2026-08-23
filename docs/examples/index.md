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

A JSON Schema or a gRPC service descriptor describes one interface in isolation; wiring these contracts together as `dependencies` — with compatibility ranges — is Pacto describing the relationships between interfaces and how they change.

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

## End-to-end: a contract in a control loop (conceptual)

The examples above are static contracts. This section walks one contract through the full [operational control loop](../architecture.md#the-operational-control-loop) to show how the pieces fit. Steps 1, 2, 5 and 6 are implemented in Pacto today; steps 3 and 4 are performed by external systems Pacto integrates with, and are labelled **conceptual** below — the flow is illustrated, not shipped as executable Pacto functionality.

### 1. Declare (implemented)

A single contract carries the service's identity, interfaces, capabilities, configuration, dependencies and the policy it must satisfy:

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

### 2. Read (implemented)

Anything that consumes the service reads the contract instead of re-inferring it. A platform runs `pacto explain` or the dashboard API; an agent connects over MCP, where each `rest-api` operation is projected into a callable tool and the surrounding contract (dependencies, state, policy) gives the agent the operational context around those tools. See [MCP — Agent capabilities](../mcp-integration.md#agent-capabilities).

### 3. Constrain (conceptual / external)

External controls decide which actions are allowed. At **authoring time** this is implemented: `pacto validate` and `pacto push` resolve `policies[].ref` and fail closed if the contract does not satisfy the referenced schema, so `pacto push` refuses to publish a non-compliant contract — it validates before it opens a connection, and `--force` overwrites an existing tag rather than skipping that check. The gate is Pacto's, not the registry's: a published bundle is an ordinary OCI artifact, so anything with push access to the repository can put one there without going through Pacto at all. At **runtime**, whether a specific action is permitted stays with the systems built for that job — OPA, Kyverno, admission control, IAM. Pacto supplies the structured contract those systems can reason about; it does not grant runtime permissions itself.

### 4. Act (conceptual / external)

A controller, a deploy pipeline, a `pacto generate` plugin or an agent performs the action through existing infrastructure — Helm, kubectl, Terraform, or a generated tool calling the live API. Pacto makes zero deployment decisions and performs no actions of its own; it is the description the actor reads before acting.

### 5. Observe (implemented)

A collector turns the running system into evidence. The Kubernetes operator's observer reads the live workload and emits a typed `EvidenceSet` — for each declared assertion, whether it was `Observed` and what was seen, or why it could not be observed. See [Runtime observations](../integrations/kubernetes/runtime-observations.md).

### 6. Evaluate (implemented)

The pure engine compares declared intent against the collected evidence:

```text
Evaluate(contract, evidence) -> (findings, coverage)
```

A confirmed contradiction (for example `auth` unreachable, or the `default` configuration absent) becomes an `error` finding; an assertion the collector could not observe becomes an `unknown` finding, never a silent pass. The operator writes those findings and the resulting compliance state back onto the resource, closing the loop. See [Compliance scenarios](compliance-scenarios.md) for the full set of states and exactly where each is proven.
