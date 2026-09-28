# Fleet tools

The three surfaces that read the fleet: the dashboard, `pacto fleet` from a
terminal or CI job and the terminal UI. The contract-to-infrastructure side is
in [For platform engineers](platform-engineers.md).

## Dashboard

`pacto dashboard` launches the operational dashboard over the same contracts the
CLI manages and the operator verifies, organised around four workflows: what
needs attention right now, the service inventory, the
[Operational Graph](operational-graph.md) and change analysis. Sources are
auto-detected at startup and merged per service. Running alongside the Kubernetes
operator therefore gives the full contract experience — version history,
interface details, configuration schemas and diffs — with no explicit OCI
arguments. The dashboard discovers repositories from the `resolvedRef` fields in
Pacto CRD statuses. See the [`pacto dashboard`
reference](cli-reference.md#pacto-dashboard) for its flags and environment
variables.

## Fleet queries without a browser

`pacto fleet` answers the same questions from a terminal or a CI job: it builds
one snapshot from the sources you name, then queries it with `search`, `get`,
`graph`, `status` and `explain`.

```bash
$ pacto fleet search --local ./contracts --oci ghcr.io/acme/unreachable-pacto:1.0.0
1 of 1 service(s):
  payments-api                 NotEvaluated owner=acme/payments  revs=1 targets=0
warning: answer is partial (as of 2026-08-23T01:30:40+02:00)
  - [SOURCE_PARTIAL] source oci returned a partial result
  - [SOURCE_RECORD_INVALID] ref ghcr.io/acme/unreachable-pacto:1.0.0 could not be resolved: artifact not found: ghcr.io/acme/unreachable-pacto:1.0.0
```

**Read the completeness before you read the rows.** An unreachable registry is
reported rather than quietly dropped, so a service missing from a `partial`
answer may be one the missing source knew about. Likewise `1 of 1` is *this
page* of `total` matches, not the whole fleet. `--output-format json` carries the
same facts in a `meta` envelope for a CI job to branch on, and
[query semantics](operational-graph.md#query-semantics) has the five operations
and the [knowledge vocabulary](operational-graph.md#knowledge) behind
`completeness`.

## The terminal UI

`pacto tui` is the dashboard's terminal equivalent, built over the snapshot
`pacto fleet` builds and taking the same source flags. It loads that snapshot
once and opens on the Services tab. From there you move through services,
revisions, targets, owners and sources, with whatever row is highlighted standing
in as the argument, so you never type a path. It needs an interactive terminal —
in a pipeline, use the plain commands.

One difference from the dashboard matters more than the rest: **the dashboard
only reads, the TUI writes.** Read verbs run in-process against the loaded
snapshot. Write verbs shell out to this same binary so they own the terminal.
Each one names what it is about to change before it waits for a `y`: the
directory a pull will overwrite, the resolved plugin binary, the output directory
a generate will write. Pass `--read-only` and the four write verbs are absent
from the in-TUI help screen rather than refused at the last moment.

```bash
# whatever bundles are under the current directory
pacto tui

# a populated screen instead of an empty one: the committed demo fixture
pacto tui --local examples/demo/bundles \
  --target-state examples/demo/fleet-targets.yaml \
  --traces examples/demo/traces.json --freshness 24h

# the same demo with no clone at all, straight out of the registry
pacto tui --root oci://ghcr.io/trianalab/pacto/pacto-demo:1.0.0 --local ""
```

The second command needs a clone and a paste from the repository root; it opens
on 16 services and 4 operational targets. The third needs nothing on disk:
`--root` follows the published contract's dependency declarations and pulls 12 of
the demo's 16 services out of `ghcr.io`, and `--local ""` switches off the default
directory scan. What a registry cannot supply is where anything runs, so every
service reads `NotEvaluated` until you add the `--target-state` fixture.

### Keys

| Key | Action |
|-----|--------|
| `tab` / `shift+tab` | cycle the tabs: kinds on the list, categories under `a`, direction on a graph |
| `/` | filter; `enter` applies it, `esc` discards what you typed |
| `enter` | open the highlighted row |
| `a` | what needs attention |
| `+` / `-` | graph depth |
| `?` | toggle the key table for the current screen |
| `r` | reload the snapshot |
| `q` | back, or quit from the root screen |
| `esc` | clear an applied filter, otherwise the same as `q` |
| `y` | copy the equivalent `pacto` command |
| `v` | validate the selected bundle |
| `E` / `e` | explain the bundle, or explain it from the fleet's point of view |
| `l` | check the lock file |
| `d` / `i` | diff or impact between two selections, pressed twice |
| `g` | the selection's neighborhood graph |
| `p` / `P` | push, or pull (writes; asks first) |
| `L` | rewrite the lock file (writes; asks first) |
| `G` | run a generate plugin (writes; asks first) |

`y` is the escape hatch. For anything the TUI does not offer, it puts the command
you would have typed on the clipboard. The line is shell-quoted, so a value
carrying a space or a semicolon still pastes as one argument. With no clipboard to write to — over
`ssh`, on a headless box — the line is printed instead, alongside the reason it
could not be copied. See the [`pacto tui`
reference](cli-reference.md#pacto-tui) for every flag.
