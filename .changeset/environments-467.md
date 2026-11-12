---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

An environment in `environments[]` can now declare its `lifetime`
(`temporary` or `persistent`), an `expires` time (RFC 3339, only with
`temporary`) and an `owner` in the shape of `service.owner` (pactoVersion 2.4).
A lifetime nobody declared reads "not declared", never persistent. Once a
temporary environment's `expires` has passed, `pacto validate` and
`pacto_check` warn `ENVIRONMENT_EXPIRED`; Pacto deletes nothing for it.
`pacto diff` classifies every change to these fields, and to an environment's
`file`, as non-breaking.

`pacto init --delivery --env` and `pacto_create` with `delivery` also declare
each environment in `pacto.yaml` with an empty values file.
`pacto_edit add_environments` takes `lifetime`, `expires` and `owner`, and
`pacto_edit remove_environments` retires an environment and deletes the values
file no other environment names. `pacto explain --environments a,b` and
`pacto_check environments` list the configuration values two or more
environments disagree on.

A service's environments are one list in `pacto fleet entity service`,
`pacto_fleet_entity` and the dashboard's service page: each environment the
current revision declares or a visible target reports, with its declaration,
its owner (declared, or the service owner labelled inferred), the targets
observed there, its provisioning tally, the differences between environments
and how the declarations changed between versions. On a delivery host the
service page also shows the Stages for each environment.
`pacto fleet entities --consumes <offering> --phase <phase> --environment <env>`
and the matching `pacto_fleet_entities` arguments list the consumers in a
provisioning phase in one environment.

The delivery promoter emits the Kubernetes Events `EnvironmentCreated` and
`EnvironmentDeleted` when a Stage is first reconciled and when it is deleted,
and the recorder posts the CDEvents `environment.created` and
`environment.deleted` to its CDEvents sink. Each names one Stage.
