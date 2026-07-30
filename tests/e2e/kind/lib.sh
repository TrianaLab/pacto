#!/usr/bin/env bash
# Shared helpers for the kind e2e scripts (run.sh, evidence.sh,
# dashboard-modes.sh, v4-to-v5-upgrade.sh). Sourced, never executed directly.
#
#   dump_diag NS
#     Best-effort cluster diagnostics for a failed run. Every command is guarded
#     with `|| true` so the dump NEVER changes the caller's exit code — it runs
#     inside an EXIT trap that must preserve the real failure code.
#
#   keep_or_teardown NS CLUSTER [TEARDOWN_FN]
#     Tear the run down UNLESS KEEP_E2E_CLUSTER is set/non-empty, in which case
#     the deployed state is left in place for interactive inspection:
#         KEEP_E2E_CLUSTER=1 bash tests/e2e/kind/<script>.sh
#     Default (unset/empty) tears down, so CI never leaks state. TEARDOWN_FN is
#     the caller's own cleanup function; if omitted a generic helm-uninstall +
#     delete-namespace is used. NS/CLUSTER are arguments (they differ per script:
#     pacto-system, pacto-modes, pacto-upgrade) — nothing is hardcoded here.

# The pacto components a script may deploy; logs are dumped for whichever exist.
_PACTO_DEPLOYS=(pacto-operator pacto-dashboard pacto-evidence)

dump_diag() {
  local ns="${1:-}" d
  echo "== DIAGNOSTICS (namespace: ${ns:-<none>}) =="
  echo "--- pods (all namespaces) ---"
  kubectl get pods -A || true
  echo "--- events ($ns, by time) ---"
  kubectl get events -n "$ns" --sort-by=.lastTimestamp || true
  echo "--- deployments ($ns) ---"
  kubectl describe deploy -n "$ns" || true
  for d in "${_PACTO_DEPLOYS[@]}"; do
    echo "--- logs deploy/$d ($ns) ---"
    kubectl logs -n "$ns" "deploy/$d" --all-containers --tail=200 || true
    echo "--- logs deploy/$d ($ns, previous) ---"
    kubectl logs -n "$ns" "deploy/$d" --all-containers --previous --tail=200 || true
  done
  echo "--- pvc ($ns) ---"
  kubectl get pvc -n "$ns" -o wide || true
  # Evidence-specific diagnostics (best-effort; only when the component exists):
  # readiness/health and a durable-store inspection make an ingestion failure
  # diagnosable without leaking secrets.
  if kubectl -n "$ns" get deploy pacto-evidence >/dev/null 2>&1; then
    echo "--- evidence readiness/health ($ns) ---"
    kubectl -n "$ns" exec deploy/pacto-evidence -- sh -c 'wget -qO- http://127.0.0.1:8686/api/evidence/v1/ready; echo; wget -qO- http://127.0.0.1:8686/api/evidence/v1/health; echo' 2>/dev/null || true
    echo "--- evidence store inspection ($ns) ---"
    kubectl -n "$ns" exec deploy/pacto-evidence -- pacto evidence inspect --bucket-url file:///var/lib/pacto/evidence 2>/dev/null | head -30 || true
  fi
}

keep_or_teardown() {
  local ns="${1:-}" cluster="${2:-}" fn="${3:-}"
  if [ -n "${KEEP_E2E_CLUSTER:-}" ]; then
    echo "== KEEP_E2E_CLUSTER set — leaving namespace '$ns' in kind cluster '$cluster' for inspection (KUBECONFIG=${KUBECONFIG:-unset}) =="
    return 0
  fi
  if [ -n "$fn" ]; then
    "$fn"
  else
    helm uninstall pacto-operator -n "$ns" --wait >/dev/null 2>&1 || true
    kubectl delete ns "$ns" --wait=false >/dev/null 2>&1 || true
  fi
}
