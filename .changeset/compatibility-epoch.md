---
"@pacto/core": minor
---

Add `pactoVersion: "2.2"` and an interface's `compatibilityEpoch`, an explicit
marker for a breaking change no spec shows, such as an amount switching from
cents to euros.

- Raise `interfaces[].compatibilityEpoch` and `pacto diff` reports
  `interfaces.compatibilityEpoch` as `BREAKING`, naming the interface, so
  `pacto impact` lists its consumers. Lowering it is `BREAKING` too: consumers
  that adapted to the higher epoch break in turn.
- A `"2.0"` or `"2.1"` contract that sets it reports the new `NEEDS_PACTO_2_2`
  finding. Every 2.1 contract is valid under 2.2.
