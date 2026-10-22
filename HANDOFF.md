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
  promoter Deployment; regenerate the chart README with the repo target.
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

## 2. Optional: the operator re-evaluates when kernel evidence changes (operator owner)

**What.** `integrations/kubernetes/internal/controller/pacto_controller.go`:
enqueue the Pactos a `NodeRuntimeObservation` covers when it changes.

**Why.** The pacto.runtime claim gate passes only when the Pacto's evaluation
shows complete kernel coverage, and a new pod's coverage is only re-read on the
Pacto's check interval (at least 30s, the CRD minimum). Every phase 2
verification waits for that tick; phase 2 is the longest phase of the parallel
run. Not needed for the 15-minute target once request 1 lands.
