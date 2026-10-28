Scaffolds three files: a valid `pacto.yaml`, a placeholder OpenAPI spec at `interfaces/openapi.yaml` and a configuration JSON Schema at `configuration/schema.json`.

`<name>` is the service name: it becomes the directory *and* `service.name`, which must match `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`. `pacto init fleet/checkout-web` therefore scaffolds a bundle that fails validation: `service.name` gets the whole path and fails `SCHEMA_VIOLATION`. To scaffold inside a directory, `cd` there first and pass the bare name.

All three are required **by the scaffolded contract**, because its `interfaces:` and `configurations:` sections point at the other two files. If you delete a directory, also delete the section that references it, or validation fails with `FILE_NOT_FOUND`. A contract that declares neither section needs only `pacto.yaml`, so drop the section and the directory together.

!!! note "The scaffold fails `validate --readiness` on purpose"
    Its two placeholder readiness claims are `not-done`, which scores 0 against the
    scaffolded `minScore: 80` and fails with `READINESS_GATE_UNMET`. Plain
    `pacto validate` passes, because the gate is opt-in. Fill the claims in as the
    work lands. The placeholder is the starting position of a real assessment, so keep it
    and update it.
