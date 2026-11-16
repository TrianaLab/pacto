/**
 * ponytail: the old Metrics page's tile wall and trend lists, kept only so the view
 * compiles between #597 P8 and P9. P9 rebuilds the view on lib/metrics.ts and deletes
 * this file (P13 dead-code sweep checks it is gone).
 */
import type { ProductStats } from './api.ts';
import { metricsUrl } from './router.ts';
import { countOf, formatCount } from './format.ts';
import { durationText } from './chart.ts';
import { RATES, NO_CI, noCI, ratioText, pooledRate, bucketLabel, isDaily, type RateField } from './metrics.ts';

type Metrics = NonNullable<ProductStats['totals']>;

export interface Tile {
  key: string;
  label: string;
  /** A second line under the label, such as the environment of a lead time: never clipped. */
  sub?: string;
  /** The number, or the word `unknown`. */
  value: string;
  unknown?: boolean;
  detail?: string;
  /** The records behind the number; absent when there are none to list. */
  href?: string;
  /** How many records `href` lists, for a link that says how many it opens. */
  lists?: number;
}

/** The most lead-time tiles a page draws; the environments table holds every one. */
export const LEAD_TILES = 4;

/** tiles are the window's headline numbers, each linked to the records behind it. */
export function tiles(st: ProductStats, window = ''): Tile[] {
  const t = st.totals;
  const w = window || undefined;
  const link = (metric: string, n: number, environment?: string) =>
    (n > 0 ? { href: metricsUrl({ window: w, metric, environment }), lists: n } : {});
  if (!t) return [];
  const g = t.gates;
  const p = t.consumersProtected;
  // With no diff or impact run in the window, the CI numbers measured nothing: unknown.
  const ci = noCI(t)
    ? [['breaking', 'Breaking changes caught'], ['protected', 'Consumers protected'], ['clear', 'Gates clear'], ['blocked', 'Gates blocked'], ['unverified', 'Gates unverified']]
      .map(([key, label]): Tile => ({ key, label, value: 'unknown', unknown: true, detail: NO_CI }))
    : [
      { key: 'breaking', label: 'Breaking changes caught', value: formatCount(t.breakingCaught.distinct),
        detail: countOf(t.breakingCaught.runs, 'run'), ...link('breaking', t.breakingCaught.distinct) },
      { key: 'protected', label: 'Consumers protected', value: formatCount(p.count),
        detail: [`by ${countOf(g.blocked, 'blocked change')}`, p.unknown ? `${formatCount(p.unknown)} unknown` : '', p.partial || p.unknown ? 'a lower bound' : '']
          .filter(Boolean).join(', '),
        ...link('protected', g.blocked) },
      { key: 'clear', label: 'Gates clear', value: formatCount(g.clear), ...link('clear', g.clear) },
      { key: 'blocked', label: 'Gates blocked', value: formatCount(g.blocked), ...link('blocked', g.blocked) },
      { key: 'unverified', label: 'Gates unverified', value: formatCount(g.unverified), ...link('unverified', g.unverified) },
    ];
  const out: Tile[] = [...ci];
  for (const r of RATES) {
    const sum = pooledRate(t.environments, r.field);
    out.push({ key: r.metric, label: `${r.label} rate`, value: ratioText(sum), unknown: !!sum.unknown, ...link(r.metric, sum.count) });
  }
  const envs = st.environments ?? [];
  // ponytail: the first few environments in promotion order get a tile; the table lists them all.
  for (const e of envs.slice(0, LEAD_TILES)) {
    const p50 = e.leadTime.p50Seconds;
    out.push({ key: `lead:${e.environment}`, label: 'Lead time p50', sub: e.environment, value: durationText(p50), unknown: p50 == null,
      detail: e.leadTime.unjoined ? `${formatCount(e.leadTime.unjoined)} with no publish record` : undefined,
      ...(e.leadTime.joined ? { href: metricsUrl({ window: w, metric: 'leadTime', environment: e.environment }) } : {}) });
  }
  return out;
}

/** The goal metrics drawn as trends, in page order. */
export const TRENDS = [
  { metric: 'breaking', title: 'Breaking changes caught' },
  { metric: 'protected', title: 'Consumers protected' },
  { metric: 'blocked', title: 'Gates blocked' },
  ...RATES.map((r) => ({ metric: r.metric, title: `${r.label} promotions` })),
] as const;

export type UnknownReason = 'no-records' | 'no-denominator' | 'no-ci';
const REASON_TEXT: Record<UnknownReason, string> = { 'no-records': 'no records', 'no-denominator': 'nothing to count', 'no-ci': NO_CI };

export interface TrendRow {
  label: string;
  value: number;
  unknown?: string;
  reason?: UnknownReason;
  hatch?: boolean;
  /** The value as printed, when it says more than the number: "12 or more". */
  display?: string;
  /** The value is a lower bound. */
  lowerBound?: boolean;
}

const CI_METRICS = ['breaking', 'protected', 'blocked'];

function valueOf(m: Metrics, metric: string): { value: number; reason?: UnknownReason; partial?: boolean; counted?: number } {
  if (CI_METRICS.includes(metric) && noCI(m)) return { value: 0, reason: 'no-ci' };
  switch (metric) {
    case 'breaking': return { value: m.breakingCaught.distinct };
    // Unknown consumers may be blocking ones: the count is a lower bound either way.
    case 'protected': return { value: m.consumersProtected.count, partial: !!(m.consumersProtected.partial || m.consumersProtected.unknown) };
    case 'blocked': return { value: m.gates.blocked };
  }
  const r = pooledRate(m.environments, metric as RateField);
  return r.unknown ? { value: 0, reason: 'no-denominator', counted: r.strayCount } : { value: r.count };
}

/** windowTotal is a trend metric over the whole window, from the totals the tiles show,
 *  so a caption never adds per-bucket counts into a different number; null when the
 *  window measured nothing for it. */
export function windowTotal(st: ProductStats, metric: string): { value: number; lowerBound?: boolean } | null {
  const t = st.totals;
  if (!t) return null;
  const v = valueOf(t, metric);
  if (v.reason === 'no-ci') return null;
  return { value: v.reason ? v.counted ?? 0 : v.value, lowerBound: v.partial };
}

/** trend is one metric per bucket, oldest first; a bucket that cannot answer says why. */
export function trend(st: ProductStats, metric: string): TrendRow[] {
  const daily = isDaily(st);
  return (st.buckets ?? []).map((b) => {
    const label = bucketLabel(b, daily);
    if (!b.metrics) return { label, value: 0, reason: 'no-records', unknown: REASON_TEXT['no-records'] };
    const v = valueOf(b.metrics, metric);
    if (v.reason) {
      const text = v.counted ? `${formatCount(v.counted)} with no rate` : REASON_TEXT[v.reason];
      return { label, value: 0, reason: v.reason, unknown: text };
    }
    return v.partial ? { label, value: v.value, hatch: true, lowerBound: true, display: `${formatCount(v.value)} or more` } : { label, value: v.value };
  });
}

/** caption says in words what a trend draws: "Gates blocked per week: 8 over 30 days; 1 week had no records."
 *  The window figure is `total`, the number the tile shows, never a sum of the buckets: a
 *  change can count in two buckets and once in the window. */
export function caption(title: string, rows: TrendRow[], daily: boolean, windowDays: number, total: ReturnType<typeof windowTotal>): string {
  const unit = daily ? 'day' : 'week';
  const of = (reason: UnknownReason) => rows.filter((r) => r.reason === reason).length;
  const missing = of('no-records'), nothing = of('no-denominator'), noCi = of('no-ci');
  const lower = rows.filter((r) => r.lowerBound).length;
  const figure = total == null ? 'unknown' : `${formatCount(total.value)}${total.lowerBound ? ' or more' : ''}`;
  return [
    `${title} per ${unit}: ${figure} over ${countOf(windowDays, 'day')}`,
    missing ? `${countOf(missing, unit)} had no records` : '',
    noCi ? `${countOf(noCi, unit)} had no CI records` : '',
    nothing ? `${countOf(nothing, unit)} had nothing to take the rate over` : '',
    lower ? `${countOf(lower, unit)} ${lower === 1 ? 'is a lower bound' : 'are lower bounds'}` : '',
  ].filter(Boolean).join('; ') + '.';
}

/** The coverage series drawn over time; unknown is its own hatched series, never folded
 *  into deployed or into a gap. */
export const COVERAGE_SERIES = [
  { key: 'services', title: 'Services' },
  { key: 'described', title: 'Described services' },
  { key: 'deployed', title: 'Deployed services' },
  { key: 'unknown', title: 'Services Pacto cannot judge' },
] as const;

/** coverageTrend is one coverage series per bucket: the newest coverage snapshot of each,
 *  hatched when the snapshot was partial; a bucket with none says so. */
export function coverageTrend(st: ProductStats, series: (typeof COVERAGE_SERIES)[number]['key']): TrendRow[] {
  const daily = isDaily(st);
  return (st.buckets ?? []).map((b) => {
    const label = bucketLabel(b, daily);
    const c = b.coverage;
    if (!c) return { label, value: 0, unknown: 'no records' };
    const partial = c.completeness !== 'complete';
    if (series === 'services') return { label, value: c.services, hatch: partial };
    const k = c.knowledge;
    if (!k) return { label, value: 0, unknown: 'unknown' };
    const value = { described: k.described, deployed: k.services - k.unknown - k.notDeployed, unknown: k.unknown }[series];
    return { label, value, hatch: partial || series === 'unknown' };
  });
}

