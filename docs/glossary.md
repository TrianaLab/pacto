# Glossary

Every word these docs use with a special meaning, in plain words. The
dashboard and the command line use the same words; where the dashboard shows
a different label, it is in the last column.

## Contracts

| Word | Meaning | In the dashboard |
|---|---|---|
| Service | Something you run and other things call, such as `payments` | Service |
| Contract | One file, `pacto.yaml`, that says what a service offers, who owns it and which other services it calls | Contract |
| Version | One published copy of a contract, such as `payments 2.4.0`. It never changes once published | Version |
| Version range | Which versions of another service a contract accepts, such as `^2.0.0` (any 2.x) | — |
| Consumer | A service that calls another one | Consumers |
| Contract graph | All your contracts together: who calls whom and which versions each accepts | Graph |
| Registry | The place your container images are stored. Pacto stores contracts there too | — |
| Digest | The fingerprint of one exact published artifact (`sha256:...`) | — |
| Bundle | A contract together with the files it points to (OpenAPI documents, schemas), as one folder or one published artifact | — |
| Interface | One way a service is called, such as an HTTP API described by an OpenAPI file | Interfaces |
| External dependency | A dependency this Pacto system does not deliver, such as a managed database or a model. It has a contract and a version range, or only a version, and then anything relying on it is Unknown. See [external dependencies](contract-reference/dependencies-and-state.md#external-dependencies) | — |
| Suite | A test or eval file a version ships in its `checks` list. A Stage runs it, and only a verdict about that exact file counts. See [checks](contract-reference/checks.md) | — |
| Agent | An AI agent is a service like any other: its tools are an `mcp` interface, its model a dependency and its evals a suite. See [an agent is a service](contract-reference/checks.md#an-agent-is-a-service) | — |
| Policy | A rule a contract must follow, such as "every service names an owner", checked with the contract | Policy |
| Clear | The contracts clear a change when every consumer accepts the new version | — |
| Target | One running deployment of a service, such as payments in one cluster | Targets |
| Shadow consumer | A service seen calling another at runtime that no contract declares | — |
| Readiness assessment | A dated statement in a contract of which readiness claims are done, with evidence; it counts for nothing once it expires | Readiness |

## Checking what runs

| Word | Meaning | In the dashboard |
|---|---|---|
| Evidence | What was observed about a running service, such as which ports listen or which services it called | Evidence |
| Collector | What gathers evidence, such as the Kubernetes observer or the eBPF node agent | — |
| Finding | One contradiction between a contract and the evidence, such as `INTERFACE_ABSENT` | Findings |
| Evidence Server | The service that receives evidence and stores it in your registry next to the contract it is about | — |
| Referrer | A file a registry stores attached to an artifact, listed by the OCI Referrers API. Evidence and records are stored this way | — |

## Delivery

| Word | Meaning | In the dashboard |
|---|---|---|
| Environment | A place a service runs, such as `test` or `prod` | Environment |
| Stage | One service in one environment, and the version it runs there | Environments |
| Promote | Move a Stage to a newer version | Promote |
| Operation | One requested change to a Stage, from request to outcome | Change |
| Plan | What Pacto would do with a change, and who must agree, before anyone asks | Plan |
| Held | A change waiting for a person to decide | Inbox |
| Pass, Fail, Unknown | What each check answers. Unknown means it could not see enough to say | — |
| Accept the unknown | A person lets a change with an Unknown check go ahead. It is recorded as accepted, never as a pass | Accept the unknown |
| Approval | A person's decision on one held change | Approve |
| Rollback | Put back the version that ran before | Roll back |
| Freeze | Stop Pacto from moving a Stage until a person lets it move again | Frozen |
| Break-glass | A group allowed to force a version past every check | — |
| Record | A signed account of one finished change, stored in your registry and checkable without Pacto | Record |
| Recorder | The part of Pacto that signs records. Off until you turn it on | — |

## The autonomy ladder

| Word | Meaning |
|---|---|
| Rung | How much a Stage lets Pacto do on its own: observe, advise, auto for non-breaking or autonomous |
| observe | Pacto plans every change and moves nothing |
| advise | A person asks for or approves every change |
| auto for non-breaking | Pacto ships changes that break no one on its own (`auto-nonbreaking`) |
| autonomous | Pacto ships every change the contracts clear on its own |
| Cold start | A new Stage begins on observe and earns more freedom from there |
| Would have | On observe, what Pacto would have done with a change. Advice, not a record |
| Proposal | The next rung Pacto suggests, with its evidence or what blocks it |
| Earned autonomy | Raising a rung after enough good changes, confirmed by a person |
| Step-down | Dropping one rung after a version Pacto shipped with no person fails |

See [the autonomy ladder](autonomy.md).

## Ideas

| Word | Meaning |
|---|---|
| GitlessOps | Deciding which version runs where from published artifacts and evidence, not from pull requests to a git repository. Your code stays in git. See [What GitlessOps means](manifesto.md#what-gitlessops-means) |
| GitOps | Keeping a cluster in line with files in git, with a tool such as Flux or Argo CD. Pacto works with it |
| Pactify | Give a service a contract and connect it to delivery. See [Pactify a service](pactify.md) |
