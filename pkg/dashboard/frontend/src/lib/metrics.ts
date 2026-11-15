/**
 * The Metrics page model (#493): what each tile, trend and table row of the page says,
 * computed from one /api/stats answer. Pure, so the drawing and its caption come from
 * the same numbers. Unknown is never a number: a bucket with no records, a rate with
 * nothing to take it over and a lead time no promotion joined all say why instead.
 */
import type { ProductStats } from './api.ts';
import { metricsUrl } from './router.ts';
import { countOf, formatCount } from './format.ts';

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
  /** The number, or the word `unknown`. */
  value: string;
  unknown?: boolean;
  detail?: string;
  /** The records behind the number; absent when there are none to list. */
  href?: string;
  /** How many records `href` lists, for a link that says how many it opens. */
  lists?: number;
}

/** ratioText prints a rate as "3/41 (7%)", or `unknown` when nothing was counted under it. */
export function ratioText(r: Ratio | undefined): string {
  if (!r || r.unknown || !r.of) return 'unknown';
  return `${formatCount(r.count)}/${formatCount(r.of)} (${Math.round((r.count / r.of) * 100)}%)`;
}

/** sumRate adds one rate across environments: each operation counts under one only. */
export function sumRate(envs: Pick<Environment, RateField>[] | null | undefined, field: RateField): Ratio {
  let count = 0, of = 0;
  for (const e of envs ?? []) { count += e[field].count; of += e[field].of; }
  return of ? { count, of } : { count: 0, of: 0, unknown: true };
}

/** duration prints seconds as the two largest units: "45s", "12m", "3h 20m", "2d 4h". */
export function duration(seconds: number | null | undefined): string {
  if (seconds == null) return 'unknown';
  const units: [string, number][] = [['d', 86_400], ['h', 3_600], ['m', 60], ['s', 1]];
  const parts: string[] = [];
  let left = Math.max(0, Math.round(seconds));
  for (const [u, n] of units) {
    const v = Math.floor(left / n);
    left -= v * n;
    if (v || parts.length) parts.push(`${v}${u}`);
    if (parts.length === 2) break;
  }
  return parts.filter((x) => !x.startsWith('0')).join(' ') || '0s';
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
  const out: Tile[] = [
    { key: 'breaking', label: 'Breaking changes caught', value: formatCount(t.breakingCaught.distinct),
      detail: countOf(t.breakingCaught.runs, 'run'), ...link('breaking', t.breakingCaught.distinct) },
    { key: 'protected', label: 'Consumers protected', value: formatCount(p.count),
      detail: [`by ${countOf(g.blocked, 'blocked change')}`, p.unknown ? `${formatCount(p.unknown)} unknown` : '', p.partial ? 'a lower bound' : '']
        .filter(Boolean).join(', '),
      ...link('protected', g.blocked) },
    { key: 'clear', label: 'Gates clear', value: formatCount(g.clear), ...link('clear', g.clear) },
    { key: 'blocked', label: 'Gates blocked', value: formatCount(g.blocked), ...link('blocked', g.blocked) },
    { key: 'unverified', label: 'Gates unverified', value: formatCount(g.unverified), ...link('unverified', g.unverified) },
  ];
  for (const r of RATES) {
    const sum = sumRate(t.environments, r.field);
    out.push({ key: r.metric, label: `${r.label} rate`, value: ratioText(sum), unknown: !!sum.unknown, ...link(r.metric, sum.count) });
  }
  const envs = st.environments ?? [];
  // ponytail: the first few environments in promotion order get a tile; the table lists them all.
  for (const e of envs.slice(0, LEAD_TILES)) {
    const p50 = e.leadTime.p50Seconds;
    out.push({ key: `lead:${e.environment}`, label: `Lead time p50, ${e.environment}`, value: duration(p50), unknown: p50 == null,
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

export type UnknownReason = 'no-records' | 'no-denominator';
const REASON_TEXT: Record<UnknownReason, string> = { 'no-records': 'no records', 'no-denominator': 'nothing to count' };

export interface TrendRow {
  label: string;
  value: number;
  unknown?: string;
  reason?: UnknownReason;
  hatch?: boolean;
}

const day = (iso: string) => new Date(iso).toLocaleDateString('en-GB', { day: 'numeric', month: 'short', timeZone: 'UTC' });

/** bucketLabel names a bucket: "3 Nov" for a day, "week of 3 Nov", or "2 days from 1 Nov" for a short one. */
export function bucketLabel(b: Bucket, daily: boolean): string {
  if (b.partial) return `${countOf(b.days, 'day')} from ${day(b.since)}`;
  return daily ? day(b.since) : `week of ${day(b.since)}`;
}

function valueOf(m: Metrics, metric: string): { value: number; reason?: UnknownReason; partial?: boolean } {
  switch (metric) {
    case 'breaking': return { value: m.breakingCaught.distinct };
    case 'protected': return { value: m.consumersProtected.count, partial: !!m.consumersProtected.partial };
    case 'blocked': return { value: m.gates.blocked };
  }
  const r = sumRate(m.environments, metric as RateField);
  return r.unknown ? { value: 0, reason: 'no-denominator' } : { value: r.count };
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
    if (v.reason) return { label, value: 0, reason: v.reason, unknown: REASON_TEXT[v.reason] };
    return v.partial ? { label: `${label} (a lower bound)`, value: v.value, hatch: true } : { label, value: v.value };
  });
}

/** caption says in words what a trend draws: "Gates blocked per week: 8 over 30 days; 1 week had no records." */
export function caption(title: string, rows: TrendRow[], daily: boolean, windowDays: number): string {
  const unit = daily ? 'day' : 'week';
  const sum = rows.reduce((n, r) => n + (r.unknown ? 0 : r.value), 0);
  const of = (reason: UnknownReason) => rows.filter((r) => r.reason === reason).length;
  const missing = of('no-records'), nothing = of('no-denominator');
  return [
    `${title} per ${unit}: ${formatCount(sum)} over ${countOf(windowDays, 'day')}`,
    missing ? `${countOf(missing, unit)} had no records` : '',
    nothing ? `${countOf(nothing, unit)} had nothing to take the rate over` : '',
  ].filter(Boolean).join('; ') + '.';
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
  breaking: 'breaking changes caught', protected: 'blocked changes that protected consumers', clear: 'gates clear',
  blocked: 'gates blocked', unverified: 'gates unverified', held: 'held promotions', failed: 'failed promotions',
  rolledBack: 'rollbacks', verificationFailed: 'failed verifications', leadTime: 'lead time', coverage: 'coverage snapshots',
};
