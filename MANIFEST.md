# The Pacto Manifesto

## The Problem

A service's operational knowledge is real, but it has no home.

The API surface has an OpenAPI spec. The container has a registry. The deployment has a Helm chart. But the service *as an operating thing* — what it is, what interfaces and capabilities it exposes, how it is configured, what it depends on, which constraints apply to it, what compatibility it guarantees and whether reality still matches any of that — is never written down in one place. It is scattered across artifacts that were never designed to describe a service and do not talk to each other:

- OpenAPI describes one HTTP interface. It says nothing about the service that serves it.
- Helm charts encode deployment mechanics for one orchestrator. They are not the service's intent.
- Kubernetes manifests carry health checks and wiring with no link back to a service definition.
- Configuration lives in `.env.example` files, wikis or nowhere.
- Dependencies live in Slack threads and in the heads of the people who wired them.
- READMEs go stale the day they are written.

Every consumer that needs to understand a service — a platform, a CI pipeline, a controller, an on-call engineer and increasingly an autonomous agent — has to reassemble that picture from those fragments and fill the gaps with assumptions. The picture is expensive to rebuild, it is different every time it is rebuilt and it is wrong the moment any fragment drifts.

This is not a tooling gap. It is the absence of a shared, machine-readable layer that states what a service is operationally, independently of the systems that build it, run it or watch it.

## Why interface specs are not enough

Interface specs solve part of this, and they are worth composing rather than replacing. An OpenAPI document, a JSON Schema, a protobuf definition — each describes one interface precisely, and each is already owned by the system that maintains it.

What no single interface spec can express is the layer around and between those interfaces: which service owns them, how they relate, what the service depends on, what it persists, which policies constrain it, how it changes across versions and how far a change reaches. A schema describes an interface. It does not describe a service, and it does not describe how interfaces change over time. That relational and temporal layer is exactly the operational knowledge that stays implicit today.

## The Thesis

Operational knowledge must be explicit, declarative, machine-readable and versioned — one contract a service owns, not six fragments a consumer reassembles.

Pacto is that contract. A single file (`pacto.yaml`), plus the interface files it composes, captures what a service *is* operationally: its identity and ownership, the interfaces and capabilities it exposes, how it is configured, what it depends on, the policies and compatibility ranges that apply to it. It is authored alongside the code, validated automatically, versioned with semver and distributed as an OCI artifact through the same registries that already carry container images. Consumers read it instead of re-inferring it.

The contract states stable operational *intent*. It is deliberately not a deployment manifest and not a snapshot of every runtime detail. How a service is built, scheduled, scaled and wired is a delivery concern owned by the platform. What reality currently looks like is an *observation*, gathered by collectors and evaluated against the contract — never baked into the declaration itself. Keeping declaration and observation separate is what lets one contract be validated at authoring time, diffed in CI and verified against a running system without the contract having to know how any of those systems work.

## Why this matters more as automation gains autonomy

The consumers of operational knowledge have shifted over time, and the direction is consistent.

Traditional operations centralised operational knowledge in a team that ran everything. DevOps moved ownership to the teams that wrote the services, which spread the knowledge closer to the source but often redistributed the complexity rather than removing it. Platform engineering encoded that knowledge into self-service platforms, golden paths and internal APIs, so a developer could consume infrastructure through a stable interface instead of a runbook.

Autonomous agents change the primary *consumer* again. An agent does not need a fixed human portal: it can discover what interfaces and tools exist, combine them and assemble a path dynamically. This does not remove platform abstractions — it changes their form. The interface an agent needs is no longer a page in a portal but a machine-readable description of the system: its identity, its capabilities, its relationships, its constraints and its expected state. Cloud APIs, infrastructure-as-code and declarative systems made self-service platforms possible; agents make dynamic consumption possible. But dynamic consumption is only safe when the knowledge it consumes is explicit, versioned and verifiable, and when the actions it takes are bounded by external controls — not when it is inferred from fragmented docs and implicit assumptions.

Pacto is useful today without any of this. It catches breaking changes, resolves dependency graphs, enforces policy and verifies runtime fidelity for entirely human-driven platforms. Agents do not justify the contract; they raise the cost of not having one. A shared operational contract is what makes both a platform and an agent able to reason about a service instead of guessing at it.

## Principles

**Operational knowledge is a first-class artifact.** It deserves the same rigor as API specs and container images — authored, versioned, validated, distributed and verified.

**Declarative over procedural.** A contract describes *what* a service is, not *how* to run it. It is committed alongside source code, versioned with semver and immutable once published. Consumers decide how to act on it.

**Declaration is separate from observation.** The contract is stable author intent. Runtime state and environment evidence are external to it, gathered by collectors and evaluated against it. The engine combines declared intent and evidence into structured, explainable results; it does not embed runtime state in the declaration.

**Implementation-agnostic.** The contract describes a service independently of any orchestrator, deployment tool or platform. A stateful service with a public HTTP interface is that whether it runs on Kubernetes, Nomad or bare metal.

**Distributed through existing infrastructure.** Contracts are OCI artifacts. They use the registries, the auth and the tooling that already carry container images. No new distribution plane.

**Compose, don't reinvent.** The interfaces a service exposes already have schemas, each owned by the system that maintains it. Pacto references those schemas instead of inventing a configuration language: the configuration you declare in a contract *is* the configuration that feeds the system beneath it, validated once, with no second definition to drift. A reference is correct by construction; a copy is wrong the moment the original changes.

**Invalid contracts must not propagate.** Every contract passes structural, cross-field, semantic and policy validation before it can be published. If it is invalid it does not reach the registry. If it introduces a breaking change, CI catches it.

## What Pacto Is

A shared, machine-readable operational contract for a service, and the engine that reasons over it.

The contract composes a service's interfaces and adds the operational layer no single interface owns: identity, capabilities, configuration, dependencies, policies, compatibility and readiness. The engine is a pure function over a contract and evidence: it produces typed, explainable findings and a coverage measure, distinguishing a confirmed contradiction from an inability to observe. Around that core, collectors gather evidence, plugins generate artifacts, controllers act and a dashboard makes all of it visible. Everything a platform, a pipeline or an agent needs to inspect and validate a service comes from one versioned artifact.

## What Pacto Is NOT

**Not an autonomous infrastructure agent.** Pacto describes and verifies services. It does not decide to act on infrastructure on its own.

**Not a replacement for your orchestrator or delivery tools.** Kubernetes, Helm, Crossplane, Argo CD and Terraform still schedule, template, provision and deploy. Pacto adds the operational meaning they act on; it makes zero deployment decisions.

**Not a policy-enforcement engine.** OPA, Kyverno and admission control decide whether an action is *permitted* at runtime. Pacto validates that a contract satisfies declared policy schemas at authoring time; it does not intercept or gate live actions.

**Not an identity or authorization system.** Pacto does not grant, scope or revoke permissions for humans or agents. Deciding who or what may do something stays with IAM and admission systems.

**Not a developer portal.** The dashboard renders contracts and runtime state; it is not an internal developer platform and does not replace one. It can feed data into one.

**Not tied to one agent framework.** Capabilities and interfaces can be projected to agents, and the Model Context Protocol is one such integration surface. MCP is a mechanism, not the definition of Pacto.

**Not a universal model of every infrastructure resource.** Pacto describes services and their operational contract, not every object in a cluster or cloud.

## The Endgame

Consumers should not reverse-engineer services. They should read a contract.

**Contract-driven platforms.** Platforms consume contracts to generate manifests, provision infrastructure and configure networking. The contract is the input; the platform is the function.

**Policy as validation, enforcement where it belongs.** Organizations express standards as policy schemas that contracts must satisfy before publication. Whether a live action is allowed stays with the runtime controls built for that job; the contract gives them a structured, shared object to reason about.

**Lifecycle-wide verification.** Contracts are validated when authored, diffed in CI and verified against runtime evidence continuously. Breaking changes are caught before production; drift between declared intent and observed reality is surfaced as it happens.

**A shared language, not another control plane.** A structured, validated, versioned contract is a natural integration point for anything that needs to understand a service — CI systems, platform controllers, compliance tooling and autonomous agents that can read, generate and reason about contracts, while the systems that permit, perform and observe actions keep doing their jobs.

The contract is the shared operational language between the people who build services, the platforms that run them and the automation that increasingly consumes both.
