# Sample Scenarios

## Quick Start: Demo Showcase (Scenario 07)

The **Demo Showcase** is a self-contained "Acme E-Commerce Platform" designed for a 20-30 second product demo. It demonstrates the full power of Pacto in a realistic microservices ecosystem.

All demo resources live in the `demo` namespace to avoid collisions with other scenarios (which use `default`).

### Apply the demo

```bash
kubectl apply -f config/samples/scenarios/00-infrastructure.yaml
kubectl apply -f config/samples/scenarios/07-demo-showcase.yaml
kubectl apply -f config/samples/scenarios/07-demo-showcase-revisions.yaml
```

### Architecture

```
                    ┌──────────────┐
                    │   frontend   │ :3000 (public)
                    └──────┬───────┘
                           │
                    ┌──────▼───────┐
                    │  api-gateway  │ :8080
                    └──┬───┬───┬───┘
                       │   │   │
          ┌────────────┘   │   └────────────┐
          │                │                │
   ┌──────▼───────┐ ┌─────▼──────┐ ┌───────▼────────┐
   │ auth-service  │ │orders-svc  │ │payments-service│ :8080
   │              │ │            │ │ v1.0→v1.1→v2.0 │
   └──────┬───────┘ └─────┬──────┘ └───┬────────┬───┘
          │               │            │        │
   ┌──────▼───────┐ ┌─────▼──────┐    │  ┌─────▼──────┐
   │    redis      │ │ postgresql │◄───┘  │ stripe-api │
   │   :6379       │ │   :5432    │       │ (external) │
   └──────────────┘ └────────────┘       └────────────┘
```

### Demo flow

1. **Open dashboard** - see the full dependency graph immediately
2. **Click "frontend"** - navigate through dependencies recursively
3. **Click into "payments-service"** - see its contract details
4. **Switch between versions** (v1.0.0 → v1.1.0 → v2.0.0)
5. **View the diff** between v1.1.0 and v2.0.0 - see the breaking change clearly
6. **Navigate to dependencies** from inside the detail view (postgres, stripe-api)

### Breaking change story (payments-service)

| Version | Type | What changed |
|---------|------|-------------|
| v1.0.0 | Baseline | `POST /charges`, `GET /charges/{id}`, `POST /refunds` |
| v1.1.0 | Non-breaking | Adds `POST /webhooks/stripe`, adds optional `WEBHOOK_SECRET` config |
| v2.0.0 | **BREAKING** | Removes `POST /charges` and `GET /charges/{id}`. Adds `POST /payment-intents` and `GET /payment-intents/{id}`. Renames `STRIPE_API_KEY` to `STRIPE_SECRET_KEY`. Replaces `amount_cents` with `amount` + `currency`. |

### Services by layer

| Layer | Service | Port | Dependencies | Description |
|-------|---------|------|-------------|-------------|
| edge | frontend | 3000 | api-gateway | Customer-facing storefront |
| edge | api-gateway | 8080 | auth, orders, payments | Routes traffic, enforces auth |
| domain | auth-service | 8080 | redis | JWT auth and session management |
| domain | orders-service | 8080 | postgresql | Order lifecycle management |
| domain | **payments-service** | 8080 | postgresql, stripe-api | Payment processing via Stripe |
| infra | postgresql | 5432 | - | Primary relational database |
| infra | redis | 6379 | - | Auth token and session cache |
| external | stripe-api | 443 | - | External payment provider |

---

## Other Scenarios

| File | Description |
|------|-------------|
| `00-infrastructure.yaml` | Kubernetes Service/Deployment stubs for all scenarios |
| `01-demo-oci.yaml` | Real OCI contracts from `ghcr.io/trianalab/pacto-demo` |
| `02-microservices.yaml` | E-commerce services with inline contracts |
| `03-stateful-infrastructure.yaml` | Stateful services (postgres, redis, rabbitmq) |
| `04-platform-standards.yaml` | Definition-only contracts and cross-references |
| `05-advanced-patterns.yaml` | Hybrid state, scheduled jobs, event consumers |
| `06-error-scenarios.yaml` | Invalid contracts and missing targets |

## Expanding the demo

To add a new service to the demo:

1. Add a Pacto CR to `07-demo-showcase.yaml` with an inline contract (namespace: `demo`)
2. Add the corresponding Service + Deployment to `00-infrastructure.yaml` (namespace: `demo`)
3. Wire it into the dependency graph by adding `dependencies` entries in upstream services
4. If modeling versions, add PactoRevision objects to `07-demo-showcase-revisions.yaml`

### Namespace isolation

The demo uses the `demo` namespace. Other scenarios (01–06) use `default`. This means you can safely apply the demo alongside any other scenario without name collisions.
