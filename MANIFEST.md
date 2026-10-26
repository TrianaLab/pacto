# The Pacto Manifesto

## The problem

A service's operational knowledge is real, but it has no home.

The API surface has an OpenAPI spec. The container has a registry. The
deployment has a Helm chart. The service *as an operating thing* is written down
nowhere. Not what it is, what it exposes, how it is configured, what it depends
on, what compatibility it guarantees or whether reality still matches any of
that. Those facts are scattered across artifacts that were never designed to
describe a service and do not talk to each other:

- OpenAPI describes one HTTP interface. It says nothing about the service that serves it.
- Helm charts encode deployment mechanics for one orchestrator. They are not the service's intent.
- Kubernetes manifests carry health checks and wiring with no link back to a service definition.
- Configuration lives in `.env.example` files, wikis or nowhere.
- Dependencies live in Slack threads and in the heads of the people who wired them.
- READMEs go stale the day they are written.

Every consumer that needs to understand a service reassembles that picture from
those fragments and fills the gaps with assumptions. The consumers: a platform,
a CI pipeline, a controller, an on-call engineer, increasingly a program acting
on someone's behalf. The picture is expensive to rebuild, different every time
it is rebuilt and wrong the moment any fragment drifts.

## A contract is version-shaped; a catalog entry is not

A catalog entry saying `dependsOn: auth` records that the edge exists. A Pacto
dependency records that this revision accepts `auth ^2.0.0`, and `pacto.lock`
pins the rest of the transitive closure by digest.

The first is a fact about the present, in a mutable store. The second can be
compared — against the previous revision, against the rules your platform sets
for every contract (policies) and against what is deployed. That comparison is what a CI job, a
controller or an agent needs before it concludes anything.

## Principles

- **Operational knowledge is a first-class artifact.** It deserves the rigor
  already given to API specs and container images: authored, versioned,
  validated, distributed and verified.

- **Declaration is separate from observation.** The contract is stable author
  intent. Runtime state is external to it, gathered by collectors (agents that
  watch running services) and evaluated against it. Observation is never written back into the declaration, which is
  what lets one contract be validated at authoring time, diffed in CI and
  checked against a running system.

- **Compose, don't reinvent.** The interfaces a service exposes already have
  schemas, each owned by the system that maintains it. A contract references
  those schemas instead of inventing a configuration language. A reference is
  correct by construction; a copy is wrong the moment the original changes.

- **Distributed through existing infrastructure.** Contracts are OCI artifacts.
  They travel through the registries, the auth and the tooling that already
  carry container images. No new distribution plane.

- **Act only on what each change can show.** The opt-in delivery component
  moves a version on its own only when this change's compatibility, its
  impact on the consumers and its checks back it. A Stage (one service in one
  environment) earns the right to
  move on its own one rung at a time, and a person confirms each rung; a good
  track record never replaces the evidence for the next change. When Pacto
  cannot see enough it holds the change and asks the teams concerned, never an
  agent.

## What GitlessOps means

In plain words: which version runs where is set from signed artifacts in
your registry and from evidence, not from commits to git. Your code and
manifests stay in git. GitlessOps is three principles, each true only under its condition:

1. **Desired versions are immutable artifacts in a registry.** Every version
   Pacto promotes is an OCI digest that `pacto publish` signed. Which digest
   runs where is a pointer Pacto writes onto a Flux OCIRepository or an
   OCI-sourced Argo CD Application. Source code and manifests stay in git, and
   Flux or Argo CD still apply them.
2. **Evidence, not a pull request, moves a change**, on a Stage set to an
   automatic rung. A person is asked when a check cannot see enough
   (Unknown: the service's owners), when a consumer's range rejects it (that consumer's
   owners), when the Stage is set to ask before every promote, on auto for non-breaking
   when the change is breaking, or when a failed version has no safe way back
   and the Stage freezes. Only the affected team is asked.
3. **Every decision can be verified without Pacto.** With the recorder (the
   part of Pacto that signs records) on,
   each finished operation leaves a signed in-toto record as an OCI referrer,
   checkable with `oras` and `cosign`. That needs a registry with the
   Referrers API, which GitHub's does not serve today.

Two ideas came first. [ConfigHub](https://confighub.com) treats configuration
as data in a database behind an API, not as templates; Pacto keeps only "which version
runs where" that way. Anthropic's
[Scion](https://www.anthropic.com/engineering/redesigning-source-control)
addresses stored content by what it holds, moves a pointer with one compare-and-swap and
tests after a change merges and before it deploys. The contract graph is
ours, and all of it is open source under the MIT licence.
