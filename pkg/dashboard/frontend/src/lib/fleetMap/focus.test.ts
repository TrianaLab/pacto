import { describe, it, expect } from 'vitest';
import { FC, edgeDash, edgeLabel, edgeTip, focusView, hitFocus, layoutFocus, renderFocus, type FocusScene } from './focus';
import { EDGE_DASH, MIN_FONT, type Palette } from './render';
import { focusAnswer, focusEdge, focusNode, recordingContext } from './testkit';

const P: Palette = {
  bg: 'bg', raised: 'raised', border: 'border', borderStrong: 'strong', text: 'text', text2: 'text2', text3: 'text3',
  ok: 'ok', warn: 'warn', err: 'err', info: 'info', neutral: 'neutral', accent: 'accent', errBg: 'errBg',
  sans: 'sans', display: 'display', dark: true,
};
const vp = { w: 1400, h: 800 };
const scene = (L: ReturnType<typeof layoutFocus>, over: Partial<FocusScene> = {}): FocusScene => ({ view: focusView(L, vp), w: vp.w, h: vp.h, dpr: 1, palette: P, t: 1, ...over });

describe('fleet map focus', () => {
  it('focus columns callers left callees right', () => {
    const L = layoutFocus(focusAnswer());
    const centre = L.byKey.get('payments-0')!;
    expect(centre.col).toBe(0);
    for (const k of ['web', 'mobile']) {
      const c = L.byKey.get(k)!;
      expect(c.col).toBe(-1);
      expect(c.rect[0] + c.rect[2]).toBeLessThan(centre.rect[0]);
    }
    const ledger = L.byKey.get('ledger')!;
    expect(ledger.col).toBe(1);
    expect(ledger.rect[0]).toBeGreaterThan(centre.rect[0] + centre.rect[2]);
    expect([L.callers, L.callees]).toEqual([2, 1]);
  });

  it('nine per column worst first with N of M', () => {
    const many = Array.from({ length: 12 }, (_, i) => focusNode(`c${String(i).padStart(2, '0')}`, i === 11 ? { severity: 'error' } : i === 10 ? { severity: 'warning' } : {}));
    const L = layoutFocus(focusAnswer('payments-0', 1, { columns: [{ hop: -1, total: 40, nodes: many }] }));
    const col = L.columns.find((c) => c.hop === -1)!;
    expect(col.n).toBe(FC.MAXR);
    expect(col.total).toBe(40);
    const left = L.cards.filter((c) => c.col === -1).sort((a, b) => a.rect[1] - b.rect[1]).map((c) => c.node.key);
    expect(left).toEqual(['c11', 'c10', 'c00', 'c01', 'c02', 'c03', 'c04', 'c05', 'c06']);
    const ctx = recordingContext();
    const texts = renderFocus(ctx, L, scene(L)).labels.map((l) => l.text);
    expect(texts).toContain('9 of 40 shown');
    expect(texts).toContain('Calls payments-0');
  });

  it('focus edges are drawn only between adjacent columns in the call direction', () => {
    const a = focusAnswer('payments-0', 2, {
      columns: [
        { hop: -2, total: 1, nodes: [focusNode('edge')] },
        { hop: -1, total: 1, nodes: [focusNode('web')] },
        { hop: 1, total: 1, nodes: [focusNode('ledger')] },
      ],
      edges: [
        focusEdge('edge', 'web'), focusEdge('web', 'payments-0'), focusEdge('payments-0', 'ledger'),
        focusEdge('edge', 'payments-0'), // skips a column
        focusEdge('ledger', 'payments-0'), // against the flow
        focusEdge('web', 'web'),
      ],
    });
    expect(layoutFocus(a).edges.map((e) => e.id)).toEqual(['edge->web', 'web->payments-0', 'payments-0->ledger']);
  });

  it('edge labels interface and range, operation only on problem edges', () => {
    expect(edgeLabel(focusEdge('a', 'b', { interface: 'orders-api', range: '^2.3' }))).toEqual({ text: 'orders-api ^2.3', sev: 0 });
    const breaking = edgeLabel(focusEdge('a', 'b', { interface: 'cache', range: '^1', problem: { code: 'BREAKING', severity: 'error', reason: 'removes PUT /cache' } }));
    expect(breaking).toEqual({ text: 'removes PUT /cache', sev: 2 });
    expect(edgeLabel(focusEdge('a', 'b', { problem: { code: 'DEPENDENCY_RANGE_UNSATISFIED', severity: 'warning', reason: 'accepts ^2.3 · runs 3.0.1' } })).sev).toBe(1);
    expect(edgeLabel(focusEdge('a', 'b', { expected: false, difference: 'observed-not-expected' }))).toEqual({ text: 'api · not declared', sev: 1 });
    expect(edgeLabel(focusEdge('a', 'b', { inferred: true }))).toEqual({ text: 'api · inferred', sev: 0 });
    // A healthy edge never names an operation.
    expect(edgeLabel(focusEdge('a', 'b')).text).not.toMatch(/GET|PUT|POST|DELETE/);
    expect(edgeDash(focusEdge('a', 'b', { difference: 'expected-not-observed' }))).toEqual(EDGE_DASH.expectedOnly);
    expect(edgeDash(focusEdge('a', 'b', { problem: { code: 'CROSS_ENVIRONMENT_CALL', severity: 'error' } }))).toEqual(EDGE_DASH.crossEnvironment);
  });

  it('edge tip lists provenance, range, running version, contract, source and asOf', () => {
    const e = focusEdge('web', 'ledger', {
      range: '^2.3', runningVersion: '3.0.1', declaredIn: { key: 'web@1', label: 'web 1.4.0', kind: 'revision', href: '' },
      observationSources: { count: 1, total: 3, truncated: true, items: [{ source: 'otel-collector', count: 4 }] },
      lastSeen: '2026-11-20T09:30:00Z', problem: { code: 'DEPENDENCY_RANGE_UNSATISFIED', severity: 'warning', reason: 'accepts ^2.3 · runs 3.0.1' },
    });
    const tip = edgeTip(e, '2026-11-20T09:40:00Z');
    expect(tip.title).toBe('web → ledger');
    expect(tip.problem?.text).toBe('accepts ^2.3 · runs 3.0.1');
    const all = tip.lines.join('\n');
    expect(all).toContain('Provenance: Expected and observed');
    expect(all).toContain('Accepts: ^2.3 · runs 3.0.1');
    expect(all).toContain('Declared in: contract web 1.4.0');
    expect(all).toContain('Evidence: otel-collector and 2 more');
    expect(all).toMatch(/As of .*2026/);
    const bare = edgeTip(focusEdge('a', 'b', { range: '', observed: false, expected: false, environmentUnknown: true }), '2026-11-20T09:40:00Z').lines.join('\n');
    expect(bare).toContain('Accepts: any version');
    expect(bare).toContain('Declared in: not declared');
    expect(bare).toContain('no evidence source covers this call · environment unknown');
  });

  it('draws arrowed edges, readable labels and hit tests cards and edges', () => {
    const L = layoutFocus(focusAnswer());
    const ctx = recordingContext();
    const f = renderFocus(ctx, L, scene(L));
    expect(ctx.calls.filter((c) => c.name === 'bezierCurveTo')).toHaveLength(3);
    for (const l of f.labels) expect(l.size).toBeGreaterThanOrEqual(MIN_FONT);
    const texts = f.labels.map((l) => l.text);
    expect(texts).toEqual(expect.arrayContaining(['payments-0', 'payments-0 calls', 'api ^1.0']));
    expect(texts.some((t) => t.startsWith('✕ '))).toBe(true);
    const r = f.rects.get('ledger')!;
    expect(hitFocus(L, f, (r[0] + r[2]) / 2, (r[1] + r[3]) / 2).node?.key).toBe('ledger');
    const g = f.geo.get('payments-0->ledger')!;
    expect(hitFocus(L, f, g[0] + 1, g[1]).edge?.id).toBe('payments-0->ledger');
    expect(hitFocus(L, f, 2, 2)).toEqual({});
  });

  it('omits card lines that do not fit at a small zoom', () => {
    const L = layoutFocus(focusAnswer());
    const ctx = recordingContext();
    const f = renderFocus(ctx, L, scene(L, { view: { k: 0.25, tx: 700, ty: 400 }, t: 0.5 }));
    // Small cards keep at most what fits, and no header or edge label is drawn while fading in.
    expect(f.labels.every((l) => l.size >= MIN_FONT)).toBe(true);
    expect(f.labels.map((l) => l.text)).not.toContain('payments-0 calls');
  });
});
