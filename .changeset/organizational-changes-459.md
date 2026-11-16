---
"@pacto/core": minor
"@pacto/k8s-module": patch
---

A service's detail now carries its `changes`: every declared change between
the revisions Pacto holds (owner, business domain, offering, dependency,
lifecycle and environment) and each validation finding that appeared or
resolved, newest first in version order, each with the revision that shows it,
the one it was compared with, its `provenance` and its `source`. `changes`
replaces the unreleased `environmentHistory`. `pacto fleet entity service
<key> --reached`, `reached` on a stdio `pacto_fleet_entity` and the service
page add when each change's revision reached a Stage, from signed delivery
records read as the caller; a change with no record reads "time not recorded",
and "time hidden" when the caller may not read delivery. The TUI and the
dashboard's service page show the same changes.

A sequential owner handover is now an owner change, never `OWNER_CONFLICT`.
`OWNER_CONFLICT` remains for targets that run revisions declaring different
owners, or for two revisions of one version that disagree, and says when the
link is inferred. Rankings, owner filters and owner pages count the current
owner, and a former owner's page still lists the revisions it declared.

`pacto delivery history`, `pacto_delivery_history` and Delivery history name
the fleet revision of every artifact and the `pacto.impact` verdict each
record signed, apart from a re-analysis against today's graph; blame names the
revision too. `pacto delivery history --at`, `at` on `pacto_delivery_history`
and `?at=` on the dashboard answer which revision each Stage ran at a past
time, from signed records, and read `unknown` where no record holds.
