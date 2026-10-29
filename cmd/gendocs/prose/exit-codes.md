## Exit codes

Pacto exits `0`, `1` or `2`, apart from `pacto run` (below). `0` means the
command produced its result and the result was acceptable. `1` means the check
failed or the command could not run, so **the exit status alone cannot
distinguish "the check failed" from "the command could not run"**. `2` means
`pacto impact` could not show that a breaking change is safe, and no other
command exits `2` for its own result. `pacto run` exits with the exit code of
the command it wraps, whatever it is, or `1` when that command exits `0` and
the final verdict fails `--fail-on`.

| Exits `1` | Exits `0` |
| --- | --- |
| `pacto diff` where the overall classification is `BREAKING` | a `POTENTIAL_BREAKING` or `NON_BREAKING` diff |
| `pacto validate` reporting at least one error | validation that reported only warnings |
| `pacto lock --check` on a stale lockfile, or with none at all (`LOCK_MISSING`) | a lockfile that is up to date |
| `pacto impact` where the gate is `blocked`: a `BREAKING` change reaches a consumer that does not accept the new version and runs on a known target | `pacto impact` where the gate is `clear`: the change is not `BREAKING`, or the snapshot is complete and no service depends on the changed one |
| **any command that could not run**, for example on an unresolvable reference, an unreadable bundle or an unknown flag | |

| Exits `2` |
| --- |
| `pacto impact` where the gate is `unverified`: a `BREAKING` change that nothing is shown to block, on a partial answer or with consumers none of which is shown to be safe |

Two cases affect CI gates:

- A `pacto diff` against a missing reference exits `1`, the same as a breaking
  change.
- `pacto impact` exits `1` only when the change is breaking **and** a source
  that knows where services run shows an incompatible consumer on a target.
  Sources that know no targets, such as `--local` alone, cannot show that, so
  a breaking change with consumers exits `2`. A job that gates on deployed
  impact alone can treat `2` as a pass; any other job fails on it.

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

