[![CI](https://github.com/TrianaLab/pacto/actions/workflows/ci.yml/badge.svg)](https://github.com/TrianaLab/pacto/actions/workflows/ci.yml) [![Docs](https://img.shields.io/badge/docs-pacto.run-blue)](https://pacto.run) [![PkgGoDev](https://pkg.go.dev/badge/github.com/trianalab/pacto/v4)](https://pkg.go.dev/github.com/trianalab/pacto/v4) [![codecov](https://codecov.io/github/TrianaLab/pacto/graph/badge.svg?token=p3AJpP3BbO)](https://codecov.io/github/TrianaLab/pacto) [![GitHub Release](https://img.shields.io/github/v/release/TrianaLab/pacto)](https://github.com/TrianaLab/pacto/releases/latest) [![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/pacto-operator)](https://artifacthub.io/packages/search?repo=pacto-operator) [![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

# Pacto

**Pacto is continuous delivery for the GitlessOps era.**

Describe your services once, as contracts. Pacto works out what each change
does to the services that depend on it. It starts by watching, and once a
person has confirmed each step it ships the changes the contract graph clears.
It asks only the teams a change affects. Flux or Argo CD still apply every
version, and your code stays in git.

The contracts are the foundation. A contract is one YAML file per service:
who owns it, what it offers, which services it calls and which of their
versions it accepts. It is published to the container registry you already
run, and it is useful on its own, without delivery.

**[Documentation](https://pacto.run)** · **[Quickstart](https://pacto.run/latest/quickstart)** · **[Specification](https://pacto.run/latest/contract-reference)** · **[Examples](https://pacto.run/latest/examples)** · **[Live demo](https://pacto.run/latest/demo/#/fleet)**

> **Why Pacto exists** — [MANIFEST.md](MANIFEST.md)

---

## Delivery: from watching to shipping on its own

Install the opt-in delivery component and each environment of each service (a
Stage) climbs a ladder of four rungs:

| Rung | What Pacto does |
| --- | --- |
| observe | Works out what it would do for every change and moves nothing; GitOps keeps control |
| advise | Shows each new version and its plan; a person asks for or approves every change |
| auto for non-breaking | Ships the changes that break no one on its own |
| autonomous | Ships every change the contract graph clears on its own |

A new Stage starts on observe. Pacto proposes the next rung after enough good
changes, a person other than the one who asked confirms it, and a raise to an
automatic rung needs the recorder, so it leaves a signed record. When a
version Pacto shipped on its own fails its checks, Pacto rolls it back once if
that is safe and otherwise freezes the Stage; after an automatic rollback the
Stage also drops one rung.

No rung skips the safety rules. A change that would break a consumer waits and
asks only that consumer's team, and the plan says which service to ship first.
When Pacto cannot see enough (Unknown), the change waits for a person. A
service seen calling yours at runtime with no contract counts as a consumer
Pacto cannot judge, so a breaking change waits too. AI agents may propose
changes and only people may approve. With the recorder on and a registry that
serves OCI Referrers (zot does, GitHub's does not), every change leaves a
signed record you verify with cosign and oras. Without the component, Pacto
never changes what runs.

Delivery synthesizes two ideas from others and adds one of ours:

- **[ConfigHub](https://confighub.com)**: configuration as data in a database behind an API,
  not as templates. Pacto keeps only "which version runs where" that way.
- **Anthropic's [Scion](https://www.anthropic.com/engineering/redesigning-source-control)**:
  source control for agent-scale change. Content-addressed storage, a pointer
  moved with one compare-and-swap and tests after a change merges, before it
  deploys.
- **Ours, the contract graph**: which services depend on which and which
  versions each accepts. That decides whether a change is safe, who must agree
  and what ships first.

Start with [Pactify a service](https://pacto.run/latest/pactify/), read
[the autonomy ladder](https://pacto.run/latest/autonomy/) and
[what GitlessOps means](MANIFEST.md#what-gitlessops-means).

## Who breaks when a dependency ships a new version

A Pacto dependency records the range this revision accepts — `auth ^2.0.0` —
and `pacto.lock` pins every dependency below it to the exact published build.
So when auth is about to ship 3.0, Pacto answers per consumer:

```console
$ pacto impact ./auth-2.1.0 ./auth-3.0.0 --local .
Impact: auth 2.1.0 -> 3.0.0
Classification: BREAKING
Breaking changes: 1
Potentially breaking changes: 0
Affected consumers (2):
  checkout                     direct     confidence=contractual  compat=incompatible owner=commerce
  reporting                    direct     confidence=contractual  compat=compatible   owner=analytics
```

`checkout` accepts `^2.0.0` and 3.0.0 falls outside it. `reporting` accepts
`>=2.0.0 <4.0.0` and 3.0.0 is inside. One file, checked when you write it,
compared against the previous revision, then compared against what is actually
running.

When Pacto cannot observe something it says `Unknown`. It never reports it as a
pass. A person reading "no findings" under a broken collector will usually smell
something wrong. A program will not.

## What each command answers

| Command | Question it answers |
| --- | --- |
| `pacto validate` | Is this contract legal? |
| `pacto diff` | Did I break my own consumers? |
| `pacto impact` | Which consumers, and where do they run? |
| The [operator](https://pacto.run/latest/integrations/kubernetes/overview/) | Does reality still match what was declared? |

`validate` and `diff` pay off at one service; `impact` and the graph pay off at
the second consumer. `pacto dashboard` and `pacto tui` put the same answers in a
browser or a terminal.

## Check what is actually running

Pacto can check what a running service actually does on the network against
what its contract says, on any Linux machine, and tells you only what it can
prove:

| What it catches | Finding |
| --- | --- |
| A process reports healthy but nothing listens on the declared port | `INTERFACE_ABSENT` |
| A declared dependency the service keeps failing to reach | `DEPENDENCY_UNREACHABLE` |
| A call to another Pacto service no dependency declares (Kubernetes) | `DEPENDENCY_UNDECLARED` |

```bash
pacto run --contract ./payments --bind api=8080 --fail-on noncompliant -- ./payments-server
```

Pacto watches TCP connections in the Linux kernel with eBPF (small, safe
programs the kernel runs on events). It never reads payloads or secrets, and
when it cannot see it says "don't know" instead of guessing. Run it three ways:
`pacto run` wraps one command (a laptop, a CI job), `pacto agent` watches a
Docker host or a server and the Kubernetes operator deploys the same agent on
every node. See [Runtime conformance](https://pacto.run/latest/runtime-conformance/).

## What a contract declares

This is the `checkout` contract from the run above:

```yaml
pactoVersion: "2.0"

service:
  name: checkout
  version: 4.2.0
  owner: { team: commerce, dri: alice }

interfaces:
  - name: rest-api
    type: openapi
    ref: interfaces/openapi.yaml       # the OpenAPI document you already maintain
    visibility: public

dependencies:
  - name: auth
    ref: oci://ghcr.io/acme/auth-pacto
    required: true
    compatibility: "^2.0.0"            # the range this revision accepts
```

Only `pactoVersion` and `service` are required. Everything else is opt-in. An
unknown field is rejected rather than ignored. There is no port, image,
replicas or namespace field: those are delivery decisions, and leaving them out
keeps one contract true in every cluster. Each interface's `ref` points at a
schema you already own.

## How Pacto compares

Pacto composes the interface tools it sits between (OpenAPI, config schemas) and
complements the deploy tools (Helm, Terraform). Its core makes no deployment
decisions. The opt-in delivery component decides which version runs where and
leaves applying it to GitOps. Unlike a promotion tool such as Kargo, it decides
from the contract graph: it asks only the owners of the consumers a change
would break, treats what it cannot see as Unknown rather than a pass and can
sign each decision into a record anyone verifies without Pacto.

| | Versioned artifact | Semantic diff | Dependency graph | Transitive policy | Runtime verify | Orchestrator-agnostic | Deploys? |
|---|---|---|---|---|---|---|---|
| **Score** ([score.dev](https://score.dev)) | — | — | — | — | — | ✅ | No |
| **Crossplane Configuration** | ✅ | — | Partial | — | Partial | — | Yes |
| **KubeVela** / OAM | — | Partial | Partial | — | Partial | Partial | Yes |
| **Radius** | — | — | ✅ | — | — | Partial | Yes |
| **Kratix** | Partial | — | Partial | — | — | Partial | Yes |
| **Backstage** / Port | — | — | Partial | — | — | ✅ | No |
| **Kargo** | ✅ | — | — | — | Partial | — | No |
| **Pacto** | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | **No** (delivery promotes) |

✅ first-class · Partial adjacent or limited · — not in scope. Verified against
each project's own documentation, August 2026; these projects move fast, so
re-check the cells before relying on them. The contested columns:

- **Semantic diff** — changes classified by compatibility impact, not rendered as text; a line-based diff is Partial
- **Transitive policy** — governance rules evaluated across the dependency closure, fail-closed
- **Runtime verify** — workloads checked against an independently declared contract; reconciling toward the tool's own desired state is Partial
- **Orchestrator-agnostic** — the tool needs no Kubernetes control plane of its own. The softest column: between ✅ and —, Partial is a judgement of degree

Several of these are complementary rather than competing: a contract can gate a
Kargo promotion, feed a Backstage card or front a Crossplane provisioner. Pacto
is the only row that scores first-class on all six capability columns.

## Installation

```bash
# Installer script
curl -fsSL https://raw.githubusercontent.com/TrianaLab/pacto/main/scripts/get-pacto.sh | bash

# Go
go install github.com/trianalab/pacto/v4/cmd/pacto@latest

# From source
git clone https://github.com/TrianaLab/pacto.git && cd pacto && make build
```

The installer script also installs the two official plugins and leaves a
version-stamped binary that `pacto update` can upgrade in place. `go install`
and `make build` install `pacto` alone into `$GOBIN`, and a `go install` build
reports its version as `dev` because the stamp is applied at release time.
[Installing](https://pacto.run/latest/installation) covers pinning a version,
installing without `sudo` and uninstalling.

## Documentation

Full documentation at **[pacto.run](https://pacto.run)**.

Pick the path that matches what you came to do:

| You want to | Start here |
|-------------|------------|
| Know what Pacto is and whether it fits | [Delivery](https://pacto.run/latest/integrations/kubernetes/delivery/), [the autonomy ladder](https://pacto.run/latest/autonomy/) and the [live demo](https://pacto.run/latest/demo/#/fleet) |
| Pactify your first service | [Quickstart](https://pacto.run/latest/quickstart), then [Pactify a service](https://pacto.run/latest/pactify/) |
| Approve or unblock a change | [Decide, unfreeze and roll back](https://pacto.run/latest/pactify-decide/) |
| Verify a record | [Check a record without Pacto](https://pacto.run/latest/verify-a-record/) |
| Drive Pacto from an AI agent | [MCP integration](https://pacto.run/latest/mcp-integration) |
| Write and check contracts | [For developers](https://pacto.run/latest/developers) and the [contract reference](https://pacto.run/latest/contract-reference) |

Every term is in the [glossary](https://pacto.run/latest/glossary/), and every
command in the [CLI reference](https://pacto.run/latest/cli-reference).

---

## License

Fully open source under the [MIT](LICENSE) licence, delivery included, with no
hosted service.
