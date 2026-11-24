---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

One deploy digest now carries every environment's values, and each Stage runs
it with the values file its applier already selects: an Argo CD Application
with `spec.source.helm.valueFiles: [values-<environment>.yaml]` or a Flux
Kustomization that builds `overlays/<environment>`. Pacto reads the selection
and never writes or renders it. The delivery plan reads the selected file from
the digest it promotes and cites the file and its sha256 on `pacto.manifests`,
and `pacto.environment` passes only when those values equal the environment
values Pacto checked. Different values, a declared environment with no file
while the applier applies one, a missing values file and a HelmRelease chart
that carries `values-<environment>.yaml` wait for a person. `pacto delivery
plan` prints a `values:` line, and `pacto delivery environments`, `pacto
delivery status`, MCP and the dashboard's Applier line show the selection.

For a configuration whose `ref` reaches an offering whose schema declares tiers
(`x-pacto-tier`), an environment may change only `tunable` fields within the
offering's bounds, enums and classes. `pacto validate`, `pacto ci`,
`pacto_check` and the delivery plan refuse the rest with four new codes,
`TierNotTunable`, `ParameterOutOfRange`, `ParameterNotDeclared` and
`ClassNotOffered`, each with the closest valid name and a `Next:` step, and
the plan stops on them with no approval. Values outside a tiered offering's
parameters that used to report `ENVIRONMENT_VALUES_INVALID` or
`CONFIG_VALUES_VALIDATION_FAILED` now report these codes. An offering whose
schema declares no tier keeps today's codes, and an inferred offering leaves
the values `ENVIRONMENT_VALUES_UNVERIFIED`.

The Stage records the selection as observed in `status.applier.values`, a new
optional status field. `Mediated` accepts exactly the Stage's own selection;
any other, such as two value files or `values-staging.yaml` on a production
Stage, reads `False` with `OtherEnvironmentValues`. This also refuses an
existing install whose Kustomization builds `overlays/<name>` where `<name>`
differs from `spec.environment`, such as `overlays/prod` on a Stage with
`spec.environment: production`, and its promotes wait for a person: rename the overlay or create the Stage with
the matching environment. `spec.environment` must now be a DNS label and is
fixed at creation; an existing Stage keeps its value.
