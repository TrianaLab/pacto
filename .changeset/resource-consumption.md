---
"@pacto/core": minor
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
