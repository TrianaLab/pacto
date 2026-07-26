# Vision documentation audit

Scope: integrate one coherent long-term VISION narrative across all Pacto
documentation, grounded in the actual V2 implementation. This audit lists the
files to change, why, and the stale-V1 / inconsistent-terminology findings that
motivate the edits. Every claim below was checked against the code, not memory.

## What V2 actually implements (the ground truth the narrative must match)

- Core engine is a pure function: `validation.Evaluate(contract, evidence) -> ([]finding.Finding, Coverage)`
  (`pkg/validation/evaluate.go`). It emits a confirmed violation only when a
  matching observation has `Outcome=Observed` and its payload contradicts the
  contract; a required assertion with no usable observation yields a
  `SeverityUnknown` finding. The engine is stateless and applies no temporal or
  Kubernetes logic.
- Compliance model (`pkg/dashboard/model.go`, `compliance.go`; operator status):
  `Compliant` / `NonCompliant` / `Unknown` / `Invalid`, plus the informational
  `Warning`, `Reference`, `NotEvaluated` outcomes. Finding families
  (`pkg/finding/codes.go`): family 1 = confirmed violations
  (`CONFIGURATION_ABSENT`, `CONFIGURATION_MISMATCH`, `INTERFACE_ABSENT`,
  `CAPABILITY_ABSENT`, `DEPENDENCY_UNREACHABLE`, `WORKLOAD_MISMATCH`,
  `PERSISTENCE_MISMATCH`) = `{RuntimeDrift, Error}`; family 2 = uncertainty
  (`EVIDENCE_MISSING`, `OBSERVATION_UNSUPPORTED`, `COLLECTION_FAILED`,
  `EVIDENCE_STALE`, `EVIDENCE_INSUFFICIENT`, `EXTENSION_EVALUATOR_UNAVAILABLE`) =
  `{Inconclusive, Unknown}`.
- Evidence model (`pkg/evidence/evidence.go`): a discriminated `Observation`
  carrying an `Outcome` (`Observed`/`Unsupported`/`Failed`/`Stale`/`Insufficient`)
  and a typed payload set iff `Observed`. Evidence is EXTERNAL to the contract;
  the collector interface (`pkg/collector/collector.go`) produces it.
- Contract model (`pkg/contract/contract.go`): top-level `pactoVersion`,
  `service`, `interfaces`, `configurations`, `dependencies`, `state`, `workload`
  (a string), `capabilities`, `policies`, `readiness`, `verification`,
  `metadata`, `extensions`. There is NO `runtime` wrapper, NO `port`, NO
  `scaling`, NO `service.image`, NO `lifecycle` — all removed in V2.
- Capabilities/tools/skills: `contract.Capability` is `health`/`metrics`/`extension`
  (observability). Separately, `pkg/capability` derives agent-callable tools from
  a bundle's OpenAPI interface (`BuildTools`), and `pkg/skills` reads
  `skills/*.md`. `internal/mcp` projects both into MCP tools. MCP is one
  integration surface, not the definition of Pacto.
- Bundles/OCI/lock: `pkg/oci` (public `BundleStore`), `pkg/lock` (deterministic
  closure), compatibility ranges (`pkg/contract` `Range`).
- Kubernetes integration (`integrations/kubernetes`): collector translates k8s ->
  Evidence; operator is ONE evidence source + continuous verification. It never
  owns contract semantics and never mutates workloads
  (`integration.yaml`: `producesEvidence`/`consumesFindings`).

## Files to change

| File | Change | Why |
|---|---|---|
| `MANIFEST.md` | Reframe WHY around the operational-contract layer + the historical arc (Ops -> DevOps -> Platform Engineering -> agents-as-consumer) + the declaration-vs-observation split. Remove stale fields. | The clearest expression of why the project exists; currently predates the V2 evidence/evaluation model and still cites `port 8080` and `scales between 2 and 10 instances` as contract content. |
| `docs/index.md` | Drop the "AI-native contracts" lead; answer What/who-consumes/why-for-both-PE-and-agents/what-it-does-NOT-replace; remove port/scaling stale language. | Task: do not lead only with AI; homepage must be balanced and accurate. |
| `README.md` | Light: tighten positioning to "operational contract layer for platforms, automation and agents"; fix stale `/operator` doc link. | Keep concise; README is already close. |
| `docs/architecture.md` | Rework "Conceptual model"; add the V2 engine model (`Evaluate(contract, evidence)`), the separation-of-concerns map (contract/bundle/interface/capability/generated-tool/skill/policy/evidence/evaluation-result/collector/plugin/external-actor) using real types; add the control loop; fix stale `pkg/contract` type list and `ValidateRuntime`; document `pkg/evidence`, `pkg/finding`, `pkg/capability`, `pkg/skills`, `pkg/collector`. | Biggest accuracy gap: the doc omits the entire V2 engine and lists removed V1 types. |
| `docs/contract-reference/index.md` | Add a short "what belongs outside the contract" framing. | Task: contract reference must state the semantic purpose + what is external. |
| `docs/contract-reference/sections.md` | Remove "mandate specific ports" example (2 spots); minor purpose framing. | Ports are not a V2 contract field. |
| `docs/mcp-integration.md` | Remove the "Scaling inputs" sections; refine Bundle -> Capability -> Generated Tools in prose; disambiguate generated tools from the contract `capabilities` section; state Pacto is not tied to MCP. | `pacto_create`/`pacto_edit` do not accept `replicas`/scaling (`internal/mcp/tools.go`). |
| `docs/plugins.md` | Fix Bash example `.contract.runtime.workload`/`.contract.runtime.state.type` -> top-level; fix guidelines `runtime`/`scaling` list. | V2 moved workload/state to top level; `runtime`/`scaling` fields do not exist. |
| `docs/platform-engineers.md` | Destale the contract-field -> platform-decision table, the `pacto explain` sample, the workload/state tables, remove the upgrade-strategy (lifecycle) section, fix diff paths, fix "mandate ports". | Pervasive V1 field names; the `explain` sample shows `Pacto Version: 1.2`, ports and `Scaling: 2-10`, none of which V2 emits. |
| `MIGRATION.md` | Add a concise WHY framing: the contract is stable operational intent; runtime observation + execution belong to integrations. | Task: explain WHY V2 removes/moves runtime data; the mechanics are present, the rationale is terse. |
| `docs/examples/index.md` | Add a conceptual end-to-end "control loop" walkthrough (declare -> discover -> policy gate -> act -> observe -> evaluate), labeling which steps are implemented vs conceptual. | Task: at least one example showing identity/interfaces/capabilities/config/dependencies + discovery + external policy + evidence evaluation. |

## Stale-V1 / inconsistency findings (evidence)

1. `docs/architecture.md` lists `pkg/contract` types `ServiceIdentity`, `Runtime`,
   `ConfigurationSource`, `PolicySource` — none exist in V2 (grep: zero matches).
2. `docs/architecture.md` documents `ValidateRuntime` as a "foundational
   abstraction" — removed; the real entry point is `Evaluate` in
   `pkg/validation/evaluate.go`.
3. `docs/architecture.md` never mentions `pkg/evidence`, `pkg/finding`,
   `pkg/capability`, `pkg/skills` or `pkg/collector`.
4. `docs/platform-engineers.md` uses `runtime.workload`, `runtime.state.*`,
   `interfaces[].port`, `runtime.health.*`, `runtime.lifecycle.*`,
   `scaling.min/max`; `pacto explain` sample prints `Pacto Version: 1.2`, ports
   and `Scaling: 2-10` (real output has no `Runtime:` wrapper, no ports, no
   scaling, and a `Capabilities` section — verified in `internal/cli/output.go`).
5. `docs/mcp-integration.md` documents `replicas`/`min_replicas`/`max_replicas`
   inputs that `pacto_create`/`pacto_edit` do not declare (`internal/mcp/tools.go`).
6. `docs/plugins.md` Bash example reads `.contract.runtime.workload` /
   `.contract.runtime.state.type`; guidelines list `runtime`/`scaling`.
7. `docs/index.md` leads with "AI-native contracts"; cites `port`, "guessed
   health checks" and a "scaling configuration" suggestion.
8. `docs/contract-reference/sections.md` and `platform-engineers.md` cite
   "mandate specific ports" as a policy example.
9. `MANIFEST.md` cites `port 8080` and "scales between 2 and 10 instances" as
   contract content.
10. `README.md` doc table links to `/operator` (page lives at
    `integrations/kubernetes/overview`).

Terminology to standardize: prefer "operational contract" over "runtime
contract system"; disambiguate the contract `capabilities` section from
agent-facing generated tools everywhere both appear.

## Known residue left untouched (code, out of scope)

- `internal/mcp/tools.go` `schemaResult.Description` still says "configuration,
  and scaling" — a code string, not docs; not edited (no code changes in this
  task). Noted as a remaining gap.

## Gate plan

Generated docs (`docs/cli-reference.md`, k8s `generated/`) and their inputs are
NOT touched, so drift checks (b/i), controller-flag (f), chart (g) and
coordinate (h) checks are unaffected. Risk is confined to (c) strict
links/anchors and (d) fenced-contract validation; every new full contract block
is validated with the built `pacto` CLI before commit.
