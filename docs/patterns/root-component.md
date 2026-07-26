# Root + component contracts

**Problem.** A repository ships several deployable units that release together — an HTTP API and a background worker, a Prefect server and its workers, a service plus its CLI shim. You want one deployment unit (one ArgoCD Application, one Helm release) but distinct runtime semantics, dependencies, and configurations for each component.

**Primitives.**

- **Multiple bundles in one repo**, each with its own `pacto.yaml`
- **Root contract** — declares the application boundary and lists components as `dependencies[]`
- **Component contracts** — declare workload, state, interfaces, and configurations for a single deployable

**Layout.**

```
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

**Why this works.** A one-component repo pays almost nothing for this layout, and adding a second component is one new bundle dir plus one root dependency. The root maps to a single deployment unit; each component is validated and versioned independently. The root is a lean aggregator — it carries only `service` and `dependencies[]` (plus any `policies`), deliberately omitting `workload`, `state`, `interfaces` and `configurations`. Your own tooling can distinguish a root from a component by naming convention (e.g. a `-root` suffix) or structurally — `dependencies[]` present, `configurations` absent — since Pacto itself does not key off the name.

**Cross-links:** [`workload`](../contract-reference/sections.md#workload) · [`dependencies`](../contract-reference/sections.md#dependencies)

---
