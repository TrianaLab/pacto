# The Pacto model

How Pacto reaches an answer: what a contract declares, what a collector observes,
what the engine concludes from the two, and which of those roles Pacto does not
play. [Core concepts](concepts.md) indexes the distinctions this model refuses to
collapse; [Architecture](architecture.md) shows how the code is arranged to keep
them apart.

## Declaration versus observation

The contract is stable author *intent*: what a service is operationally,
independent of any orchestrator. What a service actually looks like at runtime is
an *observation*, and it lives entirely outside the declared contract.

The core is built around that separation. The `Contract` type (`pkg/contract`)
carries only intent — there is no `runtime` block, no port, no scaling and no
image field; those are delivery and observation concerns owned by integrations.
Runtime facts are carried by a separate `EvidenceSet` (`pkg/evidence`), produced
by a collector, and the engine reasons over the two together.

## The engine: `Evaluate(contract, evidence)` { #the-engine }

The heart of the system is a pure function (`pkg/validation/evaluate.go`):

```text
Evaluate(contract.Contract, evidence.EvidenceSet) -> ([]finding.Finding, Coverage)
```

It is stateless and dependency-light: it reads a collector-stamped `Outcome` on
each observation and applies no temporal, network or Kubernetes logic. For each
required assertion in the contract (an interface's availability, a capability, a
required dependency or configuration, the workload, persistence) it looks for a
matching observation and produces exactly one of three results:

| Result | When | Finding |
|---|---|---|
| **Confirmed violation** | A matching observation has `Outcome=Observed` and its payload contradicts the contract. | `error`, category `RuntimeDrift` — e.g. `CONFIGURATION_ABSENT`, `CONFIGURATION_MISMATCH`, `INTERFACE_ABSENT`, `DEPENDENCY_UNREACHABLE`. |
| **Uncertainty** | No usable observation exists — missing, `Unsupported`, `Failed`, `Stale` or `Insufficient`. | `unknown`, category `Inconclusive` — e.g. `EVIDENCE_MISSING`, `COLLECTION_FAILED`. |
| **Satisfied** | A matching `Observed` observation is consistent with the contract. | None. |

A required assertion Pacto cannot observe is never silently treated as a pass.
`Coverage` reports how many required assertions were actually evaluated versus
declared; it is explanatory metadata and never changes the aggregate compliance
state, because an inability to observe is not a violation.

## Compliance model { #compliance-model }

The compliance model consumers derive from these findings has four substantive
states — **Compliant**, **NonCompliant**, **Unknown** and **Invalid** — plus three
informational ones:

| State | Meaning |
|---|---|
| **Warning** | A non-blocking finding. |
| **Reference** | The contract declares no workload, so there is nothing to run and nothing to observe. |
| **NotEvaluated** | The contract declares a workload but was never runtime-evaluated *at all* — what an offline OCI, cache or local source looks like. This is what `pacto doc`, `pacto fleet` and the dashboard report for a bundle read off disk or out of a registry. |

The guiding rule: **a confirmed contradiction is an error; an inability to observe
is Unknown, not a contradiction.** So a workload that *is* being evaluated but has
no usable evidence yet resolves to `Unknown`, not `NotEvaluated` — the two answer
different questions, "we looked and could not tell" versus "nothing has looked".

The Kubernetes operator only ever reports the former: it never emits
`NotEvaluated`, though the value is in the CRD enum for parity with the engine
(see [Kubernetes limitations](integrations/kubernetes/limitations.md#notevaluated-is-reserved)).
[Compliance scenarios](examples/compliance-scenarios.md) shows where each state is
exercised.

## Separation of concerns

The model keeps ten roles distinct.

| Concept | Where it lives | First-class type? |
|---|---|---|
| **Contract** — declared operational intent | `pkg/contract` `Contract` | Yes |
| **Bundle** — the contract plus the interface/config/policy/skill files it composes | `pkg/contract` `Bundle` (a `Contract` + `fs.FS`) | Yes |
| **Interface** — a composed spec (OpenAPI, AsyncAPI, gRPC) the service exposes | `pkg/contract` `Interface` (references a file in the bundle) | Yes |
| **Capability (contract)** — a declared observability capability: `health`, `metrics` or a namespaced `extension` | `pkg/contract` `Capability` | Yes |
| **Generated tool / skill** — an agent-facing *projection* of a bundle, not part of the domain model | `pkg/capability` `BuildTools` (tools from an OpenAPI interface); `pkg/skills` (`skills/*.md`) | Projection, not a contract type |
| **Policy** — a JSON Schema that validates the contract itself | `pkg/contract` `Policy`; resolved and enforced in `pkg/validation` | Yes |
| **Evidence** — a runtime observation, external to the contract | `pkg/evidence` `Observation` / `EvidenceSet` | Yes |
| **Evaluation result** — typed findings plus coverage | `pkg/finding` `Finding`; `pkg/validation` `Coverage` | Yes |
| **Collector** — turns a real system into evidence | any component producing a valid `EvidenceSet` (`pkg/evidence`); the first-party one is the Kubernetes collector (`integrations/kubernetes`) | No core interface — Evidence is the boundary |
| **Plugin / controller / external actor** — interprets a contract and acts through existing tools | `pkg/plugin` (out-of-process); controllers live in `integrations/*` | Boundary, not core logic |

Two clarifications the naming can obscure:

- **Generated tools are not the contract's `capabilities`.** The contract
  `capabilities` section declares observability endpoints
  (`health`/`metrics`/`extension`). Separately, `pkg/capability` *derives*
  agent-callable tools from a bundle's OpenAPI interface. A generated tool is a
  projection of an interface; it is not a new capability Pacto invents, and it is
  not the `capabilities` domain type.
- **The engine does not observe or act.** It only reasons over `Contract` and
  `EvidenceSet`. Observing reality is a collector's job; performing actions is an
  external actor's job; deciding whether an action is *permitted* is a runtime
  control's job (OPA, Kyverno, admission, IAM). Pacto supplies the structured
  operational meaning those systems interpret and verify against — it is not one
  of them.

## The operational control loop

These roles compose into a loop a platform or an agent can drive. Steps 1, 2, 5
and 6 are implemented in this codebase — the CLI, the dashboard, the collector
and `Evaluate`.

1. **Declare.** A contract states the service's identity, interfaces, capabilities, configuration, dependencies and policies — its operational intent.
2. **Read.** A platform, a controller or an agent inspects the contract (`pacto explain`, the dashboard API or generated tools over MCP) to learn what the service is and what it can do.
3. **Constrain.** *(External.)* Policies, permissions, admission and IAM decide which actions are allowed. Pacto validates a contract against policy schemas; it does not grant runtime permissions.
4. **Act.** *(External.)* Controllers, deploy systems, plugins or agents perform actions through existing infrastructure and tools.
5. **Observe.** Collectors obtain runtime evidence from the real system and produce a `pkg/evidence` `EvidenceSet` (the Kubernetes collector is the first shipped one).
6. **Evaluate.** `Evaluate(contract, evidence)` reports whether observed reality is consistent with the declared contract, producing typed findings and coverage.

## See also

- [Core concepts](concepts.md) — the distinctions this model never collapses
- [Collectors and the evidence boundary](collectors.md) — how evidence is produced
- [Validation layers](contract-reference/validation.md) — whether a contract is *valid*, a separate question from whether it *matches reality*
- [Architecture](architecture.md) — the packages and layers that hold these roles apart
