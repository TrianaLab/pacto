---
"@pacto/core": minor
---

`pacto publish`, `pacto push` and `pacto ci --publish` write one write-once
entry per published version to the organization index,
`<registry>/pacto-index`, naming the bundle by digest, its dependencies and the
publisher it claims. A failed write prints a warning and never changes the
publish outcome. `--index` names another repository and `--index none` writes
none.

`pacto impact`, `pacto ci` and `pacto_impact` over stdio find every declared
consumer through the index with no `--root` list. They check the consumers
against the target sources and report the read in `consumerIndex` and the
`Consumers:` line; an index with no target source, or a consumer it does not
list, makes the answer partial, so the index alone never clears a `BREAKING`
change.
