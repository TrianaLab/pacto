import { describe, it, expect } from 'vitest';
import { tiles, ratioText, sumRate, duration, trend, caption, envTitle, hiddenLine, bucketLabel, isDaily, windowDays, LEAD_TILES } from './metrics.ts';

const ratio = (count: number, of: number) => (of ? { count, of } : { count: 0, of: 0, unknown: true });
const env = (environment: string, held: [number, number], extra: Record<string, unknown> = {}) => ({
  environment, source: 'context', operations: held[1], held: ratio(...held), failed: ratio(0, held[1]), rolledBack: ratio(0, 0),
  verificationFailed: ratio(1, held[1]), acceptedUnknown: 0, leadTime: { joined: 0, unjoined: 2, unknown: true }, upstream: [], ...extra,
});
const metrics = (extra: Record<string, unknown> = {}) => ({
  breakingCaught: { distinct: 7, runs: 19 }, consumersProtected: { count: 12, unknown: 3, partial: 1 },
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
  services: [], servicesTotal: 0, totals: metrics(),
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
    expect(trend(st, 'protected')[1]).toEqual({ label: 'week of 18 Oct (a lower bound)', value: 12, hatch: true });
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
    expect(caption('Breaking changes caught', trend(st, 'breaking'), false, 30))
      .toBe('Breaking changes caught per week: 14 over 30 days; 1 week had no records.');
    expect(caption('Held promotions', trend(st, 'held'), false, 30))
      .toBe('Held promotions per week: 3 over 30 days; 1 week had no records; 1 week had nothing to take the rate over.');
    expect(caption('Gates blocked', [{ label: 'a', value: 2 }], true, 7)).toBe('Gates blocked per day: 2 over 7 days.');
  });

  it('titles the environments by how they are ordered', () => {
    expect(envTitle(answer())).toBe('Environments, in the order their Stages promote');
    expect(envTitle(answer({ environmentOrder: 'name' }))).toBe('Environments, listed by name: no Stage graph to order by');
  });

  it('says records are hidden without counting them', () => {
    expect(hiddenLine(answer())).toBe('');
    expect(hiddenLine(answer({ hidden: { present: true } }))).toBe('Some run records name services outside your view and are left out of every number.');
  });
});
