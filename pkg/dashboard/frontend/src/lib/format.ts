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

// ── Owner helpers ──

/** Extract a display string from the owner field (string or structured object). */
export function ownerDisplay(owner: unknown): string {
  if (!owner) return '';
  if (typeof owner === 'string') return owner;
  if (typeof owner === 'object') {
    const o = owner as Record<string, unknown>;
    if (o.team) return String(o.team);
    if (o.dri) return String(o.dri);
    return '';
  }
  return '';
}

/**
 * Canonical owner key used for grouping, aggregation, and navigation.
 * Normalization: structured.team > legacy string > structured.dri > empty.
 * This is the single source of truth — reuse everywhere.
 */
export const ownerKey = ownerDisplay;

/** Extract the team from an owner (string or structured). */
export function ownerTeam(owner: unknown): string {
  if (!owner) return '';
  if (typeof owner === 'string') return owner;
  if (typeof owner === 'object') return String((owner as Record<string, unknown>).team || '');
  return '';
}

/** Check whether an owner value matches a search query (case-insensitive). */
export function ownerMatchesFilter(owner: unknown, query: string): boolean {
  if (!owner) return false;
  const q = query.toLowerCase();
  if (typeof owner === 'string') return owner.toLowerCase().includes(q);
  if (typeof owner === 'object') {
    const o = owner as Record<string, unknown>;
    if (String(o.team || '').toLowerCase().includes(q)) return true;
    if (String(o.dri || '').toLowerCase().includes(q)) return true;
    const contacts = o.contacts as Array<Record<string, unknown>> | undefined;
    if (contacts) {
      for (const c of contacts) {
        if (String(c.value || '').toLowerCase().includes(q)) return true;
      }
    }
    return false;
  }
  return false;
}

/** Check if owner is a structured object (not a plain string). */
export function ownerIsStructured(owner: unknown): boolean {
  return owner != null && typeof owner === 'object';
}

// ── Owner detail extraction ──

export interface OwnerContact {
  type: string;
  value: string;
  purpose?: string;
}

export interface OwnerDetail {
  key: string;
  team: string;
  dri: string;
  contacts: OwnerContact[];
  isStructured: boolean;
  driConflict: boolean;
  allDris: string[];
}

/**
 * Extract a consistent OwnerDetail from the services sharing an owner key.
 * Merges contacts from all services (deduped by type+value).
 * Flags DRI inconsistency when services disagree.
 * For legacy string owners, returns the string as team.
 */
export function extractOwnerDetail(ownerKeyStr: string, services: Array<Record<string, unknown>>): OwnerDetail {
  const detail: OwnerDetail = { key: ownerKeyStr, team: '', dri: '', contacts: [], isStructured: false, driConflict: false, allDris: [] };

  const contactSet = new Set<string>();
  const mergedContacts: OwnerContact[] = [];
  const dris = new Set<string>();

  for (const svc of services) {
    const o = svc.owner;
    if (!o || typeof o !== 'object') {
      if (typeof o === 'string' && !detail.team) detail.team = o;
      continue;
    }
    detail.isStructured = true;
    const obj = o as Record<string, unknown>;
    if (!detail.team && obj.team) detail.team = String(obj.team);
    const dri = String(obj.dri || '');
    if (dri) dris.add(dri);

    const contacts = obj.contacts as OwnerContact[] | undefined;
    if (contacts) {
      for (const c of contacts) {
        const key = `${c.type}\0${c.value}`;
        if (!contactSet.has(key)) {
          contactSet.add(key);
          mergedContacts.push(c);
        }
      }
    }
  }

  if (dris.size > 0) {
    detail.allDris = Array.from(dris).sort();
    detail.dri = detail.allDris[0];
    detail.driConflict = dris.size > 1;
  }
  detail.contacts = mergedContacts;

  if (!detail.team && !detail.isStructured) detail.team = ownerKeyStr;

  return detail;
}

// ── Owner aggregation ──

export interface OwnerAggregation {
  key: string;
  services: number;
  compliant: number;
  warning: number;
  nonCompliant: number;
  reference: number;
  unknown: number;
  totalBlast: number;
  compliancePercent: number;
}

/** Aggregate services by canonical owner key. */
export function aggregateByOwner(services: Array<Record<string, unknown>>): OwnerAggregation[] {
  const map = new Map<string, OwnerAggregation>();
  const scores = new Map<string, number[]>();
  for (const svc of services) {
    const key = ownerKey(svc.owner) || '(unowned)';
    let agg = map.get(key);
    if (!agg) {
      agg = { key, services: 0, compliant: 0, warning: 0, nonCompliant: 0, reference: 0, unknown: 0, totalBlast: 0, compliancePercent: 0 };
      map.set(key, agg);
      scores.set(key, []);
    }
    agg.services++;
    const status = svc.contractStatus as string;
    if (status === 'Compliant') agg.compliant++;
    else if (status === 'Warning') agg.warning++;
    else if (status === 'NonCompliant') agg.nonCompliant++;
    else if (status === 'Reference') agg.reference++;
    else agg.unknown++;
    agg.totalBlast += (svc.blastRadius as number) || 0;
    if (svc.complianceScore != null) scores.get(key)!.push(svc.complianceScore as number);
  }
  for (const [key, agg] of map) {
    const s = scores.get(key)!;
    agg.compliancePercent = s.length > 0 ? Math.round(s.reduce((a, b) => a + b, 0) / s.length) : -1;
  }
  return Array.from(map.values()).sort((a, b) => a.key.localeCompare(b.key));
}

// ── Tooltip positioning ──

export interface TooltipPosition {
  left: number;
  top: number;
}

/**
 * Compute tooltip position in fixed viewport coordinates, avoiding clipping.
 * Prefers placement above the cursor, centered horizontally.
 * Falls back to below if insufficient space above.
 * Clamps horizontally to stay within viewport.
 */
export function computeTooltipPosition(
  cursorX: number,
  cursorY: number,
  tipWidth: number,
  tipHeight: number,
  margin: number = 8,
): TooltipPosition {
  const vw = typeof window !== 'undefined' ? window.innerWidth : 1200;
  const vh = typeof window !== 'undefined' ? window.innerHeight : 800;

  // Horizontal: center on cursor, clamp to viewport
  let left = cursorX - tipWidth / 2;
  if (left < margin) left = margin;
  if (left + tipWidth > vw - margin) left = vw - margin - tipWidth;

  // Vertical: prefer above cursor
  let top = cursorY - tipHeight - margin;
  if (top < margin) {
    // Not enough room above — place below cursor
    top = cursorY + margin;
  }
  // Clamp bottom
  if (top + tipHeight > vh - margin) {
    top = vh - margin - tipHeight;
  }

  return { left, top };
}
