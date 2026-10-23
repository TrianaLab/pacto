# HANDOFF from democompose (wave 2)

1. Merge order: `docs/examples/compose-demo.md` links `kubernetes-demo.md`, the page the
   demok8s track writes (demos spec, section C). Until that page exists
   `make docs-build-strict` fails on this one link. Merge demok8s first, or with this branch.
2. Docs check (`release/scripts/docs_check.py`, not this track's file): the demos spec asks it
   to cover the `pacto diff` command on the Compose page. The Compose harness now runs both
   documented commands (`pacto diff` and `pacto init --plan`) against the running demo, so a
   docs_check entry is optional.
3. Not this track: `docs/mcp-integration.md` is 1243 prose words (max 1200) on this base, so
   `make docs-lint` fails on (c) before and after this branch.
4. Kind: journey I ("delivery stays out of sight") also runs in `operational-graph.sh browser`.
   It ran here only on Compose; the operational-graph Kind surface installs no delivery either,
   so it should hold there. Run it at integration.
5. Fixed in this track's files, caused by core: `pacto push` now reports an unchanged bundle as
   "Already published" with its digest, so the Compose seed thought a restart re-published and
   stage 10 of the Compose acceptance failed. `tests/acceptance/scenario/seed.sh` now reads that
   wording and checks the evidence digests on every start.
