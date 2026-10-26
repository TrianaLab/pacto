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

Delivery is alpha: its settings and commands may still change, and it runs on
Kubernetes 1.30 or later with Flux (Argo CD is experimental).

A new Stage starts on observe. Pacto proposes the next rung after enough good
changes, and a person other than the one who asked confirms it. A raise to an
automatic rung needs the recorder, so it leaves a signed record. A failed
promote is rolled back once, whoever asked for it. The Stage freezes instead
when there is nothing safe to go back to, the rollback fails or someone chose
the running version by hand (a rollback, or a promote during auto-hold). A
failure of a version Pacto shipped with no person also drops the Stage one
rung, whether it was rolled back or froze.

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
- **Anthropic's [Scion](https://www.anthropic.com/research/redesigning-source-control)**:
  source control for agent-scale change. Content-addressed storage, a pointer
  moved with one compare-and-swap and tests after a change merges, before it
  deploys.
- **Ours, the contract graph**: which services depend on which and which
  versions each accepts. That decides whether a change is safe, who must agree
  and what ships first.

Start with [Pactify a service](https://pacto.run/latest/pactify/), read
[the autonomy ladder](https://pacto.run/latest/autonomy/) and
[what GitlessOps means](MANIFEST.md#what-gitlessops-means).

## Contracts and checks, without delivery

On pacto.run:

- [Who breaks when a dependency ships a new version](https://pacto.run/latest/#who-breaks-when-a-dependency-ships-a-new-version):
  `pacto impact` answers per consumer, from the version ranges each declares.
- [Check what is actually running](https://pacto.run/latest/runtime-conformance/):
  eBPF compares what a service does on the network with its contract.
- [What a contract declares](https://pacto.run/latest/contract-reference):
  one YAML file per service; only `pactoVersion` and `service` are required.

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
| **Pacto** | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ contracts and checks; delivery needs Kubernetes | **No** (delivery promotes) |

✅ first-class · Partial adjacent or limited · — not in scope. Verified against
each project's own documentation, August 2026; these projects move fast, so
re-check the cells before relying on them. The contested columns:

- **Semantic diff** — changes classified by compatibility impact, not rendered as text; a line-based diff is Partial
- **Transitive policy** — governance rules evaluated across the dependency closure, fail-closed
- **Runtime verify** — workloads checked against an independently declared contract; reconciling toward the tool's own desired state is Partial
- **Orchestrator-agnostic** — the tool needs no Kubernetes control plane of its own. The softest column: between ✅ and —, Partial is a judgement of degree

Several of these are complementary rather than competing: a contract can gate a
Kargo promotion, feed a Backstage card or front a Crossplane provisioner. We
graded our own row; the orchestrator cell holds for contracts and checks, not
for delivery.

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
| Know what Pacto is and whether it fits | [The autonomy ladder](https://pacto.run/latest/autonomy/), [what GitlessOps means](MANIFEST.md#what-gitlessops-means) and the [live demo](https://pacto.run/latest/demo/#/fleet) |
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
