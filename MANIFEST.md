# The Pacto Manifesto

## The problem

A service's operational knowledge is real, but it has no home.

The API surface has an OpenAPI spec. The container has a registry. The
deployment has a Helm chart. The service *as an operating thing* is written down
nowhere: what it is, what it exposes, how it is configured, what it depends on,
what compatibility it guarantees and whether reality still matches any of that.
Those facts are scattered across artifacts that were never designed to describe
a service and do not talk to each other:

- OpenAPI describes one HTTP interface. It says nothing about the service that serves it.
- Helm charts encode deployment mechanics for one orchestrator. They are not the service's intent.
- Kubernetes manifests carry health checks and wiring with no link back to a service definition.
- Configuration lives in `.env.example` files, wikis or nowhere.
- Dependencies live in Slack threads and in the heads of the people who wired them.
- READMEs go stale the day they are written.

Every consumer that needs to understand a service — a platform, a CI pipeline, a
controller, an on-call engineer, increasingly a program acting on someone's
behalf — reassembles that picture from those fragments and fills the gaps with
assumptions. It is expensive to rebuild, different every time it is rebuilt and
wrong the moment any fragment drifts.

## A contract is version-shaped; a catalog entry is not

A catalog entry saying `dependsOn: auth` records that the edge exists. A Pacto
dependency records that this revision accepts `auth ^2.0.0`, and `pacto.lock`
pins the rest of the transitive closure by digest.

The first is a fact about the present, in a mutable store. The second can be
compared — against the previous revision, against the policies the contract must
satisfy and against what is deployed. That comparison is what a CI job, a
controller or an agent needs before it concludes anything.

## Principles

- **Operational knowledge is a first-class artifact.** It deserves the rigor
  already given to API specs and container images: authored, versioned,
  validated, distributed and verified.

- **Declaration is separate from observation.** The contract is stable author
  intent. Runtime state is external to it, gathered by collectors and evaluated
  against it. Observation is never written back into the declaration, which is
  what lets one contract be validated at authoring time, diffed in CI and
  checked against a running system.

- **Compose, don't reinvent.** The interfaces a service exposes already have
  schemas, each owned by the system that maintains it. A contract references
  those schemas instead of inventing a configuration language. A reference is
  correct by construction; a copy is wrong the moment the original changes.

- **Distributed through existing infrastructure.** Contracts are OCI artifacts.
  They travel through the registries, the auth and the tooling that already
  carry container images. No new distribution plane.
