# HANDOFF requests from the kindfast track

## 1. Promoter login freshness as a setting (logic track) — needed for the 15-minute target

**What.** Make the promoter's login freshness a setting instead of the constant
`delivery.AuthFreshness` (15m):

- `integrations/kubernetes/cmd/delivery/main.go`: a promoter flag
  `--auth-freshness` (a Go duration, default `15m`), passed to the
  `OperationReconciler`.
- `integrations/kubernetes/internal/delivery/operation.go`: `fresh()` compares
  against that value instead of `delivery.AuthFreshness`.
- `integrations/kubernetes/charts/pacto-delivery/values.yaml`: a value
  **`promoter.authFreshness`** (written exactly as `  authFreshness: 15m` under
  `promoter:`, two spaces of indent), rendered as `--auth-freshness` in the
  promoter Deployment, and allowed in `values.schema.json` (a Go duration
  string, like `promoter.poll`; without it helm refuses the value); regenerate
  the chart README with the repo target.
- Optional, same change: the dashboard's `DecisionFreshness` (10m) stays a
  margin under it, so a smaller setting does not let the dashboard offer a
  decision the promoter then ignores.

**Why.** Phase 4 proves the promoter ignores an approval on a login older than
its freshness, so one real Dex login has to get older than 15 minutes. That
wait is now the floor of the whole run: the parallel run finishes every other
phase well before it and then sleeps. `tests/acceptance/kind/delivery.sh`
already looks for the value (`helm show values ... | grep '^  authFreshness:'`)
and, when it is there, sets it to 3m and waits 4 minutes instead of 16. Nothing
else in the kind scripts needs to change.

**Measured (2026-10-23, full runs, all DELIVERY PASS).** Sequential 1399s,
parallel 1137s (identity: 645s of its 704s is the stale-login sleep). The same
parallel run with a minimal version of this change applied in a scratch tree
(flag, `Freshness` field on `OperationReconciler`, `fresh()` taking the
maximum, the value, the schema entry and the template line; nothing else)
took 899s, of which 254s was a cold image build because the Go code changed;
with a warm build cache the setup is about 140s, so about 11 minutes. The
real change also needs the existing `fresh` tests updated, the README
regenerated and, where the zero value can reach it, a default of 15m.

## 2. Optional: the operator re-evaluates when kernel evidence changes (operator owner)

**What.** `integrations/kubernetes/internal/controller/pacto_controller.go`:
enqueue the Pactos a `NodeRuntimeObservation` covers when it changes.

**Why.** The pacto.runtime claim gate passes only when the Pacto's evaluation
shows complete kernel coverage, and a new pod's coverage is only re-read on the
Pacto's check interval (at least 30s, the CRD minimum). Every phase 2
verification waits for that tick; phase 2 is the longest phase of the parallel
run. Not needed for the 15-minute target once request 1 lands.

## 3. For review: the test-mode timings this track changed, and the baseline

**Baseline.** The "before" time is **1834s**, from a full run of commit
68ecbcf0 (the base branch plus wait timing only; no overlap, no parallel
phases, old timings). The earlier figure of 1719s had no log behind it and is
withdrawn. Logs, kept outside git under the main checkout's gitignored
`docs/superpowers/logs/`: `kindfast-baseline-68ecbcf0-1834s.log`,
`kindfast-proof-sequential.log` (1399s), `kindfast-proof-parallel.log`
(1137s) and `kindfast-proof-summary.log`. The 1399s sequential run is not a
"before": it already has the shorter timings below and the setup overlap.

**Timings changed in `tests/acceptance/kind/delivery.sh`** (test mode only; no
check was removed):

| Setting | Old | New | Why it still proves the same thing |
|---|---|---|---|
| Candidate OCIRepository `interval` (every stage) | 10s or 5s | 2s (`CANDIDATE_INTERVAL`) | Only how soon Flux sees a new tag or version; every phase still waits for it to be seen. |
| `always N never_retried` | 10 (30s) | 6 (18s) | The watch spans 9 candidate scans instead of 3 and 9 promoter polls (2s) instead of 15, so a retry or a prod promotion still has many chances to show up. |
| `always N held_back` | 10 (30s) | 6 (18s) | Same: 9 scans and 9 polls in which automation could promote F again. |
| Health `window` | 2m | 1m | Q is still held for a person only when the window ends without enough spans; the end just comes sooner. |
| Health `recheck` | 10s | 5s | H5 and R are still caught by a re-check after they shipped; it runs twice as often. |
| Mira check `soak` | 20s | 10s | `minSpans: 20` and `maxErrorRatio: 0.2` are unchanged, so a version passes only on the same evidence; the soak only sets how long it collects before deciding. |
| `eventually` poll (`KIND_POLL`) | 3s | 1s | Every wait still makes at least ROUNDS tries and sleeps at least ROUNDS x 3s between them (lib.sh, tested in `tests/architecture/kind_eventually_test.go`), so no wait gives up sooner than before. |
