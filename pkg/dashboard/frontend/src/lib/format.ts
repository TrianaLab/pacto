/** Shared formatting/classification helpers used across views. */

import { ownerKey, ownerKeyLabel, ownerKeyKind, UNOWNED_KEY, type OwnerAggregation } from './owners';
import { readinessBucket, type ReadinessSummary, type WithReadiness } from './readiness';

/** The badge class a status is drawn in. `reference` shares the info colours. */
export type StatusClass = 'ok' | 'warn' | 'err' | 'info' | 'neutral' | 'reference';

/**
 * Every compliance status: the one wording it is shown under and the one tone it is
 * drawn in, decided together.
 *
 * They were three tables — the badge class here, the legend tone in distributions.ts
 * and the node colour in graph.ts — and they disagreed. "Unknown" was a blue badge on a
 * row, an amber swatch in the legend above it and a grey node in the graph beside it:
 * one state, three colours, one screen. Amber is the answer the product settled on — a
 * target we cannot evaluate is an open question, and blue or grey next to a green
 * Compliant reads as benign. "Not evaluated" stays neutral: nothing is running to
 * evaluate.
 */
const STATUS_META: Record<string, { label: string; tone: StatusClass }> = {
  Compliant: { label: 'Compliant', tone: 'ok' },
  Warning: { label: 'Warning', tone: 'warn' },
  // Sentence case, matching the attention vocabulary these badges sit beside. The
  // Title-Case-With-Hyphens forms were transliterations of the wire enum, so one screen
  // could show "Not compliant" as a triage category and "Non-Compliant" as the badge on
  // the very service it was describing.
  NonCompliant: { label: 'Not compliant', tone: 'err' },
  Invalid: { label: 'Invalid', tone: 'err' },
  Unknown: { label: 'Unknown', tone: 'warn' },
  Reference: { label: 'Reference', tone: 'reference' },
  NotEvaluated: { label: 'Not evaluated', tone: 'neutral' },
};

export function statusClass(status: string | undefined): StatusClass {
  return STATUS_META[status || '']?.tone || 'neutral';
}

export function statusLabel(status: string | undefined): string {
  if (!status) return 'Unknown';
  return STATUS_META[status]?.label || status;
}

/**
 * The tone a status is drawn in on a surface whose palette has no separate reference
 * swatch — because there is no separate colour: `.badge-reference` IS the info colours
 * (components.css). The distribution legends, the ranked bars and the graph canvas all
 * read this, so the mapping is stated once instead of guessed three times.
 */
export function statusTone(status: string | undefined): Exclude<StatusClass, 'reference'> {
  const c = statusClass(status);
  return c === 'reference' ? 'info' : c;
}

/**
 * Every compliance status a filter may offer, worst-first so the pickers order the way
 * the triage lists do. Derived from STATUS_META, so a status the product can badge is
 * never a status the product cannot filter for: two views each hand-listed four of the
 * seven, and both omitted NotEvaluated -- the value most rows in the services list
 * actually carry.
 *
 * Reference is excluded deliberately: it marks a bundle that exists only to be
 * referenced by others, not an assessment outcome, so it is not a state to triage by.
 */
export const STATUS_FILTER_OPTIONS: string[] =
  ['NonCompliant', 'Invalid', 'Warning', 'Unknown', 'NotEvaluated', 'Compliant']
    .filter((s) => s in STATUS_META);

export function complianceClass(score: number): string {
  if (score >= 80) return 'score-ok';
  if (score >= 50) return 'score-warn';
  return 'score-err';
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
 * (dots, brackets, colons, slashes, hyphens, underscores, @ and #) instead of breaking
 * mid-word. Escapes HTML, then inserts <wbr> break opportunities after each
 * separator — so a narrow table cell renders "service.owner.\ncontacts[...]"
 * rather than "service.owner.t\neam". <wbr> is not copied into the clipboard,
 * so the path pastes cleanly. Use with {@html} + CSS `overflow-wrap: break-word`
 * (no `word-break: break-all`). Reusable across any table that shows paths.
 */
export function breakableIdentifierHtml(s: string): string {
  const escaped = (s ?? '').replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  return escaped.replace(/([.[\]:/_@#-])/g, '$1<wbr>');
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
  non_oci_ref: 'A dependency outside OCI with no contract of its own',
  auth_failed: 'Registry authentication failed. Run `pacto login` or check your credentials.',
  no_semver_tags: 'OCI repository found but contains no valid semver tags',
  not_found: 'OCI artifact not found in the registry',
  pull_failed: 'Registry unreachable. Check network connectivity or DNS.',
  discovering: 'Background OCI discovery is still running and may resolve this shortly.',
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
  kubernetes: 'Kubernetes — live cluster runtime data',
  oci: 'OCI Registry — versioned contract bundles',
  local: 'Local — contracts from local filesystem',
  cache: 'Cache — offline baseline from a previously pulled bundle',
  'target-state': 'Target state — a file describing where each revision runs',
  'evidence-http': 'Evidence Server — signed reports from remote environments',
  'evidence-ingest': 'Evidence Server — signed reports accepted by this server',
  observation: 'Observation — recorded calls between services',
  catalog: 'Catalog — contracts discovered in a registry',
};

export function sourceTooltip(src: string): string {
  return SOURCE_DESCRIPTIONS[src] || src;
}

// ── Lock and drift helpers ──

/** Strip sha256: prefix and truncate to first n hex chars for compact digest display. */
export function shortDigest(digest: string | null | undefined, n: number = 8): string {
  if (!digest) return '';
  const hex = digest.replace(/^sha256:/, '');
  return hex.slice(0, n);
}

/**
 * Shorten every content digest embedded in a piece of display text.
 *
 * A canonical revision key carries a 64-hex digest, so any sentence built around one
 * ("these revisions declare different owners: ...") turns into a wall of hex that
 * hides the words that matter. This keeps a recognizable prefix -- still enough to
 * tell two revisions apart at a glance -- and leaves the exact value to the copyable
 * identifier and the tooltip. Text with no digest in it comes back untouched.
 */
export function abbreviateDigests(text: string | null | undefined, n: number = 12): string {
  if (!text) return '';
  return text.replace(/[a-z0-9]+:([0-9a-f]{32,})/g, (_m, hex: string) => `${hex.slice(0, n)}…`);
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

// ── Fleet overview ──

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
 * This is the single source of truth.
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

    // Owner aggregation. Grouped by canonical identity, so a team and a person of
    // the same name get a row each; labelled by name, so neither row shows a reader
    // the encoding.
    const key = ownerKey(svc.owner) || UNOWNED_KEY;
    let agg = ownerMap.get(key);
    if (!agg) {
      agg = { key, label: key === UNOWNED_KEY ? UNOWNED_KEY : ownerKeyLabel(key), kind: ownerKeyKind(key), services: 0, compliant: 0, warning: 0, nonCompliant: 0, reference: 0, unknown: 0, invalid: 0, notEvaluated: 0, runtimeEvaluated: 0, conclusive: 0, totalBlast: 0, maxBlast: 0, compliancePercent: 0, ready: 0, partial: 0, notReady: 0, notConfigured: 0 };
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
    agg.maxBlast = Math.max(agg.maxBlast, (svc.blastRadius as number) || 0);

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
  // Ordered by the name a reader scans for; the key only breaks ties, so a `dri:`
  // owner never sorts below every `team:` one purely because of its encoding.
  m.byOwner = Array.from(ownerMap.values()).sort((a, b) => a.label.localeCompare(b.label) || a.key.localeCompare(b.key));

  // Finalize readiness metrics
  if (m.readiness.configured > 0) {
    m.readiness.avgScore = Math.round(readinessScoreSum / m.readiness.configured);
  }

  // Finalize category aggregation
  m.byCategory = Array.from(categoryMap.values()).sort((a, b) => a.category.localeCompare(b.category));

  return m;
}

// ── Counts ──

const COUNT = new Intl.NumberFormat('en-US');

/** formatCount groups a count's thousands, so 12431 reads 12,431. */
export function formatCount(n: number): string {
  return COUNT.format(n);
}

/**
 * countOf is a count with its noun in the matching form: "1 consumer", "2 consumers",
 * "1,500 Stages". The plural is the singular plus "s" unless one is given.
 */
export function countOf(n: number, one: string, many: string = `${one}s`): string {
  return `${formatCount(n)} ${n === 1 ? one : many}`;
}
