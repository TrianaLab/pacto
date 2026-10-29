---
"@pacto/core": minor
---

`pacto validate` reports one PASS, FAIL or SKIP line per stage (load, lock,
structural, cross-field, policy, guarantees, readiness and closure), lists each error
under its stage with its file:line:column when it can be located, and ends
with a `Next:` line naming the one command to run now. A stage after a failed
one is skipped. The JSON output adds `stages`, `next`, `file` and each error's
`line` and `column`, and `pacto_check` returns the same `stages`, `next` and
locations. An unknown field or a mistyped enum value carries a `did you mean`
hint naming the closest valid one.

The text output of `pacto validate` now goes to stdout as one stream: the
error and warning lines moved there from stderr. The one-line failure summary
stays on stderr, and exit codes are unchanged.
