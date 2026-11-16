import { describe, it, expect } from 'vitest';
import { niceMax, bandScale, linearScale, logScale, logDomain, logTicks, colPath, barPath, showTick, directLabelIndexes, durationText, SERIES, HEAT } from './chart.ts';

describe('niceMax', () => {
  it('rounds up to the next nice step', () => {
    expect([1, 3, 4.2, 7, 12, 17, 23, 45, 61, 99, 101].map(niceMax)).toEqual([1, 4, 5, 8, 16, 20, 25, 50, 80, 100, 160]);
  });
  it('handles fractions, zero and nothing known', () => {
    expect(niceMax(0.3)).toBe(0.4);
    expect(niceMax(0.5)).toBe(0.5);
    expect(niceMax(0)).toBe(1);
    expect(niceMax(Number.NaN)).toBe(1);
  });
});

describe('scales', () => {
  it('splits a width into equal bands with centres', () => {
    const b = bandScale(4, 200);
    expect(b.band).toBe(50);
    expect([b.x(0), b.mid(0), b.mid(3)]).toEqual([0, 25, 175]);
  });
  it('maps a linear domain and survives an empty one', () => {
    expect(linearScale([0, 10], [100, 0])(5)).toBe(50);
    expect(linearScale([3, 3], [0, 80])(3)).toBe(0);
  });
});

describe('log axis', () => {
  it('floors the domain at 1s so a 0s lead stays finite and pinned at the start', () => {
    const [lo, hi] = logDomain([0, 3_600, null, 86_400])!;
    expect(lo).toBe(1);
    expect(hi).toBe(86_400 * 1.25);
    const x = logScale([lo, hi], [10, 210]);
    expect(Number.isFinite(x(0))).toBe(true);
    expect(x(0)).toBe(10);
    expect(x(hi)).toBeCloseTo(210);
  });
  it('starts below the fastest known value', () => {
    expect(logDomain([1_600, 3_200])![0]).toBe(1_000);
  });
  it('knows nothing when nothing is known', () => {
    expect(logDomain([null, undefined])).toBeNull();
  });
  it('keeps a domain whose values are all 0 wider than a point', () => {
    const [lo, hi] = logDomain([0, 0])!;
    expect(hi).toBeGreaterThan(lo);
  });
  it('picks ticks inside the domain, thinner on a narrow chart', () => {
    const wide = logTicks(100, 2_000_000, 800);
    expect(wide.every((t) => t.value >= 100 && t.value <= 2_000_000)).toBe(true);
    expect(wide.map((t) => t.label)).toContain('1h');
    expect(wide.map((t) => t.label)).toContain('7d');
    const narrow = logTicks(100, 2_000_000, 300);
    expect(narrow.length).toBeLessThan(wide.length);
    expect(narrow.length).toBeLessThanOrEqual(Math.ceil(wide.length / 2));
  });
});

describe('mark paths', () => {
  it('rounds the data end and keeps the baseline square', () => {
    expect(colPath(0, 0, 20, 30)).toBe('M0,30V4Q0,0 4,0H16Q20,0 20,4V30Z');
    expect(barPath(0, 0, 30, 20)).toBe('M0,0H26Q30,0 30,4V16Q30,20 26,20H0Z');
  });
  it('clamps the radius on a short or thin mark', () => {
    expect(colPath(0, 8, 20, 2)).toBe('M0,10V10Q0,8 2,8H18Q20,8 20,10V10Z');
    expect(colPath(0, 0, 4, 30)).toBe('M0,30V2Q0,0 2,0H2Q4,0 4,2V30Z');
  });
});

describe('labels', () => {
  it('always keeps the last tick and spaces the rest by the widest measured label plus 8px', () => {
    const n = 13;
    const kept = (band: number, widest: number) => Array.from({ length: n }, (_, i) => i).filter((i) => showTick(i, n, band, widest));
    expect(kept(60, 40)).toHaveLength(13);
    const tight = kept(30, 40);
    expect(tight).toContain(12);
    for (let k = 1; k < tight.length; k++) expect((tight[k] - tight[k - 1]) * 30 - 40).toBeGreaterThanOrEqual(8);
    // A 22px band with 40px labels: centres one apart would leave the boxes overlapping.
    expect(kept(22, 40).every((i, k, a) => k === 0 || (i - a[k - 1]) * 22 - 40 >= 8)).toBe(true);
  });
  it('labels the peak and the last value only', () => {
    expect(directLabelIndexes([1, 9, 2, 3, 4], 40, 20)).toEqual([1, 4]);
  });
  it('drops the peak next to the last', () => {
    expect(directLabelIndexes([1, 2, 3, 9, 4], 40, 20)).toEqual([4]);
  });
  it('drops the peak when its label would overlap the last one', () => {
    expect(directLabelIndexes([9, 1, 1, 4], 10, 30)).toEqual([3]);
  });
  it('labels one value once when the peak is the last', () => {
    expect(directLabelIndexes([1, 2, 9], 40, 20)).toEqual([2]);
  });
  it('labels nothing in a series with no known value, and skips trailing unknowns', () => {
    expect(directLabelIndexes([null, null], 40, 20)).toEqual([]);
    expect(directLabelIndexes([9, 1, 3, null], 40, 20)).toEqual([0, 2]);
  });
});

describe('durationText', () => {
  it('prints the two largest units', () => {
    expect([45, 720, 12_000, 187_200, 3_600].map(durationText)).toEqual(['45s', '12m', '3h 20m', '2d 4h', '1h']);
  });
  it('tells 0, under a second and unknown apart', () => {
    expect([durationText(0), durationText(0.4), durationText(null), durationText(undefined)]).toEqual(['0s', 'under 1s', 'unknown', 'unknown']);
  });
});

describe('colour order', () => {
  it('names the chart tokens in fixed order', () => {
    expect(SERIES).toEqual(['--chart-1', '--chart-2']);
    expect(HEAT).toEqual(['--chart-hm-1', '--chart-hm-2', '--chart-hm-3', '--chart-hm-4']);
  });
});
