# HANDOFF from the demobrowser track (wave 2)

1. Frontend seams outside `examples/demo` (identity, integrator): `src/lib/delivery.ts`
   (`signIn`, `hostSignsIn`), `src/components/DecisionPanel.svelte` (sign in again through the
   host, keep the choice in the page), `src/components/DeliveryNav.svelte` ("Switch person" when
   the host signs in) and `src/App.svelte` (`pacto:refresh` reloads the session and the views).
   The committed UI bundle was rebuilt cold with `make ui-build`; if it conflicts with the
   identity track's bundle, regenerate it after the merge rather than hand-merging.
2. docs (after demok8s merges): `docs/examples/dashboard-demo.md` names "delivery" on a cluster
   via `integrations/kubernetes/delivery.md`; link `examples/kubernetes-demo.md` there too. The
   tour's last step already links `../examples/kubernetes-demo/`, which demok8s creates.
3. Moment order (wave 0 item 1 / wave 1 item 7): the browser tour plays beat order (pactify, safe
   change, bad release, breaking change, signed records) and does not number the moments, so it
   is right whichever way the ledger list is renumbered.
4. Local gate noise, not from this track: `docs_lint.py` (a) flags the untracked
   `WAVE2-NOTES.md` and (b) Vale fails to start on this machine (a C++ header error); (c) still
   reports `docs/mcp-integration.md` at 1243 words, as on the integration branch.
