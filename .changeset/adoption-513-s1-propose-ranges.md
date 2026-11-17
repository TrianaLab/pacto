---
"@pacto/core": minor
"@pacto/k8s-module": patch
---

pacto init and pacto_create propose dependency ranges and write
`compatibility: unknown` instead of declaring a guessed range. The proposal
goes in a trailing comment: `^MAJOR.MINOR.0` of the lowest version the
reconciled Pactos describing the called Service report, or of the dependency's
contract in `--fleet`, and none when nothing was read or those Pactos disagree
on the contract. A draft that writes a dependency declares pactoVersion 2.5.
pacto_create and pacto_edit write `compatibility: unknown` when an agent gives
no range. The CLI, the TUI, `Used by` and the dashboard say "range not
declared" for such a dependency.

The delivery controller's gate reason says range not declared for a consumer
with compatibility unknown.
