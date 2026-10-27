[![CI](https://github.com/TrianaLab/pacto/actions/workflows/ci.yml/badge.svg)](https://github.com/TrianaLab/pacto/actions/workflows/ci.yml) [![Docs](https://img.shields.io/badge/docs-pacto.run-blue)](https://pacto.run) [![PkgGoDev](https://pkg.go.dev/badge/github.com/trianalab/pacto/v4)](https://pkg.go.dev/github.com/trianalab/pacto/v4) [![codecov](https://codecov.io/github/TrianaLab/pacto/graph/badge.svg?token=p3AJpP3BbO)](https://codecov.io/github/TrianaLab/pacto) [![GitHub Release](https://img.shields.io/github/v/release/TrianaLab/pacto)](https://github.com/TrianaLab/pacto/releases/latest) [![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/pacto-operator)](https://artifacthub.io/packages/search?repo=pacto-operator) [![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

# Pacto

**Pacto is continuous delivery for the GitlessOps era.**

[GitlessOps](MANIFEST.md#what-gitlessops-means) means signed artifacts and
evidence decide which version runs where, while your code stays in git.

Describe your services once, as contracts. Pacto works out what each change
does to the services that depend on it. It starts by watching, and once a
person has confirmed each step it ships the changes the contract graph clears.
Flux or Argo CD still apply every version.

The contracts are the foundation. A contract is one YAML file per service:
who owns it, what it offers, which services it calls and which of their
versions it accepts. It is published to the container registry you already
run, and it is useful on its own, without delivery.

**[Documentation](https://pacto.run)** · **[Quickstart](https://pacto.run/latest/quickstart)** · **[Specification](https://pacto.run/latest/contract-reference)** · **[Examples](https://pacto.run/latest/examples)** · **[Live demo](https://pacto.run/latest/demo/#/fleet)**

> **Why Pacto exists:** [MANIFEST.md](MANIFEST.md)

---

## Delivery: from watching to shipping on its own

Install the opt-in delivery component and each Stage, one service in one
environment, climbs [the autonomy ladder](https://pacto.run/latest/autonomy/)
from observe, where Pacto moves nothing, to autonomous. A person confirms
every step, and on every level a change that could break a consumer or that
Pacto cannot judge waits for the teams it affects. Delivery is alpha and needs
Kubernetes; [what delivery needs](https://pacto.run/latest/#what-delivery-needs)
lists the rest.

Pacto delivery builds on Flux's Gitless GitOps, ConfigHub's configuration as
data and Anthropic's Scion; [the manifesto](MANIFEST.md#credits) says what it
takes from each. The contract graph is ours.

Start with [Pactify a service](https://pacto.run/latest/pactify/), read
[how delivery works](https://pacto.run/latest/integrations/kubernetes/delivery/)
and [what GitlessOps means](MANIFEST.md#what-gitlessops-means).

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
leaves applying it to GitOps. Compared with a promotion tool such as Kargo, it
decides from the contract graph: it asks only the owners of the consumers a
change would break, treats what it cannot see as Unknown, which never passes,
and can sign each decision into a record anyone verifies without Pacto.

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

- **Semantic diff**: changes classified by compatibility impact; a line-based text diff is Partial
- **Transitive policy**: governance rules evaluated across the dependency closure, fail-closed
- **Runtime verify**: workloads checked against an independently declared contract; reconciling toward the tool's own desired state is Partial
- **Orchestrator-agnostic**: the tool runs without a Kubernetes control plane of its own. This is the softest column, and Partial is a judgement of degree

Several of these work alongside Pacto: a contract can gate a Kargo promotion,
feed a Backstage card or front a Crossplane provisioner. We graded our own row.
The orchestrator cell holds for contracts and checks; delivery needs
Kubernetes.

## Installation

```bash
curl -fsSL https://raw.githubusercontent.com/TrianaLab/pacto/main/scripts/get-pacto.sh | bash
```

[Installation](https://pacto.run/latest/installation) covers `go install`,
building from source, pinning a version, installing without `sudo` and
uninstalling.

## Documentation

Full documentation at **[pacto.run](https://pacto.run)**.

[Where to start](https://pacto.run/latest/#where-to-start) picks the page for
what you came to do.

Every term is in the [glossary](https://pacto.run/latest/glossary/), and every
command in the [CLI reference](https://pacto.run/latest/cli-reference).

---

## License

Fully open source under the [MIT](LICENSE) license, delivery included, with no
hosted service.
