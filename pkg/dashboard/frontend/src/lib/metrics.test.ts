import { describe, it, expect } from 'vitest';
import { tiles, ratioText, sumRate, duration, trend, caption, envTitle, hiddenLine, bucketLabel, isDaily, windowDays, windowTotal, protectedText, coverageTrend, LEAD_TILES } from './metrics.ts';

const ratio = (count: number, of: number) => (of ? { count, of } : { count: 0, of: 0, unknown: true });
const env = (environment: string, held: [number, number], extra: Record<string, unknown> = {}) => ({
  environment, source: 'context', operations: held[1], held: ratio(...held), failed: ratio(0, held[1]), rolledBack: ratio(0, 0),
  verificationFailed: ratio(1, held[1]), acceptedUnknown: 0, leadTime: { joined: 0, unjoined: 2, unknown: true }, upstream: [], ...extra,
});
const metrics = (extra: Record<string, unknown> = {}) => ({
  changes: 9, breakingCaught: { distinct: 7, runs: 19 }, consumersProtected: { count: 12, unknown: 3, partial: 1 },
  gates: { clear: 30, blocked: 4, unverified: 0 }, environments: [env('staging', [2, 10]), env('production', [1, 30])], ...extra,
});
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
const answer = (extra: Record<string, unknown> = {}): any => ({
  asOf: '2026-11-15T10:00:00Z', window: { since: '2026-10-16T10:00:00Z', until: '2026-11-15T10:00:00Z' },
  recordsRead: 40, runs: 30, distinctChanges: 20, completeness: 'complete', provenance: 'unsigned statistics', truncated: '',
  skipped: { malformed: 0, oversized: 0, unreadable: 0 }, hidden: { present: false }, limitations: [],
  environmentOrder: 'stages', environments: [
    env('staging', [2, 10], { leadTime: { p50Seconds: 12_000, p90Seconds: 90_000, joined: 4, unjoined: 0 } }),
    env('production', [1, 30]),
  ],
  services: [], servicesTotal: 0, totals: metrics(), environmentsBy: { owner: [], domain: [] },
  buckets: [
    { since: '2026-10-16T10:00:00Z', until: '2026-10-18T10:00:00Z', days: 2, partial: true },
    { since: '2026-10-18T10:00:00Z', until: '2026-10-25T10:00:00Z', days: 7, metrics: metrics() },
    { since: '2026-10-25T10:00:00Z', until: '2026-11-01T10:00:00Z', days: 7, metrics: metrics({ environments: [], consumersProtected: { count: 2, unknown: 0 } }) },
  ],
  ...extra,
});

describe('metrics model', () => {
  it('links every counted tile to its records, with how many it lists', () => {
    const ts = tiles(answer(), '7d');
    const by = Object.fromEntries(ts.map((t) => [t.key, t]));
    expect(by.breaking).toMatchObject({ value: '7', detail: '19 runs', href: '#/metrics?window=7d&metric=breaking', lists: 7 });
    // The consumers tile opens the blocked changes that held them back.
    expect(by.protected).toMatchObject({ value: '12', lists: 4, detail: 'by 4 blocked changes, 3 unknown, a lower bound' });
    expect(by.held).toMatchObject({ value: '3/40 (8%)', lists: 3 });
    // Nothing to list is no link.
    expect(by.unverified.href).toBeUndefined();
    expect(by.rolledBack).toMatchObject({ value: 'unknown', unknown: true });
    expect(by.rolledBack.href).toBeUndefined();
    expect(by['lead:staging']).toMatchObject({ value: '3h 20m', href: '#/metrics?window=7d&metric=leadTime&environment=staging' });
    expect(by['lead:production']).toMatchObject({ value: 'unknown', unknown: true, detail: '2 with no publish record' });
    expect(tiles(answer({ totals: undefined }))).toEqual([]);
  });

  it('draws at most a few lead-time tiles, in the answer order', () => {
    const many = Array.from({ length: 9 }, (_, i) => env(`env-${i}`, [0, 1]));
    const lead = tiles(answer({ environments: many })).filter((t) => t.key.startsWith('lead:'));
    expect(lead.map((t) => t.key)).toEqual(many.slice(0, LEAD_TILES).map((e) => `lead:${e.environment}`));
  });

  it('prints a rate with nothing to take it over as unknown, never 0', () => {
    expect(ratioText({ count: 3, of: 41 })).toBe('3/41 (7%)');
    expect(ratioText({ count: 0, of: 0, unknown: true })).toBe('unknown');
    expect(ratioText(undefined)).toBe('unknown');
    expect(sumRate([], 'held')).toEqual({ count: 0, of: 0, unknown: true });
  });

  it('prints durations in the two largest units', () => {
    expect([duration(45), duration(720), duration(12_000), duration(187_200), duration(3_600), duration(0), duration(undefined)])
      .toEqual(['45s', '12m', '3h 20m', '2d 4h', '1h', '0s', 'unknown']);
  });

  it('says why a bucket has no number: no records, or nothing to take the rate over', () => {
    const st = answer();
    expect(trend(st, 'breaking')).toEqual([
      { label: '2 days from 16 Oct', value: 0, reason: 'no-records', unknown: 'no records' },
      { label: 'week of 18 Oct', value: 7 },
      { label: 'week of 25 Oct', value: 7 },
    ]);
    expect(trend(st, 'held')[2]).toMatchObject({ reason: 'no-denominator', unknown: 'nothing to count' });
    expect(trend(st, 'held')[1]).toEqual({ label: 'week of 18 Oct', value: 3 });
    // A count judged on a partial snapshot is hatched as a lower bound.
    // The qualifier is in the printed value, which is never clipped like a label.
    expect(trend(st, 'protected')[1]).toEqual({ label: 'week of 18 Oct', value: 12, hatch: true, lowerBound: true, display: '12 or more' });
    expect(trend(st, 'blocked')[1].value).toBe(4);
  });

  it('labels days and weeks, and knows which the answer holds', () => {
    const b = { since: '2026-11-03T00:00:00Z', until: '2026-11-04T00:00:00Z', days: 1 };
    expect(bucketLabel(b, true)).toBe('3 Nov');
    expect(bucketLabel({ ...b, days: 7 }, false)).toBe('week of 3 Nov');
    expect(isDaily(answer())).toBe(false);
    expect(isDaily(answer({ buckets: [b] }))).toBe(true);
    expect(windowDays(answer())).toBe(30);
  });

  it('writes the caption from the same rows the chart draws', () => {
    const st = answer();
    // The window figure is the tile's, never the sum of the buckets (7 + 7 here).
    expect(caption('Breaking changes caught', trend(st, 'breaking'), false, 30, windowTotal(st, 'breaking')))
      .toBe('Breaking changes caught per week: 7 over 30 days; 1 week had no records.');
    expect(caption('Held promotions', trend(st, 'held'), false, 30, windowTotal(st, 'held')))
      .toBe('Held promotions per week: 3 over 30 days; 1 week had no records; 1 week had nothing to take the rate over.');
    expect(caption('Consumers protected', trend(st, 'protected'), false, 30, windowTotal(st, 'protected')))
      .toBe('Consumers protected per week: 12 or more over 30 days; 1 week had no records; 1 week is a lower bound.');
    expect(caption('Gates blocked', [{ label: 'a', value: 2 }], true, 7, { value: 2 })).toBe('Gates blocked per day: 2 over 7 days.');
    expect(windowTotal(answer({ totals: undefined }), 'breaking')).toBeNull();
  });

  it('titles the environments by how they are ordered', () => {
    expect(envTitle(answer())).toBe('Environments, in the order their Stages promote');
    expect(envTitle(answer({ environmentOrder: 'name' }))).toBe('Environments, listed by name: no Stage graph to order by');
  });

  it('says records are hidden without counting them', () => {
    expect(hiddenLine(answer())).toBe('');
    expect(hiddenLine(answer({ hidden: { present: true } }))).toBe('Some run records name services outside your view and are left out of every number.');
  });

  it('draws the CI numbers of a window with no diff or impact run as unknown, never 0', () => {
    const st = answer({ totals: metrics({ changes: 0, breakingCaught: { distinct: 0, runs: 0 }, consumersProtected: { count: 0, unknown: 0 }, gates: { clear: 0, blocked: 0, unverified: 0 } }),
      buckets: [{ since: '2026-10-18T10:00:00Z', until: '2026-10-25T10:00:00Z', days: 7, metrics: metrics({ changes: 0 }) }] });
    const by = Object.fromEntries(tiles(st).map((t) => [t.key, t]));
    for (const key of ['breaking', 'protected', 'clear', 'blocked', 'unverified']) {
      expect(by[key]).toMatchObject({ value: 'unknown', unknown: true, detail: 'no CI records' });
      expect(by[key].href).toBeUndefined();
    }
    expect(by.held).toMatchObject({ value: '3/40 (8%)' });
    expect(trend(st, 'blocked')[0]).toMatchObject({ reason: 'no-ci', unknown: 'no CI records', value: 0 });
    expect(caption('Gates blocked', trend(st, 'blocked'), false, 30, windowTotal(st, 'blocked')))
      .toBe('Gates blocked per week: unknown over 30 days; 1 week had no CI records.');
    expect(protectedText(metrics({ changes: 0 }))).toEqual({ text: 'no CI records', qualified: true, unknown: true });
  });

  it('marks unknown consumers as a lower bound, in the trend and in a service row', () => {
    const m = metrics({ consumersProtected: { count: 0, unknown: 5 } });
    expect(trend(answer({ buckets: [{ since: '2026-10-18T10:00:00Z', until: '2026-10-25T10:00:00Z', days: 7, metrics: m }] }), 'protected')[0])
      .toMatchObject({ value: 0, hatch: true, display: '0 or more' });
    expect(protectedText(metrics({ consumersProtected: { count: 3, unknown: 5, partial: 2 } }))).toEqual({ text: '3 or more, 5 unknown', qualified: true });
    expect(protectedText(metrics({ consumersProtected: { count: 3, unknown: 0 } }))).toEqual({ text: '3', qualified: false });
  });

  it('keeps a rollback with no succeeded promote counted: the rate is unknown, never 0', () => {
    const stray = { count: 1, of: 0, unknown: true };
    const envs = [env('staging', [0, 2], { rolledBack: ratio(0, 2) }), env('production', [0, 1], { rolledBack: stray })];
    expect(sumRate(envs, 'rolledBack')).toEqual({ count: 1, of: 2, unknown: true });
    expect(ratioText(sumRate(envs, 'rolledBack'))).toBe('unknown (1 counted)');
    const by = Object.fromEntries(tiles(answer({ totals: metrics({ environments: envs }) })).map((t) => [t.key, t]));
    expect(by.rolledBack).toMatchObject({ unknown: true, lists: 1, href: '#/metrics?metric=rolledBack' });
    const st = answer({ buckets: [{ since: '2026-10-18T10:00:00Z', until: '2026-10-25T10:00:00Z', days: 7, metrics: metrics({ environments: [envs[1]] }) }] });
    expect(trend(st, 'rolledBack')[0]).toMatchObject({ reason: 'no-denominator', unknown: '1 with no rate' });
  });

  it('draws every coverage series per bucket, unknown on its own and hatched', () => {
    const cov = (completeness: string, knowledge?: Record<string, number>) => ({ services: 10, uncontracted: 0, links: { exact: 0, inferred: 0, ambiguous: 0, unresolved: 0 },
      snapshotAsOf: '2026-10-20T10:00:00Z', completeness, knowledge });
    const st = answer({ buckets: [
      { since: '2026-10-16T10:00:00Z', until: '2026-10-18T10:00:00Z', days: 2, partial: true },
      { since: '2026-10-18T10:00:00Z', until: '2026-10-25T10:00:00Z', days: 7, coverage: cov('complete', { services: 10, described: 6, withGaps: 1, unknown: 3, notDeployed: 2 }) },
      { since: '2026-10-25T10:00:00Z', until: '2026-11-01T10:00:00Z', days: 7, coverage: cov('partial') },
    ] });
    expect(coverageTrend(st, 'services').map((r) => r.unknown ?? r.value)).toEqual(['no records', 10, 10]);
    expect(coverageTrend(st, 'services')[2].hatch).toBe(true);
    expect(coverageTrend(st, 'described').map((r) => r.unknown ?? r.value)).toEqual(['no records', 6, 'unknown']);
    expect(coverageTrend(st, 'deployed')[1]).toMatchObject({ value: 5, hatch: false });
    expect(coverageTrend(st, 'unknown')[1]).toMatchObject({ value: 3, hatch: true });
  });

  it('gives a lead-time tile its environment on a line of its own', () => {
    const lead = tiles(answer()).find((t) => t.key === 'lead:staging');
    expect(lead).toMatchObject({ label: 'Lead time p50', sub: 'staging' });
  });
});
