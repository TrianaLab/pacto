import { describe, it, expect } from 'vitest';
import { postureColumn, rankModel, trustModel, sourcesModel, sourceKindNoun, POSTURE_FLOOR, SOURCE_ROWS } from './fleetFigures.ts';
import { knowsModel, edgeModel } from './explainers.ts';
import { provenance } from './provenance.ts';
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
    // Other's note lists every owner with a non-compliant service, and counts that list.
    expect(m.lanes[3]).toMatchObject({ n: 302, href: '', note: { text: '120 owners with at least one', href: hrefs.groups } });
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
    expect(m.lanes.map((l) => [l.label, l.n])).toEqual([['pay', 4], ['Other domains', 0], ['No domain', 1], ['Domain unknown', 1]]);
    expect(m.lanes[2].href).toBe('#/s?g=(none)');
    expect(m.lanes[1].note).toEqual({ text: '1 domain with at least one', href: hrefs.groups });
    expect(m.rate).toBeNull();
  });
  it('keeps empty buckets in place at 0 (SPEC 2.2)', () => {
    const m = rankModel('owner', {
      services: 10, serviceCompliance: { nonCompliant: 3 },
      nonCompliantByOwner: [{ key: 'team:a', label: 'a', services: 5, nonCompliant: 3 }], nonCompliantOwners: 1,
    }, hrefs);
    expect(m.lanes.map((l) => [l.label, l.n])).toEqual([['a', 3], ['Other owners', 0], ['No identified owner', 0]]);
    expect(m.lanes[2]).toMatchObject({ hatch: true, note: null });
    // Every disputed: no owner names one, and Other has nothing to list.
    const none = rankModel('owner', { services: 4, serviceCompliance: { nonCompliant: 1 }, nonCompliantUnidentified: 1 }, hrefs);
    expect(none.lanes.map((l) => [l.label, l.n])).toEqual([['No identified owner', 1]]);
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

describe('sourcesModel kind tallies (S2 r5)', () => {
  const src = (i: number, status: string) => ({ key: `s${i}`, label: `source-${i}`, status });
  const worst = Array.from({ length: 5 }, (_, i) => src(i, 'unavailable'));
  it('counts current sources per kind, most first, and keeps 8 rows by folding the smallest kinds', () => {
    const byKind = [{ kind: 'kubernetes', count: 41 }, { kind: 'oci', count: 5 }, { kind: 'evidence-http', count: 2 }, { kind: 'local', count: 1 }, { kind: 'cache', count: 1 }];
    const m = sourcesModel(worst.slice(0, 2), { total: 90, available: 50, unavailable: 30, stale: 10, availableByKind: byKind });
    expect(m.rows.length).toBeLessThanOrEqual(SOURCE_ROWS);
    expect(m.rows.reduce((a, r) => a + r.n, 0)).toBe(90);
    const kinds = m.rows.filter((r) => r.kind != null);
    expect(kinds.every((r) => r.st === 'available')).toBe(true);
    expect(kinds.map((r) => m.tallyLabel(r))[0]).toBe('41 more Kubernetes clusters');
    expect(kinds.at(-1)?.kind).toBe('');
    expect(m.tallyLabel(kinds.at(-1)!)).toMatch(/more sources of other kinds$/);
  });
  it('names fewer sources rather than lump every current one into a row that names no kind', () => {
    const m = sourcesModel(worst, { total: 90, available: 50, unavailable: 30, stale: 10, availableByKind: [{ kind: 'kubernetes', count: 41 }, { kind: 'oci', count: 9 }] });
    expect(m.rows.length).toBeLessThanOrEqual(SOURCE_ROWS);
    expect(m.rows.filter((r) => r.named).length).toBe(4);
    expect(m.rows.filter((r) => r.kind).map((r) => m.tallyLabel(r))).toEqual(['41 more Kubernetes clusters', '9 more OCI registries']);
  });
  it('falls back to one current tally when the split is missing, does not add up or has no room', () => {
    expect(sourcesModel(worst, { total: 30, available: 20, unavailable: 10 }).rows.filter((r) => r.st === 'available').map((r) => r.kind)).toEqual([undefined]);
    expect(sourcesModel(worst, { total: 30, available: 20, unavailable: 10, availableByKind: [{ kind: 'oci', count: 3 }] }).rows.filter((r) => r.st === 'available')).toHaveLength(1);
    const tight = sourcesModel([...worst, src(5, 'stale'), src(6, 'partial')], { total: 99, available: 9, unavailable: 30, stale: 30, partial: 30, availableByKind: [{ kind: 'oci', count: 5 }, { kind: 'local', count: 4 }] });
    expect(tight.rows.length).toBeLessThanOrEqual(SOURCE_ROWS);
    expect(tight.rows.reduce((a, r) => a + r.n, 0)).toBe(99);
  });
  it('names a kind in words, one or many', () => {
    expect(sourceKindNoun('oci', 1)).toBe('OCI registry');
    expect(sourceKindNoun('target-state', 2)).toBe('target-state files');
    expect(sourceKindNoun('acme', 2)).toBe('acme sources');
  });
});

describe('provenance', () => {
  it('says how complete the snapshot is, never complete when it is not', () => {
    expect(provenance({ asOf: 't', sourceCounts: { total: 50, available: 47, unavailable: 2, stale: 1 } })).toEqual({ asOf: 't', sources: 50, partial: true, text: 'Partial: 2 of 50 sources unavailable, 1 stale' });
    expect(provenance({ sourceCounts: { total: 1, available: 1 } }).text).toBe('Complete: its one source is current');
    expect(provenance({ sourceCounts: { total: 4, available: 4 } }).text).toBe('Complete: all 4 sources are current');
    expect(provenance({ completeness: 'partial', sourceCounts: { total: 4, available: 4 } })).toMatchObject({ partial: true, text: 'Partial: some knowledge is missing' });
    expect(provenance(null)).toMatchObject({ sources: 0, asOf: '' });
  });
});

describe('explainers', () => {
  it('walks how Pacto knows in neutral and accent, one step per station, within the Ration', () => {
    const m = knowsModel();
    expect(m.stations.map((s) => s.tone)).toEqual(['neutral', 'accent', 'neutral', 'accent']);
    expect(m.wires.every((w) => w.tone === 'accent' && w.head)).toBe(true);
    expect(m.steps).toHaveLength(m.stations.length);
    expect(timeline(m.steps, { walk: true }).total).toBeLessThanOrEqual(1200);
  });
  it('draws one edge per wire style into one service, never in a status tone', () => {
    const m = edgeModel();
    expect(m.wires.map((w) => w.style)).toEqual(['obs', 'proj', 'stale', 'broken']);
    expect(m.wires.every((w) => w.tone === 'neutral' && w.to === 'gh')).toBe(true);
  });
});
