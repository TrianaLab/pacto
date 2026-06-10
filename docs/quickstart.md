# Quickstart
From zero to a published contract in 2 minutes.

---
## 1. Install Pacto

```bash
curl -fsSL https://raw.githubusercontent.com/TrianaLab/pacto/main/scripts/get-pacto.sh | bash
```

Or via Go:

```bash
go install github.com/trianalab/pacto/cmd/pacto@latest
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

This scaffolds a bundle with a valid contract, a placeholder OpenAPI spec, and a configuration JSON Schema. Only `pacto.yaml` is required — the `interfaces/` and `configuration/` directories are optional conveniences that you can remove if your service doesn't need them.

## 3. Validate

```bash
$ pacto validate my-service
my-service is valid
```

Validation runs four layers: structural (JSON Schema), cross-field (references and consistency), semantic (strategy checks), and policy enforcement. The generated contract passes all four out of the box.

## 4. Customize your contract

Edit `my-service/pacto.yaml` to match your service. A minimal contract only requires `pactoVersion` and `service`:

```yaml
pactoVersion: "1.0"

service:
  name: my-service
  version: 1.0.0
  owner: team/backend
```

Add sections as needed — interfaces, runtime semantics, dependencies, configuration, policy, scaling. See the [Contract Reference](contract-reference.md) for every available field.

## 5. Add readiness (optional, v1.1+)

The `readiness` section declares operational evidence for the service. It requires `pactoVersion: "1.1"` — declaring it under `1.0` is rejected at validation. Bump the version and add at least one check:

```yaml
pactoVersion: "1.1"

readiness:
  minScore: 80
  checks:
    - id: dashboard
      type: url
      evidence: https://grafana.company.com/my-service
      weight: 20
      expires: 2026-12-31
```

Then run the opt-in readiness gate:

```bash
$ pacto validate my-service --readiness
```

The gate compares each check's `expires` against the run time, so its result changes as evidence goes stale — that's why plain `pacto validate` does not enforce it. See [Contract Reference](contract-reference.md#readiness) for the full scoring and gate semantics.

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
Owner: team/backend
Pacto Version: 1.0

Runtime:
  Workload: service
  State: stateless
  Persistence: local/ephemeral
  Data Criticality: low

Interfaces (1):
  - api (http, port 8080, internal)

Scaling: 1-3

# Rich Markdown documentation
$ pacto doc my-service --serve
Serving documentation at http://127.0.0.1:8484
```

## 8. Detect breaking changes

Make a change to your contract (e.g. modify a port number) and diff it against the version you just pushed:

```bash
# Edit your contract — change the port in pacto.yaml
# Then diff against the published version
$ pacto diff oci://ghcr.io/your-org/my-service-pacto:1.0.0 my-service
Classification: BREAKING
Changes (1):
  [BREAKING] interfaces.port (modified): interfaces.port modified [8080 -> 9090]
```

Exit code is non-zero when breaking changes are detected — use this in CI to gate merges. See [For Platform Engineers](platform-engineers.md) for the full CI integration guide.

---

## What to do next

| Goal | Guide |
|------|-------|
| Understand every contract field | [Contract Reference](contract-reference.md) |
| Write and maintain contracts | [For Developers](developers.md) |
| Consume contracts for deployment | [For Platform Engineers](platform-engineers.md) |
| See contracts for real services | [Examples](examples/index.md) (PostgreSQL, Redis, RabbitMQ, NGINX, gRPC, and more) |
| Integrate with CI/CD | [GitHub Actions](github-actions.md) |
| Explore contracts visually | Run `pacto dashboard` to launch the web UI with dependency graph |
| Runtime compliance in Kubernetes | [Kubernetes Operator](operator.md) |
| Build a generation plugin | [Plugin Development](plugins.md) |
