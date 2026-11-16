/**
 * The Metrics page model (#493, #597): what each tile, chart, tooltip and table cell of
 * the page says, computed from one /api/stats answer. Pure, so a drawing, its caption
 * and its table view read the same numbers and can never disagree. Unknown is never a
 * number: a bucket with no records, a rate with nothing to take it over and a lead time
 * no promotion joined all say why instead. Percentiles are pooled by the server
 * (leadTimeGroups); nothing here takes a percentile of percentiles.
 */
import type { ProductStats } from './api.ts';
import { metricsUrl } from './router.ts';
import { countOf, formatCount } from './format.ts';
import { formatDateTime } from './dateFormat.ts';
import { durationText, niceMax } from './chart.ts';

type Metrics = NonNullable<ProductStats['totals']>;
type Bucket = NonNullable<ProductStats['buckets']>[number];
type Environment = NonNullable<ProductStats['environments']>[number];
type LeadGroup = NonNullable<ProductStats['leadTimeGroups']>[number];
type LeadTime = Environment['leadTime'];
/** The numbers a group's environment carries: an owner's or a domain's share, or the whole environment. */
export type EnvMetrics = NonNullable<Metrics['environments']>[number];
export type Ratio = Environment['held'];

/** Adaptive thresholds (Decision 10). */
export const SMALL_MULTIPLES_MAX = 8;
export const COLLAPSE_ABOVE = 50;
export const GROUP_TOP = 10;
export const GROUP_PAGE = 20;
export const LEAD_TOP = 10;
export const LEAD_PAGE = 20;
export const TABLE_PAGE = 10;
/** The heatmap bins, least to worst; a real 0% is its own neutral cell, unknown is hatched. */
export const BINS = [
  { max: 0.10, label: 'up to 10%' },
  { max: 0.20, label: '10 to 20%' },
  { max: 0.35, label: '20 to 35%' },
  { max: Infinity, label: 'over 35%' },
] as const;

/** The rates an environment carries, keyed by the metric that lists their records. */
export const RATES = [
  { metric: 'held', label: 'Held', field: 'held' },
  { metric: 'failed', label: 'Failed', field: 'failed' },
  { metric: 'rolledBack', label: 'Rolled back', field: 'rolledBack' },
  { metric: 'verificationFailed', label: 'Verification failed', field: 'verificationFailed' },
] as const;
export type RateField = (typeof RATES)[number]['field'];

/** ratioText prints a rate as "3/41 (7%)", or `unknown` when there is nothing to take it
 *  over, with the records it still counted: "unknown (1 counted)". */
export function ratioText(r: Ratio | undefined): string {
  if (!r || r.unknown || !r.of) return r?.count ? `unknown (${formatCount(r.count)} counted)` : 'unknown';
  return `${formatCount(r.count)}/${formatCount(r.of)} (${Math.round((r.count / r.of) * 100)}%)`;
}

/** noCI says a Metrics counted no diff or impact run: its CI numbers measured nothing. */
export const noCI = (m: Pick<Metrics, 'changes'>) => m.changes === 0;
export const NO_CI = 'no CI records';
const NO_RECORDS = 'no records';

/** protectedText prints a consumers-protected count, "12", or "12 or more" with how many
 *  are unknown when the count is a lower bound; `qualified` says to hatch it. */
export function protectedText(m: Pick<Metrics, 'changes' | 'consumersProtected'>): { text: string; qualified: boolean; unknown?: boolean } {
  if (noCI(m)) return { text: NO_CI, qualified: true, unknown: true };
  const p = m.consumersProtected;
  if (!p.partial && !p.unknown) return { text: formatCount(p.count), qualified: false };
  return { text: [`${formatCount(p.count)} or more`, p.unknown ? `${formatCount(p.unknown)} unknown` : ''].filter(Boolean).join(', '), qualified: true };
}

const DAY_MS = 86_400_000;
const day = (t: string | number) => new Date(t).toLocaleDateString('en-GB', { day: 'numeric', month: 'short', timeZone: 'UTC' });

/** bucketLabel names a bucket for the records panel: "3 Nov", "week of 3 Nov" or "2 days from 1 Nov". */
export function bucketLabel(b: Bucket, daily: boolean): string {
  if (b.partial) return `${countOf(b.days, 'day')} from ${day(b.since)}`;
  return daily ? day(b.since) : `week of ${day(b.since)}`;
}

/** isDaily says whether the answer's buckets are days (a 7-day window) or weeks. */
export function isDaily(st: ProductStats): boolean {
  return (st.buckets ?? []).every((b) => b.days <= 1);
}

/** windowDays is how many days the answer's window spans. */
export function windowDays(st: ProductStats): number {
  return Math.round((Date.parse(st.window.until) - Date.parse(st.window.since)) / DAY_MS);
}

export interface BucketLabel { label: string; short: string; partial: boolean; days: number }

/** buckets names each bucket on the time axis: "Week of 12 Oct", a short one "10 to 11 Oct
 *  (2 days)", a day "3 Nov"; `short` is the axis tick. */
export function buckets(st: ProductStats): BucketLabel[] {
  return (st.buckets ?? []).map((b) => {
    const short = day(b.since);
    let label = short;
    if (b.partial) {
      const [a, z] = [short, day(Date.parse(b.since) + (b.days - 1) * DAY_MS)];
      const [am, zm] = [a.split(' ')[1], z.split(' ')[1]];
      label = `${am === zm ? a.split(' ')[0] : a} to ${z} (${countOf(b.days, 'day')})`;
    } else if (b.days > 1) label = `Week of ${short}`;
    return { label, short, partial: !!b.partial, days: b.days };
  });
}

const unit = (st: ProductStats) => (isDaily(st) ? 'day' : 'week');

/** unknownReasons says, per bucket, why it has no CI number: no records at all, or no diff
 *  or impact run among them; null where it answers. */
export function unknownReasons(st: ProductStats): (string | null)[] {
  return (st.buckets ?? []).map((b) => (!b.metrics ? NO_RECORDS : noCI(b.metrics) ? NO_CI : null));
}

export type SeriesKey = 'breaking' | 'runs' | 'protected' | 'clear' | 'blocked' | 'unverified';
const VALUE: Record<SeriesKey, (m: Metrics) => number> = {
  breaking: (m) => m.breakingCaught.distinct,
  runs: (m) => m.breakingCaught.runs,
  protected: (m) => m.consumersProtected.count,
  clear: (m) => m.gates.clear,
  blocked: (m) => m.gates.blocked,
  unverified: (m) => m.gates.unverified,
};
const lowerBound = (m: Metrics) => !!(m.consumersProtected.unknown || m.consumersProtected.partial);

/** series is one CI number per bucket, null where unknownReasons gives a reason. `caps`
 *  marks a consumers-protected count that is a lower bound (drawn with a hatched cap). */
export function series(st: ProductStats, key: SeriesKey): { values: (number | null)[]; caps: boolean[] } {
  const why = unknownReasons(st);
  const bs = st.buckets ?? [];
  return {
    values: bs.map((b, i) => (why[i] ? null : VALUE[key](b.metrics!))),
    caps: bs.map((b, i) => key === 'protected' && !why[i] && lowerBound(b.metrics!)),
  };
}

/** depthLabel names a promotion depth by depth only, never by an environment's name (SPEC 21). */
export function depthLabel(depth?: number | null): string {
  if (depth == null) return 'Depth unknown';
  return depth === 0 ? 'entry' : `hop ${depth + 1}`;
}

export type EnvRow = Omit<Environment, 'depth' | 'chainEnd' | 'parents'> & { depth: number | null; chainEnd: boolean; parents: string[] };

/** environments are the answer's environments in Stage order, with depth, chain end and
 *  direct parents always present (null depth: no readable Stage, a cycle or below one). */
export function environments(st: ProductStats): EnvRow[] {
  return (st.environments ?? []).map((e) => ({ ...e, depth: e.depth ?? null, chainEnd: !!e.chainEnd, parents: e.parents ?? [] }));
}

/** depthFilter is the promotion-depth row: how many environments sit at each known depth.
 *  Null at or under SMALL_MULTIPLES_MAX environments, where every panel is drawn anyway. */
export function depthFilter(st: ProductStats): { total: number; depths: { depth: number; label: string; count: number }[]; unknown: number } | null {
  const envs = environments(st);
  if (envs.length <= SMALL_MULTIPLES_MAX) return null;
  const by = new Map<number, number>();
  for (const e of envs) if (e.depth != null) by.set(e.depth, (by.get(e.depth) ?? 0) + 1);
  const depths = [...by].sort(([a], [b]) => a - b).map(([depth, count]) => ({ depth, label: depthLabel(depth), count }));
  return { total: envs.length, depths, unknown: envs.filter((e) => e.depth == null).length };
}

export type GroupBy = 'depth' | 'owner' | 'domain';
export interface Group { key: string; label: string; unknown: boolean; envs: EnvMetrics[] }
const GROUP_WORD: Record<GroupBy, string> = { depth: 'Promotion depth', owner: 'Owner', domain: 'Domain' };

/** The engine's placeholder keys, in parentheses so they never meet a real name. */
function keyLabel(by: 'owner' | 'domain', key: string, label: string): string {
  if (key === '(unknown)') return by === 'owner' ? 'Owner unknown' : 'Domain unknown';
  if (key === '(unowned)') return 'No owner';
  if (key === '(none)') return 'No domain';
  return label || key;
}

/** groupBy files the environments under promotion depth (unknown last), or under each
 *  owner or domain with that group's own numbers: an environment delivering three owners'
 *  services sits under each, counting only theirs. `depth` keeps the environments at it. */
export function groupBy(st: ProductStats, by: GroupBy, depth?: number | null): Group[] {
  const envs = environments(st);
  const at = depth == null ? null : new Set(envs.filter((e) => e.depth === depth).map((e) => e.environment));
  const keep = (e: EnvMetrics) => !at || at.has(e.environment);
  if (by !== 'depth') {
    return (st.environmentsBy?.[by] ?? [])
      .map((g) => ({ key: g.key, label: keyLabel(by, g.key, g.label), unknown: g.key.startsWith('('), envs: (g.environments ?? []).filter(keep) }))
      .filter((g) => g.envs.length);
  }
  const depths = [...new Set(envs.map((e) => e.depth))].sort((a, b) => (a ?? Infinity) - (b ?? Infinity));
  return depths
    .map((d) => ({ key: d == null ? 'depth-unknown' : `depth-${d}`, label: depthLabel(d), unknown: d == null, envs: envs.filter((e) => e.depth === d && keep(e)) }))
    .filter((g) => g.envs.length);
}

/** groupOptions are the group-bys worth offering: those that yield more than one group. */
export function groupOptions(st: ProductStats): GroupBy[] {
  return (['depth', 'owner', 'domain'] as const).filter((by) => groupBy(st, by).length > 1);
}

/** attributionLine says where owner or domain came from: inferred from a fleet snapshot. */
export function attributionLine(st: ProductStats, by: 'owner' | 'domain'): string {
  const a = st.environmentsBy?.attribution;
  if (!a) return '';
  return `${by} inferred from the fleet snapshot of ${formatDateTime(a.asOf)}${a.partial ? ', partial' : ''}`;
}

export interface Pooled extends Ratio { counted: number; excluded: number; strayCount: number }

/** pooledRate adds one rate over environments that have something to take it over
 *  (`of > 0`) only, so a stray count (a rollback with no succeeded promote) never enters
 *  the numerator; unknown when none qualify. `excluded` and `strayCount` say what it left out. */
export function pooledRate(envs: Pick<EnvMetrics, RateField>[] | null | undefined, field: RateField): Pooled {
  let count = 0, of = 0, counted = 0, excluded = 0, strayCount = 0;
  for (const e of envs ?? []) {
    const r = e[field];
    if (r.of > 0) { count += r.count; of += r.of; counted++; } else { excluded++; strayCount += r.count; }
  }
  return { count, of, counted, excluded, strayCount, ...(counted ? {} : { unknown: true }) };
}

const rate = (r: Ratio) => (r.of > 0 ? r.count / r.of : -1);

/** worstEnvironment is the environment with the highest rate, ignoring rates with nothing to count. */
export function worstEnvironment<E extends Pick<EnvMetrics, RateField | 'environment'>>(envs: E[], field: RateField): E | null {
  let worst: E | null = null;
  for (const e of envs) if (e[field].of > 0 && (!worst || rate(e[field]) > rate(worst[field]))) worst = e;
  return worst;
}

/** smallMultipleDomain is the one rate scale every environment panel shares, so panels
 *  compare: at least 50%, and above the largest known rate (a domain, never a clamp). */
export function smallMultipleDomain(envs: Pick<EnvMetrics, RateField>[]): number {
  const top = Math.max(0, ...envs.flatMap((e) => RATES.map((r) => rate(e[r.field]))));
  return Math.max(0.5, top > 0 ? niceMax(top) : 0);
}

/** bin is a rate's heatmap bin, 1 to 4 (Decision 10). */
export function bin(r: number): 1 | 2 | 3 | 4 {
  return (BINS.findIndex((b) => r <= b.max) + 1) as 1 | 2 | 3 | 4;
}

/** cell is one RateGrid cell: a bin only when there is a count over something, so a real 0
 *  is the neutral cell and nothing to count is the hatch. */
export function cell(r: Ratio, foot = ''): { count: number; of: number; bin?: 1 | 2 | 3 | 4; foot: string } {
  return { count: r.count, of: r.of, ...(r.of > 0 && r.count > 0 ? { bin: bin(r.count / r.of) } : {}), foot };
}

/** healthMode: one small panel per environment up to SMALL_MULTIPLES_MAX, then the heatmap. */
export const healthMode = (n: number): 'multiples' | 'grid' => (n <= SMALL_MULTIPLES_MAX ? 'multiples' : 'grid');
/** collapsedByDefault: heatmap groups start closed above COLLAPSE_ABOVE rows. */
export const collapsedByDefault = (n: number) => n > COLLAPSE_ABOVE;

export type LeadRow = { group: string } | { name: string; p50: number | null; p90: number | null; foot?: string };

const unjoinedText = (n: number) => (n ? `${countOf(n, 'promotion')} with no publish record ${n === 1 ? 'is' : 'are'} left out` : '');
const slowest = (a: EnvMetrics, b: EnvMetrics) => (b.leadTime.p50Seconds ?? -1) - (a.leadTime.p50Seconds ?? -1);

/** leadRows are the lead-time chart's rows: the LEAD_TOP slowest p50 over every group, or
 *  every one paged at LEAD_PAGE, under their group headers, slowest first and unknown last. */
export function leadRows(groups: Group[], { all = false, page = 0 } = {}): { rows: LeadRow[]; total: number; from: number; to: number } {
  const flat0 = groups.flatMap((g) => g.envs.map((e) => ({ g: g.label, e })));
  const picked = all ? flat0 : [...flat0].sort((a, b) => slowest(a.e, b.e)).slice(0, LEAD_TOP);
  const flat = groups.flatMap((g) => picked.filter((p) => p.g === g.label).sort((a, b) => slowest(a.e, b.e)));
  const from = all ? page * LEAD_PAGE : 0;
  const shown = all ? flat.slice(from, from + LEAD_PAGE) : flat;
  const rows: LeadRow[] = [];
  let last = '';
  for (const { g, e } of shown) {
    if (g !== last) { rows.push({ group: g }); last = g; }
    const foot = unjoinedText(e.leadTime.unjoined);
    rows.push({ name: e.environment, p50: e.leadTime.p50Seconds ?? null, p90: e.leadTime.p50Seconds == null ? null : e.leadTime.p90Seconds ?? null, ...(foot ? { foot } : {}) });
  }
  return { rows, total: flat0.length, from: shown.length ? from + 1 : 0, to: from + shown.length };
}

/** leadGroup picks the pooled lead time the tile reads: a depth when filtered, else the
 *  chain end, else every environment. */
function leadGroup(st: ProductStats, depth?: number | null): LeadGroup | undefined {
  const gs = st.leadTimeGroups ?? [];
  if (depth != null) return gs.find((g) => g.key === `depth-${depth}`);
  return gs.find((g) => g.key === 'chain-end') ?? gs.find((g) => g.key === 'all');
}

function leadTitle(st: ProductStats, depth?: number | null): string {
  if (depth != null) return `Lead time to ${depthLabel(depth)}`;
  return leadGroup(st)?.key === 'chain-end' ? 'Lead time to production' : 'Lead time to any environment';
}

export interface HeadlineOptions { window?: string; depth?: number | null }

/** headline is the three tiles, from WINDOW totals only: a change with runs in two buckets
 *  counts once here and in each bucket's column. Null when the window holds no record. */
export function headline(st: ProductStats, { window, depth }: HeadlineOptions = {}) {
  const t = st.totals;
  if (!t) return null;
  const w = window || undefined;
  const link = (metric: string, n: number) => (n > 0 ? { href: metricsUrl({ window: w, metric }), lists: n } : {});
  const ci = !noCI(t);
  const g = t.gates;
  const breaking = {
    value: ci ? formatCount(t.breakingCaught.distinct) : 'unknown', unknown: !ci,
    protected: protectedText(t).text, protectedQualified: protectedText(t).qualified,
    spark: series(st, 'breaking').values,
    ...(ci ? link('breaking', t.breakingCaught.distinct) : {}),
  };
  const gates = {
    value: ci ? formatCount(g.blocked) : 'unknown', unknown: !ci,
    blocked: g.blocked, clear: g.clear, unverified: g.unverified, total: g.clear + g.blocked + g.unverified,
    ...(ci ? link('blocked', g.blocked) : {}),
  };
  const lg = leadGroup(st, depth);
  const lt = lg?.leadTime;
  const known = lt?.p50Seconds != null;
  const lead = {
    key: lg?.key ?? '', title: leadTitle(st, depth),
    value: durationText(known ? lt!.p50Seconds : null), unknown: !known,
    p90: durationText(known ? lt!.p90Seconds : null),
    joined: lt?.joined ?? 0, environments: lg?.environments ?? 0,
    sub: leadSub(st, lg),
    spark: (lg?.buckets ?? []).map((b) => b.p50Seconds ?? null),
    ...(known && lt!.joined ? { href: metricsUrl({ window: w, metric: 'leadTime' }) } : {}),
  };
  return { breaking, gates, lead };
}

/** leadSub names the lead tile's basis (Decision 4): how many promotions into how many
 *  environments, and what it cannot see. */
function leadSub(st: ProductStats, lg: LeadGroup | undefined): string {
  if (!lg || lg.leadTime.p50Seconds == null) return 'No promotion here joined a publish record, so the lead time is unknown.';
  const envs = environments(st);
  const noStage = envs.filter((e) => e.depth == null && !e.cycle && !e.unordered).length;
  const where = { 'chain-end': ' at the end of their promotion chain', all: '' }[lg.key] ?? ` at ${lg.label}`;
  const scope = lg.key !== 'all' && (st.hidden?.present || noStage) ? ' of the Stages you can read' : '';
  const out = `p50 of ${countOf(lg.leadTime.joined, 'promotion')} into ${countOf(lg.environments, 'environment')}${where}${scope}, p90 ${durationText(lg.leadTime.p90Seconds)}`;
  return noStage && lg.key !== 'all' ? `${out}; ${countOf(noStage, 'environment')} with no readable Stage ${noStage === 1 ? 'is' : 'are'} left out` : out;
}

const COVERAGE_HIDDEN = 'COVERAGE_OUTSIDE_YOUR_VIEW';

/** coverageSeries is coverage over time: Services observed (services the fleet saw, less
 *  those it cannot judge and those not deployed) and Contracts described, null (a gap)
 *  where a bucket has no snapshot or no knowledge. `foot` is the recorded count of
 *  callers with no contract, never a subtraction. Null for a reader who cannot see every
 *  service: coverage history counts them all. */
export function coverageSeries(st: ProductStats) {
  if ((st.limitations ?? []).some((l) => l.code === COVERAGE_HIDDEN)) return null;
  const bs = st.buckets ?? [];
  const partial = bs.map((b) => !!b.coverage && b.coverage.completeness !== 'complete');
  const k = (b: Bucket) => b.coverage?.knowledge;
  return {
    series: [
      { key: 'observed', label: 'Services observed', color: 'var(--chart-1)', values: bs.map((b) => (k(b) ? k(b)!.services - k(b)!.unknown - k(b)!.notDeployed : null)), partial },
      { key: 'described', label: 'Contracts described', color: 'var(--chart-2)', values: bs.map((b) => (k(b) ? k(b)!.described : null)), partial },
    ],
    /** Services Pacto cannot judge, per bucket. */
    unknown: bs.map((b) => k(b)?.unknown ?? null),
    foot: bs.map((b) => (b.coverage ? `${countOf(b.coverage.uncontracted, 'caller')} observed with no contract` : '')),
  };
}

export interface Cell { text: string; unknown?: boolean; href?: string; lists?: number; title?: string }
export interface Table { caption: string; head: string[]; rows: Cell[][] }
export type ChartKey = 'stopped' | 'gates' | 'health' | 'lead' | 'coverage';
export interface TableOptions { window?: string; depth?: number | null; by?: GroupBy }

const num = (v: number | null | undefined, why = 'unknown'): Cell => (v == null ? { text: why, unknown: true } : { text: formatCount(v) });
const dur = (v: number | null | undefined): Cell => (v == null ? { text: 'unknown', unknown: true } : { text: durationText(v) });
const head = (text: string): Cell => ({ text, title: text });

/** tableRows are each chart's table view: the exact numbers it draws, the reason where it
 *  hatches, consumers protected in protectedText form and every countable rate linked to
 *  its records. `lead` also gives the selected group per bucket (the tile sparkline). */
export function tableRows(st: ProductStats, chart: ChartKey, { window, depth, by = 'depth' }: TableOptions = {}): Table[] {
  const bl = buckets(st);
  const why = unknownReasons(st);
  const first = unit(st) === 'day' ? 'Day' : 'Week';
  const perBucket = (caption: string, cols: string[], cells: (b: Bucket, i: number) => Cell[]): Table => ({
    caption, head: [first, ...cols], rows: (st.buckets ?? []).map((b, i) => [head(bl[i].label), ...cells(b, i)]),
  });
  const ciCells = (i: number, n: number, cells: () => Cell[]) => (why[i] ? Array.from({ length: n }, () => num(null, why[i]!)) : cells());
  switch (chart) {
    case 'stopped':
      return [perBucket('What Pacto stopped, per bucket', ['Breaking changes caught', 'Runs', 'Consumers protected'], (b, i) => ciCells(i, 3, () => {
        const p = protectedText(b.metrics!);
        return [num(b.metrics!.breakingCaught.distinct), num(b.metrics!.breakingCaught.runs), { text: p.text, ...(p.qualified ? { unknown: true } : {}) }];
      }))];
    case 'gates':
      return [perBucket('Gate outcomes, per bucket', ['Clear', 'Blocked', 'Unverified'], (b, i) => ciCells(i, 3, () =>
        [num(b.metrics!.gates.clear), num(b.metrics!.gates.blocked), num(b.metrics!.gates.unverified)]))];
    case 'coverage': {
      const c = coverageSeries(st);
      if (!c) return [];
      return [perBucket('Coverage, per bucket', ['Services observed', 'Contracts described', 'Pacto cannot judge', 'Callers with no contract'], (b, i) => {
        if (!b.coverage) return Array.from({ length: 4 }, () => num(null, NO_RECORDS));
        return [num(c.series[0].values[i]), num(c.series[1].values[i]), num(c.unknown[i]), num(b.coverage.uncontracted)];
      })];
    }
    case 'health': {
      const w = window || undefined;
      // An owner's or a domain's count is its share of the environment's records: the
      // records panel lists the whole environment, so only depth rows link.
      const rateCell = (e: EnvMetrics, r: (typeof RATES)[number]): Cell => {
        const v = e[r.field];
        const text = ratioText(v);
        return { text, ...(v.of ? {} : { unknown: true }), ...(by === 'depth' && v.count > 0 ? { href: metricsUrl({ window: w, metric: r.metric, environment: e.environment }), lists: v.count } : {}) };
      };
      return [{
        caption: 'Delivery health by environment', head: ['Environment', GROUP_WORD[by], ...RATES.map((r) => r.label)],
        rows: groupBy(st, by, depth).flatMap((g) => g.envs.map((e) => [head(e.environment), { text: g.label, ...(g.unknown ? { unknown: true } : {}) }, ...RATES.map((r) => rateCell(e, r))])),
      }];
    }
    case 'lead': {
      const lg = leadGroup(st, depth);
      const envs = groupBy(st, 'depth', depth).flatMap((g) => g.envs);
      return [
        { caption: 'Lead time by environment', head: ['Environment', 'p50', 'p90', 'No publish record'],
          rows: envs.map((e) => [head(e.environment), dur(e.leadTime.p50Seconds), dur(e.leadTime.p50Seconds == null ? null : e.leadTime.p90Seconds), num(e.leadTime.unjoined)]) },
        perBucket(`${leadTitle(st, depth)} per ${unit(st)}`, ['p50', 'p90', 'Promotions joined'], (_, i) => {
          const b: LeadTime | undefined = lg?.buckets?.[i];
          return [dur(b?.p50Seconds), dur(b?.p50Seconds == null ? null : b?.p90Seconds), num(b?.joined ?? 0)];
        }),
      ];
    }
  }
}

/** caption says in words what a weekly chart draws, from the window totals the tiles use.
 *  A change with runs in two buckets counts in each, so the buckets need not add up. */
export function caption(st: ProductStats, chart: 'stopped' | 'gates'): string {
  const u = unit(st);
  const t = st.totals;
  const figure = chart === 'stopped' ? 'breaking changes caught' : 'changes gated';
  const days = countOf(windowDays(st), 'day');
  const head = !t || noCI(t)
    ? `${figure[0].toUpperCase()}${figure.slice(1)} over ${days}: ${t ? NO_CI : NO_RECORDS}`
    : `${formatCount(chart === 'stopped' ? t.breakingCaught.distinct : t.gates.clear + t.gates.blocked + t.gates.unverified)} ${figure} over ${days}`;
  const why = unknownReasons(st);
  const missing = why.filter((r) => r === NO_RECORDS).length, noci = why.filter((r) => r === NO_CI).length;
  const tail = [
    `A change with runs in two ${u}s counts in each`,
    missing ? `${countOf(missing, u)} had no records` : '',
    noci ? `${countOf(noci, u)} had no CI records` : '',
  ].filter(Boolean).join('; ');
  return `${head}. ${tail}.`;
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
