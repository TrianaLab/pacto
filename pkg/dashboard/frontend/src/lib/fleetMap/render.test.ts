import { describe, it, expect } from 'vitest';
import { EDGE_DASH, LEGEND, MIN_FONT, NODE_DASH, SEVERITY_GLYPH, placeEdges, placeNodes, render, type DrawnLabel, type Palette, type Scene } from './render';
import { mapAnswer, nodesPage, recordingContext, type Call } from './testkit';
import type { MapEdge, MapGroup, MapNode } from './lens';
import { L2, L3, kForGap } from './view';

const P: Palette = {
  bg: 'bg', raised: 'raised', border: 'border', borderStrong: 'strong', text: 'text', text2: 'text2', text3: 'text3',
  ok: 'ok', warn: 'warn', err: 'err', info: 'info', neutral: 'neutral', accent: 'accent', errBg: 'errBg',
  sans: 'sans', display: 'display', dark: true,
};

function scene(k: number, over: Partial<Scene> = {}): Scene {
  const a = mapAnswer();
  return { groups: a.groups!, ribbons: a.ribbons!, nodes: [], view: { k, tx: 400, ty: 250 }, w: 800, h: 500, dpr: 1, palette: P, lens: 'both', groupBy: 'domain', ...over };
}

function withNodes(k: number, nodes: Partial<MapNode>[], over: Partial<Scene> = {}, edges: MapEdge[] = []): Scene {
  const s = scene(k, over);
  const g = s.groups[0];
  const page = { group: g.key, total: nodes.length, complete: true, edges, nodes: nodesPage(g.key, nodes).nodes! };
  const placed = placeNodes(g, page, s.lens);
  return { ...s, nodes: placed, edges: placeEdges(page, placed) };
}

/** Zoom factors for the Names and Details levels, past their crossfades. */
const K2 = kForGap(L2 * 1.3), K3 = kForGap(L3 * 1.2);
const texts = (calls: Call[]) => calls.filter((c) => c.name === 'fillText').map((c) => String(c.args[0]));
/** The fills of dots: arcs or rects filled right after. */
const fillsOf = (calls: Call[]) => calls.filter((c) => c.name === 'fill');
const overlap = (a: DrawnLabel, b: DrawnLabel) => a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
/** strokes made right after a path of straight segments: edges inside a group. */
function lineStrokes(calls: Call[]): Call[] {
  const out: Call[] = [];
  let line = false;
  for (const c of calls) {
    if (c.name === 'beginPath') line = false;
    if (c.name === 'lineTo') line = true;
    if (c.name === 'stroke' && line) out.push(c);
  }
  return out;
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

  it('labels never overlap and are omitted when they do not fit', () => {
    // Forty services packed 4 world units apart: there is no room to name every one.
    const nodes = Array.from({ length: 40 }, (_, i) => ({ x: (i % 8) * 4, y: Math.floor(i / 8) * 4, version: '1.2.3', severity: i % 7 ? undefined : 'warning' as const }));
    const ctx = recordingContext();
    const res = render(ctx, withNodes(K2, nodes));
    const names = res.labels.filter((l) => /^payments-\d+$/.test(l.text));
    expect(names.length).toBeGreaterThan(0);
    expect(names.length).toBeLessThan(40);
    for (const [i, a] of res.labels.entries()) {
      expect(a.size).toBeGreaterThanOrEqual(MIN_FONT);
      expect(a.x).toBeGreaterThanOrEqual(0);
      expect(a.x + a.w).toBeLessThanOrEqual(800);
      for (const b of res.labels.slice(i + 1)) expect(overlap(a, b), `${a.text} over ${b.text}`).toBe(false);
    }
    // A name carries its version, and a warning its glyph beside it.
    expect(res.labels.map((l) => l.text)).toEqual(expect.arrayContaining(['1.2.3', SEVERITY_GLYPH.warning]));
  });

  it('details cards show the worst attention item', () => {
    const ctx = recordingContext();
    render(ctx, withNodes(K3, [
      { severity: 'error', worst: 'non-compliant', version: '4.3.5', owner: 'search-experience', interfaces: 3, match: 'matched' },
    ]));
    expect(texts(ctx.calls)).toEqual(expect.arrayContaining(['payments-0', 'v4.3.5 · search-experience', '3 interfaces · observed', '✕ Not compliant']));
    // The card's frame is the error colour and thicker: not colour alone, the line carries the glyph.
    expect(ctx.calls.some((c) => c.name === 'stroke' && c.strokeStyle === 'err' && c.lineWidth === 2)).toBe(true);
    const quiet = recordingContext();
    render(quiet, withNodes(K3, [{ inferred: true, match: 'observed-only' }]));
    expect(texts(quiet.calls)).toEqual(expect.arrayContaining(['no owner', 'inferred, no contract']));
    expect(texts(quiet.calls).some((t) => t.startsWith('✕') || t.startsWith('▲'))).toBe(false);
  });

  it('drift line styles per state', () => {
    const nodes = Array.from({ length: 12 }, (_, i) => ({ x: (i % 6) * 30, y: Math.floor(i / 6) * 30 }));
    const edges: MapEdge[] = [
      { from: 0, to: 1, difference: 'matched' },
      { from: 1, to: 2, difference: 'expected-not-observed' },
      { from: 2, to: 3, difference: 'observed-not-expected' },
      { from: 3, to: 4, difference: 'insufficient' },
      { from: 4, to: 5, difference: 'observed-not-expected', inferred: true },
      { from: 6, to: 7, difference: 'matched', problem: { code: 'BREAKING', severity: 'error', reason: 'removes PUT /cache' } },
    ];
    const ctx = recordingContext();
    const res = render(ctx, withNodes(K2, nodes, {}, edges));
    expect(res.edges).toBe(6);
    const st = lineStrokes(ctx.calls);
    const by = (color: string) => st.filter((c) => c.strokeStyle === color);
    expect(by('text3')[0].dash).toEqual(EDGE_DASH.matched);
    expect(by('info')[0].dash).toEqual(EDGE_DASH.expectedOnly);
    expect(by('warn')[0].dash).toEqual(EDGE_DASH.observedOnly);
    // Insufficient and inferred are both neutral and quiet, told apart by the dash.
    expect(by('neutral').map((c) => c.dash)).toEqual(expect.arrayContaining([EDGE_DASH.insufficient, EDGE_DASH.observedOnly]));
    const problem = by('err')[0];
    expect(problem.dash).toEqual([]);
    expect(problem.lineWidth).toBeGreaterThan(by('text3')[0].lineWidth);
    expect(texts(ctx.calls)).toContain('!');
    // Below the Names level, edges inside a group wait for a hover.
    const l1 = recordingContext();
    expect(render(l1, withNodes(1.5, nodes, {}, edges)).edges).toBe(0);
    const hover = recordingContext();
    expect(render(hover, withNodes(1.5, nodes, { hoverNode: 'payments-1' }, edges)).edges).toBe(2);
    // One lens keeps its own edges: Expected drops what only Pacto observed.
    const exp = recordingContext();
    expect(render(exp, withNodes(K2, nodes, { lens: 'expected' }, edges)).edges).toBe(4);
    // Declared with nothing running is a hollow ring with its own dash.
    const hollow = recordingContext();
    render(hollow, withNodes(K2, [{ match: 'expected-only' }]));
    expect(hollow.calls.some((c) => c.name === 'stroke' && c.strokeStyle === 'info' && c.dash.join() === NODE_DASH.expectedOnly.join())).toBe(true);
  });

  it('only problems keeps healthy as grey context', () => {
    const ctx = recordingContext();
    render(ctx, withNodes(1.5, [{}, { severity: 'error' }, { severity: 'warning' }], { mode: { kind: 'problems' } }));
    const fills = fillsOf(ctx.calls);
    // The healthy service is still drawn, grey and faint; the problems at full strength.
    expect(fills.some((c) => c.fillStyle === 'text3' && c.globalAlpha <= 0.3)).toBe(true);
    expect(fills.some((c) => c.fillStyle === 'err' && c.globalAlpha === 1)).toBe(true);
    expect(fills.some((c) => c.fillStyle === 'warn' && c.globalAlpha === 1)).toBe(true);
    // At the Groups level a group with nothing wrong fades and its third line counts problems.
    const l0 = recordingContext();
    const big = mapAnswer().groups!.map((g) => ({ ...g, x: g.x * 2, r: g.r * 2 }));
    const res = render(l0, scene(0.8, { groups: big, mode: { kind: 'problems' } }));
    expect(texts(l0.calls)).toEqual(expect.arrayContaining(['5 problems', '0 problems']));
    expect(res.labels.length).toBeGreaterThan(0);
    const checkout = l0.calls.find((c) => c.name === 'fillText' && c.args[0] === 'Checkout');
    expect(checkout?.globalAlpha).toBeLessThan(1);
  });

  it('gaps chips toggle each kind', () => {
    const strandsFor = (gaps: string[]) => {
      const ctx = recordingContext();
      render(ctx, scene(0.8, { mode: { kind: 'gaps', gaps: new Set(gaps as never) } }));
      return ribbonStrokes(ctx.calls).map((c) => [c.strokeStyle, c.globalAlpha > 0.5]);
    };
    expect(strandsFor(['expected-only'])).toEqual([['text3', false], ['info', true], ['warn', false]]);
    expect(strandsFor(['expected-only', 'observed-only'])).toEqual([['text3', false], ['info', true], ['warn', true]]);
  });

  it('missing filter dims complete services', () => {
    const ctx = recordingContext();
    render(ctx, withNodes(1.5, [{ percentComplete: 100, missing: [] }, { percentComplete: 50, missing: ['readiness'] }], { mode: { kind: 'missing', section: 'readiness' } }));
    const fills = fillsOf(ctx.calls);
    expect(fills.filter((c) => c.fillStyle === 'text3').map((c) => c.globalAlpha).sort()).toEqual([0.3, 1]);
  });

  it('environment picker dims services not in it', () => {
    const nodes = [{ inEnvironment: true }, { inEnvironment: false }];
    const picked = recordingContext();
    render(picked, withNodes(1.5, nodes, { environment: 'prod' }));
    expect(fillsOf(picked.calls).filter((c) => c.fillStyle === 'text3').map((c) => c.globalAlpha).sort()).toEqual([0.3, 1]);
    const none = recordingContext();
    render(none, withNodes(1.5, nodes));
    expect(fillsOf(none.calls).filter((c) => c.fillStyle === 'text3').map((c) => c.globalAlpha)).toEqual([1]);
  });

  it('cross-environment ribbon marked', () => {
    const a = mapAnswer();
    const ctx = recordingContext();
    render(ctx, scene(0.8, { ribbons: [{ ...a.ribbons![0], crossEnvironment: 1 }] }));
    const st = ribbonStrokes(ctx.calls);
    expect(st).toHaveLength(1);
    expect(st[0].strokeStyle).toBe('err');
    expect(st[0].dash).toEqual(EDGE_DASH.crossEnvironment);
  });

  it("group by team nests past 40 (from the answer's nested flag)", () => {
    const groups = mapAnswer().groups!.map((g, i) => ({ ...g, subs: i ? [] : [{ key: 'ledger', label: 'Ledger team', services: 3, x: -40, y: 0, r: 12 }, { key: 'cards', services: 2, x: 40, y: 0, r: 10 }] })) as MapGroup[];
    const ctx = recordingContext();
    render(ctx, scene(1.5, { groups, groupBy: 'team' }));
    expect(texts(ctx.calls)).toEqual(expect.arrayContaining(['Ledger team', 'cards']));
  });

  it("environment columns in the answer's order with Previews (N)", () => {
    const a = mapAnswer();
    const groups = [
      { ...a.groups![0], key: '(previews)', label: 'Previews (2)', kind: 'previews', preview: true, r: 60 },
      { ...a.groups![1], key: 'prod', label: 'prod', kind: 'environment', x: 300, r: 60 },
    ] as MapGroup[];
    const ctx = recordingContext();
    render(ctx, scene(0.8, { groups, ribbons: [], groupBy: 'environment' }));
    expect(texts(ctx.calls)).toEqual(expect.arrayContaining(['Previews (2)', '5 running', 'prod', '3 running']));
  });

  it('TestFleetMapStatesNotColourOnly', () => {
    // Every legend state has a shape, a glyph or a dash of its own: none differs from another by colour alone.
    const cue = (e: (typeof LEGEND)[number]) => `${e.shape}|${e.glyph ?? ''}|${(e.dash ?? []).join(',')}`;
    expect(new Set(LEGEND.map(cue)).size).toBe(LEGEND.length);
    for (const e of LEGEND) expect(e.label).toMatch(/\w/);
    for (const k of ['error', 'warning', 'expected-only', 'inferred', 'external', 'matched', 'expected-not-observed', 'observed-not-expected', 'problem', 'cross-environment']) {
      expect(LEGEND.map((e) => e.key)).toContain(k);
    }
  });
});
