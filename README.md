[![CI](https://github.com/TrianaLab/pacto/actions/workflows/ci.yml/badge.svg)](https://github.com/TrianaLab/pacto/actions/workflows/ci.yml) [![Docs](https://img.shields.io/badge/docs-pacto.run-blue)](https://pacto.run) [![PkgGoDev](https://pkg.go.dev/badge/github.com/trianalab/pacto/v4)](https://pkg.go.dev/github.com/trianalab/pacto/v4) [![codecov](https://codecov.io/github/TrianaLab/pacto/graph/badge.svg?token=p3AJpP3BbO)](https://codecov.io/github/TrianaLab/pacto) [![GitHub Release](https://img.shields.io/github/v/release/TrianaLab/pacto)](https://github.com/TrianaLab/pacto/releases/latest) [![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/pacto-operator)](https://artifacthub.io/packages/search?repo=pacto-operator) [![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

# Pacto

**Pacto is an operational contract system for services.**

A service's operational facts are scattered. Ownership is in a wiki. The API is
an OpenAPI file in another repository. What it depends on is implied by Helm
values and a Service name. What breaks if you change it is in someone's memory.

Pacto puts those facts in one YAML file, published to the container registry you
already run. With the opt-in delivery component, Pacto also acts on them: it
decides which version of each service runs in each environment.

**[Documentation](https://pacto.run)** · **[Quickstart](https://pacto.run/latest/quickstart)** · **[Specification](https://pacto.run/latest/contract-reference)** · **[Examples](https://pacto.run/latest/examples)** · **[Live demo](https://pacto.run/latest/demo/#/fleet)**

> **Why Pacto exists** — [MANIFEST.md](MANIFEST.md)

---

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

## Ship with the contract graph (opt-in)

Install the delivery component and Pacto promotes versions from test to prod.
A change that breaks no consumer goes ahead on its own, unless the Stage asks a
person to approve every promote; one that would break a consumer waits and asks
only that consumer's team, and the plan says which service to ship first. A
promoted version that fails its checks after release is rolled back once;
otherwise the Stage freezes until a person decides. AI agents may propose
changes and only people may approve. Every change can leave a signed record in
your registry. Flux or Argo CD still applies the
version. Without the component, Pacto never changes what runs.

Delivery synthesizes two ideas from others and adds one of ours:

- **[ConfigHub](https://confighub.com)**: desired state as data with an API,
  the step beyond GitOps. Pacto keeps only "which version runs where" that way.
- **Anthropic's [Scion](https://www.anthropic.com/engineering/redesigning-source-control)**:
  principles for agent-scale change. Content stored once and never changed,
  a pointer only one writer can move and checks after a change lands, before
  it deploys.
- **Ours, the contract graph**: which services depend on which and which
  versions each accepts. That decides whether a change is safe, who must agree
  and what ships first.

Start with [Pactify a service](https://pacto.run/latest/pactify/).

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
leaves applying it to Flux or Argo CD, as Kargo does.

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

| Guide | Description |
|-------|-------------|
| [Quickstart](https://pacto.run/latest/quickstart) | From zero to a validated contract and a breaking change caught, in about 5 minutes |
| [Contract Reference](https://pacto.run/latest/contract-reference) | Every field, validation rule and change classification |
| [For Developers](https://pacto.run/latest/developers) | Write and maintain contracts alongside your code |
| [For Platform Engineers](https://pacto.run/latest/platform-engineers) | Consume contracts for deployment, policies and graphs |
| [CLI Reference](https://pacto.run/latest/cli-reference) | All commands, flags and output formats |
| [Dashboard](https://pacto.run/latest/dashboard-docker) | Deploy the dashboard container alongside the operator |
| [Runtime conformance](https://pacto.run/latest/runtime-conformance/) | Check what running services do on the network, on any Linux host |
| [Kubernetes Operator](https://pacto.run/latest/integrations/kubernetes/overview/) | Runtime contract tracking and verification |
| [Pactify a service](https://pacto.run/latest/pactify/) | From a plain service to its first release Pacto checks before prod |
| [Delivery](https://pacto.run/latest/integrations/kubernetes/delivery/) | What happens to a change, who is asked and why |
| [MCP Integration](https://pacto.run/latest/mcp-integration) | Connect AI tools (Claude, Cursor, Copilot) to Pacto via MCP |
| [Plugin Development](https://pacto.run/latest/plugins) | Build plugins to generate artifacts from contracts |
| [Examples](https://pacto.run/latest/examples) | PostgreSQL, Redis, RabbitMQ, NGINX, gRPC and more |

---

## License

Fully open source under the [MIT](LICENSE) licence, delivery included, with no
hosted service.
