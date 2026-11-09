import { describe, it, expect } from 'vitest';
import { originModel, creationExplainModel, creationRailModel, creationStatus, CREATION_STATUS, ORIGIN_LANES } from './goldenFigures.ts';

const hrefs = { services: (b: string) => `#/s?cf=${b}`, paths: '#/gp' };
const path = (i: number, services: number, nonCompliant = 0, hidden = 0) => ({ key: `acme/p${i}`, label: `p${i}`, services, nonCompliant, hidden });

describe('originModel', () => {
  it('partitions the fleet into at most five lanes that sum to it', () => {
    for (const n of [1, 8, 40, 400]) {
      const byPath = Array.from({ length: Math.min(n, 10) }, (_, i) => path(i, 100 - i, 1));
      const agg = { services: 5000, origins: { byPath, paths: n, beyond: { services: 7, nonCompliant: 2 }, unresolved: { services: 3, nonCompliant: 1 }, none: { services: 0, nonCompliant: 0 } } };
      const created = byPath.reduce((a, p) => a + p.services, 0) + 7;
      agg.origins.none.services = 5000 - created - 3;
      const m = originModel(agg, hrefs);
      expect(m.lanes.length).toBeLessThanOrEqual(ORIGIN_LANES);
      expect(m.lanes.reduce((a, l) => a + l.n, 0)).toBe(5000);
      expect(m.created).toBe(created);
      // Canonical tails: the named paths, Other, then Origin not resolved and None declared.
      expect(m.lanes.slice(-3).map((l) => l.label)).toEqual(['Other paths', 'Origin not resolved', 'None declared']);
      expect(m.lanes.every((l) => l.note?.text.includes('non-compliant'))).toBe(true);
      // An unresolved origin may be missing or ambiguous as well as hidden: the note never blames access alone.
      expect(m.lanes.at(-2)?.note?.text).toBe('1 of 3 non-compliant; their origin digest names no single golden path you can see');
    }
  });

  it('keeps None declared at 0, counts hidden services and never names them', () => {
    const m = originModel({ services: 4, origins: { byPath: [path(0, 4, 1, 2)], paths: 1, hidden: 2 } }, hrefs);
    expect(m.lanes.map((l) => [l.label, l.n, l.href])).toEqual([['p0', 4, '#/s?cf=acme/p0'], ['Other paths', 0, ''], ['None declared', 0, '#/s?cf=(none)']]);
    expect(m.lanes[0].note?.text).toBe('1 of 4 non-compliant; 2 more you may not see');
    expect(m.hidden).toBe(2);
    expect(m.alt).not.toContain('hidden');
  });

  it('draws only None declared for a fleet with no golden path', () => {
    const m = originModel({ services: 3, origins: { none: { services: 3, nonCompliant: 1 } } }, hrefs);
    expect(m.lanes.map((l) => l.label)).toEqual(['None declared']);
    expect(originModel(null, hrefs).total).toBe(0);
  });

  it('wires every lane into the hub', () => {
    const m = originModel({ services: 2, origins: { byPath: [path(0, 2)], paths: 3, none: {} } }, hrefs);
    expect(m.wires.map((w) => [w.from, w.to])).toEqual([['lane-0', 'hub'], ['lane-1', 'hub'], ['lane-2', 'hub']]);
    expect(m.lanes[1].note).toEqual({ text: '0 of 0 non-compliant' });
    expect(m.more).toBe(2);
  });
});

describe('creationExplainModel', () => {
  it('draws the mechanism in neutral and accent only, walking one step per station', () => {
    const m = creationExplainModel(['postgres-database']);
    expect(m.stations.every((s) => s.tone === 'neutral' || s.tone === 'accent')).toBe(true);
    expect(m.steps.length).toBe(m.stations.length);
    expect(m.steps.reduce((a, s) => a + (s.dur ?? 0) + (s.hold ?? 0), 0)).toBeLessThanOrEqual(1200);
    expect(m.stations[4].sub).toBe('It also requests postgres-database');
  });
  it('says when the path requests nothing, counts what is hidden and stays general on the list', () => {
    expect(creationExplainModel([]).stations[4].sub).toBe('This path requests no offering');
    expect(creationExplainModel(['a', 'b'], 1).stations[4].sub).toBe('It also requests a, b and 1 requirement you may not see');
    expect(creationExplainModel([], 2).stations[4].sub).toBe('It also requests 2 requirements you may not see');
    expect(creationExplainModel(null).stations[4].sub).toContain('any offerings');
  });
});

describe('creationRailModel', () => {
  const steps = [
    { step: 'signature', status: 'unknown', detail: 'the golden path is unsigned' },
    { step: 'contract', status: 'done', detail: 'revision 0.1.0' },
    { step: 'validation', status: 'done' },
    { step: 'plugin', name: 'readme', status: 'unknown', detail: 'Pacto does not observe what a plugin wrote' },
    { step: 'plugin', name: 'ci', status: 'unknown', detail: 'Pacto does not observe what a plugin wrote' },
    { step: 'plugin', name: 'docs', status: 'unknown' },
    { step: 'published', status: 'unknown' },
    { step: 'fleet', status: 'done' },
    { step: 'deployed', status: 'unknown' },
    { step: 'provisioning', status: 'not-applicable' },
  ];
  it('folds the plugins into one station and keeps at most 8', () => {
    const m = creationRailModel(steps);
    expect(m.stations.length).toBe(8);
    expect(m.stations[3].label).toBe('Plugins · Unknown');
    expect(m.stations[3].sub).toBe('3 plugins (readme, ci, docs): Pacto does not observe what a plugin wrote');
    expect(m.wires.length).toBe(7);
    expect(m.alt).toBe('8 steps: 3 done, 4 unknown, 0 failed.');
  });
  it('never draws unknown as done or as a status tone', () => {
    for (const s of creationRailModel(steps).stations.filter((x) => x.status === 'unknown')) {
      expect(s.tone).toBe('neutral');
      expect(s.hatch).toBe(true);
    }
    expect(creationStatus('bogus')).toBe(CREATION_STATUS.unknown);
    expect(Object.entries(CREATION_STATUS).filter(([, v]) => v.tone === 'ok').map(([k]) => k)).toEqual(['done']);
  });
  it('folds plugin outcomes: any failed fails, all done is done', () => {
    const fold = (a: string, b: string) => creationRailModel([{ step: 'plugin', name: 'a', status: a }, { step: 'plugin', name: 'b', status: b }]).stations[0].status;
    expect(fold('done', 'failed')).toBe('failed');
    expect(fold('done', 'done')).toBe('done');
    expect(fold('done', 'unknown')).toBe('unknown');
    expect(creationRailModel([{ step: 'mystery', status: 'done' }]).stations[0]).toMatchObject({ label: 'mystery · Done', sub: '' });
  });
});
