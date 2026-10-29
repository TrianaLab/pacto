---
"@pacto/core": minor
---

Contracts can declare behavioral guarantees (`pactoVersion: "2.2"`). An
interface lists what consumers may rely on in `guarantees`: `delivery`,
`ordering`, `dedupeWindow` and `retention` for events, `idempotent` and
`timeout` for calls. A dependency lists what it relies on in `requires`.
A `"2.0"` or `"2.1"` contract that uses either reports `NEEDS_PACTO_2_2`.
`pacto validate` reads the provider contract and reports every unmet guarantee
as `GUARANTEE_UNMET` in its `guarantees` stage, or warns with `GUARANTEE_UNVERIFIED` when it cannot read
it. `pacto graph` marks refused edges, `pacto diff` classifies a removed or
weakened guarantee as breaking and `pacto impact` names the consumers whose
requirement a change breaks.
