---
"@pacto/core": minor
---

`pacto validate` reports one PASS, FAIL or SKIP line per stage (load, lock,
structural, cross-field, policy, guarantees, readiness and closure), lists each error
under its stage with its file:line:column when it can be located (never
with `--set` or `-f`, which validate a merged document), and ends
with a `Next:` line naming the one command to run now. A failed
load or lock stage skips every stage after it, and a failed structural or
cross-field stage skips the stages after it up to policy. The guarantees,
readiness and closure stages run even when an earlier stage fails. The JSON output adds `stages`, `next`, `file` and each error's
`line` and `column`, and `pacto_check` returns the same `stages`, `next` and
locations. An unknown field or a mistyped enum value carries a `did you mean`
hint naming the closest valid one.

The text output of `pacto validate` now goes to stdout as one stream: the
error and warning lines moved there from stderr. The one-line failure summary
stays on stderr, and exit codes are unchanged.
