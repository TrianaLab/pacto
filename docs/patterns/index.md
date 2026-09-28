# Composition Patterns

Pacto's primitives — bundles, references, configurations, policies, metadata — compose into platform interfaces. Each pattern below says when to reach for it, which primitives it relies on and how a minimal version looks.

Each pattern is independent. Stack what you need; ignore what you don't.

---

## Root + component contracts

**Problem.** A repository ships several deployable units that release together — an HTTP API and a background worker, a Prefect server and its workers, a service plus its CLI shim. You want one deployment unit (one ArgoCD Application, one Helm release) but distinct runtime semantics, dependencies and configurations for each component.

**Primitives:**

- **Multiple bundles in one repository**, each with its own `pacto.yaml`
- **Root contract** — declares the application boundary and lists components as `dependencies[]`
- **Component contracts** — declare workload, state, interfaces and configurations for a single deployable

**Layout.**

```text
my-service/
├── charts/
│   └── my-service/                         # service chart (one per repo)
│       ├── Chart.yaml                      # depends on the per-component chart, aliased
│       └── values.yaml
└── pactos/
    ├── my-service-root/
    │   └── pacto.yaml                      # components as deps
    ├── my-service-api/
    │   ├── pacto.yaml                      # workload, state, configurations
    │   └── overrides/
    │       └── values.<env>.yaml
    └── my-service-worker/
        ├── pacto.yaml
        └── overrides/
            └── values.<env>.yaml
```

**Root contract.**

```yaml
pactoVersion: "2.0"

service:
  name: my-service-root
  version: 1.2.0
  owner:
    team: example

dependencies:
  # Components — built from this repo, not deployed independently
  - name: api
    ref: oci://ghcr.io/example/pactos/my-service-api:1.2.0
    required: true
    compatibility: "^1.0.0"
  - name: worker
    ref: oci://ghcr.io/example/pactos/my-service-worker:1.2.0
    required: true
    compatibility: "^1.0.0"

  # External services this app talks to at runtime
  - name: auth
    ref: oci://ghcr.io/example/pactos/auth-root:4.0.0
    required: true
    compatibility: "^4.0.0"
```

**Component contract.**

```yaml
pactoVersion: "2.0"

service:
  name: my-service-api
  version: 1.2.0
  owner:
    team: example

interfaces:
  - name: api
    type: openapi
    ref: interfaces/openapi.yaml
    visibility: internal

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
      interface: api
      path: /health

configurations:
  - name: deployment
    ref: oci://ghcr.io/example/pactos/platform-service:2.0.0
    required: true
```

**Why this works.** A one-component repository pays almost nothing for this layout, and adding a second component is one new bundle dir plus one root dependency. The root maps to a single deployment unit; each component is validated and versioned independently. The root is a lean aggregator — it carries only `service` and `dependencies[]` (plus any `policies`), deliberately omitting `workload`, `state`, `interfaces` and `configurations`. The compatibility range in the root's `dependencies[]` lets you version components independently while the root version stays pinned to what your deployment artifact published. Your own tooling can distinguish a root from a component by naming convention or structurally (`dependencies[]` present, `configurations` absent), since Pacto itself does not key off the name.

---

## Infrastructure contracts

**Problem.** Your platform offers a fixed set of infrastructure types — Postgres, Redis, object storage, secrets — provisioned by some declarative tool (Crossplane, Terraform, an internal operator). You want each type to be self-describing, governed like services and machine-readable by the tool that turns claims into real resources.

**Primitives.**

- **A pacto contract per infrastructure type**, published by the platform team
- **`policies[]`** carrying the platform rules (HA, backups, version floors)
- **`configurations[]`** carrying the provisioning schema (the team-controllable subset of fields)
- **`metadata.labels`** carrying provisioner hints — opaque to pacto, meaningful to your CI tool

**Example: a postgres infrastructure contract.**

```yaml
pactoVersion: "2.0"

service:
  name: postgres
  version: 17.0.0
  owner:
    team: platform

metadata:
  labels:
    platform/provisioner: crossplane
    platform/claim-kind: PostgreSQLClaim
    platform/claim-api-version: database.platform.example.com/v1alpha1

policies:
  - name: postgres-policy
    schema: policy/schema.json   # requires an owner and provisioner labels

configurations:
  - name: provisioning
    required: true
    schema: configuration/schema.json   # derived from the provisioning claim's OpenAPI schema
```

**How services consume it.** A service contract references the infra contract as a configuration (see [Configurations as composable claims](#configurations-as-composable-claims)). When CI generates deployment artifacts, it reads `metadata.labels` from the resolved infra contract to dispatch — no hardcoded mapping from "this configuration name" to "that claim kind". Adding a new infrastructure type is one new contract, not a code change in CI.

**Versioning the contract is versioning the platform interface.** A bump from `postgres:17.0.0` to `postgres:18.0.0` lets services migrate at their own pace by ref-pinning, and the policy can tighten with each major version (see [Progressive policy versioning](#progressive-policy-versioning)).

---

## Configurations as composable claims

**Problem.** A single deployable needs several distinct configuration inputs — Helm values for the chart, claim values for a database, declared keys for a secret store. Each has a different schema. You want all of them validated together at CI time, supplied through one file per environment, with no parallel `claims/` or `values/` directories to keep in sync.

**Primitives.**

- **`configurations` is an array.** Each entry is independently resolved against its own schema (`schema:` local file, or `ref:` another contract)
- **Override files** ([Contract overrides](../contract-reference/overrides.md#contract-overrides)) replace the array wholesale per environment

**One file, multiple typed outputs.**

```yaml
# pactos/my-service-api/overrides/values.stg.yaml
configurations:
  - name: deployment
    required: true
    schema: configuration/deployment/schema.json
    values:
      replicas: 3
      resources:
        requests:
          cpu: 1000m
          memory: 1Gi

  - name: postgres
    required: true
    schema: configuration/postgres/schema.json
    values:
      instances: 2
      size: medium
      backups:
        enabled: true
        schedule: "0 */6 * * *"

  - name: secrets
    required: true
    schema: configuration/secrets/schema.json
    values:
      secrets:
        - key: api-key
        - key: openai-token
```

`pacto validate -f overrides/values.stg.yaml` validates each entry's `values` against its local `schema`. Your deployment tooling reads the same file and produces: Helm values (`deployment` entry), a Postgres claim (`postgres` entry), a secret-store claim (`secrets` entry). Each value is written once — no drift between a chart's `values.yaml` and a separate `claims/postgres.yaml`.

!!! info
    Override files use **Helm-style array replacement** for `configurations` — the override's array replaces the contract's array entirely, not merged by name (see [Contract overrides](../contract-reference/overrides.md#contract-overrides)). Each override file must therefore include every configuration it cares about, each with its `name` and `required` plus either a `schema` (with `values`) or a `ref` (schema-only) — omitting `required` is rejected. Inline `values` require a local `schema`; a `ref`-based entry carries neither.

**Precedence:** Contract inline `values` → `-f overrides/values.<env>.yaml` → `--set` (wins). See [Precedence](../contract-reference/overrides.md#precedence) for the full chain.

---

## Platform-published policy schema contract

**Problem.** As a platform team you want to validate contract structure rules ("every service must declare an owner and its workload type") *and* publish the schema that validates deployment values for your standard chart. You want both to live in one versioned artifact that every service references — so updates propagate via a version bump, not a wiki announcement.

**Primitives.**

- **One contract** carrying both `policies[].schema` (the rules contract authors must follow) and `configurations[].schema` (the values shape the chart accepts)
- Service contracts reference it for either or both via `policies[].ref` and `configurations[].ref`

**The platform contract.**

```yaml
# pactos/platform-service/pacto.yaml
pactoVersion: "2.0"

service:
  name: platform-service
  version: 2.0.0
  owner:
    team: platform

workload: service

policies:
  - name: platform-policy
    schema: policy/schema.json     # requires service.owner and workload

configurations:
  - name: deployment
    required: true
    schema: configuration/schema.json   # the standard chart's values.schema.json
```

**A service references both.**

```yaml
# pactos/my-service-api/pacto.yaml
policies:
  - name: platform-policy
    ref: oci://ghcr.io/example/pactos/platform-service:2.0.0

configurations:
  - name: deployment
    ref: oci://ghcr.io/example/pactos/platform-service:2.0.0
    required: true
```

**Mix and match.** Teams using the platform's standard chart reference both. Teams that ship their own chart (a third-party Keycloak chart, a custom operator) still reference the policy — contract structure rules are universal — but vendor their own configuration schema locally. The moment a team needs inline or override `values`, it must vendor the schema: a `ref`-ed config is schema-only.

---

## Configuration schema ownership

**Who owns the schema.** A `configurations[]` entry's schema can be service-owned (the service declares what it requires) or platform-owned (the platform declares what it provides). Service-owned schemas live as local files in the bundle. Platform-owned schemas are distributed one of two ways. **Vendored**: services copy the platform's schema into their bundle at build time. **Referenced**: services point `configurations[].ref` at the platform's configuration contract, and Pacto records and pins that reference while the consumer reads the schema from that bundle, by convention at `configuration/schema.json`.

Because `configurations` is an array, one entry may reference a platform schema and another define a service-specific schema. `schema` and `ref` are mutually exclusive within a single entry.

See [Configuration and policy](../contract-reference/configuration-and-policy.md#configurations) for the full reference.

---

## Progressive policy versioning

**Problem.** You want to raise the bar on what a "compliant service" means — without breaking every service that's already on the platform. New services should adopt the strictest rules; existing services should migrate at their pace.

**How it works.** The policy contract is versioned. Each major version represents a new compliance bar. Services pin to whichever version they've achieved, and migrate forward by bumping the ref.

| Version | Requirements |
|---------|--------------|
| `1.0.0` | `service.owner` declared, a `health` capability present |
| `2.0.0` | + `workload` declared, `interfaces[]` if exposed |
| `3.0.0` | + `configurations[]` schema present, `metadata.labels` required |
| `4.0.0` | + the `health` capability declares a `binding.path`, a `readiness` gate present |

A service pinned to `platform-policy:2.0.0` keeps validating against v2's rules until the team is ready to bump — the platform never forces the change.

**Why this works.**

- **Forwards is opt-in, never forced.** Teams migrate when they have time
- **Backwards is checked.** A service can never silently weaken its policy — `pacto diff` flags removing or changing a policy ref as potentially breaking
- **The version is the negotiation point.** Conversations about "should we require X?" become "should we publish v4 that requires X, with a six-month adoption window?"

**Two dials.** Pinning the policy's major version (above) makes each new bar opt-in and negotiated. Alternatively, a rule layer referenced *transitively* through the platform contract can be left unpinned. Republishing that one schema then propagates a new rule fleet-wide immediately, with no per-service bump. The cost is lockfile drift: every republish changes the resolved digest and forces services to re-lock, and [`pacto lock --check`](../lockfile.md) fails until they do. Pinned is opt-in and negotiated; floating is instant and unilateral but forces re-locks.

**Coordinate with `pacto validate`.** When a service ref-bumps from `2.0.0` to `3.0.0`, `pacto diff` reports the changed policy ref. `pacto validate` resolves the new policy and fails *before* merge if the contract does not satisfy it, so the team sees the gap and either fixes it or stays on `2.0.0`.

---
