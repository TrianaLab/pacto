import { describe, it, expect } from 'vitest';
import {
  ratioText, protectedText, envTitle, hiddenLine, bucketLabel, isDaily, windowDays,
  buckets, series, unknownReasons, headline, coverageSeries, environments, depthLabel, depthFilter,
  groupBy, groupOptions, attributionLine, pooledRate, worstEnvironment, smallMultipleDomain, bin, cell,
  healthMode, collapsedByDefault, leadRows, tableRows, caption, noteLimitations, SMALL_MULTIPLES_MAX, COLLAPSE_ABOVE, LEAD_TOP, LEAD_PAGE,
} from './metrics.ts';

const ratio = (count: number, of: number) => (of ? { count, of } : { count, of: 0, unknown: true });
const lt = (p50?: number, p90?: number, joined = p50 == null ? 0 : 3, unjoined = 0) =>
  (p50 == null ? { joined, unjoined, unknown: true } : { p50Seconds: p50, p90Seconds: p90, joined, unjoined });
const env = (environment: string, held: [number, number], extra: Record<string, unknown> = {}) => ({
  environment, source: 'context', operations: held[1], held: ratio(...held), failed: ratio(0, held[1]), rolledBack: ratio(0, 0),
  verificationFailed: ratio(1, held[1]), acceptedUnknown: 0, leadTime: lt(), upstream: [], ...extra,
});
const metrics = (extra: Record<string, unknown> = {}) => ({
  changes: 9, breakingCaught: { distinct: 7, runs: 19 }, consumersProtected: { count: 12, unknown: 3, partial: 1 },
  gates: { clear: 30, blocked: 4, unverified: 2 }, environments: [env('staging', [2, 10]), env('production', [1, 30])], ...extra,
});
const week = (since: string, until: string, extra: Record<string, unknown> = {}) => ({ since, until, days: 7, ...extra });
const leadGroup = (key: string, label: string, p50: number | undefined, extra: Record<string, unknown> = {}) => ({
  key, label, environments: 2, leadTime: lt(p50, p50 == null ? undefined : p50 * 3, p50 == null ? 0 : 5), buckets: [lt(), lt(100, 300), lt(200)], ...extra,
});
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
const answer = (extra: Record<string, unknown> = {}): any => ({
  asOf: '2026-11-15T10:00:00Z', window: { since: '2026-10-16T10:00:00Z', until: '2026-11-15T10:00:00Z' },
  recordsRead: 40, runs: 30, distinctChanges: 20, completeness: 'complete', provenance: 'unsigned statistics', truncated: '',
  skipped: { malformed: 0, oversized: 0, unreadable: 0 }, hidden: { present: false }, limitations: [],
  environmentOrder: 'stages', environments: [
    env('dev', [0, 10], { depth: 0, leadTime: lt(60, 120) }),
    env('staging', [2, 10], { depth: 1, parents: ['dev'], upstream: ['dev'], leadTime: lt(12_000, 90_000) }),
    env('production', [1, 30], { depth: 2, parents: ['staging'], upstream: ['dev', 'staging'], chainEnd: true }),
  ],
  services: [], servicesTotal: 0, totals: metrics(),
  environmentsBy: { owner: [], domain: [] },
  leadTimeGroups: [leadGroup('chain-end', 'chain end', 3600), leadGroup('depth-0', 'entry', 60, { depth: 0 }), leadGroup('depth-1', 'hop 2', 12_000, { depth: 1 })],
  buckets: [
    { since: '2026-10-16T10:00:00Z', until: '2026-10-18T10:00:00Z', days: 2, partial: true },
    week('2026-10-18T10:00:00Z', '2026-10-25T10:00:00Z', { metrics: metrics() }),
    week('2026-10-25T10:00:00Z', '2026-11-01T10:00:00Z', { metrics: metrics({ consumersProtected: { count: 2, unknown: 0 } }) }),
  ],
  ...extra,
});

describe('kept helpers', () => {
  it('prints a rate with nothing to take it over as unknown, never 0', () => {
    expect(ratioText({ count: 3, of: 41 })).toBe('3/41 (7%)');
    expect(ratioText({ count: 0, of: 0, unknown: true })).toBe('unknown');
    expect(ratioText({ count: 2, of: 0, unknown: true })).toBe('unknown (2 counted)');
    expect(ratioText(undefined)).toBe('unknown');
  });

  it('marks unknown consumers as a lower bound', () => {
    expect(protectedText(metrics({ consumersProtected: { count: 3, unknown: 5, partial: 2 } }))).toEqual({ text: '3 or more, 5 unknown', qualified: true });
    expect(protectedText(metrics({ consumersProtected: { count: 3, unknown: 0, partial: 1 } }))).toEqual({ text: '3 or more', qualified: true });
    expect(protectedText(metrics({ consumersProtected: { count: 3, unknown: 0 } }))).toEqual({ text: '3', qualified: false });
    expect(protectedText(metrics({ changes: 0 }))).toEqual({ text: 'no CI records', qualified: true, unknown: true });
  });

  it('labels days and weeks, titles environments and says records are hidden without counting them', () => {
    const b = { since: '2026-11-03T00:00:00Z', until: '2026-11-04T00:00:00Z', days: 1 };
    expect(bucketLabel(b, true)).toBe('3 Nov');
    expect(bucketLabel({ ...b, days: 7 }, false)).toBe('week of 3 Nov');
    expect(isDaily(answer())).toBe(false);
    expect(isDaily(answer({ buckets: [b] }))).toBe(true);
    expect(windowDays(answer())).toBe(30);
    expect(envTitle(answer())).toBe('Environments, in the order their Stages promote');
    expect(envTitle(answer({ environmentOrder: 'name' }))).toBe('Environments, listed by name: no Stage graph to order by');
    expect(hiddenLine(answer())).toBe('');
    expect(hiddenLine(answer({ hidden: { present: true } }))).toBe('Some run records name services outside your view and are left out of every number.');
  });
});

describe('buckets and series', () => {
  it('labels each bucket in full and short', () => {
    expect(buckets(answer())).toEqual([
      { label: '16 to 17 Oct (2 days)', short: '16 Oct', partial: true, days: 2 },
      { label: 'Week of 18 Oct', short: '18 Oct', partial: false, days: 7 },
      { label: 'Week of 25 Oct', short: '25 Oct', partial: false, days: 7 },
    ]);
    const days = answer({ buckets: [{ since: '2026-11-03T00:00:00Z', until: '2026-11-04T00:00:00Z', days: 1 }] });
    expect(buckets(days)[0]).toMatchObject({ label: '3 Nov', short: '3 Nov' });
    const across = answer({ buckets: [{ since: '2026-10-30T00:00:00Z', until: '2026-11-02T00:00:00Z', days: 3, partial: true }] });
    expect(buckets(across)[0].label).toBe('30 Oct to 1 Nov (3 days)');
  });

  it('gives every series null where the bucket cannot answer, with the reason', () => {
    const st = answer();
    expect(unknownReasons(st)).toEqual(['no records', null, null]);
    expect(series(st, 'breaking').values).toEqual([null, 7, 7]);
    expect(series(st, 'runs').values).toEqual([null, 19, 19]);
    expect(series(st, 'clear').values).toEqual([null, 30, 30]);
    expect(series(st, 'blocked').values).toEqual([null, 4, 4]);
    expect(series(st, 'unverified').values).toEqual([null, 2, 2]);
    // The known count with a hatched cap where it is a lower bound.
    expect(series(st, 'protected')).toEqual({ values: [null, 12, 2], caps: [false, true, false] });
    const partialOnly = answer({ buckets: [week('2026-10-18T10:00:00Z', '2026-10-25T10:00:00Z', { metrics: metrics({ consumersProtected: { count: 1, unknown: 0, partial: 2 } }) })] });
    expect(series(partialOnly, 'protected').caps).toEqual([true]);
  });

  it('says no CI records where a bucket counted no diff or impact run', () => {
    const st = answer({ buckets: [week('2026-10-18T10:00:00Z', '2026-10-25T10:00:00Z', { metrics: metrics({ changes: 0 }) })] });
    expect(unknownReasons(st)).toEqual(['no CI records']);
    for (const k of ['breaking', 'runs', 'protected', 'clear', 'blocked', 'unverified'] as const) expect(series(st, k).values).toEqual([null]);
  });

  it('every-bucket-empty answer yields only unknown reasons and no 0', () => {
    const st = answer({ buckets: [
      { since: '2026-10-16T10:00:00Z', until: '2026-10-18T10:00:00Z', days: 2, partial: true },
      week('2026-10-18T10:00:00Z', '2026-10-25T10:00:00Z'),
    ], totals: undefined, leadTimeGroups: [] });
    expect(unknownReasons(st).every((r) => r === 'no records')).toBe(true);
    for (const k of ['breaking', 'runs', 'protected', 'clear', 'blocked', 'unverified'] as const) {
      expect(series(st, k).values.some((v) => v === 0)).toBe(false);
    }
    expect(coverageSeries(st)!.series.flatMap((s) => s.values).some((v) => v === 0)).toBe(false);
    for (const chart of ['stopped', 'gates', 'coverage'] as const) {
      const cells = tableRows(st, chart)[0].rows.flatMap((r) => r.slice(1));
      expect(cells.every((c) => c.unknown && c.text === 'no records')).toBe(true);
    }
    expect(headline(st)).toBeNull();
  });
});

describe('headline', () => {
  it('reads window totals, never a sum of buckets', () => {
    // One change with runs in two weeks: the weeks say 1 and 1, the window says 1.
    const one = metrics({ breakingCaught: { distinct: 1, runs: 2 } });
    const st = answer({ totals: one, buckets: [
      week('2026-10-18T10:00:00Z', '2026-10-25T10:00:00Z', { metrics: one }),
      week('2026-10-25T10:00:00Z', '2026-11-01T10:00:00Z', { metrics: one }),
    ] });
    expect(series(st, 'breaking').values).toEqual([1, 1]);
    const h = headline(st, { window: '7d' })!;
    expect(h.breaking).toMatchObject({ value: '1', href: '#/metrics?window=7d&metric=breaking', lists: 1, protected: '12 or more, 3 unknown', spark: [1, 1] });
    expect(h.gates).toMatchObject({ blocked: 4, total: 36, clear: 30, unverified: 2, value: '4', href: '#/metrics?window=7d&metric=blocked', lists: 4 });
  });

  it('says no CI records instead of a number when the window has no diff or impact run', () => {
    const st = answer({ totals: metrics({ changes: 0, breakingCaught: { distinct: 0, runs: 0 }, gates: { clear: 0, blocked: 0, unverified: 0 } }) });
    const h = headline(st)!;
    expect(h.breaking).toMatchObject({ value: 'unknown', unknown: true, protected: 'no CI records' });
    expect(h.breaking.href).toBeUndefined();
    expect(h.gates).toMatchObject({ value: 'unknown', unknown: true });
  });

  it('takes the lead tile from the chain-end group, a depth group when filtered, or all', () => {
    const h = headline(answer())!;
    expect(h.lead).toMatchObject({ key: 'chain-end', title: 'Lead time to production', value: '1h', p90: '3h', joined: 5, environments: 2, spark: [null, 100, 200] });
    expect(h.lead.sub).toBe('p50 of 5 promotions into 2 environments at the end of their promotion chain, p90 3h');
    expect(headline(answer(), { depth: 1 })!.lead).toMatchObject({ key: 'depth-1', title: 'Lead time to hop 2', value: '3h 20m' });
    const all = answer({ leadTimeGroups: [leadGroup('all', 'every environment', 600)] });
    expect(headline(all)!.lead).toMatchObject({ key: 'all', title: 'Lead time to any environment', value: '10m' });
    // A depth with no group is unknown, never 0.
    expect(headline(answer(), { depth: 7 })!.lead).toMatchObject({ value: 'unknown', unknown: true });
    expect(headline(answer({ leadTimeGroups: [leadGroup('chain-end', 'chain end', undefined)] }))!.lead).toMatchObject({ value: 'unknown', unknown: true, p90: 'unknown' });
  });

  it('names what the lead tile leaves out', () => {
    const restricted = answer({ hidden: { present: true } });
    expect(headline(restricted)!.lead.sub).toContain('of the Stages you can read');
    const orphan = answer({ environments: [...answer().environments, env('sandbox', [0, 1])] });
    expect(headline(orphan)!.lead.sub).toBe('p50 of 5 promotions into 2 environments at the end of their promotion chain of the Stages you can read, p90 3h; 1 environment with no readable Stage is left out');
  });

  it('pools durations: the server pools, the model never takes a median of p50s', () => {
    // A 1h,1h,1h; B 10h; C 10h: pooled p50 is 1h, the median of the p50s would be 10h.
    const st = answer({
      environments: [env('a', [0, 3], { depth: 1, chainEnd: true, leadTime: lt(3600) }), env('b', [0, 1], { depth: 1, chainEnd: true, leadTime: lt(36_000) }), env('c', [0, 1], { depth: 1, chainEnd: true, leadTime: lt(36_000) })],
      leadTimeGroups: [leadGroup('chain-end', 'chain end', 3600, { environments: 3 })],
    });
    expect(headline(st)!.lead.value).toBe('1h');
  });
});

describe('coverage', () => {
  const cov = (completeness: string, uncontracted: number, knowledge?: Record<string, number>) => ({ services: 10, uncontracted, links: { exact: 0, inferred: 0, ambiguous: 0, unresolved: 0 },
    snapshotAsOf: '2026-10-20T10:00:00Z', completeness, knowledge });

  it('draws observed and described per bucket, a gap where knowledge is absent', () => {
    const st = answer({ buckets: [
      { since: '2026-10-16T10:00:00Z', until: '2026-10-18T10:00:00Z', days: 2, partial: true },
      week('2026-10-18T10:00:00Z', '2026-10-25T10:00:00Z', { coverage: cov('complete', 4, { services: 10, described: 6, withGaps: 1, unknown: 3, notDeployed: 2 }) }),
      week('2026-10-25T10:00:00Z', '2026-11-01T10:00:00Z', { coverage: cov('partial', 1) }),
    ] });
    const c = coverageSeries(st)!;
    expect(c.series.map((s) => [s.key, s.label, s.color, s.values])).toEqual([
      ['observed', 'Services observed', 'var(--chart-1)', [null, 5, null]],
      ['described', 'Contracts described', 'var(--chart-2)', [null, 6, null]],
    ]);
    expect(c.series[0].partial).toEqual([false, false, true]);
    expect(c.unknown).toEqual([null, 3, null]);
    // The foot is the recorded count, never services minus described.
    expect(c.foot).toEqual(['', '4 callers observed with no contract', '1 caller observed with no contract']);
    const t = tableRows(st, 'coverage')[0];
    expect(t.head).toEqual(['Week', 'Services observed', 'Contracts described', 'Pacto cannot judge', 'Callers with no contract']);
    expect(t.rows[1].map((x) => x.text)).toEqual(['Week of 18 Oct', '5', '6', '3', '4']);
    expect(t.rows[2].map((x) => x.text)).toEqual(['Week of 25 Oct', 'unknown', 'unknown', 'unknown', '1']);
  });

  it('is absent for a reader who cannot see every service', () => {
    const st = answer({ limitations: [{ code: 'COVERAGE_OUTSIDE_YOUR_VIEW', message: 'x' }] });
    expect(coverageSeries(st)).toBeNull();
    expect(tableRows(st, 'coverage')).toEqual([]);
  });
});

describe('environments and groups', () => {
  it('reads depth, chain end and parents, and labels depth by depth only', () => {
    expect(environments(answer()).map((e) => [e.environment, e.depth, e.chainEnd, e.parents])).toEqual([
      ['dev', 0, false, []], ['staging', 1, false, ['dev']], ['production', 2, true, ['staging']],
    ]);
    expect([0, 1, 2, 3, undefined, null].map((d) => depthLabel(d))).toEqual(['entry', 'hop 2', 'hop 3', 'hop 4', 'Depth unknown', 'Depth unknown']);
    for (const e of answer().environments) expect([0, 1, 2].map(depthLabel)).not.toContain(e.environment);
  });

  it('offers the depth filter only above the small-multiples limit', () => {
    expect(depthFilter(answer())).toBeNull();
    const many = Array.from({ length: SMALL_MULTIPLES_MAX + 1 }, (_, i) => env(`e${i}`, [0, 1], i < 7 ? { depth: i % 2 } : {}));
    expect(depthFilter(answer({ environments: many }))).toEqual({ total: 9, depths: [{ depth: 0, label: 'entry', count: 4 }, { depth: 1, label: 'hop 2', count: 3 }], unknown: 2 });
  });

  it('groups by depth with unknown last, and the depth filter scopes the groups', () => {
    const st = answer({ environments: [...answer().environments, env('loop', [0, 1], { cycle: true })] });
    expect(groupBy(st, 'depth').map((g) => [g.key, g.label, g.unknown, g.envs.map((e) => e.environment)])).toEqual([
      ['depth-0', 'entry', false, ['dev']], ['depth-1', 'hop 2', false, ['staging']], ['depth-2', 'hop 3', false, ['production']], ['depth-unknown', 'Depth unknown', true, ['loop']],
    ]);
    expect(groupBy(st, 'depth', 1).map((g) => g.key)).toEqual(['depth-1']);
  });

  it('puts a multi-owner environment under each owner with that owner counts, labelled by key', () => {
    const st = answer({ environmentsBy: {
      attribution: { asOf: '2026-11-15T09:00:00Z', provenance: 'inferred', snapshotId: 's', partial: true },
      owner: [
        { key: 'payments', label: 'payments', environments: [env('production', [1, 10])] },
        { key: 'search', label: 'search', environments: [env('production', [0, 20]), env('staging', [2, 10])] },
        { key: '(unowned)', label: '(unowned)', environments: [env('dev', [0, 1])] },
        { key: '(unknown)', label: '(unknown)', environments: [env('dev', [0, 2])] },
      ],
      domain: [{ key: '(none)', label: '(none)', environments: [env('dev', [0, 1])] }, { key: '(unknown)', label: '(unknown)', environments: [] }],
    } });
    const owners = groupBy(st, 'owner');
    expect(owners.map((g) => [g.label, g.unknown])).toEqual([['payments', false], ['search', false], ['No owner', true], ['Owner unknown', true]]);
    expect(owners[0].envs[0].held).toEqual({ count: 1, of: 10 });
    expect(owners[1].envs[0].held).toEqual({ count: 0, of: 20 });
    expect(groupBy(st, 'domain').map((g) => g.label)).toEqual(['No domain']);
    // The depth filter keeps an owner's environments at that depth.
    expect(groupBy(st, 'owner', 1).map((g) => [g.label, g.envs.map((e) => e.environment)])).toEqual([['search', ['staging']]]);
    expect(groupOptions(st)).toEqual(['depth', 'owner']);
    expect(attributionLine(st, 'owner')).toMatch(/^owner inferred from the fleet snapshot of Nov 15, 2026, \d\d:00, partial$/);
    expect(attributionLine(answer(), 'owner')).toBe('');
  });

  it('hides a group-by that yields one group', () => {
    const one = answer({ environments: [env('a', [0, 1], { depth: 0 }), env('b', [0, 1], { depth: 0 })] });
    expect(groupOptions(one)).toEqual([]);
  });
});

describe('rates', () => {
  it('pools only environments with something to take the rate over', () => {
    const p = pooledRate([env('a', [0, 0], { held: { count: 2, of: 0, unknown: true } }), env('b', [1, 10])], 'held');
    expect(p).toEqual({ count: 1, of: 10, counted: 1, excluded: 1, strayCount: 2 });
    expect(p.count / p.of).toBeLessThanOrEqual(1);
    expect(pooledRate([env('a', [0, 0], { held: { count: 2, of: 0, unknown: true } })], 'held')).toEqual({ count: 0, of: 0, counted: 0, excluded: 1, strayCount: 2, unknown: true });
    expect(pooledRate([], 'held')).toMatchObject({ unknown: true, counted: 0 });
    const many = [env('a', [3, 3]), env('b', [5, 5]), env('c', [0, 0], { held: { count: 9, of: 0, unknown: true } })];
    const q = pooledRate(many, 'held');
    expect(q.count / q.of).toBeLessThanOrEqual(1);
  });

  it('names the worst environment, ignoring rates with nothing to count', () => {
    const envs = [env('a', [1, 10]), env('b', [0, 0], { held: { count: 5, of: 0, unknown: true } }), env('c', [3, 10])];
    expect(worstEnvironment(envs, 'held')?.environment).toBe('c');
    expect(worstEnvironment([envs[1]], 'held')).toBeNull();
  });

  it('shares one small-multiple domain, at least 50% and never a clamp', () => {
    expect(smallMultipleDomain([env('a', [1, 10])])).toBe(0.5);
    expect(smallMultipleDomain([env('a', [6, 10])])).toBeGreaterThanOrEqual(0.6);
    expect(smallMultipleDomain([env('a', [0, 0], { held: { count: 4, of: 0, unknown: true } })])).toBe(0.5);
  });

  it('bins rates and keeps a real 0 and unknown apart', () => {
    expect([0.01, 0.1, 0.15, 0.2, 0.3, 0.35, 0.9].map(bin)).toEqual([1, 1, 2, 2, 3, 3, 4]);
    expect(cell({ count: 0, of: 5 })).toEqual({ count: 0, of: 5, foot: '' });
    expect(cell({ count: 2, of: 5 }, 'x')).toEqual({ count: 2, of: 5, bin: 4, foot: 'x' });
    expect(cell({ count: 2, of: 0, unknown: true })).toEqual({ count: 2, of: 0, foot: '' });
  });

  it('picks the health form and the collapse by size', () => {
    expect([1, 8, 9, 200].map(healthMode)).toEqual(['multiples', 'multiples', 'grid', 'grid']);
    expect([50, 51].map(collapsedByDefault)).toEqual([false, true]);
    expect(COLLAPSE_ABOVE).toBe(50);
  });
});

describe('lead rows', () => {
  const many = Array.from({ length: 25 }, (_, i) => env(`e${String(i).padStart(2, '0')}`, [0, 1], { depth: i % 2, leadTime: i === 3 ? lt() : lt(1000 + i, 2000 + i) }));

  it('lists the slowest p50 first, unknown last, under group headers', () => {
    const st = answer({ environments: many });
    const r = leadRows(groupBy(st, 'depth'));
    expect(r.total).toBe(25);
    const names = r.rows.filter((x) => 'name' in x);
    expect(names).toHaveLength(LEAD_TOP);
    expect(r.rows[0]).toEqual({ group: 'entry' });
    expect(names.map((x) => ('name' in x ? x.name : ''))).not.toContain('e03');
    const withUnknown = leadRows(groupBy(answer({ environments: many.slice(0, 5) }), 'depth'));
    const odd = withUnknown.rows.slice(withUnknown.rows.findIndex((x) => 'group' in x && x.group === 'hop 2'));
    expect(odd.at(-1)).toMatchObject({ name: 'e03', p50: null, p90: null });
  });

  it('pages every environment at 20 when all are shown', () => {
    const st = answer({ environments: many });
    const p0 = leadRows(groupBy(st, 'depth'), { all: true });
    expect(p0.rows.filter((x) => 'name' in x)).toHaveLength(LEAD_PAGE);
    expect([p0.from, p0.to, p0.total]).toEqual([1, 20, 25]);
    const p1 = leadRows(groupBy(st, 'depth'), { all: true, page: 1 });
    expect(p1.rows.filter((x) => 'name' in x)).toHaveLength(5);
    expect(p1.rows.at(-1)).toMatchObject({ name: 'e03' });
  });

  it('says how many promotions it leaves out', () => {
    const r = leadRows(groupBy(answer({ environments: [env('a', [0, 1], { depth: 0, leadTime: lt(10, 20, 3, 2) })] }), 'depth'));
    expect(r.rows[1]).toEqual({ name: 'a', p50: 10, p90: 20, foot: '2 promotions with no publish record are left out' });
  });
});

describe('table rows', () => {
  it('prints the exact numbers each chart draws, with unknown where it hatches', () => {
    const st = answer();
    const [stopped] = tableRows(st, 'stopped');
    expect(stopped.head).toEqual(['Week', 'Breaking changes caught', 'Runs', 'Consumers protected']);
    expect(stopped.rows.map((r) => r.map((c) => c.text))).toEqual([
      ['16 to 17 Oct (2 days)', 'no records', 'no records', 'no records'],
      ['Week of 18 Oct', '7', '19', '12 or more, 3 unknown'],
      ['Week of 25 Oct', '7', '19', '2'],
    ]);
    expect(stopped.rows[1][3].unknown).toBe(true);
    const [gates] = tableRows(st, 'gates');
    expect(gates.rows[1].map((c) => c.text)).toEqual(['Week of 18 Oct', '30', '4', '2']);
    const ci = answer({ buckets: [week('2026-10-18T10:00:00Z', '2026-10-25T10:00:00Z', { metrics: metrics({ changes: 0 }) })] });
    expect(tableRows(ci, 'gates')[0].rows[0].slice(1).map((c) => [c.text, c.unknown])).toEqual([['no CI records', true], ['no CI records', true], ['no CI records', true]]);
  });

  it('links each environment rate to its records, with how many it lists', () => {
    const st = answer({ environments: [env('dev', [0, 10], { depth: 0, rolledBack: { count: 2, of: 0, unknown: true } })] });
    const [health] = tableRows(st, 'health', { window: '7d' });
    expect(health.head).toEqual(['Environment', 'Promotion depth', 'Held', 'Failed', 'Rolled back', 'Verification failed']);
    const row = health.rows[0];
    expect(row[0]).toEqual({ text: 'dev', title: 'dev' });
    expect(row[2]).toEqual({ text: '0/10 (0%)' });
    expect(row[4]).toEqual({ text: 'unknown (2 counted)', unknown: true, href: '#/metrics?window=7d&metric=rolledBack&environment=dev', lists: 2 });
    expect(row[5]).toMatchObject({ text: '1/10 (10%)', lists: 1 });
  });

  it('never links an owner row: its count is that owner share of the environment records', () => {
    const st = answer({ environmentsBy: { owner: [{ key: 'a', label: 'a', environments: [env('dev', [1, 10])] }, { key: 'b', label: 'b', environments: [env('dev', [2, 10])] }], domain: [] } });
    const [health] = tableRows(st, 'health', { by: 'owner' });
    expect(health.head[1]).toBe('Owner');
    expect(health.rows.map((r) => [r[0].text, r[1].text, r[2].text, r[2].href])).toEqual([['dev', 'a', '1/10 (10%)', undefined], ['dev', 'b', '2/10 (20%)', undefined]]);
  });

  it('lists lead time per environment and the selected group per bucket', () => {
    const [envs, perBucket] = tableRows(answer(), 'lead');
    expect(envs.head).toEqual(['Environment', 'p50', 'p90', 'No publish record']);
    expect(envs.rows.map((r) => r.map((c) => c.text))).toEqual([['dev', '1m', '2m', '0'], ['staging', '3h 20m', '1d 1h', '0'], ['production', 'unknown', 'unknown', '0']]);
    expect(perBucket.caption).toBe('Lead time to production per week');
    expect(perBucket.rows.map((r) => r.map((c) => c.text))).toEqual([
      ['16 to 17 Oct (2 days)', 'unknown', 'unknown', '0'], ['Week of 18 Oct', '1m 40s', '5m', '3'], ['Week of 25 Oct', '3m 20s', 'unknown', '3'],
    ]);
    const filtered = tableRows(answer(), 'lead', { depth: 1 });
    expect(filtered[0].rows.map((r) => r[0].text)).toEqual(['staging']);
    expect(filtered[1].caption).toBe('Lead time to hop 2 per week');
  });
});

describe('captions', () => {
  it('writes the caption from the model, saying a change can count in two weeks', () => {
    expect(caption(answer(), 'stopped')).toBe('7 breaking changes caught over 30 days. A change with runs in two weeks counts in each; 1 week had no records.');
    expect(caption(answer(), 'gates')).toBe('36 changes gated over 30 days. A change with runs in two weeks counts in each; 1 week had no records.');
    const daily = answer({ buckets: [{ since: '2026-11-03T00:00:00Z', until: '2026-11-04T00:00:00Z', days: 1 }], window: { since: '2026-11-03T00:00:00Z', until: '2026-11-10T00:00:00Z' } });
    expect(caption(daily, 'stopped')).toBe('7 breaking changes caught over 7 days. A change with runs in two days counts in each; 1 day had no records.');
    const noci = answer({ totals: metrics({ changes: 0 }), buckets: [week('2026-10-18T10:00:00Z', '2026-10-25T10:00:00Z', { metrics: metrics({ changes: 0 }) })] });
    expect(caption(noci, 'stopped')).toBe('Breaking changes caught over 30 days: no CI records. A change with runs in two weeks counts in each; 1 week had no CI records.');
  });
});

describe('noteLimitations', () => {
  const l = (code: string) => ({ code, message: code.toLowerCase() });
  it('puts the limitation no other line says on the note, and the rest behind it', () => {
    const st = answer({ limitations: [l('RUN_RECORDS_SKIPPED'), l('ENVIRONMENT_ORDER_UNKNOWN'), l('LEAD_TIME_UNJOINED'), l('COVERAGE_OUTSIDE_YOUR_VIEW')] });
    const { note, rest } = noteLimitations(st);
    expect(note?.code).toBe('LEAD_TIME_UNJOINED');
    // The coverage card says its own limitation, so the list leaves it out.
    expect(rest.map((r) => r.code)).toEqual(['RUN_RECORDS_SKIPPED', 'ENVIRONMENT_ORDER_UNKNOWN']);
  });
  it('leaves the note empty when the provenance line already says everything', () => {
    const { note, rest } = noteLimitations(answer({ limitations: [l('RUN_RECORDS_TRUNCATED')] }));
    expect(note).toBeNull();
    expect(rest.map((r) => r.code)).toEqual(['RUN_RECORDS_TRUNCATED']);
  });
  it('takes an unranked code after every ranked one', () => {
    expect(noteLimitations(answer({ limitations: [l('SOMETHING_NEW'), l('ATTRIBUTION_PARTIAL')] })).note?.code).toBe('ATTRIBUTION_PARTIAL');
    expect(noteLimitations(answer({ limitations: [l('SOMETHING_NEW')] })).note?.code).toBe('SOMETHING_NEW');
  });
});
