## OCI version resolution

All commands that accept `oci://` references support automatic version resolution. When a reference omits the tag (for example `oci://ghcr.io/acme/svc-pacto` with no `:1.0.0`), pacto queries the registry for available tags and selects the **highest semver version**.

For dependency references declared with a `compatibility` constraint, only tags satisfying the constraint are considered. For example, a dependency with `compatibility: "^2.0.0"` and available tags `1.0.0`, `2.0.0`, `2.3.0`, `3.0.0` resolves to `2.3.0`.

Resolution ignores non-semver tags such as `latest` and `main`. Digest-pinned references (`@sha256:...`) and explicitly tagged references are used as-is.

!!! note "Constraints apply to the CLI's dependency graph"
    The CLI applies `compatibility` constraints when it resolves a contract's
    **declared dependencies**, for example in `pacto graph`. The **dashboard's**
    version discovery selects the highest semver tag and ignores per-dependency
    `compatibility` constraints. Root-contract pulls (a tagless top-level `oci://`
    ref) also select the highest semver tag with no constraint.

---

