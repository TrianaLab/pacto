import { describe, it, expect } from 'vitest';
import { postureColumn, rankModel, trustModel, sourcesModel, POSTURE_FLOOR, SOURCE_ROWS } from './fleetFigures.ts';
import { complianceSegments } from './distributions.ts';
import { timeline } from './narrate.ts';

describe('postureColumn', () => {
  it('stacks worst on top with a floor, draws nothing for empty strata and wires each to its row', () => {
    const segs = complianceSegments({ nonCompliant: 1, unknown: 300, compliant: 4699 });
    const m = postureColumn(segs, 5000);
    expect(m.strata.map((s) => segs[s.i].label)).toEqual(['Not compliant', 'Unknown', 'Compliant']);
    expect(m.strata[0].h).toBe(POSTURE_FLOOR);
    expect(m.strata[1].hatch).toBe(true);
    // Unknown is never ok-toned: its wire is the neutral line.
    expect(m.wires.map((w) => w.tone)).toEqual(['err', 'neutral', 'ok']);
    expect(m.strata[1].y).toBeGreaterThan(m.strata[0].y + m.strata[0].h);
  });
  it('keeps a minimal column for an empty population', () => {
    expect(postureColumn(complianceSegments({}), 0)).toMatchObject({ strata: [], wires: [], H: 10 });
  });
});

const hrefs = { services: (g: string) => `#/s?g=${g}`, groups: '#/groups?sort=nonCompliant' };

describe('rankModel', () => {
  const agg = {
    services: 5000,
    serviceCompliance: { nonCompliant: 389 },
    nonCompliantByOwner: [
      { key: 'team:a', label: 'a', services: 297, nonCompliant: 20 },
      { key: 'team:b', label: 'b', services: 12, nonCompliant: 9 },
      { key: 'team:c', label: 'c', services: 3, nonCompliant: 3 },
      { key: 'team:d', label: 'd', services: 50, nonCompliant: 2 },
    ],
    nonCompliantBeyondRanking: 300, nonCompliantUnidentified: 55, nonCompliantOwners: 120,
  };
  it('partitions the non-compliant services into at most 5 lanes', () => {
    const m = rankModel('owner', agg, hrefs);
    expect(m.lanes.map((l) => l.label)).toEqual(['a', 'b', 'c', 'Other owners', 'No identified owner']);
    expect(m.lanes.reduce((a, l) => a + l.n, 0)).toBe(389);
    expect(m.lanes[3]).toMatchObject({ n: 302, href: '', note: { text: '117 owners with at least one', href: hrefs.groups } });
    expect(m.lanes[4]).toMatchObject({ hatch: true, href: '#/s?g=' });
    expect(m.lanes[0].note?.text).toBe('7% of their 297 services');
    // The highest rate among groups large enough to say anything.
    expect(m.rate).toEqual({ label: 'b', text: '75% of 12 services' });
    expect(m.wires).toHaveLength(5);
  });
  it('ranks domains with their own tails', () => {
    const m = rankModel('domain', {
      services: 10, serviceCompliance: { nonCompliant: 6 },
      nonCompliantByDomain: [{ domain: 'pay', services: 4, nonCompliant: 4 }], nonCompliantDomains: 1,
      nonCompliantNoDomain: 1, nonCompliantDomainUnknown: 1,
    }, hrefs);
    expect(m.lanes.map((l) => [l.label, l.n])).toEqual([['pay', 4], ['No domain', 1], ['Domain unknown', 1]]);
    expect(m.lanes[1].href).toBe('#/s?g=(none)');
    expect(m.rate).toBeNull();
  });
  it('draws nothing to rank when nothing is non-compliant', () => {
    const m = rankModel('domain', null, hrefs);
    expect(m.total).toBe(0);
    expect(m.lanes).toEqual([]);
  });
});

describe('trustModel', () => {
  it('always shows unavailable, stale and current, partial only when present', () => {
    expect(trustModel({ total: 3, available: 3 }).rows.map((r) => r.st)).toEqual(['unavailable', 'stale', 'available']);
    expect(trustModel({ total: 4, available: 3, partial: 1 }).rows.map((r) => r.st)).toEqual(['unavailable', 'stale', 'partial', 'available']);
  });
  it('narrates only unavailable and stale, never a signal on a broken wire, nothing on partial alone', () => {
    const calm = trustModel({ total: 4, available: 3, partial: 1 });
    expect(calm.steps).toEqual([]);
    const m = trustModel({ total: 50, available: 44, partial: 2, stale: 2, unavailable: 2 });
    expect(m.steps.map((s) => s.cap)).toEqual(['No report from 2 sources: unavailable.', '2 sources last reported too long ago: stale.']);
    const broken = m.wires.filter((w) => w.style === 'broken').map((w) => w.id);
    expect(m.steps.flatMap((s) => s.sig ?? []).some((id) => broken.includes(id))).toBe(false);
    expect(() => timeline(m.steps)).not.toThrow();
    // The key holds only what the steps animate.
    expect(trustModel({ total: 51, available: 45, partial: 2, stale: 2, unavailable: 2 }).key).toEqual(m.key);
  });
});

describe('sourcesModel', () => {
  const src = (i: number, status: string) => ({ key: `s${i}`, label: `source-${i}`, status });
  it('names every source of a small fleet', () => {
    const m = sourcesModel([src(1, 'unavailable'), src(2, 'available')], { total: 2, available: 1, unavailable: 1 });
    expect(m.rows.map((r) => r.named?.label)).toEqual(['source-1', 'source-2']);
    expect(m.wires.map((w) => w.style)).toEqual(['broken', 'obs', 'obs']);
  });
  it('500 sources with 40 non-current: at most 8 rows, every wire matches every source behind it', () => {
    const worst = [...Array.from({ length: 7 }, (_, i) => src(i, i < 4 ? 'unavailable' : 'stale')), src(7, 'available')];
    const m = sourcesModel(worst, { total: 500, available: 460, stale: 15, partial: 5, unavailable: 20 });
    expect(m.rows.length).toBeLessThanOrEqual(SOURCE_ROWS);
    const named = m.rows.filter((r) => r.named);
    expect(named.length).toBeGreaterThan(0);
    expect(named.every((r) => r.st !== 'available')).toBe(true);
    const tallies = m.rows.filter((r) => !r.named);
    expect(tallies.map((r) => r.st)).toEqual(['unavailable', 'stale', 'partial', 'available']);
    expect(m.rows.reduce((a, r) => a + r.n, 0)).toBe(500);
    for (const r of m.rows) expect(m.wires[r.k].style).toBe(r.st === 'unavailable' ? 'broken' : r.st === 'available' ? 'obs' : 'stale');
    expect(m.tallyLabel(tallies[3])).toBe('460 more current');
    expect(m.key.length).toBe(named.length);
  });
  it('folds names until the tallies fit', () => {
    const worst = Array.from({ length: 8 }, (_, i) => src(i, ['unavailable', 'stale', 'partial'][i % 3]));
    const m = sourcesModel(worst, { total: 900, available: 800, stale: 40, partial: 30, unavailable: 30 });
    expect(m.rows.length).toBeLessThanOrEqual(SOURCE_ROWS);
    expect(m.tallyLabel(m.rows.find((r) => !r.named)!)).toMatch(/ more /);
  });
  it('labels tallies without "more" when none is named', () => {
    const m = sourcesModel([], { total: 20, available: 20 });
    expect(m.rows).toHaveLength(1);
    expect(m.tallyLabel(m.rows[0])).toBe('20 current');
    expect(sourcesModel([], null).rows).toEqual([]);
  });
});
