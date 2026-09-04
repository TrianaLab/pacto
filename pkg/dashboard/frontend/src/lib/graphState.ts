/**
 * Pure state + presentation helpers for the search-first Operational Graph.
 *
 * The graph is search-first: with no focus it shows a discovery state, never a
 * whole-fleet hairball; with a focus it consumes the PRODUCT neighborhood API (never
 * the FleetSnapshot) for a bounded local neighborhood. This module owns the shareable
 * graph state (URL <-> query input) and the backend-authoritative difference/relation
 * vocabularies the UI renders verbatim (never infer a difference from
 * booleans, never color-only).
 */
import type { KnowledgeView, Direction, ProductNeighborhood } from './api.ts';

export const GRAPH_PERSPECTIVES = ['service', 'revision', 'target'] as const;
export type GraphPerspective = (typeof GRAPH_PERSPECTIVES)[number];

// The default focused neighborhood: depth 1, both directions, and the
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
// The one home for this mapping. It is the reconciliation of an EXPECTED (declared)
// dependency against OBSERVED runtime traffic (ADR-3), and every surface that shows a
// difference -- the graph drawer, the graph's edge list, the entity page's relationship
// list -- reads it from here. A second copy in entityLabels.ts had drifted into the
// OPPOSITE tones, so the same edge was amber on the canvas and blue in the list.

export type EdgeDifference = 'matched' | 'expected-not-observed' | 'observed-not-expected' | 'insufficient';

export function differenceLabel(d: string | undefined): string {
  switch (d) {
    case 'matched': return 'Matched';
    case 'expected-not-observed': return 'Expected, not observed';
    case 'observed-not-expected': return 'Observed, not expected';
    case 'insufficient': return 'Insufficient evidence';
    // An unrecognised wire value prints as itself, never as an empty pill: the
    // badge draws its border regardless, so '' renders a blank box.
    default: return d || 'Unknown';
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

// ── focus/perspective validity ────────────────────────────────
// Backend validation is stricter than "show every button": a service cannot be
// projected as one revision or one target, and a revision perspective needs an
// authoritatively-linked revision. These helpers make ordinary navigation unable to
// produce a backend 422 -- the UI only offers transitions the backend will accept.

/** defaultPerspectiveForKind chooses the projection a search result opens in, from its
 *  entity kind: a revision result opens the revision projection, a target result the
 *  target projection, everything else the service projection. */
export function defaultPerspectiveForKind(kind: string): GraphPerspective {
  switch (kind) {
    case 'revision': return 'revision';
    case 'target': return 'target';
    default: return 'service';
  }
}

/** availablePerspectives lists the perspectives valid for a focus of the given kind.
 *  A target can be projected as a revision only when its revision link is
 *  authoritative (exact/inferred), so that button is offered only then -- never
 *  producing a 422 through the primary control. */
export function availablePerspectives(
  kind: string,
  opts: { targetRevisionAuthoritative?: boolean } = {},
): GraphPerspective[] {
  switch (kind) {
    case 'revision': return ['service', 'revision'];
    case 'target': return opts.targetRevisionAuthoritative ? ['service', 'revision', 'target'] : ['service', 'target'];
    case 'service': return ['service'];
    default: return ['service'];
  }
}

/** revisionLinkAuthoritative reports whether a target's revision-link state permits a
 *  revision projection (an exact or inferred link). */
export function revisionLinkAuthoritative(revisionState: string | undefined): boolean {
  return revisionState === 'exact' || revisionState === 'inferred';
}

// The canonicalizer reads only these fields of the neighborhood; their TYPES are DERIVED
// from the generated ProductNeighborhood via indexed access + Pick, never
// a hand-mirrored wire shape with primitive fields that could drift from the SDK. CanonRef
// narrows a generated ProductRef to the two fields the canonicalizer uses, keeping its
// `kind` the generated enum (not a bare string).
export type CanonRef = Pick<NonNullable<ProductNeighborhood['projectionFocus']>, 'kind' | 'key'>;
type CanonEdge = { relation?: NonNullable<ProductNeighborhood['edges']>[number]['relation']; to?: CanonRef };
export interface CanonNeighborhood {
  focusService?: CanonRef | null;
  projectionFocus?: CanonRef | null;
  edges?: readonly CanonEdge[] | null;
}

/** canonicalFocusForPerspective returns the canonical {kind, key} the graph should focus
 *  when switching to `perspective`, or null to keep the current focus. A perspective that
 *  reinterprets identity (target->service, target->revision, revision->service) MUST
 *  canonicalize the URL to the entity actually projected, so the URL never disagrees with
 *  the visible graph and RequestedFocus is never silently reinterpreted. The canonical
 *  identity is read from the CURRENT neighborhood's backend data
 *  (its focusService, or the runs edge's linked revision), never inferred from labels. */
export function canonicalFocusForPerspective(
  nb: CanonNeighborhood | null | undefined,
  currentKind: string,
  perspective: GraphPerspective,
): { kind: string; key: string } | null {
  if (!nb) return null;
  if (perspective === 'service') {
    const svc = nb.focusService;
    return svc?.kind && svc.key ? { kind: svc.kind, key: svc.key } : null;
  }
  if (perspective === 'revision' && currentKind === 'target') {
    // The linked revision is the "runs" edge's target (a backend ProductRef).
    const runs = (nb.edges ?? []).find((e) => e?.relation === 'runs');
    const rev = runs?.to;
    return rev?.kind && rev.key ? { kind: rev.kind, key: rev.key } : null;
  }
  // revision->revision, target->target, service->service keep the current identity.
  return null;
}

/** projectionFocusMismatch reports the canonical {kind, key} the URL should adopt when
 *  the backend projected a DIFFERENT entity than the URL focus (e.g. a bookmarked
 *  target URL under the revision perspective, which the backend resolves to the linked
 *  revision). It reads the explicit ProjectionFocus the backend supplies, so an old deep
 *  link canonicalizes on load rather than showing a URL that disagrees with the graph.
 *  Returns null when the projection focused exactly the requested entity. */
export function projectionFocusMismatch(
  nb: CanonNeighborhood | null | undefined,
  currentKind: string,
  currentKey: string,
): { kind: string; key: string } | null {
  const pf = nb?.projectionFocus;
  if (!pf?.kind || !pf.key) return null;
  if (pf.kind === currentKind && pf.key === currentKey) return null;
  return { kind: pf.kind, key: pf.key };
}

/** perspectiveSupportsDepth reports whether a perspective has a real bounded depth
 *  model. The target projection is intentionally one hop (a deployment runs a revision
 *  and requires services; deeper exploration is the revision perspective's job), so
 *  its depth/expand controls are disabled rather than left inert. */
export function perspectiveSupportsDepth(perspective: string): boolean {
  return perspective !== 'target';
}

// ── service-scoped corroboration (rendered, never re-inferred) ─────────────────
// A fine-grained (revision/target) dependency edge is never marked observed; the
// backend surfaces the SERVICE-scoped reconciliation as context. These render that
// verbatim and clearly scoped, so the UI never claims the fine-grained edge itself was
// observed.

export function corroborationLabel(c: string | undefined): string {
  switch (c) {
    case 'matched': return 'Service traffic corroborates it';
    case 'expected-not-observed': return 'Declared, service traffic did not witness it';
    case 'insufficient': return 'No service observation to corroborate';
    default: return '';
  }
}

export function corroborationTone(c: string | undefined): 'ok' | 'warn' | 'info' | 'neutral' {
  switch (c) {
    case 'matched': return 'ok';
    case 'expected-not-observed': return 'info';
    default: return 'neutral';
  }
}

/** serviceScopedCaveat explains, for a fine-grained projection, that any corroboration
 *  is service-scoped and not an edge-scope observation claim. */
export function serviceScopedCaveat(perspective: string): string {
  if (perspective === 'revision') {
    return 'Observation is recorded per service, so this is service-scoped corroboration, not proof this specific revision edge was observed.';
  }
  if (perspective === 'target') {
    return 'Observation is recorded per service, so this is service-scoped corroboration, not proof this specific deployment edge was observed.';
  }
  return '';
}

/** neighborhoodIsEmpty reports a focused neighborhood that resolved to just the focus
 *  node (no neighbors), so the UI can say so honestly rather than showing a bare dot.
 *  It reads only the two structural fields it needs, so any neighborhood-shaped value
 *  (the full ProductNeighborhood or a partial) satisfies it. */
export function neighborhoodIsEmpty(
  nb: { edges?: unknown[]; unresolvedDependencies?: { count?: number } } | null | undefined,
): boolean {
  return !!nb && (nb.edges?.length ?? 0) === 0 && (nb.unresolvedDependencies?.count ?? 0) === 0;
}
