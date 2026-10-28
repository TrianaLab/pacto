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

  it('wraps a legend that would run past its width', async () => {
    const { legendLayout } = await import('./charts.ts');
    const l = legendLayout(['Compliant', 'Not compliant', 'Unknown', 'Warning', 'Invalid', 'Not evaluated', 'Reference'], 200);
    expect(l.rows).toBeGreaterThan(1);
    expect(l.at[0]).toEqual({ x: 0, row: 0 });
    expect(legendLayout(['a'], 200)).toEqual({ rows: 1, at: [{ x: 0, row: 0 }] });
  });
});
