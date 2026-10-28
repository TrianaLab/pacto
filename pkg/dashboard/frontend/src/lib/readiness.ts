/** The readiness model: buckets, gate classes and per-check status of a readiness assessment. */

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

export type WithReadiness = { readiness?: ReadinessInfo | null };

export type ReadinessBucket = 'ready' | 'partial' | 'not-ready' | 'unknown';

/**
 * Bucket a service's overall readiness. The contract's gate (`passing`,
 * i.e. score >= minScore) is the primary signal; score bands break the rest.
 * `unknown` means no readiness block is declared (not configured) — never an error.
 */
export function readinessBucket(svc: WithReadiness): ReadinessBucket {
  const r = svc?.readiness;
  if (!r) return 'unknown';
  // An expired assessment proves nothing about today, whatever it scored, so it is
  // never Ready; it reads by its score like any assessment below the gate. The engine
  // already reports passing=false then; this holds when a payload says otherwise.
  // ponytail: no Expired bucket of its own; the Expiry column says it in red.
  if (r.passing && !r.expired) return 'ready';
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
  if (r.passing && !r.expired) return 'score-ok';
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
