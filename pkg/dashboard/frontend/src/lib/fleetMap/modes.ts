/**
 * The fleet map's emphasis modes (mockup, computeEmphasis). A mode never hides
 * or moves anything: what it does not emphasise stays as grey context.
 *
 * Only problems emphasises services with an error or a warning and the
 * dependencies that carry a problem. Gaps emphasises each chosen kind of
 * difference between what contracts declare and what Pacto observes. Missing
 * emphasises services whose contract leaves out a section (or a chosen one),
 * so the complete ones are dimmed.
 */
import { countOf, formatCount } from '../format';
import type { FleetMapAnswer } from '../api';
import type { MapEdge, MapGroup, MapNode, MapRibbon } from './lens';

export type GapKind = 'expected-only' | 'observed-only' | 'insufficient';
export const GAP_KINDS: readonly GapKind[] = ['expected-only', 'observed-only', 'insufficient'];

export type Mode =
  | { kind: 'problems' }
  | { kind: 'gaps'; gaps: ReadonlySet<GapKind> }
  | { kind: 'missing'; section: string }
  | null;

/** The contract sections a service can leave out, in the checklist's order, as a reader names them. */
export const MISSING_SECTIONS: Record<string, string> = {
  interfaces: 'interface documents', configuration: 'configuration schema', dependencies: 'dependency ranges',
  owner: 'owner', summary: 'summary', domain: 'domain', runtime: 'health and runtime', readiness: 'readiness',
};

/** An edge's difference as the gap kind it is. */
const EDGE_GAP: Record<string, GapKind> = { 'expected-not-observed': 'expected-only', 'observed-not-expected': 'observed-only', insufficient: 'insufficient' };
/** A gap kind as its ribbon and tally field. */
const FIELD: Record<GapKind, 'expectedOnly' | 'observedOnly' | 'insufficient'> = { 'expected-only': 'expectedOnly', 'observed-only': 'observedOnly', insufficient: 'insufficient' };

/** modeFrom reads the mode from the map's hash query (gaps, only, missing). */
export function modeFrom(p: Record<string, string | undefined>): Mode {
  if (p.gaps) {
    const gaps = new Set(p.gaps === 'all' ? GAP_KINDS : GAP_KINDS.filter((k) => p.gaps!.split(',').includes(k)));
    if (gaps.size) return { kind: 'gaps', gaps };
  }
  if (p.only === 'problems') return { kind: 'problems' };
  if (p.missing) return { kind: 'missing', section: p.missing };
  return null;
}

/** modeParams writes a mode back as hash query keys; the others are cleared. */
export function modeParams(m: Mode): { only: string | null; gaps: string | null; missing: string | null } {
  const gaps = m?.kind === 'gaps' ? (m.gaps.size === GAP_KINDS.length ? 'all' : GAP_KINDS.filter((k) => m.gaps.has(k)).join(',')) : null;
  return { only: m?.kind === 'problems' ? 'problems' : null, gaps, missing: m?.kind === 'missing' ? m.section : null };
}

/** toggleGap switches one gap kind; the last one stays, so the mode always shows something. */
export function toggleGap(gaps: ReadonlySet<GapKind>, k: GapKind): Set<GapKind> {
  const out = new Set(gaps);
  if (out.has(k) && out.size > 1) out.delete(k);
  else out.add(k);
  return out;
}

const isProblem = (sev: string | undefined) => sev === 'error' || sev === 'warning';

/** nodeEmphasis says whether the mode emphasises a service (true with no mode). */
export function nodeEmphasis(n: MapNode, m: Mode): boolean {
  if (!m) return true;
  if (m.kind === 'problems') return isProblem(n.severity);
  if (m.kind === 'gaps') return !!n.match && m.gaps.has(n.match as GapKind);
  // An unjudged service (no completeness) is not complete, nor known to miss anything.
  const missing = n.missing ?? [];
  return m.section === 'any' ? missing.length > 0 : missing.includes(m.section);
}

/** edgeEmphasis says whether the mode emphasises a dependency (true with no mode). */
export function edgeEmphasis(e: MapEdge, m: Mode): boolean {
  if (!m) return true;
  if (m.kind === 'problems') return !!e.problem;
  if (m.kind === 'gaps') return m.gaps.has((e.match as GapKind | undefined) ?? EDGE_GAP[e.difference ?? '']);
  return false;
}

/** gapCount is how many services and dependencies of a match tally are of the given gap kinds. */
export const gapCount = (t: MapGroup['match'], gaps: Iterable<GapKind>): number =>
  [...gaps].reduce((a, k) => a + (t.services[FIELD[k]] ?? 0) + (t.dependencies[FIELD[k]] ?? 0), 0);

/** groupEmphasis says whether a group holds anything the mode emphasises. */
export function groupEmphasis(g: MapGroup, m: Mode): boolean {
  if (!m) return true;
  if (m.kind === 'problems') return g.attention.errors + g.attention.warnings > 0;
  if (m.kind === 'gaps') return gapCount(g.match, m.gaps) > 0;
  // Like a service: only a group with a service that misses the section is
  // emphasised, never one whose services are unjudged.
  const missing = g.missing ?? [];
  return m.section === 'any' ? missing.length > 0 : missing.some((c) => c.section === m.section && c.count > 0);
}

/** groupModeLine is the line a group bubble shows under its count in a mode, or null. */
export function groupModeLine(g: MapGroup, m: Mode): string | null {
  if (!m) return null;
  if (m.kind === 'problems') return `${formatCount(g.attention.errors + g.attention.warnings)} problems`;
  if (m.kind === 'gaps') return countOf(gapCount(g.match, m.gaps), 'gap');
  return g.percentComplete == null ? 'not judged' : `${g.percentComplete}% complete`;
}

/** ribbonEmphasis is how many of a ribbon's dependencies the mode emphasises. */
export function ribbonEmphasis(r: MapRibbon, m: Mode): number {
  if (!m) return r.total;
  if (m.kind === 'problems') return r.problems ?? 0;
  if (m.kind === 'gaps') return [...m.gaps].reduce((a, k) => a + (r[FIELD[k]] ?? 0), 0);
  return 0;
}

export interface EnvironmentChoice { value: string; label: string; preview?: boolean }

/**
 * environmentChoices lists the environments of an environment-grouped answer
 * for the picker: in the answer's column order (from the Stages the reader may
 * read, or by name when it says so), then each preview.
 */
export function environmentChoices(a: FleetMapAnswer | null): EnvironmentChoice[] {
  const gs = a?.groups ?? [];
  const main = gs.filter((g) => g.kind === 'environment')
    .map((g, i) => ({ g, i }))
    .sort((x, y) => (x.g.depth ?? Infinity) - (y.g.depth ?? Infinity) || x.i - y.i)
    .map(({ g }) => ({ value: g.key, label: g.label }));
  const previews = gs.filter((g) => g.kind === 'previews').flatMap((g) => g.subs ?? [])
    .map((s) => ({ value: s.key, label: s.label ?? s.key, preview: true }));
  return [...main, ...previews];
}
