import { describe, it, expect } from 'vitest';
import { quadrantData, heatmapData, versionTimelineData } from './chartData';

const svcs = [
  { name: 'a', contractStatus: 'NonCompliant', blastRadius: 9, owner: { team: 'team/x' },
    readiness: { score: 20, checks: [{ category: 'docs', status: 'done' }, { category: 'docs', status: 'not_done' }, { category: 'test', status: 'done' }] } },
  { name: 'b', contractStatus: 'Compliant', blastRadius: 0, owner: { team: 'team/y' },
    readiness: { score: 90, checks: [{ category: 'docs', status: 'done' }] } },
  { name: 'c', contractStatus: 'Compliant', blastRadius: 3, owner: null, readiness: null },
];

describe('quadrantData', () => {
  it('maps score→x, blast→y and drops unconfigured', () => {
    const d = quadrantData(svcs);
    expect(d.map(p => p.name).sort()).toEqual(['a', 'b']); // c has no score
    expect(d.find(p => p.name === 'a')).toMatchObject({ x: 20, y: 9 });
  });

  // The plot's claim is "left of the line needs work". Against a fixed midpoint that
  // claim was false in both directions, and these two services are the proof: one
  // passes its gate on a low score, the other fails its gate on a high one.
  it('measures each service against its OWN threshold, not a shared midpoint', () => {
    const d = quadrantData([
      { name: 'lenient', blastRadius: 1, readiness: { score: 45, minScore: 40 } },
      { name: 'strict', blastRadius: 1, readiness: { score: 60, minScore: 80 } },
    ]);
    expect(d.find(p => p.name === 'lenient')?.x).toBe(5);
    expect(d.find(p => p.name === 'strict')?.x).toBe(-20);
  });

  // Nobody set a bar, so every score clears it.
  it('treats a missing threshold as zero rather than dropping the service', () => {
    expect(quadrantData([{ name: 'a', blastRadius: 0, readiness: { score: 30 } }])[0].x).toBe(30);
  });

  it('handles empty input', () => {
    expect(quadrantData([])).toEqual([]);
  });
});

describe('heatmapData', () => {
  it('aggregates score per owner × category', () => {
    const d = heatmapData(svcs);
    expect(d.owners).toContain('team:team/x');
    expect(d.categories).toEqual(['docs', 'test']); // sorted, unique
    const docsX = d.cells.find(c => c.owner === 'team:team/x' && c.category === 'docs');
    expect(docsX).toMatchObject({ score: 50, n: 2 }); // 1 done of 2
  });

  it('handles empty input', () => {
    const d = heatmapData([]);
    expect(d).toEqual({ owners: [], categories: [], cells: [] });
  });
});

describe('versionTimelineData', () => {
  it('sorts by date asc, drops undated, keeps classification/current', () => {
    const d = versionTimelineData([
      { version: '2.0.0', createdAt: '2026-02-01T00:00:00Z', classification: 'BREAKING', isCurrent: true },
      { version: '1.0.0', createdAt: '2026-01-01T00:00:00Z', classification: 'NON_BREAKING' },
      { version: '0.9.0' }, // undated → dropped
    ]);
    expect(d.map(v => v.version)).toEqual(['1.0.0', '2.0.0']);
    expect(d[1]).toMatchObject({ classification: 'BREAKING', isCurrent: true });
  });

  it('handles empty input', () => {
    expect(versionTimelineData([])).toEqual([]);
  });
});
