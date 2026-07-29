/** Shared formatting/classification helpers used across views. */

export function statusClass(status: string | undefined): string {
  if (status === 'Compliant') return 'ok';
  if (status === 'Warning') return 'warn';
  if (status === 'NonCompliant') return 'err';
  if (status === 'Invalid') return 'err';
  if (status === 'Unknown') return 'info';
  if (status === 'Reference') return 'reference';
  if (status === 'NotEvaluated') return 'neutral';
  return 'neutral';
}

const STATUS_LABELS: Record<string, string> = {
  Compliant: 'Compliant',
  Warning: 'Warning',
  NonCompliant: 'Non-Compliant',
  Invalid: 'Invalid',
  Unknown: 'Unknown',
  Reference: 'Reference',
  NotEvaluated: 'Not Evaluated',
};

export function statusLabel(status: string | undefined): string {
  if (!status) return 'Unknown';
  return STATUS_LABELS[status] || status;
}

export function complianceClass(score: number): string {
  if (score >= 80) return 'score-ok';
  if (score >= 50) return 'score-warn';
  return 'score-err';
}

/**
 * "E of R" evaluation-coverage label for the detail view and list badge. Returns
 * '' unless the service was runtime-evaluated AND carries an evaluationCoverage —
 * a definition-only view cannot assert how many assertions were evaluated. Metadata
 * only; never affects status or percentage.
 */
export function evaluationCoverageLabel(
  svc: { runtimeEvaluated?: boolean; evaluationCoverage?: { evaluated: number; required: number } | null } | null | undefined,
): string {
  const ec = svc?.evaluationCoverage;
  if (!svc?.runtimeEvaluated || !ec) return '';
  return `${ec.evaluated} of ${ec.required}`;
}

export function complianceStatusClass(status: string): string {
  if (status === 'OK') return 'score-ok';
  if (status === 'WARNING') return 'score-warn';
  if (status === 'ERROR') return 'score-err';
  if (status === 'UNKNOWN') return 'score-info';
  return '';
}

/** Paths of in-bundle docs referenced by a readiness check (docPath set). */
export function referencedDocPaths(
  readiness: { checks?: Array<{ docPath?: string }> } | null | undefined,
): string[] {
  if (!readiness?.checks) return [];
  return readiness.checks.filter((c) => !!c.docPath).map((c) => c.docPath as string);
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

// ── Fleet completeness (operational-graph coverage) ──

const COMPLETENESS_LABELS: Record<string, string> = {
  complete: 'Complete',
  partial: 'Partial',
  empty: 'Empty',
};

/** Human label for a fleet snapshot/answer completeness. */
export function completenessLabel(c: string | undefined): string {
  if (!c) return 'Unknown';
  return COMPLETENESS_LABELS[c] || c;
}

/** Badge class for a completeness — a partial answer reads as a warning. */
export function completenessClass(c: string | undefined): string {
  if (c === 'complete') return 'badge-ok';
  if (c === 'partial') return 'badge-warn';
  if (c === 'empty') return 'badge-neutral';
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

/**
 * HTML for a long dotted identifier/path that wraps at NATURAL boundaries
 * (dots, brackets, colons, slashes, hyphens, underscores) instead of breaking
 * mid-word. Escapes HTML, then inserts <wbr> break opportunities after each
 * separator — so a narrow table cell renders "service.owner.\ncontacts[...]"
 * rather than "service.owner.t\neam". <wbr> is not copied into the clipboard,
 * so the path pastes cleanly. Use with {@html} + CSS `overflow-wrap: break-word`
 * (no `word-break: break-all`). Reusable across any table that shows paths.
 */
export function breakableIdentifierHtml(s: string): string {
  const escaped = (s ?? '').replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  return escaped.replace(/([.[\]:/_-])/g, '$1<wbr>');
}

// ── Dependency resolution reason helpers ──

const REASON_LABELS: Record<string, string> = {
  non_oci_ref: 'External',
  auth_failed: 'Auth required',
  no_semver_tags: 'No versions',
  not_found: 'Not found',
  pull_failed: 'Unreachable',
  discovering: 'Discovering…',
};

const REASON_TOOLTIPS: Record<string, string> = {
  non_oci_ref: 'Non-OCI dependency — not a contract-backed service',
  auth_failed: 'Registry authentication failed — run `pacto login` or check credentials',
  no_semver_tags: 'OCI repository found but contains no valid semver tags',
  not_found: 'OCI artifact not found in the registry',
  pull_failed: 'Registry unreachable — check network connectivity or DNS',
  discovering: 'Background OCI discovery is still running — this may resolve shortly',
};

const REASON_BADGE_CLASSES: Record<string, string> = {
  non_oci_ref: 'badge-neutral',
  auth_failed: 'badge-err',
  no_semver_tags: 'badge-warn',
  not_found: 'badge-warn',
  pull_failed: 'badge-err',
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
  return reason === 'auth_failed' || reason === 'not_found' || reason === 'no_semver_tags' || reason === 'pull_failed';
}

const SOURCE_DESCRIPTIONS: Record<string, string> = {
  k8s: 'Kubernetes — live cluster runtime data',
  oci: 'OCI Registry — versioned contract bundles',
  local: 'Local — contracts from local filesystem',
  cache: 'Cache — offline baseline from a previously pulled bundle',
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

// ── Lock and drift helpers ──

/** Strip sha256: prefix and truncate to first n hex chars for compact digest display. */
export function shortDigest(digest: string | null | undefined, n: number = 8): string {
  if (!digest) return '';
  const hex = digest.replace(/^sha256:/, '');
  return hex.slice(0, n);
}

/** Badge class for drift status (locked, drift, unlocked, unknown). */
export function driftBadgeClass(status: string | null | undefined): string {
  if (status === 'locked') return 'badge-ok';
  if (status === 'drift') return 'badge-warn';
  if (status === 'unlocked') return 'badge-neutral';
  return 'badge-neutral'; // unknown or absent
}

/** Human label for drift status badge. */
export function driftBadgeLabel(status: string | null | undefined): string {
  if (status === 'locked') return 'Locked';
  if (status === 'drift') return 'Drift';
  if (status === 'unlocked') return 'Unlocked';
  return '';
}

// ── Stats helpers ──

const HIGH_IMPACT_THRESHOLD = 3;

/** Count services with blast radius at or above the high-impact threshold. */
export function countHighImpact(services: Array<{ blastRadius?: number }>): number {
  return services.filter(s => (s.blastRadius || 0) >= HIGH_IMPACT_THRESHOLD).length;
}

/**
 * Apply all active filters (name, source, status) to a service list.
 * Used to derive the visible set that stats like "high impact" should reflect.
 */
export function filterServices(
  services: Array<Record<string, any>>,
  filters: { nameFilter?: string; sourceFilter?: string; statusFilter?: string },
): Array<Record<string, any>> {
  let list = services;
  if (filters.nameFilter) {
    const q = filters.nameFilter.toLowerCase();
    list = list.filter((s) => s.name.toLowerCase().includes(q) || ownerMatchesFilter(s.owner, q));
  }
  if (filters.sourceFilter && filters.sourceFilter !== 'all') {
    list = list.filter((s) => (s.sources || [s.source]).includes(filters.sourceFilter));
  }
  if (filters.statusFilter && filters.statusFilter !== 'all') {
    list = list.filter((s) => s.contractStatus === filters.statusFilter);
  }
  return list;
}

/** Result of paginating a list: the visible slice plus navigation metadata. */
export interface Paginated<T> {
  items: T[];
  page: number; // clamped current page (1-based)
  totalPages: number;
  total: number;
  perPage: number;
}

/**
 * Slice `items` into a single page. `page` is 1-based and clamped to the valid
 * range; a non-positive `perPage` disables pagination (everything on one page).
 */
export function paginate<T>(items: T[], page: number, perPage: number): Paginated<T> {
  const total = items.length;
  if (perPage <= 0) {
    return { items: items.slice(), page: 1, totalPages: 1, total, perPage: total };
  }
  const totalPages = Math.max(1, Math.ceil(total / perPage));
  const clamped = Math.min(Math.max(1, Math.floor(page) || 1), totalPages);
  const start = (clamped - 1) * perPage;
  return { items: items.slice(start, start + perPage), page: clamped, totalPages, total, perPage };
}

// ── Owner helpers ──

/** Extract a display string from the owner object. */
export function ownerDisplay(owner: unknown): string {
  if (!owner || typeof owner !== 'object') return '';
  const o = owner as Record<string, unknown>;
  if (o.team) return String(o.team);
  if (o.dri) return String(o.dri);
  return '';
}

/**
 * Canonical owner key used for grouping, aggregation, and navigation.
 * Normalization: structured.team > structured.dri > empty.
 * This is the single source of truth — reuse everywhere.
 */
export const ownerKey = ownerDisplay;

/** Extract the team from an owner object. */
export function ownerTeam(owner: unknown): string {
  if (!owner || typeof owner !== 'object') return '';
  return String((owner as Record<string, unknown>).team || '');
}

/** Check whether an owner object matches a search query (case-insensitive). */
export function ownerMatchesFilter(owner: unknown, query: string): boolean {
  if (!owner || typeof owner !== 'object') return false;
  const q = query.toLowerCase();
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

/** Check if owner is a structured object. Always returns true if owner is present. */
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
 */
export function extractOwnerDetail(ownerKeyStr: string, services: Array<Record<string, unknown>>): OwnerDetail {
  const detail: OwnerDetail = { key: ownerKeyStr, team: '', dri: '', contacts: [], isStructured: false, driConflict: false, allDris: [] };

  const contactSet = new Set<string>();
  const mergedContacts: OwnerContact[] = [];
  const dris = new Set<string>();

  for (const svc of services) {
    const o = svc.owner;
    if (!o || typeof o !== 'object') continue;
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
  invalid: number;
  notEvaluated: number;
  // Secondary conclusive/verification metrics (mirror the fleet tile).
  runtimeEvaluated: number; // compliant + warning + nonCompliant + unknown
  conclusive: number; // compliant + warning + nonCompliant
  totalBlast: number;
  compliancePercent: number;
  // Per-owner readiness composition (buckets sum to `services`).
  ready: number;
  partial: number;
  notReady: number;
  notConfigured: number;
}

/** Aggregate services by canonical owner key. */
export function aggregateByOwner(services: Array<Record<string, unknown>>): OwnerAggregation[] {
  return summarize(services).byOwner;
}

/**
 * Numeric comparator that always sorts "unassessed" sentinel values (< 0, e.g.
 * compliancePercent/score of -1) to the END, regardless of sort direction — a
 * not-yet-assessed row is not the same as a 0% one and must never rank as the
 * lowest (or highest) real value. `dir` is 1 for ascending, -1 for descending.
 */
export function compareScoresUnassessedLast(a: number, b: number, dir: number): number {
  const au = a < 0, bu = b < 0;
  if (au && bu) return 0;
  if (au) return 1;  // a (unassessed) after b
  if (bu) return -1; // b (unassessed) after a
  return (a - b) * dir;
}

// ── Dependency graph aggregation ──

const STATUS_SEVERITY: Record<string, number> = {
  Invalid: 5, NonCompliant: 4, Unknown: 3, Warning: 2, external: 1, Reference: 0, NotEvaluated: 0, Compliant: 0,
};
/** The worst (most-severe) status in a set — used to color an aggregated node. */
export function worstStatus(statuses: Iterable<string>): string {
  let worst = 'Compliant', sev = -1;
  for (const s of statuses) {
    const v = STATUS_SEVERITY[s] ?? 0;
    if (v > sev) { sev = v; worst = s; }
  }
  return worst;
}

interface GNode { id: string; serviceName: string; status: string; edges?: Array<{ targetId: string; type?: string }> }

/**
 * A subgraph of the "core" nodes (e.g. one team's services) plus their related
 * nodes — always their direct dependencies, and (optionally) their direct
 * dependents. Unrelated services are dropped, so an owner view shows the team and
 * what it touches, not the whole fleet.
 *
 * `includeDependents` defaults to false: when a team owns something everyone
 * depends on (e.g. postgres), pulling in every dependent would drag in the whole
 * fleet and defeat the point of focusing the team. Deps are always bounded by
 * what the team's services actually consume.
 */
export function relatedSubgraph<T extends GNode>(
  graphData: { nodes?: T[] } | null,
  isCore: (node: T) => boolean,
  { includeDependents = false }: { includeDependents?: boolean } = {},
): { nodes: T[] } {
  const nodes = graphData?.nodes || [];
  const core = new Set(nodes.filter(isCore).map((n) => n.id));
  if (!core.size) return { nodes: [] };
  const keep = new Set(core);
  for (const n of nodes) {
    if (core.has(n.id)) for (const e of n.edges || []) keep.add(e.targetId); // deps
    if (includeDependents) for (const e of n.edges || []) if (core.has(e.targetId)) keep.add(n.id);
  }
  return { nodes: nodes.filter((n) => keep.has(n.id)) };
}

interface AggNode { id: string; serviceName: string; status: string; version?: string; edges?: Array<{ targetId: string; type?: string; required?: boolean }> }

/**
 * Collapse a service dependency graph into a per-owner one: each owner becomes a
 * single node (status = its worst service, label = service count), and edges are
 * the DISTINCT cross-owner dependencies. Answers "which team depends on which".
 * `ownerLabelOf` maps a service node to its owner label.
 */
export function aggregateGraphByOwner(
  graphData: { nodes?: Array<{ id: string; serviceName: string; status: string; edges?: Array<{ targetId: string; type?: string }> }> } | null,
  ownerLabelOf: (node: { id: string; serviceName: string; status: string }) => string,
): { nodes: AggNode[] } {
  const nodes = graphData?.nodes || [];
  const ownerOf = new Map<string, string>();
  for (const n of nodes) ownerOf.set(n.id, ownerLabelOf(n));

  const stats = new Map<string, { count: number; statuses: string[] }>();
  for (const n of nodes) {
    const o = ownerOf.get(n.id);
    if (!o) continue;
    const a = stats.get(o) ?? stats.set(o, { count: 0, statuses: [] }).get(o)!;
    a.count++; a.statuses.push(n.status);
  }
  const targets = new Map<string, Set<string>>();
  for (const o of stats.keys()) targets.set(o, new Set());
  for (const n of nodes) {
    const so = ownerOf.get(n.id);
    for (const e of n.edges || []) {
      const to = ownerOf.get(e.targetId);
      if (so && to && so !== to) targets.get(so)!.add(to);
    }
  }
  return {
    nodes: [...stats.entries()].map(([owner, a]) => ({
      id: owner,
      serviceName: owner,
      status: worstStatus(a.statuses),
      version: `${a.count} service${a.count !== 1 ? 's' : ''}`,
      edges: [...(targets.get(owner) || [])].map((t) => ({ targetId: t, type: 'dependency', required: true })),
    })),
  };
}

// ── Readiness ──

/** Readiness check revision history entry. */
export interface ReadinessRevision {
  date: string;
  version: string;
  author: string;
  description: string;
}

/** A single derived readiness check, as carried by the API. */
export interface ReadinessCheck {
  id: string;
  type: string;
  category?: string;
  status: string; // done | partial | not-done | deferred
  evidence?: string;
  description?: string;
  weight: number;
  earnedWeight: number;
  excluded: boolean;
  docPath?: string;
}

/** Derived readiness assessment for one service, as carried by the API. */
export interface ReadinessInfo {
  score: number;
  minScore: number;
  totalWeight: number;
  earnedWeight: number;
  partialCredit: number;
  passing: boolean;
  expires: string;
  expired: boolean;
  daysRemaining?: number | null;
  doneCount: number;
  partialCount: number;
  notDoneCount: number;
  deferredCount: number;
  revisions?: ReadinessRevision[];
  checks: ReadinessCheck[];
}

type WithReadiness = { readiness?: ReadinessInfo | null };

export type ReadinessBucket = 'ready' | 'partial' | 'not-ready' | 'unknown';

/**
 * Bucket a service's overall readiness. The contract's gate (`passing`,
 * i.e. score >= minScore) is the primary signal; score bands break the rest.
 * `unknown` means no readiness block is declared (not configured) — never an error.
 */
export function readinessBucket(svc: WithReadiness): ReadinessBucket {
  const r = svc?.readiness;
  if (!r) return 'unknown';
  if (r.passing) return 'ready';
  if (r.score >= 50) return 'partial';
  return 'not-ready';
}

/**
 * Score-cell class driven by the readiness GATE (passing), not the absolute
 * score. A service at 70% that clears its minScore should read green; one at 79%
 * that misses a higher gate should read red — so two similar scores no longer
 * look identical. Falls back to absolute bands when there is no readiness block.
 */
export function readinessGateClass(r: ReadinessInfo | null | undefined): string {
  if (!r) return '';
  if (r.passing) return 'score-ok';
  // Below the gate: amber when it is at least within striking distance,
  // red when it is far off — but never green, because it does not pass.
  if (r.score >= 50 && !r.expired) return 'score-warn';
  return 'score-err';
}

/**
 * Explicit gate tooltip, e.g.
 *   "79% — passing (minScore 75)"
 *   "70% — below gate (minScore 75)"
 *   "82% — expired (minScore 75)"
 * so it is obvious which services clear minScore.
 */
export function readinessGateTip(r: ReadinessInfo | null | undefined): string {
  if (!r) return '';
  if (r.expired) return `${r.score}% — expired (minScore ${r.minScore})`;
  const verdict = r.passing ? 'passing' : 'below gate';
  return `${r.score}% — ${verdict} (minScore ${r.minScore})`;
}

const READINESS_BUCKET_LABELS: Record<ReadinessBucket, string> = {
  ready: 'Ready',
  partial: 'Partial',
  'not-ready': 'Not Ready',
  unknown: 'Not configured',
};

export function readinessBucketLabel(b: ReadinessBucket): string {
  return READINESS_BUCKET_LABELS[b] || 'Not configured';
}

/** Badge class for a readiness bucket — reuses the shared status palette. */
export function readinessBucketClass(b: ReadinessBucket): string {
  if (b === 'ready') return 'badge-ok';
  if (b === 'partial') return 'badge-warn';
  if (b === 'not-ready') return 'badge-err';
  return 'badge-neutral';
}

/** Global rollup of readiness across all services for the overview summary. */
export interface ReadinessSummary {
  total: number; // all services
  ready: number;
  partial: number;
  notReady: number;
  notConfigured: number;
  configured: number; // total - notConfigured
  avgScore: number; // mean score over configured services; -1 if none configured
  expiredAssessments: number; // count of services where expired=true
  totalDone: number; // sum of doneCount across configured
  totalPartial: number; // sum of partialCount across configured
  totalNotDone: number; // sum of notDoneCount across configured
  totalDeferred: number; // sum of deferredCount across configured
}

/** Aggregate readiness across services. Services without a readiness block are
 *  counted as "not configured" and excluded from the average score. */
export function summarizeReadiness(services: WithReadiness[]): ReadinessSummary {
  return summarize(services).readiness;
}

/** Whether a readiness check's evidence string is a clickable web URL. */
export function isUrlEvidence(evidence: string | null | undefined): boolean {
  if (!evidence) return false;
  return /^https?:\/\//i.test(evidence);
}

// ── Per-check declared status (done | partial | not-done | deferred) ──

const CHECK_STATUS_LABELS: Record<string, string> = {
  done: 'Done',
  partial: 'Partial',
  'not-done': 'Not done',
  deferred: 'Deferred',
};

/** Human label for a readiness check's declared status. */
export function checkStatusLabel(status: string | undefined): string {
  if (!status) return '—';
  return CHECK_STATUS_LABELS[status] || status;
}

/** Badge class for a readiness check's declared status — reuses the shared palette. */
export function checkStatusClass(status: string | undefined): string {
  if (status === 'done') return 'badge-ok';
  if (status === 'partial') return 'badge-warn';
  if (status === 'not-done') return 'badge-err';
  if (status === 'deferred') return 'badge-neutral';
  return 'badge-neutral';
}

/**
 * Countdown copy for a readiness assessment's overall expiry.
 * `expired` wins; otherwise renders the whole-days-remaining value.
 * Returns '' when there is nothing to show (no expiry declared).
 */
export function assessmentCountdownLabel(
  expired: boolean | undefined,
  days: number | null | undefined,
): string {
  if (expired) return 'Expired';
  if (days == null) return '';
  if (days < 0) return 'Expired';
  if (days === 0) return 'expires today';
  if (days === 1) return 'expires in 1 day';
  return `expires in ${days} days`;
}

/** Sorted unique evidence-kind types present across all declared checks. */
export function readinessCheckTypes(services: WithReadiness[]): string[] {
  const types = new Set<string>();
  for (const svc of services) {
    for (const c of svc.readiness?.checks ?? []) {
      if (c.type) types.add(c.type);
    }
  }
  return Array.from(types).sort();
}

// ── Fleet overview ──

/** Headline fleet metrics for the landing-page overview. */
export interface FleetSummary {
  total: number;
  assessed: number; // compliant + warning + nonCompliant + unknown + invalid (excludes reference/notEvaluated)
  compliant: number;
  warning: number;
  nonCompliant: number;
  reference: number;
  unknown: number;
  invalid: number;
  notEvaluated: number;
  needsAttention: number; // warning + nonCompliant + invalid (unknown surfaced separately)
  compliancePercent: number; // compliant / assessed * 100; -1 if nothing assessed
  highImpact: number; // services with blast radius >= HIGH_IMPACT_THRESHOLD
  runtimeEvaluated: number; // compliant + warning + nonCompliant + unknown (secondary metric)
  conclusive: number; // compliant + warning + nonCompliant (secondary metric)
}

/** Roll up the whole service list into the few signals that matter at a glance. */
export function summarizeFleet(services: Array<Record<string, unknown>>): FleetSummary {
  const metrics = summarize(services);
  return {
    total: metrics.total,
    assessed: metrics.assessed,
    compliant: metrics.compliant,
    warning: metrics.warning,
    nonCompliant: metrics.nonCompliant,
    reference: metrics.reference,
    unknown: metrics.unknown,
    invalid: metrics.invalid,
    notEvaluated: metrics.notEvaluated,
    needsAttention: metrics.needsAttention,
    compliancePercent: metrics.compliancePercent,
    highImpact: metrics.highImpact,
    runtimeEvaluated: metrics.runtimeEvaluated,
    conclusive: metrics.conclusive,
  };
}

/** Category breakdown for readiness checks. */
export interface CategoryBreakdown {
  category: string;
  checks: number;
  done: number;
  partial: number;
  notDone: number;
  deferred: number;
}

/** Unified metrics from a service list — single source of truth. */
export interface Metrics {
  total: number;
  assessed: number;
  compliant: number;
  warning: number;
  nonCompliant: number;
  reference: number;
  unknown: number;
  invalid: number;
  notEvaluated: number;
  needsAttention: number;
  compliancePercent: number;
  highImpact: number;
  runtimeEvaluated: number;
  conclusive: number;
  // Fleet evaluation coverage: {evaluated, required} summed across services that
  // carry it. Metadata only — never affects any status or percentage.
  evaluationCoverage: { evaluated: number; required: number };
  readiness: ReadinessSummary;
  byOwner: OwnerAggregation[];
  byCategory: CategoryBreakdown[];
}

/**
 * Unified metrics computation — single-pass aggregation producing all KPIs + breakdowns.
 * This is the single source of truth; legacy summarizers delegate to this.
 */
export function summarize(services: Array<Record<string, unknown>>): Metrics {
  const m: Metrics = {
    total: services.length, assessed: 0, compliant: 0, warning: 0, nonCompliant: 0,
    reference: 0, unknown: 0, invalid: 0, notEvaluated: 0,
    needsAttention: 0, compliancePercent: -1, highImpact: 0,
    runtimeEvaluated: 0, conclusive: 0,
    evaluationCoverage: { evaluated: 0, required: 0 },
    readiness: {
      total: services.length, ready: 0, partial: 0, notReady: 0, notConfigured: 0,
      configured: 0, avgScore: -1, expiredAssessments: 0,
      totalDone: 0, totalPartial: 0, totalNotDone: 0, totalDeferred: 0,
    },
    byOwner: [],
    byCategory: [],
  };

  // Owner aggregation state
  const ownerMap = new Map<string, OwnerAggregation>();

  // Category aggregation state
  const categoryMap = new Map<string, CategoryBreakdown>();

  let readinessScoreSum = 0;

  for (const svc of services) {
    // Contract status bucketing
    const status = svc.contractStatus as string;
    switch (status) {
      case 'Compliant': m.compliant++; break;
      case 'Warning': m.warning++; break;
      case 'NonCompliant': m.nonCompliant++; break;
      case 'Reference': m.reference++; break;
      case 'Unknown': m.unknown++; break;
      case 'Invalid': m.invalid++; break;
      case 'NotEvaluated': m.notEvaluated++; break;
      default: m.unknown++; break;
    }

    // High impact
    if (((svc.blastRadius as number) || 0) >= HIGH_IMPACT_THRESHOLD) m.highImpact++;

    // Fleet evaluation coverage — sum evaluated/required across services that carry it.
    const ec = svc.evaluationCoverage as { evaluated?: number; required?: number } | undefined;
    if (ec) {
      m.evaluationCoverage.evaluated += ec.evaluated || 0;
      m.evaluationCoverage.required += ec.required || 0;
    }

    // Owner aggregation
    const key = ownerKey(svc.owner) || '(unowned)';
    let agg = ownerMap.get(key);
    if (!agg) {
      agg = { key, services: 0, compliant: 0, warning: 0, nonCompliant: 0, reference: 0, unknown: 0, invalid: 0, notEvaluated: 0, runtimeEvaluated: 0, conclusive: 0, totalBlast: 0, compliancePercent: 0, ready: 0, partial: 0, notReady: 0, notConfigured: 0 };
      ownerMap.set(key, agg);
    }
    agg.services++;
    if (status === 'Compliant') agg.compliant++;
    else if (status === 'Warning') agg.warning++;
    else if (status === 'NonCompliant') agg.nonCompliant++;
    else if (status === 'Reference') agg.reference++;
    else if (status === 'Unknown') agg.unknown++;
    else if (status === 'Invalid') agg.invalid++;
    else if (status === 'NotEvaluated') agg.notEvaluated++;
    else agg.unknown++;
    agg.totalBlast += (svc.blastRadius as number) || 0;

    // Readiness aggregation
    const r = (svc as WithReadiness).readiness;
    const bucket = readinessBucket(svc as WithReadiness);

    // Per-owner readiness composition (unknown → notConfigured).
    if (bucket === 'ready') agg.ready++;
    else if (bucket === 'partial') agg.partial++;
    else if (bucket === 'not-ready') agg.notReady++;
    else agg.notConfigured++;

    if (bucket === 'unknown') {
      m.readiness.notConfigured++;
    } else {
      if (bucket === 'ready') m.readiness.ready++;
      else if (bucket === 'partial') m.readiness.partial++;
      else m.readiness.notReady++;
      m.readiness.configured++;
      readinessScoreSum += r!.score || 0;
      if (r!.expired) m.readiness.expiredAssessments++;
      m.readiness.totalDone += r!.doneCount || 0;
      m.readiness.totalPartial += r!.partialCount || 0;
      m.readiness.totalNotDone += r!.notDoneCount || 0;
      m.readiness.totalDeferred += r!.deferredCount || 0;

      // Category aggregation
      for (const check of r!.checks || []) {
        const cat = check.category || 'other';
        let catAgg = categoryMap.get(cat);
        if (!catAgg) {
          catAgg = { category: cat, checks: 0, done: 0, partial: 0, notDone: 0, deferred: 0 };
          categoryMap.set(cat, catAgg);
        }
        catAgg.checks++;
        if (check.status === 'done') catAgg.done++;
        else if (check.status === 'partial') catAgg.partial++;
        else if (check.status === 'not-done') catAgg.notDone++;
        else if (check.status === 'deferred') catAgg.deferred++;
      }
    }
  }

  // Finalize contract metrics per B-2 ruling.
  // assessed = compliant + warning + nonCompliant + unknown + invalid (excludes reference/notEvaluated)
  m.assessed = m.compliant + m.warning + m.nonCompliant + m.unknown + m.invalid;
  // needsAttention = nonCompliant + warning + invalid (unknown surfaced separately)
  m.needsAttention = m.nonCompliant + m.warning + m.invalid;
  // compliancePercent = compliant / assessed; -1 sentinel when assessed == 0
  if (m.assessed > 0) m.compliancePercent = Math.round((m.compliant / m.assessed) * 100);
  // Secondary conclusive/verification metric (NEW)
  m.runtimeEvaluated = m.compliant + m.warning + m.nonCompliant + m.unknown;
  m.conclusive = m.compliant + m.warning + m.nonCompliant;

  // Finalize owner aggregation. compliancePercent is the SHARE of assessed
  // services that are compliant (matches the fleet metric and the "Compliant N"
  // count on the same row), or -1 when the owner has nothing assessed. It is NOT
  // a mean of compliance scores — that read differently from the count beside it.
  for (const agg of ownerMap.values()) {
    const assessed = agg.compliant + agg.warning + agg.nonCompliant + agg.unknown + agg.invalid;
    agg.compliancePercent = assessed > 0 ? Math.round((agg.compliant / assessed) * 100) : -1;
    // Secondary conclusive/verification metric (mirrors the fleet summarize()).
    agg.runtimeEvaluated = agg.compliant + agg.warning + agg.nonCompliant + agg.unknown;
    agg.conclusive = agg.compliant + agg.warning + agg.nonCompliant;
  }
  m.byOwner = Array.from(ownerMap.values()).sort((a, b) => a.key.localeCompare(b.key));

  // Finalize readiness metrics
  if (m.readiness.configured > 0) {
    m.readiness.avgScore = Math.round(readinessScoreSum / m.readiness.configured);
  }

  // Finalize category aggregation
  m.byCategory = Array.from(categoryMap.values()).sort((a, b) => a.category.localeCompare(b.category));

  return m;
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
