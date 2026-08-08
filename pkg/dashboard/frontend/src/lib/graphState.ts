/**
 * Pure state + presentation helpers for the search-first Operational Graph (Phase 4).
 *
 * The graph is search-first: with no focus it shows a discovery state, never a
 * whole-fleet hairball; with a focus it consumes the PRODUCT neighborhood API (never
 * the FleetSnapshot) for a bounded local neighborhood. This module owns the shareable
 * graph state (URL <-> query input) and the backend-authoritative difference/relation
 * vocabularies the UI renders verbatim (requirement O: never infer a difference from
 * booleans, never color-only).
 */
import type { KnowledgeView, Direction, ProductNeighborhood } from './api.ts';

export const GRAPH_PERSPECTIVES = ['service', 'revision', 'target'] as const;
export type GraphPerspective = (typeof GRAPH_PERSPECTIVES)[number];

// The default focused neighborhood (requirement L): depth 1, both directions, and the
// expected + differences views. Rationale: a newcomer's first question is "what does
// this depend on / what depends on it, and where does intent diverge from observed
// reality" -- expected shows intent, differences surfaces observed-not-expected and
// reconciliation without making the graph an observed-only firehose. Observed is one
// toggle away. Absence of observation is never treated as absence of runtime use.
export const DEFAULT_VIEWS: KnowledgeView[] = ['expected', 'differences'];
export const MAX_DEPTH = 6;

export interface GraphState {
  kind: string;
  key: string;
  perspective: GraphPerspective;
  views: KnowledgeView[];
  direction: Direction;
  depth: number;
}

const KNOWN_VIEWS = new Set<KnowledgeView>(['expected', 'observed', 'differences']);
const KNOWN_DIRECTIONS = new Set<Direction>(['dependencies', 'dependents', 'both']);

/** hasFocus reports whether the route selects a specific entity (vs the discovery
 *  state). A missing kind OR key means discovery -- never a fleet-wide render. */
export function hasFocus(params: Record<string, string>): boolean {
  return !!(params.kind && params.sel);
}

/** graphStateFromParams derives the graph state from route params, applying the
 *  focused defaults and clamping to valid, bounded values. */
export function graphStateFromParams(params: Record<string, string>): GraphState {
  const views = (params.views ? params.views.split(',') : [])
    .map((v) => v.trim() as KnowledgeView)
    .filter((v) => KNOWN_VIEWS.has(v));
  const perspective = GRAPH_PERSPECTIVES.includes(params.perspective as GraphPerspective)
    ? (params.perspective as GraphPerspective)
    : 'service';
  const direction = KNOWN_DIRECTIONS.has(params.direction as Direction)
    ? (params.direction as Direction)
    : 'both';
  const depth = clampDepth(parseInt(params.depth, 10));
  return {
    kind: params.kind || '',
    key: params.sel || '',
    perspective,
    views: views.length ? dedupeViews(views) : [...DEFAULT_VIEWS],
    direction,
    depth,
  };
}

function clampDepth(d: number): number {
  if (!Number.isFinite(d) || d < 1) return 1;
  return Math.min(MAX_DEPTH, Math.trunc(d));
}

function dedupeViews(views: KnowledgeView[]): KnowledgeView[] {
  const out: KnowledgeView[] = [];
  for (const v of views) if (!out.includes(v)) out.push(v);
  return out;
}

/** toggleView returns the views with `v` added or removed, never producing an empty
 *  set (a graph with no knowledge view is meaningless, so the last view stays on). */
export function toggleView(views: KnowledgeView[], v: KnowledgeView): KnowledgeView[] {
  if (views.includes(v)) {
    const next = views.filter((x) => x !== v);
    return next.length ? next : views;
  }
  // Preserve a stable order: expected, observed, differences.
  const order: KnowledgeView[] = ['expected', 'observed', 'differences'];
  return order.filter((x) => views.includes(x) || x === v);
}

// ── backend-authoritative difference vocabulary (rendered verbatim) ───────────

export type EdgeDifference = 'matched' | 'expected-not-observed' | 'observed-not-expected' | 'insufficient';

export function differenceLabel(d: string | undefined): string {
  switch (d) {
    case 'matched': return 'Matched';
    case 'expected-not-observed': return 'Expected, not observed';
    case 'observed-not-expected': return 'Observed, not expected';
    case 'insufficient': return 'Insufficient evidence';
    default: return '';
  }
}

export function differenceTone(d: string | undefined): 'ok' | 'warn' | 'info' | 'neutral' {
  switch (d) {
    case 'matched': return 'ok';
    case 'observed-not-expected': return 'warn';
    case 'expected-not-observed': return 'info';
    default: return 'neutral';
  }
}

export function differenceDescription(d: string | undefined): string {
  switch (d) {
    case 'matched': return 'Declared and corroborated by observed traffic.';
    case 'expected-not-observed': return 'Declared, but not witnessed in the observation window. This is not proof it is unused.';
    case 'observed-not-expected': return 'Observed at runtime but never declared.';
    case 'insufficient': return 'Declared, but there is no observation data to reconcile against.';
    default: return '';
  }
}

export function relationLabel(rel: string | undefined): string {
  return rel === 'runs' ? 'Runs' : 'Depends on';
}

/** neighborhoodIsEmpty reports a focused neighborhood that resolved to just the focus
 *  node (no neighbors), so the UI can say so honestly rather than showing a bare dot. */
export function neighborhoodIsEmpty(nb: ProductNeighborhood | null | undefined): boolean {
  return !!nb && (nb.edges?.length ?? 0) === 0 && (nb.unresolvedDependencies?.count ?? 0) === 0;
}
