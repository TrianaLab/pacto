---
"@pacto/core": minor
"@pacto/operator-chart": minor
---

`pacto export backstage --index <registry>/pacto-index` writes Backstage
entities for every service the organization index lists, offline: it pulls
each newest entry's bundle by digest with the same provenance and owner checks
as `--oci`, and reads the inferred contracts those bundles name under
`<registry>/pacto-inferred`. Add `--target-snapshot` for the inferred services
the clusters published. `pacto fleet`, `pacto tui`, `pacto mcp` and
`pacto reconcile` read `--index` the same way, up to 2000 services; scripts
that passed `--index` to them and want the old behaviour drop it or pass
`--index none`. `pacto impact` and `pacto ci` keep reading only consumers from
it, so the index alone never clears a gate.

`pacto dashboard --index` (`PACTO_DASHBOARD_INDEX`) serves the same entities at
`/api/backstage/entities`. The operator-managed dashboard reads
`<registry>/pacto-index` when `registry` is set; opt out with
`dashboard.index: none`. On a private registry it needs a pull credential in
`dashboard.ociSecrets`, or the route answers 503. A missing index on a
long-running dashboard reads empty until it has once listed entries, then
partial.

Backstage entities carry three new annotations: `pacto.trianalab.io/source`,
`pacto.trianalab.io/workload` and `pacto.trianalab.io/inferred-contract`.
