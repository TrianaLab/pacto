import { describe, it, expect } from 'vitest';
import { slab, ringPath, stackGeometry, widthClass, CHIP_CAP, anchorPoint, wirePath, sharePct, GLYPH_PATH } from './figure.ts';

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
