---
title: Quickstart
layout: default
nav_order: 3
---

# Quickstart
{: .no_toc }

Get a valid Pacto contract running in under two minutes.

---

<details open markdown="block">
  <summary>Table of contents</summary>
- TOC
{:toc}
</details>

## 1. Install Pacto

```bash
go install github.com/trianalab/pacto/cmd/pacto@latest
```

## 2. Initialize a new contract

```bash
pacto init my-service
```

This creates a complete bundle structure:

```
my-service/
  pacto.yaml
  interfaces/
    openapi.yaml
  configuration/
    schema.json
```

## 3. Validate the contract

```bash
pacto validate my-service
```

```
my-service is valid
```

The generated contract passes all three validation layers out of the box.

## 4. Pack the bundle

```bash
pacto pack my-service
```

```
Packed my-service@0.1.0 -> my-service-0.1.0.tar.gz
```

## 5. Push to a registry

```bash
# Authenticate first (or use gh auth for GitHub registries)
pacto login ghcr.io -u your-username

# Push the bundle (auto-tags with contract version)
pacto push oci://ghcr.io/your-org/my-service-pacto -p my-service
```

```
Pushed my-service@0.1.0 -> ghcr.io/your-org/my-service-pacto:0.1.0
Digest: sha256:a1b2c3...
```

## 6. Pull from a registry

```bash
pacto pull oci://ghcr.io/your-org/my-service-pacto:0.1.0
```

## 7. Inspect the contract

```bash
pacto explain my-service

# Or generate rich Markdown documentation
pacto doc my-service
```

```
Service: my-service@0.1.0
Owner: team/my-team
Pacto Version: 1.0

Runtime:
  Workload: service
  State: stateless
  Persistence: local/ephemeral
  Data Criticality: low

Interfaces (1):
  - api (http, port 8080, internal)

Scaling: 1-3
```

---

## Next steps

- [Customize your contract]({{ site.baseurl }}{% link contract-reference.md %}) to match your service
- Read the guide for [developers]({{ site.baseurl }}{% link developers.md %}) or [platform engineers]({{ site.baseurl }}{% link platform-engineers.md %})
- Explore [example contracts]({{ site.baseurl }}{% link examples/index.md %}) for common services like PostgreSQL and Redis
- Learn how to [build plugins]({{ site.baseurl }}{% link plugins.md %}) for artifact generation
