---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

The dashboard's Metrics page, under Change and delivery, draws the run records
in a stats repository as daily or weekly trends: breaking changes caught,
consumers protected, gates, delivery rates, lead time per environment and
coverage of contracts and observations. Every number is labelled unsigned statistics
and links to the records behind it. Start the dashboard with
`pacto dashboard --stats-registry` (or `PACTO_STATS_REGISTRY`); without one the
page explains how to turn recording on. A signed-in viewer sees only the
services their Kubernetes RBAC allows: hidden services are never named or
counted. `pacto fleet status --stats-registry` (or `--stats-file`) writes a
coverage record of the snapshot it read. The operator chart value
`dashboard.stats.registry` points the managed dashboard at a stats repository.
