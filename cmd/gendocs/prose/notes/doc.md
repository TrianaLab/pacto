The header line reports the contract's own state. `pacto doc` reads a bundle and observes no cluster. A contract that has never been runtime-evaluated therefore reads `status NotEvaluated`, and one that declares no workload reads `compliance REFERENCE`. Both are the expected result for a freshly scaffolded contract. See [Compliance states](model.md#compliance-model).

Markdown prints to stdout by default. `-o DIR` writes `DIR/<service>.md`. `-o NAME.html` writes a self-contained static documentation site (a directory) that reuses the dashboard UI offline.

`--serve` serves that static site locally. `--ui swagger` launches an interactive API explorer.

`--serve`, `--ui` and `-o` are mutually exclusive. Use `--interface` to pick one OpenAPI interface with `--ui`. `--target` and `--target iface=url` point try-it-out at a backend.

Sibling dependencies are resolved in parallel. OCI bundles cache under `~/.cache/pacto/oci/`. `--no-cache` bypasses the cache.
