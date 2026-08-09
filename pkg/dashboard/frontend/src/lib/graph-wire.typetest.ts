/**
 * Compile-time architecture guard (reopen section 7): the Operational Graph adapter and the
 * perspective canonicalizers must consume the GENERATED ProductNeighborhood wire type, never
 * a hand-mirrored partial that could drift from the SDK. Type-checked by `npm run lint`
 * (svelte-check, threshold error); it runs no code. If a hand mirror incompatible with the
 * generated type is reintroduced, or an input is widened to `any`, these assertions fail.
 *
 * The renderer-owned GraphData/GraphNode/GraphEdge (graph.ts) are intentionally NOT wire
 * DTOs and are exempt: they are the internal presentation model the adapter targets.
 */
import { neighborhoodToGraph } from './neighborhoodGraph.ts';
import { canonicalFocusForPerspective, projectionFocusMismatch } from './graphState.ts';
import type { CanonNeighborhood, CanonRef } from './graphState.ts';
import type { ProductNeighborhood } from './api.ts';

// ── type-level assertion helpers ─────────────────────────────────────────────
type Expect<T extends true> = T;
type Equal<A, B> =
  (<G>() => G extends A ? 1 : 2) extends (<G>() => G extends B ? 1 : 2) ? true : false;

declare const wireNb: ProductNeighborhood;

// A full generated neighborhood is a valid input to the adapter and both canonicalizers,
// with no cast: the params derive from ProductNeighborhood.
const _adaptWire = neighborhoodToGraph(wireNb);
const _canonWire = canonicalFocusForPerspective(wireNb, 'service', 'service');
const _projWire = projectionFocusMismatch(wireNb, 'service', 'x');
// The canonicalizer input is a genuine structural PROJECTION of the generated type (a real
// subset), so the generated neighborhood is assignable to it.
const _canonSubset: CanonNeighborhood = wireNb;
void _adaptWire; void _canonWire; void _projWire; void _canonSubset;

// CanonRef.kind is the generated ProductRef `kind` enum (not a bare `string`, not `any`);
// were it hand-declared as `string`, this equality would fail.
type _CanonKindIsEnum = Expect<Equal<CanonRef['kind'], NonNullable<ProductNeighborhood['requestedFocus']>['kind']>>;
export type _ReopenSectionSeven = [_CanonKindIsEnum];

// The adapter/canonicalizer inputs are NOT `any`: a non-neighborhood argument is rejected.
// If a future change widened an input to `any`, the directive would be unused and
// svelte-check (threshold error) would fail.
function _notAny(): void {
  // @ts-expect-error a bare string is not a neighborhood-shaped adapter input.
  neighborhoodToGraph('not a neighborhood');
  // @ts-expect-error a bare string is not a neighborhood-shaped canonicalizer input.
  canonicalFocusForPerspective('not a neighborhood', 'service', 'service');
}
void _notAny;

// Non-vacuousness: prove Equal actually rejects a false equality, so the assertion is
// meaningful (were Equal vacuous, this directive would be unused and lint would fail).
// @ts-expect-error string and number are not equal, so this must be a type error.
export type _NonVacuous = Expect<Equal<string, number>>;
