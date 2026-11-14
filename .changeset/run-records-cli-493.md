---
"@pacto/core": minor
---

`pacto diff`, `pacto impact` and `pacto publish` can record what they catch in
CI as an unsigned run record (`--stats-registry`, `--stats-file`,
`--stats-context`, `PACTO_STATS_REGISTRY`, `PACTO_STATS_FILE`), an OCI
referrer in a stats repository you name. `pacto stats publish` publishes
record files written by a job without write access. Recording is opt-in and
best effort: it never changes an exit code or stdout. Run records fall back to
the referrers tag on registries without native Referrers, such as GHCR. See
the run record reference.
