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

GitlessOps means signed artifacts in your registry and evidence decide which
version runs where, while your code and manifests stay in git. GitlessOps is
three principles, and each one holds only under its stated condition:

1. **Desired versions are immutable artifacts in a registry.** Every version
   Pacto promotes is an OCI digest that `pacto publish` signed. Which digest
   runs where is a pointer Pacto writes onto a Flux
   [OCIRepository](https://pacto.run/latest/glossary/#ocirepository) or an OCI-sourced
   [Argo CD](https://pacto.run/latest/glossary/#argo-cd) Application. Source code and manifests stay in git, and
   Flux or Argo CD still apply them.
2. **Evidence moves a change** on a Stage set to an
   automatic level. A person is asked only when the evidence runs out, and
   only the team concerned;
   [Why you were asked](https://pacto.run/latest/pactify-decide/#why-you-were-asked)
   lists every case.

3. **Every decision can be verified without Pacto.** With the
   [recorder](https://pacto.run/latest/glossary/#recorder) on, each finished
   operation leaves a signed [in-toto](https://pacto.run/latest/glossary/#in-toto) record as an OCI
   referrer, checkable with [oras](https://pacto.run/latest/glossary/#oras) and [cosign](https://pacto.run/latest/glossary/#cosign); see
   [Verify a record](https://pacto.run/latest/verify-a-record/).

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
[transitive closure](https://pacto.run/latest/glossary/#transitive-closure) by digest. A catalog entry such as `dependsOn: auth` records
only that the edge exists.

The range lives in a versioned artifact, so you can compare it with the
previous revision, with the rules your platform sets for every contract
(policies) and with what is deployed. A CI job, a controller or an agent needs
that comparison before it concludes anything.

## Tenets

Pacto tells you which services a change breaks before it ships, and checks
what is actually running. These tenets decide its design questions. They are
in priority order: when two conflict, the earlier one wins.

1. **Every feature answers who a change breaks.** A feature belongs in Pacto
   when it makes that answer more accurate, available earlier or easier to
   act on.
2. **A confirmed contradiction is an error. A missing observation is
   Unknown.** Pacto reports a pass only for what it checked, and says so when
   it cannot see.
3. **The contract describes the service you already run.** Pacto works with
   existing services in any language, and adopting it never requires a
   change to service code.
4. **Declaration stays separate from observation.** The contract records
   author intent. Collectors gather runtime state, and Pacto evaluates that
   state against the contract, so one contract is validated at authoring
   time, compared in CI and checked against a running system.
5. **Every dependency states what it relies on.** A dependency names the
   provider, the versions it accepts and the behavior it needs from it. CI
   can evaluate a range, and a bare edge gives it nothing to evaluate.
6. **Compatibility is computed from the contract.** The diff rules, the
   version ranges and the lock decide whether a change is compatible. A
   version number is a claim that Pacto checks.
7. **Contracts travel through existing infrastructure.** Each revision is an
   immutable OCI artifact, carried by the registries and the auth that
   already move container images. A contract references the schemas its
   interfaces already have, so it stays correct when they change.
8. **The core observes. Acting is opt-in.** The core never changes what runs.
   The opt-in delivery component moves a version only when this change's
   compatibility, its impact on the consumers and its checks back it. A
   [Stage](https://pacto.run/latest/glossary/#stage) earns the right to move
   on its own one level at a time, and a person confirms each level. When
   Pacto cannot see enough, it holds the change and asks a person on the
   teams concerned.
9. **People, CI and agents take the same path.** They get the same checks,
   the same answer and the same next step.

## Agents take the same path

An agent, like any automation, reaches its goal by whatever path is open to
it. A golden path it can skip protects little. Pacto gives agents building
blocks that it checks and, with the recorder on, records. Those blocks are the
only way Pacto makes a change.

Every change Pacto makes goes through the same checks for its type and the
same admission policies, whether a person, a CI job or an agent over MCP asks
for it. Agents get no approve tool, and the policies refuse an approval from
an agent.

### What stays outside the path

The admission policies decide who may write an Operation, an Approval and a
pointer's digest. They do not cover workloads, objects without the pointer
label or anyone allowed to delete the policies. RBAC alone limits
`kubectl apply` on a workload. Your platform closes the rest with RBAC and
its own admission. The [delivery security model](https://pacto.run/latest/integrations/kubernetes/delivery-security/)
gives the detail.

## Credits

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
