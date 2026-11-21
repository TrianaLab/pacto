import { describe, it, expect } from 'vitest';
import { EDGE_DASH, MIN_FONT, placeNodes, render, type Palette, type Scene } from './render';
import { mapAnswer, nodesPage, recordingContext, type Call } from './testkit';
import type { MapNode } from './lens';

const P: Palette = {
  bg: 'bg', raised: 'raised', border: 'border', borderStrong: 'strong', text: 'text', text2: 'text2', text3: 'text3',
  ok: 'ok', warn: 'warn', err: 'err', info: 'info', neutral: 'neutral', accent: 'accent', errBg: 'errBg',
  sans: 'sans', display: 'display', dark: true,
};

function scene(k: number, over: Partial<Scene> = {}): Scene {
  const a = mapAnswer();
  return { groups: a.groups!, ribbons: a.ribbons!, nodes: [], view: { k, tx: 400, ty: 250 }, w: 800, h: 500, dpr: 1, palette: P, lens: 'both', groupBy: 'domain', ...over };
}

function withNodes(k: number, nodes: Partial<MapNode>[]): Scene {
  const s = scene(k);
  const g = s.groups[0];
  const page = { group: g.key, total: nodes.length, complete: true, edges: [], nodes: nodesPage(g.key, nodes).nodes! };
  return { ...s, nodes: placeNodes(g, page, 'both') };
}

/** strokes made right after a path that holds a quadratic curve: the ribbons. */
function ribbonStrokes(calls: Call[]): Call[] {
  const out: Call[] = [];
  let curve = false;
  for (const c of calls) {
    if (c.name === 'beginPath') curve = false;
    if (c.name === 'quadraticCurveTo') curve = true;
    if (c.name === 'stroke' && curve) out.push(c);
  }
  return out;
}

describe('fleet map render', () => {
  it('L0 draws one bubble per group with ring and pills', () => {
    const ctx = recordingContext();
    const res = render(ctx, scene(0.8));
    const arcs = ctx.calls.filter((c) => c.name === 'arc');
    // payments R = 40 * 0.8, checkout R = 30 * 0.8, each one bubble and one ring around it.
    for (const R of [32, 24]) {
      expect(arcs.filter((c) => c.args[2] === R)).toHaveLength(1);
      expect(arcs.filter((c) => c.args[2] === R + 3.5).length).toBeGreaterThanOrEqual(1);
    }
    // The matched share is a solid arc over part of the ring: 75% for payments.
    const share = arcs.find((c) => c.args[2] === 35.5 && c.args[3] === -Math.PI / 2);
    expect(share?.args[4]).toBeCloseTo(-Math.PI / 2 + Math.PI * 1.5);
    // Pills carry a glyph and a count, so severity never reads by colour alone.
    const texts = ctx.calls.filter((c) => c.name === 'fillText').map((c) => c.args[0]);
    expect(texts).toEqual(expect.arrayContaining(['✕ 2', '▲ 3', 'Payments', 'Checkout', '3 services', '50% match']));
    expect(ctx.calls.filter((c) => c.name === 'roundRect')).toHaveLength(2);
    expect(res.nodes).toBe(0);
    expect(res.labels.every((l) => l.size >= MIN_FONT)).toBe(true);
  });

  it('L1 colours only problem dots', () => {
    const healthy = recordingContext();
    render(healthy, withNodes(1.5, [{}, {}, { severity: 'info' }]));
    const dotFills = (calls: Call[]) => calls.filter((c) => c.name === 'fill').map((c) => c.fillStyle);
    expect(dotFills(healthy.calls)).toContain('text3');
    expect(dotFills(healthy.calls)).not.toContain('err');
    expect(dotFills(healthy.calls)).not.toContain('warn');

    const sick = recordingContext();
    const res = render(sick, withNodes(1.5, [{}, { severity: 'error' }, { severity: 'warning' }, { inferred: true }]));
    expect(res.nodes).toBe(4);
    expect(dotFills(sick.calls)).toEqual(expect.arrayContaining(['text3', 'err', 'warn', 'errBg']));
    // An error also gets a double ring (not colour alone); an inferred service is a dashed hollow ring.
    expect(sick.calls.filter((c) => c.name === 'stroke' && c.strokeStyle === 'err').length).toBeGreaterThanOrEqual(2);
    expect(sick.calls.some((c) => c.name === 'stroke' && c.strokeStyle === 'neutral' && c.dash.length === 2)).toBe(true);
  });

  it('ribbons are thin and low contrast at rest', () => {
    const ctx = recordingContext();
    const res = render(ctx, scene(0.8));
    expect(res.ribbons).toBe(1);
    const strokes = ribbonStrokes(ctx.calls);
    // matched, expected-only and observed-only strands, side by side.
    expect(strokes).toHaveLength(3);
    for (const s of strokes) {
      expect(s.lineWidth).toBeLessThanOrEqual(5);
      expect(s.globalAlpha).toBeLessThanOrEqual(0.5);
    }
    const matched = strokes.find((s) => s.strokeStyle === 'text3')!;
    expect(matched.globalAlpha).toBeLessThanOrEqual(0.3);
    expect(matched.dash).toEqual([]);
    expect(strokes.find((s) => s.strokeStyle === 'info')?.dash).toEqual(EDGE_DASH.expectedOnly);
    expect(strokes.find((s) => s.strokeStyle === 'warn')?.dash).toEqual(EDGE_DASH.observedOnly);
    // One lens draws one strand of the lens's count.
    const one = recordingContext();
    render(one, scene(0.8, { lens: 'expected' }));
    expect(ribbonStrokes(one.calls)).toHaveLength(1);
  });

  it('node coordinates are the group centre plus the node offset', () => {
    const a = mapAnswer();
    const g = a.groups![1]; // checkout at (200, 0)
    const page = { group: g.key, total: 2, complete: true, edges: [], nodes: nodesPage(g.key, [{ x: -3.5, y: 12.1 }, { x: 0, y: 0 }]).nodes! };
    expect(placeNodes(g, page, 'both').map((p) => [p.x, p.y])).toEqual([[196.5, 12.1], [200, 0]]);
  });

  it('draws no label under the HUD or over the canvas edge', () => {
    const ctx = recordingContext();
    const res = render(ctx, scene(0.8, { reserved: [[0, 0, 800, 500]] }));
    expect(res.labels).toEqual([]);
  });
});
