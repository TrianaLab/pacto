/**
 * Test kit for the fleet map: a recording 2D context (jsdom has no canvas) and
 * a small map answer. Tests assert the calls a frame makes, never pixels.
 */
import type { FleetMapAnswer, FleetMapNodesPage } from '../api';

export interface Call {
  name: string;
  args: unknown[];
  fillStyle: string;
  strokeStyle: string;
  globalAlpha: number;
  lineWidth: number;
  font: string;
  dash: number[];
}

/** recordingContext is a CanvasRenderingContext2D stand-in that records every call with the state it ran in. */
export function recordingContext(): CanvasRenderingContext2D & { calls: Call[]; reset: () => void } {
  const state = { fillStyle: '', strokeStyle: '', globalAlpha: 1, lineWidth: 1, font: '10px sans-serif', textAlign: 'start', textBaseline: 'alphabetic', lineJoin: 'miter', lineCap: 'butt' };
  let dash: number[] = [];
  const calls: Call[] = [];
  const rec = (name: string) => (...args: unknown[]) => {
    calls.push({ name, args, fillStyle: state.fillStyle, strokeStyle: state.strokeStyle, globalAlpha: state.globalAlpha, lineWidth: state.lineWidth, font: state.font, dash });
  };
  const ctx = {
    calls,
    reset: () => { calls.length = 0; },
    setLineDash: (d: number[]) => { dash = [...d]; },
    getLineDash: () => dash,
    measureText: (s: string) => ({ width: s.length * (parseFloat(/(\d+(?:\.\d+)?)px/.exec(state.font)?.[1] ?? '10') * 0.55) }),
  } as Record<string, unknown>;
  for (const n of ['setTransform', 'fillRect', 'strokeRect', 'beginPath', 'moveTo', 'lineTo', 'arc', 'rect', 'roundRect', 'quadraticCurveTo', 'bezierCurveTo', 'fill', 'stroke', 'fillText', 'strokeText', 'save', 'restore', 'clearRect']) ctx[n] = rec(n);
  return new Proxy(ctx, {
    get: (t, k: string) => (k in state ? state[k as keyof typeof state] : t[k]),
    set: (t, k: string, v) => { if (k in state) (state as Record<string, unknown>)[k] = v; else t[k] = v; return true; },
  }) as unknown as CanvasRenderingContext2D & { calls: Call[]; reset: () => void };
}

const tally = { services: { matched: 3, expectedOnly: 0, observedOnly: 1, insufficient: 1 }, dependencies: { matched: 2, expectedOnly: 1, observedOnly: 0, insufficient: 0 }, completeness: 'complete' as const };
const attention = (errors: number, warnings: number) => ({ errors, warnings, info: 0, worst: [] });

/** mapAnswer is a two-domain map: payments (with problems) and checkout, and one ribbon between them. */
export function mapAnswer(snapshotId = 'snap-1', over: Partial<FleetMapAnswer> = {}): FleetMapAnswer {
  return {
    meta: { schemaVersion: 'pacto.dev/fleet-product/v1', snapshotId, asOf: '2026-11-20T09:40:00Z', completeness: 'complete' },
    groupBy: 'domain',
    groups: [
      { key: 'payments', label: 'Payments', kind: 'domain', services: 5, inferred: 1, x: 0, y: 0, r: 40, match: { ...tally, percent: 75, coverage: 80 }, attention: attention(2, 3) },
      { key: 'checkout', label: 'Checkout', kind: 'domain', services: 3, inferred: 0, x: 200, y: 0, r: 30, match: { ...tally, percent: 50 }, attention: attention(0, 0) },
    ],
    ribbons: [{ from: 'payments', to: 'checkout', total: 4, matched: 2, expectedOnly: 1, observedOnly: 1 }],
    match: { ...tally, percent: 70, coverage: 80 },
    knowledge: {} as unknown as FleetMapAnswer['knowledge'],
    totals: { services: 8, inferred: 1, externals: 0, targets: 0, groups: 2, ribbons: 1 },
    bounds: { minX: -40, minY: -40, maxX: 230, maxY: 40 },
    limitations: {} as FleetMapAnswer['limitations'],
    ...over,
  } as FleetMapAnswer;
}

/** nodesPage is one page of a group's nodes, at offsets relative to the group centre. */
export function nodesPage(group: string, nodes: Partial<NonNullable<FleetMapNodesPage['nodes']>[number]>[], over: Partial<FleetMapNodesPage> = {}): FleetMapNodesPage {
  return {
    meta: { schemaVersion: 'pacto.dev/fleet-product/v1', snapshotId: 'snap-1', asOf: '2026-11-20T09:40:00Z', completeness: 'complete' },
    group, groupBy: 'domain', offset: 0, limit: 1000, total: nodes.length,
    nodes: nodes.map((n, i) => ({ key: `${group}-${i}`, name: `${group}-${i}`, x: i * 5, y: 0, ...n })),
    edges: [],
    ...over,
  } as FleetMapNodesPage;
}
