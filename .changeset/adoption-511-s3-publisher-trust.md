---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

Accept a version only from a publisher Pacto knows. A first Stage with
`spec.candidates.registry` watches the registry, verifies each new version's
Sigstore signature and accepts it only when the signer is the CI the
installation's registry names (`delivery.publishers.registry`, a rule derived
for `ghcr.io` and `registry.gitlab.com`), an identity a person confirmed for
that service or a rule of a signed delivery policy
(`delivery.publishers.policy`). It needs no Flux, so an Argo CD-only cluster
promotes its first Stage. A new CI signer raises one "Confirm identity" item
in the delivery inbox; confirming it writes a signed `identity-pin/v1` record
to the registry. An unsigned or unreadable version, or a deploy digest a
person signed, never passes. Keyless signers are classified as `human`, `agent` or `ci`, and an
approval signed by anything but a person is a proposal that never counts.
`pacto delivery trust`, the `pacto_delivery_trust` MCP tool and
`GET /api/delivery/trust` show which publishers each service accepts.

A fleet signature `status` is now `verified` only when a publisher rule, a
pin or a signed policy accepts the signer, and the new `source` field says
which. A signer the contract's own `publish.sign` declares is no longer
`verified`. `pacto publish` stamps `org.opencontainers.image.source` on the
artifacts it builds in CI.
