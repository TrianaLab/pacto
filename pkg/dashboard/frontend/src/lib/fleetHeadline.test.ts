import { describe, it, expect } from 'vitest';
import { matchOfCoverage, matchTile, completenessTile, matchTrend, completenessTrend, trendHidden } from './fleetHeadline.ts';

describe('the fleet figures (509-S12)', () => {
  const cm = (sm: number, seo: number, soo: number, si: number, dm = 0, deo = 0, doo = 0, di = 0) => ({
    servicesMatched: sm, servicesExpectedOnly: seo, servicesObservedOnly: soo, servicesInsufficient: si,
    dependenciesMatched: dm, dependenciesExpectedOnly: deo, dependenciesObservedOnly: doo, dependenciesInsufficient: di,
  });

  it('reads a coverage record like the server: matched over observable, observable over all, rounded down', () => {
    expect(matchOfCoverage(cm(5, 1, 1, 2, 1, 0, 1, 0))).toMatchObject({ percent: 66, coverage: 81 });
    expect(matchOfCoverage(null)).toBeNull();
  });

  it('gives no percentage over nothing observable, never 100%', () => {
    const m = matchOfCoverage(cm(0, 0, 0, 4))!;
    expect(m.percent).toBeUndefined();
    expect(m.coverage).toBe(0);
    expect(matchTile(m)).toMatchObject({ value: 'none yet', unknown: true });
    expect(matchOfCoverage(cm(0, 0, 0, 0))).toMatchObject({ services: { matched: 0 } });
    expect(matchOfCoverage(cm(0, 0, 0, 0))!.coverage).toBeUndefined();
  });

  it('keeps insufficient evidence out of the percentage, as its own hatched segment', () => {
    const t = matchTile(matchOfCoverage(cm(8, 1, 1, 30))!);
    expect(t.value).toBe('80%');
    expect(t.segments.find((s) => s.label === 'Insufficient evidence')).toMatchObject({ value: 30, hatch: true });
    expect(matchTile(null)).toMatchObject({ value: 'unknown', unknown: true, total: 0 });
  });

  it('says completeness over its judged base and names the sections most often missing', () => {
    const t = completenessTile({ percentComplete: 72, judged: 2950, unknown: 50, fullyDescribed: 1204, mostMissing: [{ section: 'readiness', count: 640 }, { section: 'owner', count: 0 }] }, { readiness: 'readiness' });
    expect(t).toMatchObject({ value: '72%', note: '1,204 of 2,950 judged services fully described, 50 not judged', sub: 'Most missing: readiness 640', total: 3000 });
    expect(completenessTile({ judged: 0, unknown: 3, fullyDescribed: 0 })).toMatchObject({ value: 'unknown', unknown: true });
    expect(completenessTile(null).unknown).toBe(true);
  });

  it('draws a trend point only where a bucket recorded the figure', () => {
    const st = { buckets: [{ coverage: { match: cm(1, 1, 0, 0), contracts: { percentComplete: 40 } } }, {}, { coverage: {} }], limitations: [] } as never;
    expect(matchTrend(st)).toEqual([50, null, null]);
    expect(completenessTrend(st)).toEqual([40, null, null]);
    expect(trendHidden(st)).toBe(false);
    expect(trendHidden({ limitations: [{ code: 'COVERAGE_OUTSIDE_YOUR_VIEW' }] } as never)).toBe(true);
  });
});
