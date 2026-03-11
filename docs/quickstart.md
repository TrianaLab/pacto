---
title: Quickstart
layout: default
nav_order: 3
---

# Quickstart
{: .no_toc }

From zero to a published contract in 2 minutes.

---

<details open markdown="block">
  <summary>Table of contents</summary>
- TOC
{:toc}
</details>

---

## 1. Install Pacto

```bash
curl -fsSL https://raw.githubusercontent.com/TrianaLab/pacto/main/scripts/get-pacto.sh | bash
```

Or via Go:

```bash
go install github.com/trianalab/pacto/cmd/pacto@latest
```

See [Installation]({{ site.baseurl }}{% link installation.md %}) for more options.

## 2. Scaffold a contract

```bash
$ pacto init my-service
Created my-service/
  my-service/pacto.yaml
  my-service/interfaces/
  my-service/configuration/
```

This creates a complete bundle structure with a valid contract, a placeholder OpenAPI spec, and a configuration JSON Schema.

## 3. Validate

```bash
$ pacto validate my-service
my-service is valid
```

Validation runs three layers: structural (JSON Schema), cross-field (references and consistency), and semantic (strategy checks). The generated contract passes all three out of the box.

## 4. Customize your contract

Edit `my-service/pacto.yaml` to match your service. A minimal contract only requires `pactoVersion` and `service`:

```yaml
pactoVersion: "1.0"

service:
  name: my-service
  version: 1.0.0
  owner: team/backend
```

Add sections as needed — interfaces, runtime semantics, dependencies, configuration, scaling. See the [Contract Reference]({{ site.baseurl }}{% link contract-reference.md %}) for every available field.

## 5. Pack and push

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

## 6. Pull and inspect

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

---

## What to do next

| Goal | Guide |
|------|-------|
| Understand every contract field | [Contract Reference]({{ site.baseurl }}{% link contract-reference.md %}) |
| Write and maintain contracts | [For Developers]({{ site.baseurl }}{% link developers.md %}) |
| Consume contracts for deployment | [For Platform Engineers]({{ site.baseurl }}{% link platform-engineers.md %}) |
| See contracts for real services | [Examples]({{ site.baseurl }}{% link examples/index.md %}) (PostgreSQL, Redis, RabbitMQ, NGINX, Cron Worker) |
| Integrate with CI/CD | [GitHub Actions]({{ site.baseurl }}{% link github-actions.md %}) |
| Build a generation plugin | [Plugin Development]({{ site.baseurl }}{% link plugins.md %}) |
