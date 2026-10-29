---
"@pacto/core": minor
---

Contracts can declare an interface's `compatibilityEpoch`
(`pactoVersion: "2.2"`), an explicit marker for a breaking change no spec shows, such as an amount switching from
cents to euros.

- Raise `interfaces[].compatibilityEpoch` and `pacto diff` reports
  `interfaces.compatibilityEpoch` as `BREAKING`, naming the interface, so
  `pacto impact` lists its consumers. Lowering it is `BREAKING` too: consumers
  that adapted to the higher epoch break in turn.
- A `"2.0"` or `"2.1"` contract that sets it reports `NEEDS_PACTO_2_2`.
