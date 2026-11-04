---
"@pacto/core": minor
---

The dashboard's service page now shows a service's whole context. Its facts name
the contract it runs, its capabilities and the sources that saw it, and the owner
reads Owner unknown when Pacto could not read any of its contracts. Expected
dependencies list external and unresolved dependencies, and operational targets
show their environment. A Delivery section lists the service's Stages, found
through a new exact `service` filter on `/api/delivery/environments`. The
dependency drift counts dependencies outside their accepted range and those that
cannot be observed, and a readiness bar tallies the service's revisions. When a
revision is read from a registry, Pacto checks its bundle signature against the
signer its contract declares in `publish.sign` and lists its OCI referrers; the
result is the new `provenance` field in `pacto_fleet_get` and on the revision
page. Policy and configuration schema content is read from the bundle and shown
on the service and revision pages. Revision-match buckets no longer link to the
unresolved dependencies list, and a service with stale or missing evidence no
longer reads Healthy.
