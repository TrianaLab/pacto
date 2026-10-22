#!/usr/bin/env bash
# kind-diagnostics.sh OUTDIR — collect what a failed kind run leaves behind, for
# the artifact upload: for every kind cluster, `kind export logs` (every
# container's log, controllers included, and the node's journal), events,
# `kubectl describe` of pods and Deployments, and every resource; plus the
# containers a scenario ran beside the cluster (identity providers; the harness
# keeps them, and the failed cluster state, when a run fails in CI). Best effort
# and bounded: a step that hangs or fails is skipped, never fatal.
set -uo pipefail
out="$1"
mkdir -p "$out"
t() { timeout 120 "$@"; }
docker ps -a > "$out/docker-ps.txt" 2>&1
for c in $(docker ps -a --format '{{.Names}}' | grep '^pacto-e2e-'); do
  t docker logs "$c" > "$out/container-$c.log" 2>&1
done
for cluster in $(kind get clusters 2>/dev/null); do
  d="$out/$cluster"
  mkdir -p "$d"
  t kind export logs "$d/kind-logs" --name "$cluster" >/dev/null 2>&1
  kc="$(mktemp)"
  kind get kubeconfig --name "$cluster" > "$kc" 2>/dev/null || continue
  k() { t kubectl --kubeconfig "$kc" "$@"; }
  k get events -A --sort-by=.lastTimestamp > "$d/events.txt" 2>&1
  k describe pods -A > "$d/describe-pods.txt" 2>&1
  k describe deployments,daemonsets -A > "$d/describe-workloads.txt" 2>&1
  k get all -A -o wide > "$d/all.txt" 2>&1
  # One call per type: a combined `get` fails whole on a cluster without one of
  # the CRDs (most scenarios install neither delivery nor Flux).
  : > "$d/custom-resources.yaml"
  for r in pactos pactorevisions noderuntimeobservations stages operations approvals ocirepositories kustomizations; do
    out_r="$(k get "$r" -A -o yaml 2>/dev/null)" && printf -- '---\n# %s\n%s\n' "$r" "$out_r" >> "$d/custom-resources.yaml"
  done
done
exit 0
