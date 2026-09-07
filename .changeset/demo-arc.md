---
"@pacto/core": patch
---

A guided tour of what Pacto tells a human and an agent, and the five fixes
building it uncovered.

`docs/examples/demo-tour.md` walks a sixteen-service fleet in twelve beats: read
four compliance states at once, refuse a breaking change, find a consumer no
contract declares, reconcile declared against observed, then hand the same fleet
to an agent over MCP and watch it reach the same conclusions. Every command on
the page is real. Beats 1 to 10 are generated from a live run by
`make gen-demo-transcripts`, snippet-included rather than pasted, and compared
by the docs drift gate — so a page claiming output Pacto no longer produces
fails CI. Beats 11 and 12 are copied in by hand, because no generator covers a
server that holds stdin open, and the page says so. All twelve run in
`tests/acceptance/local/demo-arc.sh` on every pull request, and both readers
plus the generator share one argument table, so the page and the test can never
drift apart on what was actually run.

Building it against a fleet large enough to be interesting surfaced five bugs
that a three-service fixture never reaches:

- `pacto diff` iterated Go maps directly in eleven collections, so its change
  list came out in a different order each run: twenty runs of one diff produced
  six distinct byte sequences in text, six in JSON and five in Markdown. A tool
  whose output people paste into review comments and gate CI on cannot print
  different bytes each time. It is now byte-deterministic.
- `pacto impact` keyed the changed service by a ref classifier that disagreed
  with the one that actually loads the bundle: one treats a ref as local only on
  `.`, `/` or `~`, the other treats it as OCI only on `oci://`. A bare relative
  path loaded as a local bundle but was keyed under an invented OCI domain, so
  its identity never joined the fleet and every consumer silently vanished. The
  same two bundles found four consumers and exited 1 when passed as `./a ./b`,
  and reported none and exited 0 when passed as `a b`.
- `pacto impact` left completeness untouched when the changed service was missing
  from the graph, so a breaking change printed "No affected consumers.", nothing
  on stderr and exit 0 — a silent all-clear that actually meant "I could not
  tell". The answer is now marked partial and says so. The exit code is
  deliberately unchanged; that interacts with the documented declared-only
  contract and is filed separately.
- `pacto fleet` picked a service's owner by sorting revisions on `RevisionKey`,
  which is `ServiceKey@sha256:<content digest>` — digest order, not chronology.
  Editing any byte of any bundle could silently change who Pacto says to notify,
  which lands directly on the blast-radius answer. It now uses the semver-first
  revision chronology the package already documents for this exact hazard.
- `pacto mcp --fleet` read `--traces` but never registered it: the flag was
  persistent on the `fleet` parent, and `mcp` is not its child, so the lookup
  error was discarded and the snapshot got nil. An agent could never see
  `provenance=observed` — the observed edges that find undeclared consumers were
  absent from the agent-facing surface entirely.

One limitation is now documented rather than implied: only OpenAPI interfaces
are compared spec-against-spec. Rewriting an AsyncAPI or gRPC document behind an
unchanged ref produces no interface finding, because for those types only the
ref is diffed. `docs/contract-reference/diff.md` states this in the table
instead of leaving a reader to infer coverage Pacto does not have.

Two fixture versions moved: `payments-service` 1.2.0 and 2.0.0 are already
published to `ghcr.io/trianalab/pacto`, and the `capabilities[]` and `sbom/`
additions this tour needs change their bytes, so they ship as 1.2.1 and 2.0.1
and a published tag keeps meaning what it meant.

CI now runs on the demo. `examples/demo/**`, its transcript generator, the five
subsystems that produce the committed transcripts and the two those reach
through are in the docs-check path filter, the transcripts are
inside `generated_paths()` so the drift gate covers them, and an MCP integration
test drives the demo fleet over a real stdio session — including the case where
`pacto_fleet_status` called with no arguments returns a null item list, which an
agent asking the obvious way reads as a clean bill of health. That is pinned as
the trap it is, so it cannot change quietly before it is fixed.
