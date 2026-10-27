## Exit codes

Pacto exits `0` or `1`. `0` means the command produced its result and the
result was acceptable. `1` means anything else, so **the exit status alone
cannot distinguish "the check failed" from "the command could not run"**.

| Exits `1` | Exits `0` |
| --- | --- |
| `pacto diff` where the overall classification is `BREAKING` | a `POTENTIAL_BREAKING` or `NON_BREAKING` diff |
| `pacto validate` reporting at least one error | validation that reported only warnings |
| `pacto lock --check` on a stale lockfile, or with none at all (`LOCK_MISSING`) | a lockfile that is up to date |
| **any command that could not run**, for example on an unresolvable reference, an unreadable bundle or an unknown flag | `pacto impact` run on declared-only sources, *whatever* it classifies (see below) |

Two cases affect CI gates:

- A `pacto diff` against a missing reference exits `1`, the same as a breaking
  change.
- `pacto impact` exits `1` only when the change is breaking **and** a live
  source shows an incompatible consumer. Declared-only sources have no
  consumers, so it prints `Classification: BREAKING` and exits `0`.

**Use stdout to tell a verdict from a failure.** A command that produced a
verdict writes it to stdout and puts a one-line summary on stderr. A command
that could not run leaves stdout empty:

| Invocation | Exit | stdout | stderr |
| --- | --- | --- | --- |
| `pacto diff --output-format json OLD NEW` (breaking) | `1` | the full JSON result | `breaking changes detected` |
| `pacto diff --output-format json oci://…/absent:1.0.0 NEW` | `1` | *empty* | `old contract: artifact not found: …` |

In a script, branch on stdout when it is non-empty, and treat an empty stdout
with exit `1` as a tool failure.

---

