---
"@pacto/core": minor
---

Add `pacto tui`, a full-screen terminal front-end over the CLI.

`pacto dashboard` answers "what is the fleet doing" in a browser and only reads.
`pacto tui` answers it in the terminal you are already in, and it also writes —
because the point of a front-end over the CLI is that you do not have to leave it
to run the command. It takes the same source flags as `pacto fleet` and navigates
the same snapshot: services, revisions, targets, owners and sources, opening on
the Services tab. Whatever row is highlighted becomes the argument, so you never
type a path.

Read verbs run in-process against the loaded snapshot — validate, explain, fleet
explain, lock check, diff, impact and the neighborhood graph. Write verbs — push,
pull, lock update and generate — shell out to this same binary so they own the
terminal, and each names what it is about to change before it waits for a `y`:
the directory a pull will overwrite, the resolved plugin binary and the output
directory a generate will write. A successful write reloads the snapshot rather
than leaving a confidently stale list on screen. `--read-only` hides the four
write verbs entirely rather than refusing them at the last moment, and `y` copies
the equivalent `pacto` command — shell-quoted, so a contract value carrying a
space or a semicolon pastes as one argument — for anything the TUI does not
offer.

Building it made a gap in shell completion obvious: the closed vocabularies the
code already owned were never declared to cobra. Seven flags now complete from
their real source of truth — `--status` and `--compliance` from
`fleet.CanonicalStatuses()`, `--workload` from the `contract.Workload*` constants,
and `--direction`, `--ui`, `--transport` and `--output-format` from the values
their own validators accept — and the four `pacto fleet` positionals no longer
offer filenames for arguments that are never paths.

**One behaviour change comes with that, and a script can trip over it.** `pacto
doc`'s three mutual-exclusion checks are now
`cmd.MarkFlagsMutuallyExclusive("serve", "ui", "output")` instead of hand-rolled
value comparisons. Cobra tests whether a flag was *set*, not what it was set to,
so all three of these now error where they used to be accepted:

```
pacto doc --serve=false -o out.md
pacto doc --ui= -o /tmp/o.md .
pacto doc -o "" --serve .
```

Nothing in the repo relied on any of those spellings. The one check cobra has no
primitive for — `--interface` requires `--ui` — stays hand-rolled. `--ui`'s help
string now names the closed set it enforces instead of advertising an open one.
