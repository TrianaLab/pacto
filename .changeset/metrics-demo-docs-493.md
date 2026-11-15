---
"@pacto/core": patch
"@pacto/k8s-module": patch
---

The demos ship generated run records, labelled unsigned statistics: the browser
demo opens Metrics with about 90 days of records that follow the payments story,
the Compose demo seeds CI records into its own registry and the Kubernetes demo
records its real publishes and promoter operations on top of a seeded history.
The delivery tour visits Metrics before its recap. The new guide "Measure what
Pacto does" explains what is recorded, where it goes, how to turn it on in CI
and in delivery, who sees which records and how to prune them, and the run
record reference says for every field whether it can hold personal data.
