/** Shared formatting/classification helpers used across views. */

export function statusClass(status: string | undefined): string {
  if (status === 'Compliant') return 'ok';
  if (status === 'Warning') return 'warn';
  if (status === 'NonCompliant') return 'err';
  return 'neutral';
}

export function complianceClass(score: number): string {
  if (score >= 80) return 'score-ok';
  if (score >= 50) return 'score-warn';
  return 'score-err';
}

export function complianceStatusClass(status: string): string {
  if (status === 'OK') return 'score-ok';
  if (status === 'WARNING') return 'score-warn';
  if (status === 'ERROR') return 'score-err';
  return '';
}

export function methodClass(method: string | null | undefined): string {
  const m = method?.toUpperCase();
  if (m === 'GET') return 'badge-ok';
  if (m === 'POST') return 'badge-info';
  if (m === 'PUT' || m === 'PATCH') return 'badge-warn';
  if (m === 'DELETE') return 'badge-err';
  return 'badge-neutral';
}

export function classificationClass(c: string): string {
  if (c === 'BREAKING') return 'badge-err';
  if (c === 'POTENTIAL_BREAKING') return 'badge-warn';
  if (c === 'NON_BREAKING') return 'badge-ok';
  return 'badge-neutral';
}

export function changeTypeClass(t: string): string {
  if (t === 'added') return 'diff-added';
  if (t === 'removed') return 'diff-removed';
  if (t === 'modified') return 'diff-modified';
  return '';
}

export function formatDiffValue(val: unknown): string {
  if (val == null) return '—';
  if (typeof val === 'object') return JSON.stringify(val, null, 2);
  return String(val);
}

// ── Dependency resolution reason helpers ──

const REASON_LABELS: Record<string, string> = {
  non_oci_ref: 'External',
  auth_failed: 'Auth required',
  no_semver_tags: 'No versions',
  not_found: 'Not found',
  discovering: 'Discovering…',
};

const REASON_TOOLTIPS: Record<string, string> = {
  non_oci_ref: 'Non-OCI dependency — not a contract-backed service',
  auth_failed: 'Registry authentication failed — run `pacto login` or check credentials',
  no_semver_tags: 'OCI repository found but contains no valid semver tags',
  not_found: 'OCI dependency could not be found or the registry is unreachable',
  discovering: 'Background OCI discovery is still running — this may resolve shortly',
};

const REASON_BADGE_CLASSES: Record<string, string> = {
  non_oci_ref: 'badge-neutral',
  auth_failed: 'badge-err',
  no_semver_tags: 'badge-warn',
  not_found: 'badge-warn',
  discovering: 'badge-info',
};

export function reasonLabel(reason: string | undefined): string {
  if (!reason) return 'External';
  return REASON_LABELS[reason] || 'External';
}

export function reasonTooltip(reason: string | undefined): string {
  if (!reason) return 'External dependency';
  return REASON_TOOLTIPS[reason] || 'External dependency';
}

export function reasonBadgeClass(reason: string | undefined): string {
  if (!reason) return 'badge-neutral';
  return REASON_BADGE_CLASSES[reason] || 'badge-neutral';
}

export function isReasonActionable(reason: string | undefined): boolean {
  return reason === 'auth_failed' || reason === 'not_found' || reason === 'no_semver_tags';
}

const SOURCE_DESCRIPTIONS: Record<string, string> = {
  k8s: 'Kubernetes — live cluster runtime data',
  oci: 'OCI Registry — versioned contract bundles',
  local: 'Local — contracts from local filesystem',
};

export function sourceTooltip(src: string): string {
  return SOURCE_DESCRIPTIONS[src] || src;
}

const VERSION_POLICY_LABELS: Record<string, string> = {
  'tracking': 'Tracking latest',
  'pinned-tag': 'Pinned to tag',
  'pinned-digest': 'Pinned to digest',
};

export function versionPolicyLabel(policy: string | undefined): string {
  if (!policy) return '';
  return VERSION_POLICY_LABELS[policy] || policy;
}

export function versionPolicyClass(policy: string | undefined): string {
  if (policy === 'pinned-digest') return 'policy-digest';
  if (policy === 'pinned-tag') return 'policy-tag';
  if (policy === 'tracking') return 'policy-tracking';
  return '';
}
