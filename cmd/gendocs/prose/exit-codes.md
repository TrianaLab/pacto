## Exit codes

Pacto exits `0` or `1` and nothing else. `0` means the command produced its
result and the result was acceptable; `1` means anything else. Because there is
no third code, **the exit status alone never distinguishes "the check failed"
from "the command could not run"**.

| Exits `1` | Exits `0` |
| --- | --- |
| `pacto diff` where the overall classification is `BREAKING` | a `POTENTIAL_BREAKING` or `NON_BREAKING` diff |
| `pacto validate` reporting at least one error | validation that reported only warnings |
| `pacto lock --check` on a stale lockfile, or with none at all (`LOCK_MISSING`) | a lockfile that is up to date |
| **any command that could not run** — an unresolvable reference, an unreadable bundle, an unknown flag | `pacto impact` run on declared-only sources, *whatever* it classifies (see below) |

Two rows deserve emphasis, because both silently break a CI gate:

- A `pacto diff` against a reference that does not exist exits `1` exactly like a
  breaking change does.
- `pacto impact` exits non-zero only when the change is breaking **and** a live
  source shows an incompatible consumer. With declared-only sources there are no
  consumers to find, so it prints `Classification: BREAKING` and still exits `0`.

**Use stdout, not the exit code, to tell a verdict from a failure.** A command
that produced a verdict writes it to stdout and puts a one-line summary on
stderr. A command that could not run writes *nothing* to stdout:

| Invocation | Exit | stdout | stderr |
| --- | --- | --- | --- |
| `pacto diff --output-format json OLD NEW` (breaking) | `1` | the full JSON result | `breaking changes detected` |
| `pacto diff --output-format json oci://…/absent:1.0.0 NEW` | `1` | *empty* | `old contract: artifact not found: …` |

So the rule for a script is: branch on stdout when it is non-empty, and treat an
empty stdout with a non-zero exit as a tool failure rather than a verdict.

!!! warning "JSON key casing is not uniform"
    `pacto validate` emits `Path`, `Valid` and `Errors` in PascalCase; every other
    command emits camelCase (`oldPath`, `classification`). One parser will not read
    both without allowing for it.

---

