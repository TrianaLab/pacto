/**
 * The fleet map component: keyboard, pointer drag and pinch, the touch-size
 * rule and the label hook, over a recording canvas (jsdom draws nothing).
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import FleetMap from './FleetMap.svelte';
import { FleetMapStore } from '../lib/fleetMap/store.svelte';
import { mapAnswer, nodesPage, recordingContext } from '../lib/fleetMap/testkit';
import { toWorld, type View } from '../lib/fleetMap/view';

type Hooks = { labels: () => { text: string; x: number; y: number; w: number; h: number; size: number }[]; idle: () => Promise<void>; view: () => View & { level: number }; bench: () => { frames: number[]; p95: number } };
const hooks = () => (window as unknown as { __fleetMap: Hooks }).__fleetMap;

let done: () => void = () => {};
beforeEach(() => {
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(() => recordingContext() as never);
  vi.spyOn(HTMLCanvasElement.prototype, 'getBoundingClientRect').mockReturnValue({ x: 0, y: 0, left: 0, top: 0, right: 800, bottom: 500, width: 800, height: 500, toJSON: () => ({}) } as DOMRect);
  vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => setTimeout(() => cb(performance.now()), 0) as unknown as number);
  vi.stubGlobal('cancelAnimationFrame', (id: number) => clearTimeout(id));
});
afterEach(() => { done(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

async function renderMap(answer = mapAnswer()) {
  const nodes = vi.fn(async (q: { group: string }) => nodesPage(q.group, [{}, { severity: 'error' }]));
  const store = new FleetMapStore({ map: async () => answer, nodes });
  const target = document.createElement('div');
  document.body.appendChild(target);
  const c = mount(FleetMap, { target, props: { store } });
  done = () => { unmount(c); target.remove(); done = () => {}; };
  await vi.waitFor(() => expect(store.answer).not.toBeNull());
  await hooks().idle();
  await new Promise((r) => setTimeout(r, 0));
  flushSync();
  return { target, canvas: target.querySelector('canvas[role="img"]') as HTMLCanvasElement, nodes };
}

const key = (el: Element, k: string) => el.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true }));
const ptr = (el: Element, type: string, id: number, x: number, y: number) =>
  el.dispatchEvent(new PointerEvent(type, { pointerId: id, clientX: x, clientY: y, bubbles: true }));

describe('FleetMap', () => {
  it('is a labelled image with its level ladder, breadcrumbs, zoom buttons and live region', async () => {
    const { target, canvas } = await renderMap();
    expect(canvas.getAttribute('aria-roledescription')).toBe('dependency map');
    expect(canvas.style.touchAction === '' || canvas.style.touchAction === 'none').toBe(true);
    expect([...target.querySelectorAll('[aria-label="Level of detail"] button')].map((b) => b.textContent)).toEqual(['Groups', 'Services', 'Names', 'Details']);
    expect(target.querySelector('[aria-current="step"]')?.textContent).toBe('Names');
    expect(target.querySelector('[aria-live="polite"]')?.textContent).toBe('Level 2, Names');
    expect(target.querySelector('nav[aria-label="Where you are"]')?.textContent).toContain('All services');
  });

  it('keyboard + - 0 and arrows', async () => {
    const { canvas } = await renderMap();
    const fit = hooks().view();
    key(canvas, '+');
    expect(hooks().view().k).toBeCloseTo(fit.k * 1.4);
    key(canvas, '-');
    expect(hooks().view().k).toBeCloseTo(fit.k);
    key(canvas, 'ArrowRight');
    expect(hooks().view().tx).toBeCloseTo(fit.tx - 80);
    key(canvas, 'ArrowDown');
    expect(hooks().view().ty).toBeCloseTo(fit.ty - 80);
    key(canvas, 'ArrowLeft'); key(canvas, 'ArrowUp');
    key(canvas, '+'); key(canvas, '+');
    key(canvas, '0');
    await hooks().idle();
    const back = hooks().view();
    expect(back.k).toBeCloseTo(fit.k);
    expect(back.tx).toBeCloseTo(fit.tx);
  });

  it('pointer drag pans', async () => {
    const { canvas } = await renderMap();
    const before = hooks().view();
    ptr(canvas, 'pointerdown', 1, 100, 100);
    ptr(canvas, 'pointermove', 1, 150, 130);
    ptr(canvas, 'pointerup', 1, 150, 130);
    const after = hooks().view();
    expect(after.k).toBe(before.k);
    expect(after.tx - before.tx).toBeCloseTo(50);
    expect(after.ty - before.ty).toBeCloseTo(30);
  });

  it('two-pointer pinch zooms about the midpoint', async () => {
    const { canvas } = await renderMap();
    const before = hooks().view();
    ptr(canvas, 'pointerdown', 1, 100, 100);
    ptr(canvas, 'pointerdown', 2, 300, 100);
    const under = toWorld(before, 300, 100);
    ptr(canvas, 'pointermove', 2, 500, 100);
    const after = hooks().view();
    expect(after.k).toBeCloseTo(before.k * 2);
    const still = toWorld(after, 300, 100);
    expect(still[0]).toBeCloseTo(under[0]);
    expect(still[1]).toBeCloseTo(under[1]);
    ptr(canvas, 'pointerup', 2, 500, 100);
    ptr(canvas, 'pointerup', 1, 100, 100);
  });

  it('coarse pointer controls are at least 44px', () => {
    // jsdom applies no component style, so the rule is read from the source.
    const source = readFileSync(join(process.cwd(), 'src/components/FleetMap.svelte'), 'utf8');
    expect(source).toMatch(/@media \(pointer: coarse\) \{ \.fm-zoom button, \.fm-ladder button, \.fm-crumbs button \{ min-inline-size: 44px; min-block-size: 44px; \} \}/);
    expect(source).toMatch(/canvas:not\(\.fm-mini\) \{[^}]*touch-action: none/);
  });

  it("labels() reports each drawn label's rect and font size", async () => {
    await renderMap();
    const labels = hooks().labels();
    expect(labels.length).toBeGreaterThan(0);
    for (const l of labels) {
      expect(l.size).toBeGreaterThanOrEqual(13);
      expect(l.w).toBeGreaterThan(0);
      expect(l.x).toBeGreaterThanOrEqual(0);
      expect(l.x + l.w).toBeLessThanOrEqual(800);
    }
    expect(labels.map((l) => l.text)).toEqual(expect.arrayContaining([expect.stringContaining('Payments')]));
    expect(hooks().bench().frames.length).toBeGreaterThan(0);
  });

  it('asks for the node pages of the groups in view from the Services level on', async () => {
    // A wide map fits at the Groups level, which needs no node.
    const wide = mapAnswer('snap-1', { bounds: { minX: -1500, minY: -900, maxX: 1500, maxY: 900 } });
    const { canvas, nodes } = await renderMap(wide);
    expect(hooks().view().level).toBe(0);
    expect(nodes).not.toHaveBeenCalled();
    while (hooks().view().level < 1) key(canvas, '+');
    await vi.waitFor(() => expect(nodes).toHaveBeenCalled());
    expect(nodes.mock.calls.map(([q]) => q.group).sort()).toEqual(['checkout', 'payments']);
  });
});
