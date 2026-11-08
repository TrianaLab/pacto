import { describe, it, expect } from 'vitest';
import { historyModel, tickLabel, TL_KINDS, TL_HEIGHT } from './historyFigure.ts';

const zero = { missing: 0, failed: 0, held: 0, acceptedUnknown: 0, released: 0, other: 0 };
const end = '2026-11-08T14:04:00Z';
function timeline(per: (i: number) => Partial<typeof zero>) {
  const start = Date.parse(end) - 24 * 3600_000;
  const buckets = Array.from({ length: 12 }, (_, i) => ({
    start: new Date(start + i * 7200_000).toISOString().replace('.000', ''), end: new Date(start + (i + 1) * 7200_000).toISOString().replace('.000', ''), ...zero, ...per(i),
  }));
  const totals = { ...zero };
  for (const b of buckets) for (const k of Object.keys(zero) as (keyof typeof zero)[]) totals[k] += b[k];
  return { start: buckets[0].start, end, buckets, totals, untimed: 0 };
}

describe('tickLabel', () => {
  it('carries the day of a time outside the last day (SPEC 6.2 r3)', () => {
    expect(tickLabel('2026-11-08T09:00:00Z', end)).toBe('09:00');
    expect(tickLabel('2026-11-07T14:04:00Z', end)).toBe('yesterday 14:04');
    expect(tickLabel('2026-11-05T23:13:00Z', end)).toBe('Nov 5 23:13');
    expect(tickLabel('soon', end)).toBe('soon');
  });
});

describe('historyModel', () => {
  it('stacks each column worst on top with floors, and names every missing slice in words', () => {
    const t = timeline((i) => (i === 0 ? { missing: 2, released: 1 } : i === 11 ? { failed: 1, held: 2, released: 40, acceptedUnknown: 1 } : {}));
    const m = historyModel(t, { source: 'records', live: true });
    expect(m.columns).toHaveLength(12);
    expect(m.columns[11].segs.map((s) => s.k.key)).toEqual(['failed', 'held', 'acceptedUnknown', 'released']);
    expect(m.columns[11].segs.map((s) => s.h)).toEqual([4, 4, 4, Math.round((40 / 44) * TL_HEIGHT)]);
    expect(m.columns[0].missing).toBe(2);
    expect(m.gapWords).toEqual(['2 records between yesterday 14:04 and yesterday 16:04']);
    expect(m.ticks).toEqual({ first: 'yesterday 14:04', mid: '02:04', last: 'now, 14:04' });
    expect(m.caption).toBe('1 failed and 2 held for a person, of 45 changes from yesterday 14:04 to now, 14:04 UTC. The record is missing in 1 slice, 2 records in all.');
    expect(TL_KINDS.find((k) => k.key === 'acceptedUnknown')).toMatchObject({ tone: 'neutral', hatch: true });
  });

  it('never claims a complete record it cannot see', () => {
    const t = { ...timeline(() => ({ missing: 1 })), untimed: 3 };
    const cluster = historyModel(t, { source: 'cluster', live: false });
    expect(cluster.gaps).toHaveLength(0);
    expect(cluster.caption).toContain('Counted from the changes still in the cluster, which cannot show a missing record');
    expect(cluster.caption).toContain('3 changes with no time are not drawn.');
    expect(cluster.ticks.last).toBe('14:04');
    expect(historyModel(t, { source: 'status', live: true }).caption).toContain('this Stage keeps no signed records');
    expect(historyModel(timeline(() => ({})), { source: 'records', live: true }).caption).toContain('No record is missing in this window.');
  });
});
