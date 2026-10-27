# The Pacto Manifesto

## The problem

Moving a new version into an environment is still a pull request: someone
bumps a tag in git, someone else approves it and a GitOps tool applies it.
That made sense while a person wrote every change. Agents now produce changes
faster than people can review them. The approval also rarely answers the real
question: whether this version breaks the services that depend on it.

Answering that question needs a description of each service as an operating
thing, and the pipeline has none. A reviewer approves a one-line diff, and the
safety of the change rests on whoever remembers who calls whom.

Pacto moves that one line out of git and decides it from evidence. The
contract graph says who depends on a service and which versions each accepts,
checks say whether the new version behaves and a person is asked only when
the evidence runs out.

## What GitlessOps means

GitlessOps sets which version runs where from signed artifacts in your
registry and from evidence. Your code and manifests stay in git. GitlessOps is
three principles, and each one holds only under its stated condition:

1. **Desired versions are immutable artifacts in a registry.** Every version
   Pacto promotes is an OCI digest that `pacto publish` signed. Which digest
   runs where is a pointer Pacto writes onto a Flux OCIRepository or an
   OCI-sourced Argo CD Application. Source code and manifests stay in git, and
   Flux or Argo CD still apply them.
2. **Evidence moves a change** on a Stage set to an
   automatic level. A person is asked only when the evidence runs out, and
   only the team concerned:

    - a check cannot see enough (Unknown): the service's owners;
    - a consumer's range rejects the change: that consumer's owners;
    - the Stage is set to ask before every promote;
    - the Stage is on auto for non-breaking and the change is classified
      anything other than non-breaking;
    - a failed version has no safe way back, so the Stage freezes.

3. **Every decision can be verified without Pacto.** With the recorder (the
   part of Pacto that signs records) on,
   each finished operation leaves a signed in-toto record as an OCI referrer,
   checkable with `oras` and `cosign`. That needs a registry with the
   Referrers API. GitHub's registry does not serve it today.

## Contracts are the foundation

A service's operational knowledge is real, but it has no home.

The API surface has an OpenAPI spec. The container has a registry. The
deployment has a Helm chart. The service *as an operating thing* has no
document: what it is, what it exposes, how it is configured, what it depends
on, what compatibility it guarantees and whether reality still matches. Those
facts are spread across artifacts built for other jobs, with no links between
them:

- OpenAPI describes one HTTP interface.
- Helm charts encode how one orchestrator deploys the service.
- Kubernetes manifests carry health checks and wiring with no link back to a service definition.
- Configuration lives in `.env.example` files, wikis or nowhere.
- Dependencies live in Slack threads and in the heads of the people who wired them.
- READMEs go stale the day they are written.

Every consumer that needs to understand a service reassembles that picture from
those fragments and fills the gaps with assumptions. The consumers: a platform,
a CI pipeline, a controller, an on-call engineer, increasingly a program acting
on someone's behalf. The picture is expensive to rebuild, different every time
it is rebuilt and wrong the moment any fragment drifts.

## Contracts record version ranges

A Pacto dependency records which versions of another service a revision
accepts, for example `auth ^2.0.0`. `pacto.lock` pins the rest of the
transitive closure by digest. A catalog entry such as `dependsOn: auth` records
only that the edge exists.

The range lives in a versioned artifact, so you can compare it with the
previous revision, with the rules your platform sets for every contract
(policies) and with what is deployed. A CI job, a controller or an agent needs
that comparison before it concludes anything.

## Principles

- **Operational knowledge is a first-class artifact.** It deserves the rigor
  already given to API specs and container images: authored, versioned,
  validated, distributed and verified.

- **Declaration is separate from observation.** The contract is stable author
  intent. Runtime state is external to it, gathered by collectors (agents that
  watch running services) and evaluated against it. Pacto keeps observations
  out of the declaration, so one contract can be validated at authoring time,
  diffed in CI and checked against a running system.

- **Reference existing schemas.** The interfaces a service exposes already
  have schemas, each owned by the system that maintains it. A contract
  references those schemas. A reference stays correct when the original
  changes, while a copy goes stale.

- **Distributed through existing infrastructure.** Contracts are OCI artifacts.
  They travel through the registries, the auth and the tooling that already
  carry container images.

- **Act only on what each change can show.** The opt-in delivery component
  moves a version on its own only when this change's compatibility, its
  impact on the consumers and its checks back it. A Stage (one service in one
  environment) earns the right to
  move on its own one level at a time, and a person confirms each level. Each
  change still needs its own evidence, whatever the track record. When Pacto
  cannot see enough, it holds the change and asks a person on the teams
  concerned.

Pacto builds on ideas from others. The [Flux](https://fluxcd.io/flux/concepts/)
team pioneered Gitless GitOps in 2022: container registries hold desired
state as OCI artifacts and controllers read it from there. On Flux, Pacto
moves a version by writing its digest onto an OCIRepository.
[ConfigHub](https://confighub.com) treats configuration as data in a database
behind an API. Pacto stores only "which version runs where" that way.
Anthropic's [Scion](https://www.anthropic.com/research/redesigning-source-control)
addresses stored content by what it holds, moves a pointer with one
compare-and-swap and tests after a change merges and before it deploys. The
contract graph is ours, and all of it is open source under the MIT licence.
