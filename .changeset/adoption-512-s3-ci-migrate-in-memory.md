---
"@pacto/core": minor
"@pacto/k8s-module": patch
---

`pacto ci` publishes a contract that declares an older `pactoVersion` as the current version, raised in memory, when that keeps what the contract means: only the version token of the bundled `pacto.yaml` changes, the bundle records the raise as a `migrations` entry in `pacto.inferred.yaml` and the plan adds one line per migrated or kept contract without changing the exit code. A raise alone does not republish an unchanged service, and the file in the repository is never rewritten. The fleet revision detail reads `2.5 (migrated from 2.4, inferred)`, with the inferred `migratedFrom` JSON key read only from a verified record, in `pacto fleet`, the TUI, MCP `pacto_fleet_get` and the dashboard revision page. The `CONTRACT_BEHIND` message now says when `pacto ci` publishes the contract as the current version.
