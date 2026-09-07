---
"@pacto/core": patch
---

A guided tour of what Pacto tells a human and an agent, and the five fixes
building it uncovered.

`docs/examples/demo-tour.md` walks a sixteen-service fleet as six user stories,
in the order the questions arrive: I inherited this fleet, something says
Unknown, I am about to ship a break, who do I have to tell, my diagram and my
traffic disagree, and I want an agent on this without handing it write access.
Every command on the page is real. The terminal transcripts are generated from a
live run by `make gen-demo-transcripts`, snippet-included rather than pasted, and
compared by the docs drift gate — so a page claiming output Pacto no longer
produces fails CI. The two commands no generator can cover — an MCP server
holding stdin open and a live dashboard — are copied in by hand, and the page
says so. Every one of them runs in `tests/acceptance/local/demo-arc.sh` on each
pull request, and both readers plus the generator share one argument table, so
the page and the test can never drift apart on what was actually run. A release
test asserts the two sets cover each other exactly: nothing runs in CI untaught,
and nothing on the page includes a transcript that does not exist.

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

The limitation that made the tour honest is now gone: AsyncAPI and gRPC
interfaces are compared spec-against-spec, not just by ref. AsyncAPI channels
and operations are deep-diffed so a payload property or a `required` entry
surfaces on its own, and a `.proto` is scanned for services, rpcs, messages and
fields. The proto scan is a text scan, not a compile, so a single pre-scan
blanks comments and string-literal contents before anything is matched — a `//`
or a `}` inside a string neither truncates a line nor closes a block early — and
a field's inline `[...]` option block is parsed off and dropped, so adding
`[deprecated = true]` is not a change while a retype behind one still is.
`docs/contract-reference/diff.md` states what each comparison does and, in "What
the proto comparison does not do", what it does not.

The in-browser dashboard demo now teaches itself. Where the fixture notice was a
banner and nothing more, it offers a guided tour: six steps, each naming one
thing to do and each gated on the dashboard actually reaching the state that
proves it was done. The gate is an observation, so no step is ever pressed to
confirm a thing the tour just watched happen — it detects the action, holds the
verdict until the gate has been quiet long enough that a search typed one
character at a time is never interrupted mid-word, then says so.

What follows depends on where the answer is. Where the result is read on the
next screen, the tour moves itself after a moment's confirmation. Where the
result IS the answer — the service in full, the field-by-field comparison that
says Breaking, the neighborhood a change reaches — the next step navigates away
from it, so those hold: the confirmation hands over a Continue the reader
presses once they have finished reading, and the spotlight moves off the control
onto what it produced. Showing someone a breaking change and taking it away a
second later is the one outcome worse than making them press a button. The two
steps with nothing to observe keep a real button throughout. Every gated step
carries Skip, which performs the action for the reader rather than jumping past
it — on a holding step that means they still get to read the result — and Exit
leaves at any point. It is opt-in, and it ships only with the WebAssembly demo:
`examples/demo/boot.js` is loaded by nothing else, so it cannot appear in front
of a real fleet.

The image tags a reader copy-pastes no longer age. The Compose demo page named
`ghcr.io/trianalab/pacto/demo:3.2.7`, the dashboard page named its image five
times and the operator's install page quoted a startup log naming
`dashboard:3.2.1` — three releases behind — and nothing compared any of them to
what was published. `apply-release-plan` now rewrites those coordinates the way
it already rewrote the chart's `--version` pin, so the Version PR carries the
new tag instead of leaving a command that resolves to nothing, and the
idempotency proof covers the pages. That list is not the guarantee: the docs
gate holds every page on the site to the tag its release unit published, so a
page pinning a coordinate the rewriter has never heard of fails CI rather than
rotting. Both pages now say the tag is the current release and that swapping it
is how you run an older one, instead of repeating the number in prose where
nothing can check it.

The published `pacto-dashboard` bundle described half of its own API. The
OpenAPI document it shipped was maintained by hand and had fallen to 18 paths
against the 32 the server registers, so a consumer reading the contract could
not see the fleet endpoints at all. `make gen-openapi` now generates it from the
live Huma registrations, which makes the published bundle byte-identical to the
drift-gated SDK contract instead of a second copy that ages on its own.

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
