# HANDOFF from the roadmap track (phase 7, agents as services)

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
