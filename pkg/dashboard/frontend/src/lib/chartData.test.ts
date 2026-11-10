import { describe, it, expect } from 'vitest';
import { heatmapData, versionTimelineData } from './chartData';

const svcs = [
  { name: 'a', contractStatus: 'NonCompliant', owner: { team: 'team/x' },
    readiness: { score: 20, checks: [{ category: 'docs', status: 'done' }, { category: 'docs', status: 'not_done' }, { category: 'test', status: 'done' }] } },
  { name: 'b', contractStatus: 'Compliant', owner: { team: 'team/y' },
    readiness: { score: 90, checks: [{ category: 'docs', status: 'done' }] } },
  { name: 'c', contractStatus: 'Compliant', owner: null, readiness: null },
];

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

describe('charts that scale', () => {
  it('bins a long timeline by week, worst change first, named by its newest version', async () => {
    const { binTimeline } = await import('./chartData.ts');
    const week = 7 * 86400000;
    const items = Array.from({ length: 130 }, (_, i) => ({
      version: `1.${i}.0`, at: Math.floor(i / 2) * week + 1000, classification: i === 5 ? 'BREAKING' : 'NON_BREAKING', isCurrent: i === 129,
    }));
    const bins = binTimeline(items);
    expect(bins.length).toBe(65);
    expect(bins[2]).toMatchObject({ count: 2, version: '1.5.0', classification: 'BREAKING' });
    expect(bins[64].isCurrent).toBe(true);
    expect(binTimeline(items.slice(0, 10))).toHaveLength(10);
  });

  it('keeps the least ready teams for the heatmap', async () => {
    const { worstOwners } = await import('./chartData.ts');
    const owners = Array.from({ length: 30 }, (_, i) => `t${i}`);
    const data = { owners, categories: ['ops'], cells: owners.map((o, i) => ({ owner: o, category: 'ops', score: i * 3, n: 1 })) };
    const w = worstOwners(data, 20);
    expect(w.owners).toEqual(owners.slice(0, 20));
    expect(w.cells).toHaveLength(20);
    expect(worstOwners({ ...data, owners: owners.slice(0, 5) }, 20).owners).toHaveLength(5);
  });
});
