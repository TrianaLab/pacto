---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

Probe each registry once and record the mode Pacto uses. Delivery records now
work on registries without the OCI Referrers API, such as GHCR and CNCF
distribution, as one write-once, content-addressed tag per record in the
streams repository; every reader reads both forms. Stages gain
`status.stream.registryMode` and the `RecordStoreReady` reason `WriteOnceTags`;
the `ReferrersUnsupported` reason is removed, and a Stage that recorded
`native` is never moved to write-once tags on its own. `pacto fleet providers`
shows a `Registry <host>` row for every registry an OCI source reads.
