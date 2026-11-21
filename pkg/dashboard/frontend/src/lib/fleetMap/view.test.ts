import { describe, it, expect } from 'vitest';
import { Camera, FIT_MARGIN, GAP_WORLD, fitView, interpolate, kForGap, level, smooth, toScreen, toWorld, zoomAt } from './view';

describe('fleet map view', () => {
  it('level thresholds 14/28/110', () => {
    expect([0, 13.9, 14, 27.9, 28, 109.9, 110, 400].map(level)).toEqual([0, 0, 1, 1, 2, 2, 3, 3]);
    expect(kForGap(14) * GAP_WORLD).toBe(14);
    expect(smooth(10, 12, 16)).toBe(0);
    expect(smooth(20, 12, 16)).toBe(1);
    const mid = smooth(14, 12, 16);
    expect(mid).toBeGreaterThan(0);
    expect(mid).toBeLessThan(1);
  });

  it('fit frames the measured canvas minus the rail', () => {
    const b = { minX: -100, minY: -50, maxX: 100, maxY: 50 };
    const open = fitView(b, { w: 1000, h: 500, rail: 360 });
    // The content is centred in the 640px the rail leaves, inside the margin.
    const [x0, y0] = toScreen(open, b.minX, b.minY), [x1, y1] = toScreen(open, b.maxX, b.maxY);
    expect((x0 + x1) / 2).toBeCloseTo(320);
    expect((y0 + y1) / 2).toBeCloseTo(250);
    expect(x0).toBeGreaterThanOrEqual(FIT_MARGIN - 1e-9);
    expect(x1).toBeLessThanOrEqual(640 - FIT_MARGIN + 1e-9);
    expect(y1 - y0).toBeLessThanOrEqual(500 - 2 * FIT_MARGIN + 1e-9);
    const closed = fitView(b, { w: 1000, h: 500 });
    expect(closed.k).toBeGreaterThan(open.k);
  });

  it('zoom keeps the point under the cursor', () => {
    const v = { k: 1, tx: 30, ty: -20 };
    const before = toWorld(v, 250, 120);
    const z = zoomAt(v, 250, 120, 2, 0.1);
    expect(z.k).toBe(2);
    const after = toWorld(z, 250, 120);
    expect(after[0]).toBeCloseTo(before[0]);
    expect(after[1]).toBeCloseTo(before[1]);
    // The zoom stays within half the fitted zoom and 240px gaps.
    expect(zoomAt(v, 0, 0, 1e-6, 1).k).toBe(0.5);
    expect(zoomAt(v, 0, 0, 1e6, 1).k).toBe(kForGap(240));
  });

  it('reduced motion jumps', () => {
    const target = { k: 3, tx: 10, ty: 10 };
    const still = new Camera(() => true);
    still.go(target, true, 0);
    expect(still.view).toEqual(target);
    expect(still.moving).toBe(false);

    const moving = new Camera(() => false);
    moving.go(target, true, 0);
    expect(moving.moving).toBe(true);
    expect(moving.step(120)).toBe(true);
    expect(moving.view.k).toBeGreaterThan(1);
    expect(moving.view.k).toBeLessThan(3);
    expect(moving.step(1000)).toBe(false);
    expect(moving.view).toEqual(target);
    expect(interpolate({ k: 1, tx: 0, ty: 0 }, { k: 1, tx: 100, ty: 0 }, 1).tx).toBe(100);
  });
});
