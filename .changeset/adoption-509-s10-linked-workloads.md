---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

A workload whose running image a published bundle records, or which carries pacto.trianalab.io/contract, is evaluated with no Pacto. The operator links it to the published contract, judges it with the same evaluation a Pacto resource gets and writes the result to the `linked.json` key of its `<release>-detected` ConfigMap and to the signed target snapshot. `pacto fleet --detected`, the dashboard, the TUI and the MCP fleet tools read each linked workload as a target of its contract revision with a `link`, and hand its inferred twin over to the declared service; the `Replacement.link` enum gains `observed`. A hand-written Pacto resource still wins, ambiguous matches link nothing and a digest several release versions record links to the newest with `LINK_VERSION_AMBIGUOUS`, which `pacto impact` reads as unverified. No Helm value, RBAC rule, CRD or contract field changes.
