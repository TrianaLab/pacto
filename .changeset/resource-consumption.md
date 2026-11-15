---
"@pacto/core": minor
"@pacto/k8s-module": patch
---

A service's detail lists every resource it consumes. A use of a provider that
declares no offering now shows once a provider reports on it, and the service's
`hiddenProviders` says at least how many resources come from providers the
viewer may not see. Each use carries `providerOwner`, `providerAvailability`,
`requested` (the values the contract declares, on the configuration-reference
row) and `consumption`, which reads `not-reported` until a provider reports
usage or cost. A new `provisioning` attention category raises
`PROVISIONING_FAILED` and `PROVISIONING_STALE`; a resource's phase never changes
the service's compliance. `pacto fleet entity service` prints a Resources block
with provenance, and the dashboard's service page gains a Resources section
that joins each use with its requested configuration.

Provider adapters can report a resource's usage and cost with the new
`ResourceUsage` observation kind, written by `pacto evidence usage`. Only a key
that lists `ResourceUsage` in `allowedObservationKinds` may report it, scoped by
`allowedProviderRepos` like `ResourceProvisioned`, and an EvidenceSet carries
one provider-reported kind only. The Evidence Server's targets endpoint lists
usage under schema `pacto.dev/evidence-source/v4`, written only when it holds
usage. `consumption` reads `reported`, with the figures, only for a signed,
fresh, uncontested report about the current revision; otherwise it reads
`unknown` and keeps the raw figures apart under `reported`. The CLI, MCP and the
dashboard's Resources section show both, and Pacto never sums or converts a cost.
