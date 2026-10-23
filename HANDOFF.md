# HANDOFF from the roadmap track (phase 7 agents as services, phase 8 reach)

What landed (feat/pd-roadmap): pactoVersion 2.1 `mcp` interface type and `agent`
section (`models`, `evals`), diff and validation for both, the `eval` gate
provider in `pkg/delivery` and the chart, reference page
`docs/contract-reference/agent.md`, eval provider section in
`integrations/kubernetes/docs/delivery-health.md`.

Requests for other tracks:

1. Demo tracks (story, demobrowser, demok8s, democompose): optional agent beat.
   Suggested beat after B7: `support-agent` (contract with an `mcp` tools
   interface, a dependency on payments `^3`, `agent.models` and `agent.evals`)
   is published with a model swap as a patch and refused (a model change needs
   at least a minor); the minor ships to test, where its Stage runs an `eval`
   check. Show one of: 47 of 50 cases at `minPassRate: "0.95"` fails and rolls
   back, or the runner is down and the change waits (Unknown never passes).
   Browser demo: the eval verdict is simulated and labelled so. Kubernetes
   demo: needs a tiny eval runner HTTP service answering
   `{"subject","suite","run","passed","total"}` and a chart provider
   `{name: evals, type: eval, url: ...}`. Story file: `examples/story/**`.
2. dash: the entity page does not show the `agent` section or that an `mcp`
   interface lists tools. Optional: show `agent.models` and `agent.evals`
   beside interfaces (the generated SDK already carries `Contract.Agent`).
3. core (optional): `pacto_create` writes 2.0 contracts and cannot scaffold an
   agent. Only if a user asks: a 2.1 path with an `mcp` tools stub and an
   `agent` section.
4. Ledger: section H phase 7 can be ticked once a verifier confirms.
5. Pre-existing, not from this track: `docs/mcp-integration.md` is 1243 prose
   words (budget 1200) in `make docs-lint`.

## Phase 8 (reach)

What landed: Stage `applyRef.kind: Application` (Argo CD applier, chart
`argocd.enabled`), `candidates.upstreamRecords` (follow a Stage in another
cluster through its signed records; promoter `--upstream-trust-config`, chart
`promoter.upstreamTrust`), reference page
`integrations/kubernetes/docs/delivery-reach.md`. External operation types stay
later (design: "after M5, when a real team asks"); the page says so.

6. Kind (ci or demok8s, optional): no live run of either piece. An Argo CD
   shard could reuse the GitOps gate's core-install fixture (patch `argocd-cm`
   before the controller starts, create the `default` AppProject, enable
   apps-in-any-namespace for the Stage namespace, OCI source from the zot
   registry). Two-cluster: two kind clusters sharing the zot registry, the
   upstream recorder on, the downstream promoter with `promoter.upstreamTrust`.
7. Demos (rows, not demos): optional "applied by Argo CD" and "prod in another
   cluster follows staging" rows on the delivery pages.
8. dash (optional): the Stage views do not show where candidates come from or
   which applier a Stage uses. If they ever do, include `upstreamRecords` and
   `applyRef.kind`. The Ready reason `UpstreamUntrusted` is new.
9. Ledger: section H phase 8 can be ticked once a verifier confirms.

## Out-of-ownership paths (phase 7)

Phase 7 commits 28a54d28, 3d4a150e and 50526990 touched paths outside the
roadmap row. Check them against the docs and ci branches when integrating:

10. ci and docs tooling: `release/scripts/docs_check.py` writes an `mcp` tools
    stub (`MCP_STUB`) for doc examples with an `mcp` interface, and follows
    `agent.evals` as a local ref, so the doc contract check validates 2.1
    agent examples the way the real CLI does.
11. core: `pkg/diff/{classification,engine,interfaces,mcp}.go` classify `mcp`
    interface and `agent` changes (a model swap needs at least a minor), and
    `pkg/validation/crossfield.go` adds the agent cross-field checks.
12. core: `pkg/validation/schema/pacto-v2.1.schema.json` and
    `field-coverage.yaml` add the `mcp` interface type and the `agent`
    section; `tests/integration/field_coverage_test.go` covers them.
13. story: commit 4666fbfb renames the Stage applier type in
    `examples/story/story.go` (`ApplyRef: delivery.LocalRef{...}` becomes
    `delivery.ApplyRef{...}`). The old `LocalRef` no longer compiles there, so
    keep the new type when resolving a conflict with the story branch, and
    apply the same rename to any `StageSpec` the story branch added since.
14. demo: commits 3d4a150e, 4666fbfb and 0ad3e6cf regenerate
    `examples/demo/pacto-dashboard/interfaces/openapi.json` (a copy of the
    dashboard OpenAPI). Do not hand-merge it: after merging the demo and dash
    branches, run `make generate-dashboard-openapi` (or
    `make generate-dashboard-sdk`) and commit the result.
