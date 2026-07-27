# Quickstart
From zero to a published contract in 2 minutes.

---
## 1. Install Pacto

```bash
curl -fsSL https://raw.githubusercontent.com/TrianaLab/pacto/main/scripts/get-pacto.sh | bash
```

Or via Go:

```bash
go install github.com/trianalab/pacto/v3/cmd/pacto@latest
```

See [Installation](installation.md) for more options.

## 2. Scaffold a contract

```bash
$ pacto init my-service
Created my-service/
  my-service/pacto.yaml
  my-service/interfaces/
  my-service/configuration/
```

This scaffolds a bundle with a valid contract, a placeholder OpenAPI spec and a configuration JSON Schema. Only `pacto.yaml` is required — if your service doesn't use them, remove the `interfaces/` and `configuration/` directories along with the `interfaces:`/`configurations:` sections in `pacto.yaml` (deleting the directories alone breaks validation). The scaffold's `health` and `metrics` capabilities are declared without an interface binding, so they need no changes.

These are standard formats — OpenAPI for interfaces, JSON Schema for configuration — so you can drop in the interface files you already own (for example your Helm chart's `values.schema.json`) instead of authoring new ones. Pacto composes the interfaces you already have rather than inventing a config language.

## 3. Validate

```bash
$ pacto validate my-service
my-service is valid
```

Validation runs three layers — structural, cross-field and policy enforcement. See the [Contract Reference](contract-reference/validation.md#validation-layers) for the full rules.

## 4. Customize your contract

Edit `my-service/pacto.yaml` to match your service. A minimal contract only requires `pactoVersion` and `service`:

```yaml
pactoVersion: "2.0"

service:
  name: my-service
  version: 1.0.0
  owner:
    team: backend
```

Add sections as needed — interfaces, workload, state, capabilities, dependencies, configuration, policy, readiness. See the [Contract Reference](contract-reference/index.md) for every available field.

## 5. Add readiness (optional)

The `readiness` section declares operational readiness state for the service. It is a `pactoVersion: "2.0"` feature. Add at least one claim with a status:

```yaml
pactoVersion: "2.0"

readiness:
  expires: 2099-12-31
  minScore: 80
  claims:
    - id: dashboard
      type: url
      status: done
      category: observability
      weight: 20
      evidence: https://grafana.example.com/d/service-dashboard
      description: Grafana dashboard exists

    - id: runbook
      type: document
      status: partial
      category: documentation
      weight: 15
      evidence: docs/runbook.md
      description: Basic runbook exists
```

Then run the opt-in readiness gate:

```bash
$ pacto validate my-service --readiness
```

The gate derives a score from claim statuses and weights, comparing it against `minScore`. The result is time-dependent (expired assessments score 0), so plain `pacto validate` does not enforce it. See [Contract Reference](contract-reference/sections.md#readiness) for the full scoring and gate semantics.

## 6. Pack and push

```bash
# Create a tar.gz bundle
$ pacto pack my-service
Packed my-service@1.0.0 -> my-service-1.0.0.tar.gz

# Authenticate (or use gh auth for GitHub registries)
$ pacto login ghcr.io -u your-username

# Push to any OCI registry (auto-tags with the contract version)
# Skips if the artifact already exists; use --force to overwrite
$ pacto push oci://ghcr.io/your-org/my-service-pacto -p my-service
Pushed my-service@1.0.0 -> ghcr.io/your-org/my-service-pacto:1.0.0
Digest: sha256:a1b2c3...
```

## 7. Pull and inspect

```bash
# Pull from the registry
$ pacto pull oci://ghcr.io/your-org/my-service-pacto:1.0.0

# Human-readable summary
$ pacto explain my-service
Service: my-service@1.0.0
Owner: backend
Pacto Version: 2.0

Workload: service

State:
  Type: stateless
  Persistence: local/ephemeral
  Data Criticality: low

Capabilities (2):
  - health
  - metrics

Interfaces (1):
  - api (openapi: interfaces/openapi.yaml, internal)

# Serve an interactive documentation site
$ pacto doc my-service --serve
Serving documentation at http://127.0.0.1:8484
```

## 8. Detect breaking changes

Make a change to your contract (e.g. remove an endpoint from the OpenAPI spec) and diff it against the version you just pushed:

```bash
# Edit your contract — remove the /users path from interfaces/openapi.yaml
# Then diff against the published version
$ pacto diff oci://ghcr.io/your-org/my-service-pacto:1.0.0 my-service
Classification: BREAKING
Changes (1):
  [BREAKING] openapi.paths[/users] (removed): API path /users removed [- /users]
```

Exit code is non-zero when breaking changes are detected — use this in CI to gate merges. See [Detecting breaking changes](developers.md#detecting-breaking-changes) and the official [GitHub Actions](github-actions.md) integration for wiring this into CI.

---

## What to do next

| Goal | Guide |
|------|-------|
| Understand every contract field | [Contract Reference](contract-reference/index.md) |
| Write and maintain contracts | [For Developers](developers.md) |
| Consume contracts for deployment | [For Platform Engineers](platform-engineers.md) |
| See contracts for real services | [Examples](examples/index.md) (PostgreSQL, Redis, RabbitMQ, NGINX, gRPC, and more) |
| Integrate with CI/CD | [GitHub Actions](github-actions.md) |
| Explore contracts visually | Run `pacto dashboard` to launch the web UI with dependency graph |
| Runtime compliance in Kubernetes | [Kubernetes Operator](integrations/kubernetes/overview.md) |
| Build a generation plugin | [Plugin Development](plugins.md) |
