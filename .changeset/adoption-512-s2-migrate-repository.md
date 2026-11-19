---
"@pacto/core": minor
---

`pacto migrate [dir...]` finds every pacto.yaml under the given directories
(the current one by default) and raises its pactoVersion to the newest
version, or to `--to`, when validation finds nothing new. It changes only the
version, writes atomically, prints one line per file and a summary, previews
with `--dry-run`, lists the folders it skips and exits 1 only when a file
cannot be read or written, a read-only one included. 1.x
contracts follow MIGRATION.md. It also removes the `delivery` section that
left pactoVersion 2.5 before its release, and every `publish.artifacts[].build`
block that `pacto ci --publish` detection reproduces; a block that differs stays
with a suggestion. `CONTRACT_BEHIND` now says to run `pacto migrate` when the
raise keeps the contract's meaning.
