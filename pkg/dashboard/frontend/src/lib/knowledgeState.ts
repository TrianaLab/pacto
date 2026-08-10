/**
 * Truthful knowledge-state decision (Phase 2, requirement H).
 *
 * The dashboard must never turn a lack of knowledge into a claim of health. A
 * partial, stale or degraded snapshot with zero known attention items is NOT the
 * same as "everything is healthy": we simply cannot see everything. This module is
 * the ONE place that decision is made, so no view invents an ad-hoc "All clear"
 * string. Views derive their empty/degraded rendering from these pure functions;
 * ProductEmptyState renders the result.
 *
 * The inputs are structural (not tied to a specific DTO) so the same decision serves
 * the overview, entity search, entity detail and any future list.
 */

import { ApiError, SchemaCompatibilityError, ApiContractError } from './api.ts';

/**
 * CompletenessLevel is the worst knowledge level a snapshot's sources imply.
 *
 * `empty` is a DISTINCT level from `unknown`: it is the backend `empty`
 * completeness (all sources healthy, but no record exists), i.e. a fully-understood
 * empty fleet. It is NOT incomplete -- knowledge is complete, there is simply
 * nothing. `unknown` is the opposite: we never received a completeness we could
 * assert (a missing meta), which IS incomplete. Conflating the two would render a
 * confidently-empty fleet as "knowledge unavailable".
 */
export type CompletenessLevel = 'complete' | 'empty' | 'partial' | 'stale' | 'unavailable' | 'unknown';

export interface SnapshotKnowledge {
  level: CompletenessLevel;
  degradedSources: number;
  staleSources: number;
  unavailableSources: number;
  /** incomplete is true when knowledge is anything less than fully complete, so a
   *  caller can gate "all clear" on it without re-checking every counter. A
   *  fully-understood empty snapshot (`empty`) is COMPLETE knowledge, so it is NOT
   *  incomplete. */
  incomplete: boolean;
}

/** A minimal structural view of a product meta envelope (ProductMeta). */
interface MetaLike {
  completeness?: string;
  sources?: Array<{ status?: string } | null> | null;
  sourcesTruncated?: boolean;
}

/**
 * snapshotKnowledge derives a snapshot's knowledge quality from its product meta:
 * the declared completeness AND the per-source health. A source that is partial,
 * stale or unavailable makes knowledge incomplete even if `completeness` says
 * otherwise, so the strictest signal wins. A missing meta is `unknown` (we cannot
 * assert completeness we never received) and therefore incomplete.
 */
export function snapshotKnowledge(meta: MetaLike | null | undefined): SnapshotKnowledge {
  if (!meta) {
    return { level: 'unknown', degradedSources: 0, staleSources: 0, unavailableSources: 0, incomplete: true };
  }
  let degraded = 0, stale = 0, unavailable = 0;
  for (const s of meta.sources ?? []) {
    switch (s?.status) {
      case 'partial': degraded++; break;
      case 'stale': stale++; break;
      case 'unavailable': unavailable++; break;
    }
  }
  // Strictest-first: an unavailable source is the worst, then stale, then partial.
  // A degraded source ALWAYS wins over the declared completeness (so a source that
  // is down is never masked by a snapshot that happens to say `empty`/`complete`).
  // Fall back to the declared completeness when no source is individually degraded:
  // `empty` (all sources healthy, no records) is a distinct, COMPLETE-knowledge
  // level, never `unknown`; a missing/unrecognized completeness is `unknown`.
  let level: CompletenessLevel;
  if (unavailable > 0) level = 'unavailable';
  else if (stale > 0) level = 'stale';
  else if (degraded > 0) level = 'partial';
  else if (meta.completeness === 'partial') level = 'partial';
  else if (meta.completeness === 'complete') level = 'complete';
  else if (meta.completeness === 'empty') level = 'empty';
  else level = 'unknown';
  return {
    level, degradedSources: degraded, staleSources: stale, unavailableSources: unavailable,
    incomplete: level !== 'complete' && level !== 'empty',
  };
}

/**
 * classifyError maps a caught error to the honest failure kind. A 404 is a real
 * not-found; a schema/contract violation is a distinct incompatibility the user must
 * see (reload/upgrade); anything else transport-level is backend-unavailable.
 */
export type ErrorKind = 'not-found' | 'schema-error' | 'backend-error';
export function classifyError(err: unknown): ErrorKind {
  if (err instanceof SchemaCompatibilityError || err instanceof ApiContractError) return 'schema-error';
  if (err instanceof ApiError && err.status === 404) return 'not-found';
  return 'backend-error';
}

/**
 * ViewState is the reusable UI state a list/detail view renders. Every empty screen
 * is disambiguated: a genuinely empty fleet, a filter that matched nothing, and
 * "nothing known under incomplete knowledge" are three different truths.
 */
export type ViewState =
  | { kind: 'loading' }
  | { kind: 'backend-error'; message: string }
  | { kind: 'schema-error'; message: string }
  | { kind: 'not-found'; message: string }
  | { kind: 'empty-fleet' }        // no items, knowledge complete/empty -> genuinely empty
  | { kind: 'empty-unknown'; knowledge: SnapshotKnowledge } // no items, knowledge incomplete
  // A filter/search matched nothing. It carries the snapshot knowledge so a caller
  // can STILL surface an incompleteness caveat: "no records match this filter" and
  // "knowledge is incomplete" are both true and neither may hide the other.
  | { kind: 'filtered-empty'; knowledge: SnapshotKnowledge }
  // has data (possibly degraded). `revalidating` marks a refresh in flight OVER that
  // data and `refreshError` a refresh that failed over it -- both are reported
  // alongside the data rather than instead of it.
  | { kind: 'ready'; knowledge: SnapshotKnowledge; revalidating?: boolean; refreshError?: unknown };

export interface ViewStateInput {
  loading: boolean;
  error?: unknown;
  itemCount: number;
  /** filtered is true when a search/filter is active, so 0 results is "no matches",
   *  not "empty fleet". */
  filtered?: boolean;
  knowledge?: SnapshotKnowledge;
}

/** decideViewState is the single list/detail state machine (requirement H). */
export function decideViewState(input: ViewStateInput): ViewState {
  const knowledge = input.knowledge ?? snapshotKnowledge(null);

  // STALE-WHILE-REVALIDATE, decided once for every view.
  //
  // Every product view is polled: App.loadGlobal() advances refreshTick on a timer and
  // each view re-runs its query against the SAME question. Ranking "a request is in
  // flight" above "we already have the answer" is what made the entity body, the
  // service list and the graph disappear every few seconds -- the content unmounts,
  // the document shortens, and the browser clamps the reader's scroll position toward
  // the top. A failed refresh was worse still: good data became a not-found or
  // backend-error screen.
  //
  // Data on hand outranks a request in flight and outranks that request's failure.
  // The caller is told both facts through `revalidating` / `refreshError` so it can
  // say so honestly without throwing the page away. The caller is responsible for
  // ensuring itemCount describes the CURRENT question -- data for a question the user
  // has navigated away from must not be counted here.
  if (input.itemCount > 0) {
    const out: ViewState = { kind: 'ready', knowledge };
    if (input.loading) out.revalidating = true;
    if (input.error != null) out.refreshError = input.error;
    return out;
  }

  if (input.loading) return { kind: 'loading' };
  if (input.error != null) {
    const msg = input.error instanceof Error ? input.error.message : String(input.error);
    switch (classifyError(input.error)) {
      case 'not-found': return { kind: 'not-found', message: msg };
      case 'schema-error': return { kind: 'schema-error', message: msg };
      default: return { kind: 'backend-error', message: msg };
    }
  }
  // Nothing on hand, nothing in flight, no failure: the emptiness itself is the answer.
  // A filter matched nothing: distinct from an empty fleet, and it still carries the
  // knowledge so an incompleteness caveat is not hidden by the filtered-empty state.
  if (input.filtered) return { kind: 'filtered-empty', knowledge };
  // The non-negotiable rule: no items under INCOMPLETE knowledge is not "empty",
  // it is "nothing known that we can see" -- never an all-clear. A fully-understood
  // empty snapshot (complete/empty knowledge) is a genuine empty-fleet.
  if (knowledge.incomplete) return { kind: 'empty-unknown', knowledge };
  return { kind: 'empty-fleet' };
}

/**
 * allClearAllowed gates any blanket "all clear / everything healthy" affordance on
 * COMPLETE knowledge with zero attention. Under partial/stale/unavailable/unknown
 * knowledge it returns false, so the caller shows "no attention items known" with a
 * visible incompleteness caveat instead of asserting health (requirement H).
 */
export function allClearAllowed(knowledge: SnapshotKnowledge, attentionCount: number): boolean {
  return !knowledge.incomplete && attentionCount <= 0;
}
