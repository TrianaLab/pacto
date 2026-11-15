---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

The delivery promoter can publish an unsigned run record of every operation it
concludes to a repository you name with `delivery.stats.repository` (Helm) or
`--stats-repository`. Records hold the type, outcome, verification, gate
results, how long each operation was held, the class of who asked, the Stage
and the digests, never a name. Publishing is best effort: a full queue or a
registry error is counted in `pacto_delivery_stats_dropped_total` and never
changes an outcome. `pkg/runrecord` describes `kind: operation` records.
