/**
 * The Metrics page model (#493): what each tile, trend and table row of the page says,
 * computed from one /api/stats answer. Pure, so the drawing and its caption come from
 * the same numbers. Unknown is never a number: a bucket with no records, a rate with
 * nothing to take it over and a lead time no promotion joined all say why instead.
 */
import type { ProductStats } from './api.ts';
import { metricsUrl } from './router.ts';
import { countOf, formatCount } from './format.ts';
import { durationText } from './chart.ts';

type Metrics = NonNullable<ProductStats['totals']>;
type Bucket = NonNullable<ProductStats['buckets']>[number];
type Environment = NonNullable<ProductStats['environments']>[number];
export type Ratio = Environment['held'];

/** The rates an environment carries, keyed by the metric that lists their records. */
export const RATES = [
  { metric: 'held', label: 'Held', field: 'held' },
  { metric: 'failed', label: 'Failed', field: 'failed' },
  { metric: 'rolledBack', label: 'Rolled back', field: 'rolledBack' },
  { metric: 'verificationFailed', label: 'Verification failed', field: 'verificationFailed' },
] as const;
type RateField = (typeof RATES)[number]['field'];

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

/** ratioText prints a rate as "3/41 (7%)", or `unknown` when there is nothing to take it
 *  over, with the records it still counted: "unknown (1 counted)". */
export function ratioText(r: Ratio | undefined): string {
  if (!r || r.unknown || !r.of) return r?.count ? `unknown (${formatCount(r.count)} counted)` : 'unknown';
  return `${formatCount(r.count)}/${formatCount(r.of)} (${Math.round((r.count / r.of) * 100)}%)`;
}

/** sumRate adds one rate across environments: each operation counts under one only. An
 *  environment that counted records with nothing to take them over (a rollback with no
 *  succeeded promote) makes the sum unknown, never a rate that leaves them out. */
export function sumRate(envs: Pick<Environment, RateField>[] | null | undefined, field: RateField): Ratio {
  let count = 0, of = 0, stray = false;
  for (const e of envs ?? []) {
    count += e[field].count;
    of += e[field].of;
    stray ||= !!e[field].unknown && e[field].count > 0;
  }
  return of && !stray ? { count, of } : { count, of, unknown: true };
}

/** noCI says a Metrics counted no diff or impact run: its CI numbers measured nothing. */
export const noCI = (m: Pick<Metrics, 'changes'>) => m.changes === 0;
export const NO_CI = 'no CI records';

/** protectedText prints a consumers-protected count, "12", or "12 or more" with how many
 *  are unknown when the count is a lower bound; `qualified` says to hatch it. */
export function protectedText(m: Pick<Metrics, 'changes' | 'consumersProtected'>): { text: string; qualified: boolean; unknown?: boolean } {
  if (noCI(m)) return { text: NO_CI, qualified: true, unknown: true };
  const p = m.consumersProtected;
  if (!p.partial && !p.unknown) return { text: formatCount(p.count), qualified: false };
  return { text: [`${formatCount(p.count)} or more`, p.unknown ? `${formatCount(p.unknown)} unknown` : ''].filter(Boolean).join(', '), qualified: true };
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
    const sum = sumRate(t.environments, r.field);
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

const day = (iso: string) => new Date(iso).toLocaleDateString('en-GB', { day: 'numeric', month: 'short', timeZone: 'UTC' });

/** bucketLabel names a bucket: "3 Nov" for a day, "week of 3 Nov", or "2 days from 1 Nov" for a short one. */
export function bucketLabel(b: Bucket, daily: boolean): string {
  if (b.partial) return `${countOf(b.days, 'day')} from ${day(b.since)}`;
  return daily ? day(b.since) : `week of ${day(b.since)}`;
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
  const r = sumRate(m.environments, metric as RateField);
  return r.unknown ? { value: 0, reason: 'no-denominator', counted: r.count } : { value: r.count };
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

/** isDaily says whether the answer's buckets are days (a 7-day window) or weeks. */
export function isDaily(st: ProductStats): boolean {
  return (st.buckets ?? []).every((b) => b.days <= 1);
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

/** windowDays is how many days the answer's window spans. */
export function windowDays(st: ProductStats): number {
  return Math.round((Date.parse(st.window.until) - Date.parse(st.window.since)) / 86_400_000);
}

/** envTitle names the environments table by how its rows are ordered (SPEC 21). */
export function envTitle(st: ProductStats): string {
  return st.environmentOrder === 'stages'
    ? 'Environments, in the order their Stages promote'
    : 'Environments, listed by name: no Stage graph to order by';
}

/** hiddenLine says that records outside the reader's view exist; it never counts them. */
export function hiddenLine(st: Pick<ProductStats, 'hidden'>): string {
  return st.hidden?.present ? 'Some run records name services outside your view and are left out of every number.' : '';
}

/** The labels the records panel uses for each metric. */
export const METRIC_LABEL: Record<string, string> = {
  breaking: 'breaking changes caught', protected: 'blocked changes, with the consumers they held back', clear: 'gates clear',
  blocked: 'gates blocked', unverified: 'gates unverified', held: 'held promotions', failed: 'failed promotions',
  rolledBack: 'rollbacks', verificationFailed: 'failed verifications', leadTime: 'lead time', coverage: 'coverage snapshots',
};
