---
# See the note on the sibling "Try it" pages.
search:
  boost: 3
---

# Guided tour

Six things people actually need from a fleet they did not build, answered against
one fixture, in the order the questions arrive. Each story is one or two commands
and what its output means.

## Before you start

Everything here is offline except `pacto dashboard`. No cluster, no registry, no
running service and no network — the fixture is committed to the repository, so
it works on a plane.

You need the [Pacto CLI](../installation.md) on your `PATH` and a clone:

```bash
git clone https://github.com/TrianaLab/pacto.git && cd pacto
```

Every command below is written to be pasted from the repository root.

To drive a real stack instead, see the [Docker Compose demo](compose-demo.md),
which needs no clone and no CLI.

## The fleet

Sixteen services are in the snapshot. Five carry the stories:

| Service | Owner | Its part |
|---------|-------|----------|
| `payments-service` | payments-team | six revisions; the change stories 3 and 4 analyse |
| `orders-service` | commerce-team | declares a dependency on payments, and is observed calling it |
| `api-gateway` | platform-foundations | declares the same dependency, and has an expired readiness assessment |
| `auth-service` | identity-team | deployed, but never observed — the Unknown target |
| `audit-log` | platform-foundations-security | calls payments and declares nothing — the shadow consumer |

A contract says what a service exposes, what it needs and how it behaves — never
how it is deployed. The
[contract reference](../contract-reference/sections.md) has every section.

## Story 1 — "I inherited this fleet and I do not know what is in it"

*Enumerate everything from contracts alone, then find out what state it is in.*

--8<-- "examples/demo/generated/_beat-01.md"

Sixteen services, each with its owner, how many contract revisions it has
published and how many deployed targets are running it. `NotEvaluated` is not a
verdict: no evidence source is wired up, so nothing has been evaluated.

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

  - { scope: production-eu, kind: kubernetes-workload, name: identity/auth-service,
      service: auth-service, compliance: Unknown,
      coverage: { evaluated: 1, required: 4 } }
```

--8<-- "examples/demo/generated/_beat-02.md"

Four states in one screen. `Compliant` is a verdict backed by evidence,
`NonCompliant` is a confirmed contradiction, `Unknown` is evidence that never
arrived and `NotEvaluated` means nobody is deployed there to evaluate.

## Story 2 — "Something says Unknown and I need to know whether that is bad"

*Get the reason behind a verdict, so absence of evidence never reads as a pass.*

--8<-- "examples/demo/generated/_beat-03.md"

`EVIDENCE_MISSING` is a fact about the observer, not about the service.
`api-gateway` declares five readiness claims, every one marked `done` with
evidence attached, and the assessment earns zero because it expired on
2025-01-01. Pacto fails closed on both.

A confirmed violation reads differently:

--8<-- "examples/demo/generated/_beat-04.md"

`Coverage: 5/5 evaluated` is what separates this from the Unknown above: every
check ran, so the verdict is a conclusion rather than a gap. The finding names
the exact contradiction — the contract declares the workload stateless, the
observation found a persistent volume mounted. One fact disagreeing with one
declaration, not a score.

## Story 3 — "I am about to ship a change that might break someone"

*Classify a change from two contracts, with nothing running, and gate CI on it.*

--8<-- "examples/demo/generated/_beat-05.md"

Thirty-nine changes, one verdict and a non-zero exit so CI can gate on it. Two
API paths removed, two event channels withdrawn, a required request field
swapped for a differently-named one, two configuration keys becoming required.
A capability dropped, an optional dependency became mandatory and one SBOM
package version moved. All of it is one contract compared with another — no
running service was consulted. The
[classification rules](../contract-reference/diff.md#change-classification-rules)
are a published table, not a heuristic.

Not every release is a break. The same service, one pair earlier:

--8<-- "examples/demo/generated/_beat-06.md"

`POTENTIAL_BREAKING`, not `BREAKING`: a new endpoint, a new event channel and a
new optional configuration property break nobody by themselves, but adding a
property to a schema can still surprise a consumer that validates strictly. Three
classifications exist because two would force every additive change into one of
the wrong ones.

## Story 4 — "It does break. Who do I have to tell?"

*Turn a classification into a list of consumers, each graded by how it is known.*

--8<-- "examples/demo/generated/_beat-07.md"

Four consumers. `confidence=contractual` means the consumer declared this
dependency in its own contract; `confidence=inferred` means it was reached
transitively. `compat=incompatible` is a second judgement: the consumer's
declared version range does not admit 2.0.1.

Contracts only find consumers that wrote one down. Add observed traffic:

--8<-- "examples/demo/generated/_beat-08.md"

Five now. `audit-log` calls `payments-service` in production and declares
nothing, so no amount of reading contracts would ever have found it — it arrives
as `confidence=observed`. `orders-service`, which both declared the dependency
and was seen using it, is upgraded to `confidence=corroborated`.

Then ask where the change actually lands:

--8<-- "examples/demo/generated/_beat-09.md"

The consumer list is identical. What is new is the last two lines. Two deployed
targets sit in the path of this change — `orders-service`, one of the five
consumers, and `payments-service` itself — so this is not hypothetical, and the
command exits non-zero. Story 3 refused a change for what it is. This refuses it
for where it lands.

## Story 5 — "My architecture diagram and my traffic disagree"

*Reconcile the edges contracts declare against the edges something observed.*

--8<-- "examples/demo/generated/_beat-10.md"

Three verdicts. `observed-not-declared` is the shadow dependency from story 4,
stated as a reconciliation result. `declared-not-observed` is the opposite risk
— an edge in the diagram that no traffic has ever taken.

## Story 6 — "I want an agent on this, without handing it write access"

*Serve a contract as MCP tools, and check what the default withholds.*

`pacto mcp` turns a bundle's OpenAPI interface into MCP tools. By default it
turns only the safe half. Point a human and an agent at one process:

```bash
pacto dashboard examples/demo/bundles --port 8899
```

Then open <http://127.0.0.1:8899/#/fleet>. The dashboard is itself a Pacto
bundle with a real OpenAPI contract, so the agent's tools are generated from the
contract of the very server the human is looking at. The tools come from the
dashboard's OpenAPI interface, one per read-only operation. Beside the interface
operations the server always registers Pacto's own authoring tools —
`pacto_create` and `pacto_edit` write contract files to disk.

Mutating operations are withheld by default. Five mutating operations — every
`POST`, `PUT`, `PATCH` and `DELETE` the interface declares — are dropped with a
warning on stderr:

```bash
pacto mcp examples/demo/bundles/payments-service/v2.1.0 --base-url http://127.0.0.1:1
```

Add `--allow-writes` and both the warning and the restriction disappear.
`--allow-writes` governs the interface half only; withhold the authoring tools
by not registering this server where you do not want contracts written. See
[MCP integration](../mcp-integration.md) for the full wiring.

## Next

The [Quickstart](../quickstart.md) takes an empty directory to a published
contract in about five minutes. The
[contract reference](../contract-reference/sections.md) is the complete list of
sections, and [`pacto diff`](../contract-reference/diff.md#change-classification-rules)
is the complete table of what counts as a breaking change.
