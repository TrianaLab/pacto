# HANDOFF from the identity track (wave 2)

1. Committed UI bundle: `pkg/dashboard/ui` is rebuilt here (cold `make ui-build`). Any other
   wave 2 track that touches the frontend (demobrowser) will conflict on hashed assets. Resolve
   by taking either side and re-running a cold `make ui-build` after the merge, then commit.
2. New frontend rule for every track: a label is the label role. Use `class="t-label"`, or on a
   scoped element `font: var(--role-label-font); font-variant-caps: var(--role-label-caps);
   letter-spacing: var(--role-label-tracking)`. Never `text-transform: uppercase`; the browser
   test "the signed-document identity paints on every kind of page" (e2e/typography.spec.ts)
   fails on visible uppercase text.
3. Not from this track: `tests/architecture` `TestDeliveryPhaseFailReadsInCI` failed once
   under a full parallel `go test ./tests/architecture/...` ("CI would report \"\", want
   \"[health] boom\"") and passed alone with `-count=1`. Looks load-sensitive (kindfast's
   run_phases output capture); owner kindfast or ci.
4. The Source Serif 4 files are the unmodified Adobe 4.005 release (OFL reserves the name
   "Source" for unmodified files, so do not subset or rename them). The licence ships in the
   bundle as `licenses/source-serif-4-OFL.md` (from `pkg/dashboard/frontend/public/`).
