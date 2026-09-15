# Fleet tools

The three surfaces that read the fleet: the dashboard, `pacto fleet` from a
terminal or CI job, and the terminal UI. The contract-to-infrastructure side is
in [For platform engineers](platform-engineers.md).

## Dashboard

`pacto dashboard` launches the operational dashboard — the same contracts the CLI manages and the operator verifies, organised around four workflows:

- **Overview** — what needs attention right now, and how complete the data behind that answer is
- **Services** — the inventory, with interfaces, configuration schemas and policy references per service
- **Operational Graph** — dependency chains and where each revision actually runs, declared against observed
- **Change analysis** — what changed between two revisions, and what that change affects

Sources (local, Kubernetes, OCI) are auto-detected at startup and merged per service. The platform-relevant behavior: when running alongside the Kubernetes operator, the dashboard auto-discovers OCI repositories from the `resolvedRef` fields in Pacto CRD statuses, so a Kubernetes deployment gives the full contract experience — version history, interface details, configuration schemas and diffs — without explicit OCI arguments.

See [Dashboard architecture](dashboard-architecture.md) for the source model, merge priority, graph edges and version-tracking rules, and the [`pacto dashboard` command reference](cli-reference.md#pacto-dashboard) for its flags (`--host`, `--port`, `--namespace`, `--cors-origin`, `--traces`, `--trace-source`) and environment variables. `--no-cache` works here too but is a global flag, not one of the command's own. Pass OCI repositories as positional `oci://` arguments or via the `PACTO_DASHBOARD_REPO` env var.

### Feeding the Operational Graph observed dependencies

The Operational Graph compares declared dependencies against observed ones, and the observed half comes from **offline OTLP/JSON trace exports** — files, not a live feed. Pacto ships no OTLP receiver and deploys no collector; if you run a Collector, you own it, and you point Pacto at whatever file it exports.

Ad hoc, `pacto dashboard --trace-source orders=/path/traces.json` names a source explicitly (`--traces <file>` still works and names sources by position). For the operator-managed dashboard, declare them in Helm values instead:

```yaml
dashboard:
  observation:
    sources:
      - name: orders
        file: traces.json
        existingClaim: orders-trace-export
```

The operator mounts the claim read-only, reads only the file you declared, and exposes the source under the name you gave it. Storage lifecycle stays yours: Pacto reads, never writes, and never rotates.

The naming rules are load-bearing rather than cosmetic — `name` is the identity the API and UI use, it has to be unique against *every* other Data Source including `k8s`, `local` and `oci`, and a collision is refused before a snapshot is built. [Observation sources](observation-sources.md) states those rules, the read root each source is confined to, and why an unreadable source and a stale one are different answers; [Observed dependencies and reconciliation](operational-graph.md#observed-dependencies-and-reconciliation) is the model underneath.

---

## Fleet queries without a browser

`pacto fleet` answers the same questions the dashboard's Operational Graph view answers, from a terminal or a CI job. It builds one snapshot from the sources you name — `--local`, `--oci`, `--k8s`, `--evidence-url`, `--traces`, `--cache` — then queries it with `search`, `get`, `graph`, `status` and `explain`:

```bash
$ pacto fleet search --local ./contracts
1 of 1 service(s):
  payments-api                 NotEvaluated owner=acme/payments  revs=1 targets=0
```

`NotEvaluated` is not a failure here: nothing has told the fleet where this service runs, so there is no operational target to evaluate it against. Add `--k8s` or an Evidence Server and the same service gets a compliance state per target.

**Read the completeness before you read the rows.** A degraded source is reported, never quietly dropped:

```bash
$ pacto fleet search --local ./contracts --oci ghcr.io/acme/unreachable-pacto:1.0.0
1 of 1 service(s):
  payments-api                 NotEvaluated owner=acme/payments  revs=1 targets=0
warning: answer is partial (as of 2026-08-23T01:30:40+02:00)
  - [SOURCE_PARTIAL] source oci returned a partial result
  - [SOURCE_RECORD_INVALID] ref ghcr.io/acme/unreachable-pacto:1.0.0 could not be resolved: artifact not found: ghcr.io/acme/unreachable-pacto:1.0.0
```

An unreachable registry never becomes an empty result, so a service missing from a `partial` answer may be one the missing source knew about. The header is equally deliberate: `1 of 1` is *this page* of `total` matches — `search` returns 100 rows unless you raise `--limit` (500 is the cap), so a bounded page can never be mistaken for the whole fleet. `--output-format json` carries the same facts in a `meta` envelope — `completeness`, `limitations`, per-source status — for a CI job to branch on.

[Freshness and completeness](fleet-sources.md#freshness-and-completeness) has the full vocabulary, [query semantics](fleet-queries.md#query-semantics) the five operations, and the [`pacto fleet` reference](cli-reference.md#pacto-fleet) every flag.

---

## The terminal UI

`pacto tui` is the dashboard's terminal equivalent, built over the snapshot `pacto fleet` builds and taking the same source flags. It loads that snapshot once and opens on the Services tab, then lets you move through services, revisions, targets, owners and sources with whatever row is highlighted standing in as the argument, so you never type a path. One difference from the dashboard matters more than the rest: **the dashboard only reads, the TUI writes**. Read verbs run in-process against the loaded snapshot; write verbs shell out to this same binary so they own the terminal, and each one names what it is about to change before it waits for a `y` — the directory a pull will overwrite, the resolved plugin binary and the output directory a generate will write. Pass `--read-only` and the four write verbs are absent from the in-TUI help screen rather than refused at the last moment. It needs an interactive terminal — in a pipeline, use the plain commands.

```bash
# whatever bundles are under the current directory
pacto tui

# a populated screen instead of an empty one: the committed demo fixture
pacto tui --local examples/demo/bundles \
  --target-state examples/demo/fleet-targets.yaml \
  --traces examples/demo/traces.json --freshness 24h

# the same demo with no clone at all, straight out of the registry
pacto tui --root oci://ghcr.io/trianalab/pacto/pacto-demo:1.0.0 --local ""

# the same bundles, without the write verbs
pacto tui --local examples/demo/bundles --read-only
```

### Opening the demo fixture

`--local` defaults to the current directory, so the bare form needs no arguments — with no bundles under it, the first screen says the snapshot is empty and names every source it consulted. The fixture is committed to the repository, so the second command needs a clone (`git clone https://github.com/TrianaLab/pacto.git && cd pacto`) and a paste from the repository root; it opens on 16 services and 4 operational targets. Start it, then read the keys below with it open.

### Straight out of the registry

The third command needs nothing on disk. `--root` follows a published contract's dependency declarations rather than scanning a directory, so pointing it at the demo's root bundle pulls that whole closure out of `ghcr.io` — 12 of the demo's 16 services. The four it does not reach are the three shared `platform-*` bundles, which services declare as configuration and policy references rather than dependencies, and `audit-log`, which nothing depends on. `--local ""` switches off the default directory scan, so the screen holds the demo rather than the demo plus whatever your working directory happens to contain. What a registry cannot supply is where anything runs: targets come from the `--target-state` fixture in the repository, so every service reads `NotEvaluated`, and the attention tab holds the 6 contract-level findings without the compliance ones. Contracts and the graph come from the registry; the states need the fixture.

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

`y` is the escape hatch: for anything the TUI does not offer, it puts the command you would have typed on the clipboard, shell-quoted so a value carrying a space or a semicolon still pastes as one argument. When there is no clipboard to write to — over `ssh`, on a headless box — the line is printed instead, alongside the reason it could not be copied. See the [`pacto tui` reference](cli-reference.md#pacto-tui) for every flag.
