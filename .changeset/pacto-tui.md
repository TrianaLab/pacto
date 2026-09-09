---
"@pacto/core": minor
---

Add `pacto tui`, a full-screen terminal front-end over the CLI. It loads one
fleet snapshot, lets you navigate services, revisions, targets, owners and
sources, then runs the read verbs (validate, diff, impact, lock check, explain)
in process against the selection. Write verbs (push, pull, lock update,
generate) run as confirmed subprocesses so they keep the terminal they need.
`--read-only` hides the write verbs entirely, and `y` copies the equivalent
`pacto` command for anything the TUI does not offer.
