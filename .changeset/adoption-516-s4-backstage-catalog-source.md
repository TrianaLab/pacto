---
"@pacto/core": minor
"@pacto/k8s-module": minor
"@pacto/operator-chart": minor
---

Pacto reads the owners, systems and component dependencies a Backstage catalog
already holds. `--backstage-url` on `pacto fleet`, `pacto impact`, `pacto tui`,
`pacto mcp --fleet`, `pacto export backstage` and `pacto dashboard`
(`PACTO_DASHBOARD_BACKSTAGE_URL`) names the catalog, and the reader token comes
only from `PACTO_BACKSTAGE_TOKEN`. Each value is inferred, with its source,
entity and read time: it never fills `owner` or the business domain, never
becomes an owner claim, never clears a gate and never changes an impact
verdict. The `backstage-catalog` source is advisory, so an unreachable or
capped catalog shows as its own state and never makes an answer partial or
blocks the Backstage export. Entities Pacto exported are never read back.

Fleet answers and `pacto_fleet_get` carry the new keys `inferredOwner` and
`inferredBusinessDomain`, the dashboard detail carries `ownership.inferred`
and `inferredBusinessDomain`, and `ownerProvenance` reads `inferred` when no
other owner exists. A name that matches several services raises
`SERVICE_FACT_AMBIGUOUS`. `pacto export backstage` writes the inferred owner
and domain only to the `pacto.trianalab.io/inferred-owner` and
`pacto.trianalab.io/inferred-business-domain` annotations, with their
`-source` annotations, and keeps `spec.owner: unknown`.

The chart adds `dashboard.backstage.url` and `dashboard.backstage.tokenSecret`
(the operator flags `--dashboard-backstage-url`,
`--dashboard-backstage-token-secret` and `--dashboard-backstage-token-key`).
Every dashboard user sees the owners, systems and dependencies the reader
token can read, so give it catalog read on the entities every dashboard user
may see. Detection proposes an in-cluster Backstage on port 7007, and with no
URL set the dashboard reads it without a token.
