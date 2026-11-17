---
"@pacto/core": patch
---

Contracts name no deploy tool. A pactoVersion 2.5 contract with a `delivery`
section fails to load with `PARSE_ERROR` and a hint: the Stage's
`spec.applyRef` names the applier. The `pacto.applier` promote gate judges
the applier the promoter observed against `spec.applyRef` and nothing else.
