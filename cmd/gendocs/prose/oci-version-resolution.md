## OCI version resolution

All commands that accept `oci://` references support automatic version resolution. When a reference omits the tag (e.g. `oci://ghcr.io/acme/svc-pacto` instead of `oci://ghcr.io/acme/svc-pacto:1.0.0`), pacto queries the registry for available tags and selects the **highest semver version**.

For dependency references declared with a `compatibility` constraint, only tags satisfying the constraint are considered. For example, a dependency with `compatibility: "^2.0.0"` and available tags `1.0.0`, `2.0.0`, `2.3.0`, `3.0.0` resolves to `2.3.0`.

Non-semver tags (e.g. `latest`, `main`) are ignored during resolution. Digest-pinned references (`@sha256:...`) and explicitly tagged references are used as-is.

!!! note "Constraints apply to the CLI's dependency graph, not dashboard discovery"
    `compatibility` constraints are applied by the CLI when resolving a contract's
    **declared dependencies** (e.g. `pacto graph`). The **dashboard's** version
    discovery selects the highest semver tag and does **not** apply per-dependency
    `compatibility` constraints. Root-contract pulls (a tagless top-level `oci://`
    ref) also select the highest semver tag with no constraint.

---

