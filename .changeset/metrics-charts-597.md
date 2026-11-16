---
"@pacto/core": minor
---

The dashboard Metrics page draws what Pacto did as charts: three headline
tiles with sparklines (breaking changes caught with the consumers they
protected, release gates by newest verdict and lead time to production at p50
with its p90) and five charts (what Pacto stopped, gate outcomes, delivery
health by environment, lead time by environment and coverage over time), each
with a table view and exact numbers in hover and keyboard tooltips. Unknown is
hatched and never drawn as 0, and unverified gates are dotted. The page adapts
from a few to hundreds of environments: one panel per environment up to eight,
then a heatmap with a promotion-depth filter that groups environments by
promotion depth, owner or domain and collapses large groups to their pooled
rate. Lead time to production pools the promotions into the environments at
the end of their promotion chain. `/api/stats` gains `environmentsBy`,
`leadTimeGroups` and each environment's `depth`, `chainEnd` and `parents`;
`pacto stats` and the MCP `pacto_stats` answer are unchanged.
