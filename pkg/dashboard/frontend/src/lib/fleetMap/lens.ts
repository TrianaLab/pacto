/**
 * What each lens shows (mockup, computeVisibility). The lens only decides
 * visibility: it never moves a node, so switching it keeps the reader's place.
 * Both shows everything; Expected shows what contracts declare (inferred
 * services hidden); Observed shows what Pacto can see (a service it cannot
 * observe is a grey ghost, a declared one Pacto sees not running is hidden).
 */
import { countOf, formatCount } from '../format';
import { degradedSourceSummary, snapshotKnowledge } from '../knowledgeState';
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

/**
 * edgeVisible says whether the lens shows a dependency whose ends it shows:
 * Expected keeps what contracts declare, Observed what Pacto sees.
 */
export function edgeVisible(e: MapEdge, lens: Lens): boolean {
  // The match state is what the ribbons count: a runtime-inferred call has no
  // difference but is observed-only.
  if (lens === 'expected') return !e.inferred && e.difference !== 'observed-not-expected' && e.match !== 'observed-only';
  if (lens === 'observed') return e.difference === 'matched' || e.difference === 'observed-not-expected' || e.match === 'observed-only';
  return true;
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
  // An environment's nodes are targets, never expected-only: its expected-only
  // tally counts services that declare it and run elsewhere, which are not drawn.
  if (lens === 'observed' && g.kind !== 'environment' && g.kind !== 'previews') return Math.max(0, g.services - (g.match.services.expectedOnly ?? 0));
  return g.services;
}

/** dependencyCount is how many of the fleet's dependencies the lens shows. */
export function dependencyCount(a: FleetMapAnswer, lens: Lens): number {
  const d = a.match.dependencies;
  const m = d.matched ?? 0, eo = d.expectedOnly ?? 0, oo = d.observedOnly ?? 0, ins = d.insufficient ?? 0;
  return lens === 'expected' ? m + eo + ins : lens === 'observed' ? m + oo : m + eo + oo + ins;
}

const LENS_WORDS: Record<Lens, string> = { both: 'expected and observed', expected: 'expected (declared in contracts)', observed: 'observed and inferred' };
const SPECIAL = new Set(['external', 'unowned', 'none', 'unknown', 'other']);
/** A special group (externals, no owner, no domain, unknown, folded) sits at the rim and draws no match ring. */
export const isSpecial = (g: MapGroup): boolean => SPECIAL.has(g.kind);

/** summary is the sentence over the map: what the lens shows and how it is grouped. */
export function summary(a: FleetMapAnswer, lens: Lens): string {
  const gs = a.groups ?? [];
  const n = gs.reduce((s, g) => s + groupCount(g, lens), 0);
  const real = gs.filter((g) => !isSpecial(g));
  let where: string;
  if (a.groupBy === 'team' && a.nested) {
    where = `${countOf(real.reduce((s, g) => s + (g.subs?.length || 1), 0), 'team')} across ${countOf(real.length, 'domain')}`;
  } else if (a.groupBy === 'environment') {
    const previews = real.filter((g) => g.kind === 'previews').reduce((s, g) => s + (g.subs?.length ?? 0), 0);
    where = countOf(real.filter((g) => g.kind !== 'previews').length, 'environment') + (previews ? ` and ${countOf(previews, 'preview')}` : '');
  } else {
    where = a.groupBy === 'team' ? countOf(real.length, 'team') : countOf(real.length, 'domain');
  }
  return `Showing ${countOf(n, 'service')} and ${countOf(dependencyCount(a, lens), 'dependency', 'dependencies')}, ${LENS_WORDS[lens]}, in ${where}.`;
}

/**
 * mapNote is what the reader must know to trust the map, under the summary:
 * when it was read, whether every source was complete and what the answer
 * left out or could not tell (hidden services, environments by name, folded
 * groups, dropped links). Empty parts are left out.
 */
export function mapNote(a: FleetMapAnswer, asOf: (iso: string) => string): string {
  const k = snapshotKnowledge(a.meta);
  const codes = new Set([...(a.meta?.limitations ?? []), ...(a.limitations?.items ?? [])].map((l) => l.code));
  const parts = [
    a.meta?.asOf ? `As of ${asOf(a.meta.asOf)}` : '',
    k.incomplete ? `partial: ${degradedSourceSummary(k) || 'not every source is complete'}` : '',
    codes.has('HIDDEN_BY_PERMISSIONS') ? 'some services are hidden from you' : '',
    a.groupBy === 'environment' && a.environmentOrder === 'name' ? 'environments read by name, as no Stage you may read orders them' : '',
    a.totals.folded ? `the ${formatCount(a.totals.folded)} smallest groups are folded into Other` : '',
    a.ribbonsTruncated ? `the ${formatCount(a.totals.ribbons - (a.ribbons?.length ?? 0))} smallest links between groups are left out` : '',
  ].filter(Boolean);
  return parts.length ? `${parts.join('; ')}.` : '';
}
