---
title: NGINX
layout: default
parent: Examples
nav_order: 4
---

# NGINX

A Pacto contract for NGINX — a stateless reverse proxy and web server.

```yaml
pactoVersion: "1.0"

service:
  name: nginx
  version: 1.27.3
  owner: infra/networking
  image:
    ref: docker.io/library/nginx:1.27.3
    private: false

interfaces:
  - name: http
    type: http
    port: 80
    visibility: public

  - name: https
    type: http
    port: 443
    visibility: public

  - name: metrics
    type: http
    port: 9113
    visibility: internal

configurations:
  - name: default
    schema: configuration/schema.json

runtime:
  workload: service

  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low

  lifecycle:
    upgradeStrategy: rolling
    gracefulShutdownSeconds: 30

  health:
    interface: http
    path: /health

  metrics:
    interface: metrics
    path: /metrics

scaling:
  min: 2
  max: 20

metadata:
  tier: critical
  edge: true
```

### Key decisions

- **`state.type: stateless`** — NGINX holds no local state; any instance can serve any request
- **`durability: ephemeral`** — no persistent storage needed
- **`upgradeStrategy: rolling`** — zero-downtime updates with gradual rollout
- **`scaling: min 2, max 20`** — high availability with auto-scaling for traffic spikes
- **`visibility: public`** — the HTTP and HTTPS interfaces are externally reachable
