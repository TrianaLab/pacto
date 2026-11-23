---
"@pacto/core": minor
"@pacto/operator-chart": minor
---

The Evidence Server accepts signed evidence for every immutable revision
published under a registry prefix, set with `pacto evidence serve
--subject-prefix` or the chart value `evidence.registry.subjectPrefixes`,
including revisions published after install, with no Helm edit and no
restart. Refs must still be `@sha256` digests, exact subjects keep working and
the trust config still narrows each key. The server records each subject in
the `<prefix>pacto-evidence` repository, so a restart loses nothing. Where the
registry serves the OCI Referrers API evidence stays a referrer of the
revision; on GHCR and CNCF distribution each record is a write-once tag in
`<prefix>pacto-evidence`, so a `ResourceProvisioned` report reads `Ready`
there too. The server never writes the shared `sha256-<digest>` referrers tag.
