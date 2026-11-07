---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

`service.offering` (pactoVersion 2.4) names the platform offering a service
provides to other teams such as `postgresql`, and `service.summary` a one-line
purpose; `pacto diff` reports an added, changed or removed offering or summary as `NON_BREAKING`.
The operator reports both in `status.contract.offering` and
`status.contract.summary`.

The dashboard gains an Offerings page. Each offering lists its providers with
their versions, owners, interfaces, configuration and policies, the services
that consume it and a snippet to consume it from a contract. A service page
lists the platform offerings the service uses and, marked inferred, the
providers its unresolved dependencies may mean. The capability filter and chips
now read "Declared capability" and count each extension by its ref.
`pacto fleet search --offering`, the `offering` argument of
`pacto_fleet_search` and the TUI Offerings tab find providers, and `pacto_edit`
gains `offering`, `summary`, `add_configuration_refs` and `add_policy_refs`.
