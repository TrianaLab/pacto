---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

Delivery matches contract environments to Stages. A promote runs a new
`pacto.environment` check that compares the Stage's `spec.environment` with
the environments the version's contract declares, using the same
`environments` stage as `pacto validate` on the version's bundle.

- A contract that declares no `environments` passes the check, which says it
  does not apply, so services that do not use environments are unaffected.
- A contract that declares environments but none named like the Stage's
  `spec.environment` waits for the service's team to approve it, for example
  `version 1.1.0 declares no environment prod for Stage payments-prod (it
  declares dev, staging)`. So does one whose values for that environment fail
  with `ENVIRONMENT_VALUES_INVALID`, `ENVIRONMENT_VALUES_MISSING` or
  `ENVIRONMENT_UNKNOWN_CONFIGURATION`, each located in the values file.
- Values Pacto cannot check (`ENVIRONMENT_VALUES_UNVERIFIED`) wait for a
  person to accept the unknown.

The promoter also holds a promote that leaves a consumer running in the
environment with a required guarantee unmet, as `pacto impact` and
`pacto publish` already refuse it. The consumer's team is asked, and the
question names consumer, provider and guarantee. A rollback to such a version
waits the same way. An unmet guarantee of an optional dependency holds
nothing, and one on a consumer Pacto cannot place is Unknown.

The dashboard labels the check "Environment values" and shows N/A when it
does not apply. The Stage's `spec.environment` field documents the match.
