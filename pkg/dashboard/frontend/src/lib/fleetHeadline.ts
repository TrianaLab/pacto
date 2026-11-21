/**
 * The fleet's two figures (509-S12): the declared <-> observed match and contract
 * completeness, as the Overview and Metrics say them, and their weekly trend from the
 * coverage records of /api/stats. Pure and small, so the Overview's first load carries
 * only what its Contract completeness section needs. Unknown is never a number.
 */
import type { ProductStats } from './api.ts';
import { formatCount } from './format.ts';

type Bucket = NonNullable<ProductStats['buckets']>[number];

/** The limitation a reader who cannot see every service gets instead of coverage history. */
export const COVERAGE_HIDDEN = 'COVERAGE_OUTSIDE_YOUR_VIEW';

type CoverageRecord = NonNullable<Bucket['coverage']>;
/** A match tally's four states, for services or for dependencies. */
export interface MatchCounts { matched: number; expectedOnly: number; observedOnly: number; insufficient: number }
/** The match figure: fleet.MatchTally's shape, from the overview or from a coverage record. */
export interface MatchFigure { services: MatchCounts; dependencies: MatchCounts; percent?: number; coverage?: number }
/** The completeness figure: fleet.FleetCompleteness's shape; a coverage record has no most-missing list. */
export interface CompletenessFigure { percentComplete?: number; judged: number; unknown: number; fullyDescribed: number; mostMissing?: { section: string; count: number }[] | null }

/** matchOfCoverage reads a coverage record's eight match counters as the tally the server
 *  sends: matched over what Pacto can observe and the observable share of all, both rounded
 *  down like the server, both absent over nothing (never 100%). null when the record has none. */
export function matchOfCoverage(c: CoverageRecord['match'] | null | undefined): MatchFigure | null {
  if (!c) return null;
  const services = { matched: c.servicesMatched, expectedOnly: c.servicesExpectedOnly, observedOnly: c.servicesObservedOnly, insufficient: c.servicesInsufficient };
  const dependencies = { matched: c.dependenciesMatched, expectedOnly: c.dependenciesExpectedOnly, observedOnly: c.dependenciesObservedOnly, insufficient: c.dependenciesInsufficient };
  const matched = services.matched + dependencies.matched;
  const observable = matched + services.expectedOnly + dependencies.expectedOnly + services.observedOnly + dependencies.observedOnly;
  const all = observable + services.insufficient + dependencies.insufficient;
  return {
    services, dependencies,
    ...(observable ? { percent: Math.floor((matched * 100) / observable) } : {}),
    ...(all ? { coverage: Math.floor((observable * 100) / all) } : {}),
  };
}

/** The sentence a reader who cannot see every service reads instead of a trend. */
export const TREND_RESTRICTED = 'The trend counts every service, so only a reader who can see every service sees it.';

/** trendHidden reports that the coverage history is outside the reader's view. */
export function trendHidden(st: ProductStats): boolean {
  return (st.limitations ?? []).some((l) => l.code === COVERAGE_HIDDEN);
}

/** matchTrend is the match percentage per bucket, null (a gap) where a bucket has no
 *  coverage record, no match counters or nothing observable. */
export function matchTrend(st: ProductStats): (number | null)[] {
  return (st.buckets ?? []).map((b) => matchOfCoverage(b.coverage?.match)?.percent ?? null);
}

/** completenessTrend is the fleet's contract completeness per bucket, null where unknown. */
export function completenessTrend(st: ProductStats): (number | null)[] {
  return (st.buckets ?? []).map((b) => b.coverage?.contracts?.percentComplete ?? null);
}

export interface FigureSegment { label: string; value: number; tone?: string; hatch?: boolean }
/** What a figure tile says: its value, the words after it, a sub-line and the segment bar. */
export interface FigureTile { value: string; unknown: boolean; note: string; sub: string; segments: FigureSegment[]; total: number }

const sum2 = (m: MatchFigure, k: keyof MatchCounts) => (m.services[k] ?? 0) + (m.dependencies[k] ?? 0);

/** matchTile says the match figure. Unknown never counts as matched: insufficient
 *  evidence is its own hatched segment, outside the percentage. */
export function matchTile(m: MatchFigure | null | undefined): FigureTile {
  if (!m) return { value: 'unknown', unknown: true, note: '', sub: 'No match figure has been recorded yet.', segments: [], total: 0 };
  const segments: FigureSegment[] = [
    { label: 'Matched', value: sum2(m, 'matched'), tone: 'ok' },
    { label: 'Expected, not observed', value: sum2(m, 'expectedOnly'), tone: 'info' },
    { label: 'Observed, not expected', value: sum2(m, 'observedOnly'), tone: 'warn' },
    { label: 'Insufficient evidence', value: sum2(m, 'insufficient'), tone: 'neutral', hatch: true },
  ];
  const total = segments.reduce((n, x) => n + x.value, 0);
  if (m.percent == null) return { value: 'none yet', unknown: true, note: '', sub: 'Pacto observes nothing yet, so nothing can match.', segments, total };
  return { value: `${m.percent}%`, unknown: false, note: 'of what Pacto can observe', sub: `${m.coverage ?? 0}% observed coverage · unknown never counts as matched`, segments, total };
}

/** completenessTile says the completeness figure over its judged base, never past it. */
export function completenessTile(c: CompletenessFigure | null | undefined, sectionWords: Record<string, string> = {}): FigureTile {
  if (!c) return { value: 'unknown', unknown: true, note: '', sub: 'No completeness figure has been recorded yet.', segments: [], total: 0 };
  const judged = c.judged ?? 0, full = c.fullyDescribed ?? 0, unknown = c.unknown ?? 0;
  const segments: FigureSegment[] = [
    { label: 'Fully described', value: full, tone: 'ok' },
    { label: 'Partly described', value: Math.max(0, judged - full), tone: 'info' },
    { label: 'Not judged', value: unknown, tone: 'neutral', hatch: true },
  ];
  const base = `${formatCount(full)} of ${formatCount(judged)} judged ${judged === 1 ? 'service' : 'services'} fully described${unknown ? `, ${formatCount(unknown)} not judged` : ''}`;
  const missing = (c.mostMissing ?? []).filter((x) => x.count > 0);
  const sub = missing.length ? `Most missing: ${missing.map((x) => `${sectionWords[x.section] ?? x.section} ${formatCount(x.count)}`).join(' · ')}` : '';
  if (c.percentComplete == null) return { value: 'unknown', unknown: true, note: base, sub, segments, total: judged + unknown };
  return { value: `${c.percentComplete}%`, unknown: false, note: base, sub, segments, total: judged + unknown };
}
