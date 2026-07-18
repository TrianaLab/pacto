# The Pacto Manifesto

## The Problem

Operational behavior is implicit. It shouldn't be.

A service's API has a spec. Its container image has a registry. Its deployment has a chart. But the service itself — what it exposes, what it depends on, how it persists data, how it scales, what breaks when you change it — has no spec at all.

That knowledge lives in six places that don't talk to each other:

- OpenAPI describes the API surface. Not the runtime.
- Helm charts encode deployment mechanics for one orchestrator. Not the service's intent.
- Environment variables are documented in wikis, `.env.example` files, or nowhere.
- Kubernetes manifests hardcode ports and health checks with no link to the service definition.
- Dependencies live in Slack threads and team leads' heads.
- README files go stale the day they're written.

Platforms reverse-engineer services from these fragments. Developers ship code and hope someone figures out how to run it. Breaking changes surface in production. Dependency relationships are tribal knowledge.

This is not a tooling problem. It's the absence of a contract layer between development and operations.

## The Thesis

Operational behavior must be explicit, declarative, and machine-readable.

A service that exposes HTTP on port 8080, depends on auth-service ^2.0.0, persists data locally, and scales between 2 and 10 instances — that is its operational contract. It should be written down once, validated automatically, and consumed by every tool that needs to understand the service.

Pacto is that contract.

It is a single file (`pacto.yaml`) that captures what a service *is* operationally. Not how to deploy it. Not how to build it. What it is.

A single schema describes one interface in isolation — it cannot express how interfaces relate, or how they change. That is the contract's job: ownership, dependencies, compatibility, readiness and lifecycle. JSON Schema describes an interface; Pacto describes the relationships between interfaces and how they change over time.

If a platform has to guess whether a service is stateful, the contract is incomplete. If a dependency relationship only exists in someone's head, the contract is incomplete. If a breaking change reaches production undetected, the contract is incomplete.

## Principles

**Operational behavior is a first-class artifact.** It deserves the same rigor as API specs and container images — authored, versioned, validated, distributed, and verified.

**Declarative over procedural.** A contract describes *what*, not *how*. It is committed alongside source code, versioned with semver, and immutable once published. Platforms decide how to act on it.

**Implementation-agnostic.** The contract describes service behavior independent of any orchestrator, deployment tool, or platform. A stateful service with an HTTP interface on port 8080 is that — whether it runs on Kubernetes, Nomad, or bare metal.

**Distributed through existing infrastructure.** Contracts are OCI artifacts. They use the same registries, the same auth, and the same tooling as container images. No new infrastructure.

**Compose, don't reinvent.** The interfaces a service exposes already have schemas — an OpenAPI spec for its API, a JSON Schema for its configuration — each owned by the system that already maintains it. Pacto references those schemas instead of inventing a new configuration language: the configuration you declare in a contract *is* the configuration that feeds the system beneath it, validated once, with no second definition to drift out of sync. When an interface is already described and owned elsewhere, compose it rather than mirroring it — a copy is wrong the moment the original changes; a reference is correct by construction. Pacto is deliberately not another abstraction to learn.

**Runtime-aware.** If the contract doesn't capture state management, persistence, health checks, scaling bounds, and dependency relationships, it's just another API spec. Runtime semantics are what make it an operational contract.

**Invalid contracts must not propagate.** Every contract passes structural, cross-field, semantic, and policy validation before it can be published. If it's invalid, it doesn't reach the registry. If it introduces a breaking change, CI catches it.

## What Pacto Is

A standard for describing how a service behaves operationally.

The contract captures interfaces, dependencies, runtime semantics, configuration, scaling, readiness, and policy. It can be validated, diffed, distributed as an OCI artifact, resolved into a dependency graph, verified against running workloads, and explored through a dashboard.

## What Pacto Is NOT

**Not a deployment tool.** Pacto describes services. It does not deploy, orchestrate, or manage infrastructure. Platforms consume contracts and decide how to act.

**Not a service mesh.** No sidecars, no traffic interception. The operator watches custom resources and compares declared state to running workloads.

**Not a replacement for OpenAPI, JSON Schema or Helm.** Pacto references the schemas that already describe a service's interfaces — its OpenAPI spec, its config JSON Schema — and complements deployment tools by adding the operational context and cross-interface relationships they lack.

**Not a service catalog.** The dashboard visualizes contracts and runtime state. It is not a developer portal. It can feed data into one.

**Not a platform.** Pacto provides the contract layer. What you build on top — manifest generation, policy enforcement, automated provisioning — is up to you.

## The Endgame

Platforms should not reverse-engineer services. They should read a contract.

**Contract-driven platforms.** Platforms consume contracts to generate manifests, provision infrastructure, and configure networking. The contract is the input. The platform is the function.

**Policy as validation.** Organizations define policy schemas that contracts must satisfy. Non-compliant contracts fail validation before they reach a registry. Enforcement happens at authoring time, not after deployment.

**Lifecycle-wide verification.** Contracts are validated when authored, diffed in CI, and verified at runtime. Breaking changes are caught before production. Runtime drift is detected continuously.

**Machine-readable foundation.** A structured, validated contract is a natural integration point for any tool that needs to understand service behavior — CI systems, platform controllers, compliance tools, and AI agents that can read, generate, and reason about contracts.

The contract is the API between developers and the platform.
