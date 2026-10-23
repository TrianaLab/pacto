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
