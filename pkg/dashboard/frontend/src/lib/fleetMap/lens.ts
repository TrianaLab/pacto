/**
 * What each lens shows (mockup, computeVisibility). The lens only decides
 * visibility: it never moves a node, so switching it keeps the reader's place.
 * Both shows everything; Expected shows what contracts declare (inferred
 * services hidden); Observed shows what Pacto can see (a service it cannot
 * observe is a grey ghost, a declared one Pacto sees not running is hidden).
 */
import type { FleetMapAnswer, FleetMapNodesPage } from '../api';

export type Lens = 'both' | 'expected' | 'observed';
export const LENSES: readonly Lens[] = ['both', 'expected', 'observed'];

export type MapGroup = NonNullable<FleetMapAnswer['groups']>[number];
export type MapRibbon = NonNullable<FleetMapAnswer['ribbons']>[number];
export type MapNode = NonNullable<FleetMapNodesPage['nodes']>[number];
export type MapEdge = NonNullable<FleetMapNodesPage['edges']>[number];

/** Visibility of a node: 0 hidden, 1 shown, 2 ghost (drawn as grey context). */
export type Visibility = 0 | 1 | 2;

export function nodeVisibility(n: MapNode, lens: Lens): Visibility {
  if (lens === 'expected') return n.inferred ? 0 : 1;
  if (lens === 'observed' && !n.external) {
    if (n.match === 'insufficient') return 2;
    if (n.match === 'expected-only') return 0;
  }
  return 1;
}

/** ribbonCount is how many of a ribbon's dependencies the lens shows. */
export function ribbonCount(r: MapRibbon, lens: Lens): number {
  const m = r.matched ?? 0;
  if (lens === 'expected') return m + (r.expectedOnly ?? 0) + (r.insufficient ?? 0);
  if (lens === 'observed') return m + (r.observedOnly ?? 0);
  return r.total;
}

/** groupCount is how many of a group's nodes the lens shows (ghosts count: they are drawn). */
export function groupCount(g: MapGroup, lens: Lens): number {
  if (lens === 'expected') return g.services - g.inferred;
  if (lens === 'observed') return g.services - (g.match.services.expectedOnly ?? 0);
  return g.services;
}
