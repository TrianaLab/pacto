---
# See the note on the sibling "Try it" pages.
search:
  boost: 3
---

# Guided tour

This tour answers six questions people ask about services they did not
build. Each answer uses one or two `pacto` commands on a fixed set of example
contracts and explains the output. The tour covers contracts and the checks on
them. Delivery has its own demos.

## Before you start

Everything here runs offline except `pacto dashboard`. The fixture is
committed to the repository, so you need no cluster, registry, running service
or network.

You need the [Pacto CLI](../installation.md) on your `PATH` and a clone:

```bash
git clone https://github.com/TrianaLab/pacto.git && cd pacto
```

Every command below is written to be pasted from the repository root.

To drive a real stack, see the [Docker Compose demo](compose-demo.md). It
needs neither a clone nor the CLI.

The tour shows [runtime conformance](../integrations/kubernetes/runtime-conformance.md)
(checking what a service really does on the network) as a recorded finding only:
story 2 shows one the kernel saw. To watch it live, use the Compose demo or the
node agent on a Kubernetes cluster.

## The fleet

Sixteen services are in the snapshot. Five carry the stories:

| Service | Owner | Its part |
|---------|-------|----------|
| `payments-service` | payments-team | six revisions; the change stories 3 and 4 analyse |
| `orders-service` | commerce-team | declares a dependency on payments, and is observed calling it |
| `api-gateway` | platform-foundations | declares the same dependency, and has an expired readiness assessment |
| `auth-service` | identity-team | deployed and never observed: the Unknown target |
| `audit-log` | platform-foundations-security | calls payments and declares nothing: the shadow consumer |

A target is one running deployment of a service. A readiness assessment is a
dated list of claims in a contract, and a shadow consumer is a service seen
calling another that no contract declares; the [glossary](../glossary.md)
has these and the other words the tour uses.

A contract says what a service exposes, what it needs and how it behaves. How
the service is deployed stays in its own files. The
[contract reference](../contract-reference/sections.md) has every section.

## Story 1: List an inherited fleet and its state

*Enumerate everything from contracts alone, then find out what state it is in.*

--8<-- "examples/demo/generated/_beat-01.md"

Sixteen services, each with its owner, how many contract revisions it has
published and how many deployed targets are running it. `NotEvaluated` means
Pacto knows no deployment of the service yet, so it has nothing to evaluate.

`--target-state` folds in what a platform observed about the running deployments.
Three of its targets, abridged:

```yaml
schemaVersion: pacto.dev/fleet-targets/v1
targets:
  - { scope: production-eu, kind: kubernetes-workload, name: payments/payments-service,
      service: payments-service, compliance: Compliant,
      coverage: { evaluated: 6, required: 6 }, evidenceAt: 2026-07-29T09:40:00Z }

  - scope: production-eu
    kind: kubernetes-workload
    name: commerce/orders-service
    service: orders-service
    compliance: NonCompliant
    coverage: { evaluated: 5, required: 5 }
    evidenceAt: 2026-07-29T09:40:00Z
    findings:
      - code: STATELESS_PERSISTENT_CONFLICT
        severity: error
        category: RuntimeDrift
        message: declared stateless but the observed workload mounts a persistent volume
      - code: INTERFACE_ABSENT
        severity: error
        category: RuntimeDrift
        message: nothing listens on the declared http interface
        evidenceRefs: [{ source: k8s, collector: k8s-observer+ebpf }]

  - { scope: production-eu, kind: kubernetes-workload, name: identity/auth-service,
      service: auth-service, compliance: Unknown,
      coverage: { evaluated: 1, required: 4 } }
```

--8<-- "examples/demo/generated/_beat-02.md"

Four states in one screen. `Compliant` is a verdict backed by evidence,
`NonCompliant` is a confirmed contradiction, `Unknown` is evidence that never
arrived and `NotEvaluated` means nobody is deployed there to evaluate.

## Story 2: Find the reason behind an Unknown

*Get the reason behind a verdict. Missing evidence counts as Unknown.*

--8<-- "examples/demo/generated/_beat-03.md"

`EVIDENCE_MISSING` describes the observer and leaves the service's own
verdict open.
`api-gateway` declares five readiness claims, every one marked `done` with
evidence attached, and the assessment earns zero because it expired on
2025-01-01. Pacto fails closed on both.

A confirmed violation reads differently:

--8<-- "examples/demo/generated/_beat-04.md"

`Coverage: 5/5 evaluated` marks the difference from the Unknown above: every
check ran, so the verdict is a conclusion. Each finding names
one exact contradiction. The contract declares the workload stateless and the
observation found a persistent volume mounted. The contract declares an `http`
interface and nothing listens on its port: `seen by k8s-observer+ebpf` says the
node agent saw that in the kernel, and Kubernetes agreed. Each finding is one
fact that disagrees with one declaration.

## Story 3: Check a change before you ship it

*Classify a change from two contracts, with nothing running, then gate CI on it.*

--8<-- "examples/demo/generated/_beat-05.md"

Thirty-nine changes, one verdict and a non-zero exit so CI can gate on it. Two
API paths removed, two event channels withdrawn, a required request field
swapped for a differently-named one, two configuration keys becoming required.
A capability dropped, an optional dependency became mandatory and one SBOM
package version moved. Pacto found all of it by comparing one contract with
another, with no running service. The
[classification rules](../contract-reference/diff.md#change-classification-rules)
are a published table.

The same service, one pair earlier, shows a release that breaks nobody:

--8<-- "examples/demo/generated/_beat-06.md"

The result is `POTENTIAL_BREAKING`. A new endpoint, a new event channel and a
new optional configuration property break nobody by themselves. Adding a
property to a schema can still surprise a consumer that validates strictly. The
third classification gives additive changes like these their own verdict.

## Story 4: Find who a breaking change affects

*Turn a classification into a list of consumers, each graded by how it is known.*

--8<-- "examples/demo/generated/_beat-07.md"

Four consumers. `confidence=contractual` means the consumer declared this
dependency in its own contract; `confidence=inferred` means it was reached
transitively. `compat=incompatible` is a second judgement: 2.0.1 falls outside
the consumer's declared version range. The release gate is `unverified` and the
command exits 2. The bundles say nothing about where anything runs, so Pacto
cannot show a consumer is hit and does not call the change safe either.

Contracts find only the consumers that declare the dependency. Add observed
traffic:

--8<-- "examples/demo/generated/_beat-08.md"

Five now. `audit-log` calls `payments-service` in production and declares
nothing, so only observed traffic finds it. It arrives as
`confidence=observed`. `orders-service`, which both declared the dependency
and was seen using it, is upgraded to `confidence=corroborated`.

Then ask where the change actually lands:

--8<-- "examples/demo/generated/_beat-09.md"

The consumer list is identical. The active targets are new, and the gate turns
from `unverified` to `blocked`. Two deployed targets sit in the path of this
change: `orders-service`, one of the five consumers, and `payments-service`
itself. The command exits 1. Story 3
refused a change for what it is. This command refuses it for where it lands.

## Story 5: Compare declared and observed dependencies

*Reconcile the edges contracts declare against the edges something observed.*

--8<-- "examples/demo/generated/_beat-10.md"

Three verdicts. Reconciling means comparing each declared edge with what was
observed. `observed-not-declared` is the shadow dependency from story 4,
stated as a reconciliation result. `declared-not-observed` is the opposite
risk: an edge in the diagram that no traffic has taken.

## Story 6: Give an agent read-only tools

*Serve a contract as MCP tools, and check what the default withholds.*

`pacto mcp` turns a bundle's OpenAPI interface into MCP tools. By default it
turns only the read-only operations into tools. Point a human and an agent at
one process:

```bash
pacto dashboard examples/demo/bundles --port 8899
pacto mcp examples/demo/pacto-dashboard --base-url http://127.0.0.1:8899
```

Then open <http://127.0.0.1:8899/#/fleet>, and connect your MCP client to the
second command. The dashboard is itself a Pacto
bundle with a real OpenAPI contract, so the agent's tools are generated from the
contract of the very server the human is looking at. The tools come from the
dashboard's OpenAPI interface, one per read-only operation. Beside the interface
operations, the server always registers Pacto's own authoring tools:
`pacto_create` and `pacto_edit` write contract files to disk.

Every bundle withholds mutating operations by default. Serve the demo's
payments service, and Pacto drops its five mutating operations, every `POST`,
`PUT`, `PATCH` and `DELETE` that bundle's interface declares, with a warning on
stderr:

```bash
pacto mcp examples/demo/bundles/payments-service/v2.1.0 --base-url http://127.0.0.1:1
```

The base URL is a dead address on purpose: Pacto registers and lists the
tools and calls none of them.

Add `--allow-writes` and both the warning and the restriction disappear.
`--allow-writes` governs the interface tools only. To withhold the authoring
tools, register this server only where you want contracts written. See
[MCP integration](../mcp-integration.md) for the full wiring.

## What's next

The [Quickstart](../quickstart.md) takes an empty directory to a validated
contract and a caught breaking change in about five minutes. The
[contract reference](../contract-reference/sections.md) is the complete list of
sections, and [`pacto diff`](../contract-reference/diff.md#change-classification-rules)
is the complete table of what counts as a breaking change.
