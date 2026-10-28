Dependencies resolved from local paths are annotated with `[local]`. Shared dependencies (referenced by multiple parents) are annotated with `(shared)`.

Reports cycles, version conflicts and unreachable dependencies.

Sibling dependencies are resolved in parallel. OCI bundles are cached locally in `~/.cache/pacto/oci/` for faster subsequent operations. Use `--no-cache` to bypass the cache.
