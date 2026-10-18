# Run a command under Pacto

`pacto run` starts your command, watches its network activity while it runs and
tells you whether it kept its contract. It is the quickest way to try
[runtime conformance](runtime-conformance.md): one command, no daemon, no
cluster. Use it on a laptop, in a CI job or in front of any process you start
yourself.

## Quick start

```bash
pacto run --contract ./payments \
  --bind api=8080 \
  --dep postgres=127.0.0.1:5432 \
  --fail-on noncompliant \
  -- ./payments-server --port 8080
```

What each part means:

- `--contract` is the contract: a bundle directory or an `oci://` reference.
- `--bind api=8080` says the contract's interface `api` is served on port 8080.
  Bind every interface the contract declares; an unbound one reads "don't know".
- `--dep postgres=127.0.0.1:5432` says the contract's dependency `postgres` is
  reached at that address. A host name is resolved at every check.
- `--fail-on noncompliant` turns a broken contract into a failed run.
- Everything after `--` is your command, untouched.

A name in `--bind` or `--dep` that the contract does not declare is rejected
before the command starts, so a typo cannot hide an unchecked interface.

Every 10 seconds (`--interval`) Pacto prints a round to stderr. When the command
exits it prints a final round, judged from the last view taken while the command
was still running:

```text
2026-10-18T09:12:44Z  ebpf  final
session/pacto-run-4121  Compliant  2/2 evaluated  ./payments
```

`2/2 evaluated` means both checks the contract requires got an answer. A broken
check adds a line per finding, such as
`- [error] INTERFACE_ABSENT interface/api: interface "api" is not being served (pacto-run+ebpf)`.
The part in brackets at the end names who saw it.

## Use it in CI

Run your integration tests against the service while `pacto run` wraps it. The
job fails if the service broke its contract:

```bash
pacto run --contract ./payments --bind api=8080 --dep postgres=db:5432 \
  --stabilization-window 10s --interval 5s --fail-on noncompliant \
  --output-format json --report pacto-rounds.jsonl \
  -- ./payments-server &
PACTO=$!
./run-integration-tests.sh
kill -TERM "$PACTO"   # passed on to the server; Pacto prints its final round
wait "$PACTO"
```

`wait` returns the server's exit code, so a server that dies on `SIGTERM`
returns 143 and fails the job on its own. Make it exit 0 on `SIGTERM`. Two
settings matter in CI:

- **`--stabilization-window`.** A negative is reported only once it has lasted
  this long (two minutes by default). Keep it shorter than the job, or a
  missing listener never gets reported.
- **`--fail-on`.** `noncompliant` fails the run when the contract is broken;
  `unknown` also fails it when Pacto could not decide. The default `none` never
  changes the exit code.

## What you see with and without privileges

Pacto picks the best mode the machine allows and says which one on stderr
before the command starts.

**With privileges** (root, or the `BPF`, `PERFMON` and `SYS_PTRACE` capabilities
plus write access to its own cgroup, on Linux with BTF and cgroup v2), the
command starts in its own cgroup `pacto-run-<pid>` and an eBPF program sees
every listener and every connection attempt of the command and its children,
failures included. Findings cite the collector `pacto-run+ebpf`. In a container
this needs `--privileged` and its own cgroup namespace.

**Without privileges**, Pacto falls back to reading the command's sockets from
`/proc` once a second and says so:

```text
pacto run: unprivileged mode (eBPF: ...): polling the process tree's sockets in /proc every 1s. ...
```

The fallback sees listeners in full, so `INTERFACE_ABSENT` is still reported. It
sees a connection only while it is open at a poll and never sees one fail, so a
dependency is reported reachable or "don't know", never unreachable. Findings
cite the collector `pacto-run`.

**Outside Linux** (macOS, Windows), the command runs unobserved and every
runtime check reads `OBSERVATION_UNSUPPORTED`. Run it inside a Linux container
to get real results.

| | eBPF mode | Unprivileged mode |
| --- | --- | --- |
| Missing listener (`INTERFACE_ABSENT`) | reported | reported |
| Reached dependency | reachable | reachable if the connection is open at a poll |
| Refused dependency (`DEPENDENCY_UNREACHABLE`) | reported | never: reads "don't know" |
| A child that leaves the process tree | still watched: it stays in the cgroup | still watched: Pacto adopts orphaned children |

## Exit codes

| Exit code | When |
| --- | --- |
| the command's own | the command exited; `--fail-on` did not trigger |
| `128 + n` | the command was killed by signal `n`, as a shell reports it |
| `1` | the command exited 0, but the final verdict met `--fail-on` |
| `1` | Pacto could not start: a bad flag, a contract that does not load or a binding the contract does not declare |

`SIGINT` and `SIGTERM` are passed on to the command; Pacto waits for it and
then prints the final round.

## Keep the rounds: `--report`

Your command keeps its own stdin, stdout and stderr. Pacto writes rounds to
stderr, each in a single write so they never interleave with the command's
lines. With `--report <file>` the rounds go to that file instead. The file is
emptied when the run starts and every round is appended whole, so a reader
following it never sees half a round. With `--output-format json` each round is
one JSON object per line:

```json
{"evaluatedAt":"2026-10-18T09:12:44Z","mode":"ebpf","final":true,"targets":[{"target":"session/pacto-run-4121","contract":"./payments","status":"Compliant","coverage":{"evaluated":2,"required":2}}]}
```

The round with `"final": true` is the verdict. `--output-format markdown` writes
a heading and a bullet per finding, handy for a CI job summary.

## Send the result to your platform

Add the `--evidence-*` flags and every round is also signed and sent to an
[Evidence Server](evidence.md). The server stores evidence only about exact
contract revisions, so `--contract` must be pinned by digest:

```bash
pacto run --contract oci://ghcr.io/acme/payments@sha256:4f1c... --bind api=8080 \
  --evidence-url https://evidence.example.com --evidence-key ci.key \
  --evidence-key-id ci-2026 --evidence-producer ci-payments \
  -- ./payments-server
```

[Evidence](evidence.md) explains the keys and how the server trusts them.

## Other flags

- `--host` (default `127.0.0.1`) is the address health, metrics and protocol
  probes dial, at the bound interface's port.
- `--enable-probing` also checks that each listening interface speaks the
  protocol its type implies (HTTP for `openapi`, HTTP/2 for `grpc`).

The [CLI reference](cli-reference.md#pacto-run) lists every flag.
