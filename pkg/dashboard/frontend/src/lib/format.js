/** Shared formatting/classification helpers used across views. */

export function phaseClass(phase) {
  if (phase === 'Healthy') return 'ok';
  if (phase === 'Degraded') return 'warn';
  if (phase === 'Invalid') return 'err';
  return 'neutral';
}

export function complianceClass(score) {
  if (score >= 80) return 'score-ok';
  if (score >= 50) return 'score-warn';
  return 'score-err';
}

export function complianceStatusClass(status) {
  if (status === 'OK') return 'score-ok';
  if (status === 'WARNING') return 'score-warn';
  if (status === 'ERROR') return 'score-err';
  return '';
}

export function methodClass(method) {
  const m = method?.toUpperCase();
  if (m === 'GET') return 'badge-ok';
  if (m === 'POST') return 'badge-info';
  if (m === 'PUT' || m === 'PATCH') return 'badge-warn';
  if (m === 'DELETE') return 'badge-err';
  return 'badge-neutral';
}

export function classificationClass(c) {
  if (c === 'BREAKING') return 'badge-err';
  if (c === 'POTENTIAL_BREAKING') return 'badge-warn';
  if (c === 'NON_BREAKING') return 'badge-ok';
  return 'badge-neutral';
}

export function changeTypeClass(t) {
  if (t === 'added') return 'diff-added';
  if (t === 'removed') return 'diff-removed';
  if (t === 'modified') return 'diff-modified';
  return '';
}

const SOURCE_DESCRIPTIONS = {
  k8s: 'Kubernetes — live cluster runtime data',
  oci: 'OCI Registry — versioned contract bundles',
  local: 'Local — contracts from local filesystem',
  cache: 'Cache — offline cached bundles',
};

export function sourceTooltip(src) {
  return SOURCE_DESCRIPTIONS[src] || src;
}
