import { describe, it, expect } from 'vitest';
import { slab, ringPath, stackGeometry, widthClass, CHIP_CAP, anchorPoint, wirePath, wirePoint, breakMark, arrowHead, BREAK_AT, planChips, CHIP_FLOOR, sharePct, GLYPH_PATH } from './figure.ts';

describe('slab and ringPath', () => {
  it('reproduce the hero checkout slab and ring byte for byte', () => {
    expect(slab(114, 388, 74, 14)).toEqual({
      top: 'M114 351 L188 388 L114 425 L40 388 Z',
      left: 'M40 388 L114 425 L114 439 L40 402 Z',
      right: 'M114 425 L188 388 L188 402 L114 439 Z',
      edge: 'M114 351 L188 388 L114 425 L40 388 Z M40 388 V402 L114 439 L188 402 V388 M114 425 V439',
    });
    expect(ringPath(114, 388, 74)).toBe('M114 345 L200 388 L114 431 L28 388 Z');
  });
});

describe('stackGeometry', () => {
  it('caps the layers at 5 and keeps every layer inside its cell', () => {
    for (const n of [1, 2, 5, 40]) {
      const g = stackGeometry(n);
      expect(g.layers.length).toBe(Math.min(5, n));
      const bottom = g.layers[g.layers.length - 1];
      expect(bottom.y + 26 + 9).toBeLessThanOrEqual(g.H);
      for (const L of g.layers) expect(L.anchor.x).toBeLessThanOrEqual(g.W);
    }
    expect(stackGeometry(0).layers.length).toBe(1);
  });
});

describe('width classes (S1)', () => {
  it('names at most 4, then 2, then no chips as the figure narrows', () => {
    expect([1280, 800, 799, 560, 559, 480, 479, 0].map(widthClass)).toEqual(['wide', 'wide', 'mid', 'mid', 'narrow', 'narrow', 'col', 'col']);
    expect(CHIP_CAP).toEqual({ wide: 4, mid: 2, narrow: 0, col: 0 });
  });
});

describe('wires', () => {
  const r = { left: 10, top: 20, right: 30, bottom: 60, width: 20, height: 40 };
  const o = { left: 10, top: 10 };
  it('anchors on the middle of each side, relative to the stage', () => {
    expect(anchorPoint(r, 'l', o)).toEqual([0, 30]);
    expect(anchorPoint(r, 'r', o)).toEqual([20, 30]);
    expect(anchorPoint(r, 't', o)).toEqual([10, 10]);
    expect(anchorPoint(r, 'b', o)).toEqual([10, 50]);
    expect(anchorPoint(r, 'c', o)).toEqual([10, 30]);
  });
  it('curves between two sides, and elbows round a corner', () => {
    expect(wirePath([0, 0], 'r', [10, 20], 'l')).toBe('M0 0 C5 0 5 20 10 20');
    expect(wirePath([0, 0], 'b', [10, 20], 't')).toBe('M0 0 C0 10 10 10 10 20');
    expect(wirePath([0, 0], 'b', [10, 20], 'l')).toBe('M0 0 C0 20 0 20 10 20');
    expect(wirePath([0, 0], 'r', [10, 20], 't')).toBe('M0 0 C10 0 10 0 10 20');
    expect(wirePath([0, 0], 'c', [10, 20], 'c')).toBe('M0 0 L10 20');
  });
});

describe('wire marks (2.1, C12, C13b)', () => {
  it('puts the broken ✕ at 35% of the wire, on the curve wirePath draws', () => {
    expect(wirePoint([0, 0], 'r', [100, 0], 'l', 0)).toEqual([0, 0]);
    expect(wirePoint([0, 0], 'r', [100, 0], 'l', 1)).toEqual([100, 0]);
    expect(wirePoint([0, 0], 'c', [100, 50], 'c', BREAK_AT)).toEqual([35, 17.5]);
    const [x] = wirePoint([0, 0], 'r', [100, 40], 'l', BREAK_AT);
    expect(x).toBeGreaterThan(20);
    expect(x).toBeLessThan(50);
    expect(breakMark([10, 10])).toBe('M6 6 L14 14 M14 6 L6 14');
  });
  it('points the arrowhead the way the wire enters its target, tip on the anchor', () => {
    expect(arrowHead([0, 0], 'r', [100, 0], 'l')).toBe('M100 0 L93 4 L93 -4 Z');
    expect(arrowHead([0, 0], 'b', [0, 50], 't')).toBe('M0 50 L-4 43 L4 43 Z');
    expect(arrowHead([0, 0], 'r', [50, 80], 't')).toBe('M50 80 L46 73 L54 73 Z');
    expect(arrowHead([0, 0], 'c', [30, 40], 'c')).toBe('M30 40 L22.6 36.8 L29 32 Z');
  });
});

describe('planChips (C8a)', () => {
  it('names every chip that fits whole, and shares the room between long ones', () => {
    expect(planChips([80, 90], 400, 4)).toEqual({ shown: 2, widths: [80, 90] });
    // Two long names split the room; a short one keeps its width and gives the rest.
    expect(planChips([900, 900], 300, 4)).toEqual({ shown: 2, widths: [146, 146] });
    expect(planChips([900, 60, 900], 340, 4)).toEqual({ shown: 3, widths: [132, 60, 132] });
  });
  it('folds chips until each elided one keeps 8rem, and keeps the first down to 90px', () => {
    expect(planChips([900, 900, 900], 300, 4).shown).toBe(2);
    expect(planChips([900, 900], 200, 4)).toEqual({ shown: 1, widths: [200] });
    expect(planChips([900], 95, 4)).toEqual({ shown: 1, widths: [95] });
    expect(planChips([900], 80, 4)).toEqual({ shown: 0, widths: [] });
    expect(planChips([50, 50, 50, 50, 50], 1000, 4).shown).toBe(4);
    expect(planChips([50], 1000, 0).shown).toBe(0);
  });
  // G3 (r5): N = 4,000 consumers with a 200-character highlight. At a 1,120px stage
  // (1280 viewport) a lane of the flagship leaves its chips the room below (stack and
  // split columns, node, the widest short lane label, count, 160px bar, open cap, the
  // gaps and the "List all 3,880" reserve): two highlights a lane, so the three lanes
  // with highlights name at least 4.
  it('names at least 4 on the flagship at a 1,120px stage, 1 in every lane with highlights', () => {
    const stage = 1120;
    const room = stage - (148 + 20 + 28 + 32) - (28 + 136 + 112 + 160 + 12 + 5 * 8) - (120 + 8) - 8;
    const verdana = (s: string) => Math.ceil(s.length * 7.6) + 16;
    const lanes = [
      ['a'.repeat(200), 'checkout-web-adapter-europe-west', 'refunds-worker'],
      ['search-scheduler-for-partner-feed-compliance', 'orders-reverse-sync-bridge-legacy'],
      ['ledger-export-worker-eu-west-1-canary'],
    ];
    const named = lanes.map((names) => planChips(names.map(verdana), room, CHIP_CAP.wide).shown);
    expect(named.every((n) => n >= 1)).toBe(true);
    expect(named.reduce((a, b) => a + b, 0)).toBeGreaterThanOrEqual(4);
    expect(planChips([verdana('a'.repeat(200)), verdana('b'.repeat(200))], room, 4).widths.every((w) => w >= CHIP_FLOOR)).toBe(true);
  });
});

describe('sharePct', () => {
  it('never prints 0% for a non-zero share', () => {
    expect(sharePct(1, 5000)).toBe('<1%');
    expect(sharePct(37, 1284)).toBe('3%');
    expect(sharePct(0, 10)).toBe('0%');
    expect(sharePct(3, 0)).toBe('0%');
  });
});

it('draws state glyphs as paths, never characters (C15)', () => {
  for (const d of Object.values(GLYPH_PATH)) expect(d).toMatch(/^M[-\d. MLVHC]+$/);
});
