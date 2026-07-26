# NGINX

A Pacto contract for NGINX — a stateless reverse proxy and web server.

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

### Key decisions

See the [state reference](../contract-reference/sections.md#state) for what each enum value means; the notes below cover why NGINX picks these.

- **`state.type: stateless`** — any instance can serve any request
- **`durability: ephemeral`** — no persistent storage needed
- **`visibility: public`** — the `proxy` interface is externally reachable; HTTP/HTTPS termination and ports are a deployment concern, not part of the contract
- **Capabilities over ports** — health and metrics are declared as [capabilities](../contract-reference/sections.md#capabilities) bound to the declared interfaces
- **No replica scaling** — v2 has no `scaling` section; high availability and autoscaling are deployment concerns
