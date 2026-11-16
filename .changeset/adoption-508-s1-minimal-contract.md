---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

An empty `pacto.yaml`, or one line of it, is now a valid contract
(pactoVersion 2.5). An absent `pactoVersion` reads as the newest version the
CLI supports, and `service`, `service.name` and `service.version` may be left
out. Pacto infers the service name from `Chart.yaml`, a Kustomization or a
single-service Compose file, the `org.opencontainers.image.title` label, the
`app.kubernetes.io/name` label or the folder name, and `pacto validate`, the
fleet and the dashboard say where each inferred value came from. An inferred
name that another service also claims is dropped from the operational graph
with a `SERVICE_NAME_COLLISION` limitation, and an inferred name never clears
an impact gate (`SERVICE_NAME_INFERRED`).

`pacto pack`, `pacto push` and `pacto publish` refuse a contract that does not
declare `service.name` (`PUBLISH_NEEDS_SERVICE_NAME`) or `service.version`
(`PUBLISH_NEEDS_SERVICE_VERSION`) and write `pactoVersion` into the bundled
`pacto.yaml`. A bundle pulled from a registry, or an inline contract in a Pacto
resource, that does not declare all three is rejected; the operator reports
`ContractUndeclared`.

A dependency may leave out `name`, which is derived from its `ref` and resolves
only against a provider published at that repository, and `required`, which
defaults to `true`. `compatibility: unknown` records a range nobody decided
yet: it is never read as compatible and never clears a gate or a promotion.
An interface can declare its `port`, and `breaks[]` announces a deliberate
breaking change to the consumers it names. Validation, `pacto diff`, the fleet,
the dashboard, MCP and the operator status (`status.interfaces[].port`) read
the new fields.
