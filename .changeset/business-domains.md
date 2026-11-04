---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

Add `pactoVersion: "2.4"` and `service.domain`, the business domain a service
belongs to. Every 2.3 contract is valid under 2.4. A `"2.0"` to `"2.3"` contract
that sets `service.domain` reports `NEEDS_PACTO_2_4`, and `pacto diff` reports an
added, changed or removed domain as `NON_BREAKING`. The operator reports the
domain in `status.contract.domain` and writes `status.statusFormat` 2, so a
service seen only through Kubernetes is still grouped by it. A status from an
older operator leaves that service's domain unknown.

The dashboard groups services by domain: domains have a list and a page with
their services, owners, operational targets and attention, and the overview
breaks services down by domain, with services that declare none under No domain
and services whose domain Pacto cannot tell under Domain unknown. Sources that
disagree on a domain raise `DOMAIN_CONFLICT`.
The registry path that used to read "domain" now reads "Registry". Services with
no owner from any source appear in attention as `unowned`. An operational target shows
the environment it runs in when its Pacto carries the
`delivery.pacto.trianalab.io/environment` label, and whether the revision it runs
declares that environment; without the label it reads Unknown. The services list
filters by declared capability. `pacto fleet search --business-domain`, the
`pacto_fleet_search` MCP tool and the TUI accept a domain, `(none)` or
`(unknown)`, and `pacto_edit`
sets one.
