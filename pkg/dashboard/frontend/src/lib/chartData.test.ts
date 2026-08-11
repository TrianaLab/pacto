import { describe, it, expect } from 'vitest';
import { treemapData, quadrantData, heatmapData, versionTimelineData } from './chartData';

const svcs = [
  { name: 'a', contractStatus: 'NonCompliant', blastRadius: 9, owner: { team: 'team/x' },
    readiness: { score: 20, checks: [{ category: 'docs', status: 'done' }, { category: 'docs', status: 'not_done' }, { category: 'test', status: 'done' }] } },
  { name: 'b', contractStatus: 'Compliant', blastRadius: 0, owner: { team: 'team/y' },
    readiness: { score: 90, checks: [{ category: 'docs', status: 'done' }] } },
  { name: 'c', contractStatus: 'Compliant', blastRadius: 3, owner: null, readiness: null },
];

describe('treemapData', () => {
  it('sizes by blast (min 1) and carries status', () => {
    const d = treemapData(svcs);
    expect(d.find(t => t.name === 'a')).toMatchObject({ value: 9, status: 'NonCompliant', blast: 9 });
    expect(d.find(t => t.name === 'b')!.value).toBe(1); // 0 blast → min 1
  });

  it('handles empty input', () => {
    expect(treemapData([])).toEqual([]);
  });
});

describe('quadrantData', () => {
  it('maps score→x, blast→y and drops unconfigured', () => {
    const d = quadrantData(svcs);
    expect(d.map(p => p.name).sort()).toEqual(['a', 'b']); // c has no score
    expect(d.find(p => p.name === 'a')).toMatchObject({ x: 20, y: 9 });
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
