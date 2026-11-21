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
import { focusAnswer, mapAnswer, nodesPage, recordingContext } from '../lib/fleetMap/testkit';
import { api, type FleetMapFocusAnswer } from '../lib/api';
import { toScreen, toWorld, type View } from '../lib/fleetMap/view';
import { LEGEND } from '../lib/fleetMap/render';
import type { FleetMapAnswer } from '../lib/api';
import type { MapGroup } from '../lib/fleetMap/lens';

type Hooks = { labels: () => { text: string; x: number; y: number; w: number; h: number; size: number }[]; idle: () => Promise<void>; view: () => View & { level: number }; bench: () => { frames: number[]; p95: number } };
const hooks = () => (window as unknown as { __fleetMap: Hooks }).__fleetMap;

let done: () => void = () => {};
beforeEach(() => {
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(() => recordingContext() as never);
  vi.spyOn(HTMLCanvasElement.prototype, 'getBoundingClientRect').mockReturnValue({ x: 0, y: 0, left: 0, top: 0, right: 800, bottom: 500, width: 800, height: 500, toJSON: () => ({}) } as DOMRect);
  vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => setTimeout(() => cb(performance.now()), 0) as unknown as number);
  vi.stubGlobal('cancelAnimationFrame', (id: number) => clearTimeout(id));
  vi.spyOn(api, 'fleetAttention').mockResolvedValue({ items: [], total: 0, count: 0, limit: 30, offset: 0, truncated: false, categories: [], severities: { errors: 0, warnings: 0, infos: 0, unknown: 0 }, meta: {} } as never);
  location.hash = '';
});
afterEach(() => { done(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

async function renderMap(answer = mapAnswer(), props: { params?: Record<string, string>; onparams?: (p: Record<string, string | null>) => void; variant?: 'embedded' | 'full' } = {}, mapFn?: (q: { groupBy?: string }) => Promise<FleetMapAnswer>) {
  const nodes = vi.fn(async (q: { group: string }) => nodesPage(q.group, [{}, { severity: 'error' }]));
  const map = vi.fn(mapFn ?? (async () => answer));
  const focus = vi.fn(async (q: { service: string; hops?: number }): Promise<FleetMapFocusAnswer> => focusAnswer(q.service, q.hops ?? 1));
  const store = new FleetMapStore({ map, nodes, focus });
  const target = document.createElement('div');
  document.body.appendChild(target);
  const c = mount(FleetMap, { target, props: { store, onparams: () => {}, ...props } });
  done = () => { unmount(c); target.remove(); done = () => {}; };
  await vi.waitFor(() => expect(store.answer).not.toBeNull());
  await hooks().idle();
  await new Promise((r) => setTimeout(r, 0));
  flushSync();
  return { target, canvas: target.querySelector('canvas[role="img"]') as HTMLCanvasElement, nodes, map, focus, store };
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
    expect(target.querySelector('[data-testid="fleet-map-live"]')?.textContent).toBe('Level 2, Names');
    expect(target.querySelector('nav[aria-label="Where you are"]')?.textContent).toContain('Whole fleet');
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

  const pressed = (root: Element, label: string) =>
    [...root.querySelectorAll(`[aria-label="${label}"] button`)].filter((b) => b.getAttribute('aria-pressed') === 'true').map((b) => b.textContent?.trim());
  const button = (root: Element, text: string) => [...root.querySelectorAll('button')].find((b) => b.textContent?.trim().startsWith(text)) as HTMLButtonElement;

  it('groups three ways and switches the lens from the toolbar, writing the hash query', async () => {
    const onparams = vi.fn();
    const { target, map } = await renderMap(mapAnswer(), { onparams });
    expect(pressed(target, 'Group by')).toEqual(['Domain']);
    expect(pressed(target, 'Show')).toEqual(['Both']);
    expect(target.querySelector('[data-testid="fleet-map-summary"]')?.textContent).toBe('Showing 8 services and 3 dependencies, expected and observed, in 2 domains.');
    button(target, 'Owner team').click();
    flushSync();
    expect(onparams).toHaveBeenLastCalledWith({ groupBy: 'team' });
    await vi.waitFor(() => expect(map).toHaveBeenLastCalledWith({ groupBy: 'team', environment: undefined }));
    expect(pressed(target, 'Group by')).toEqual(['Owner team']);
    button(target, 'Expected').click();
    flushSync();
    expect(onparams).toHaveBeenLastCalledWith({ lens: 'expected' });
    expect(target.querySelector('[data-testid="fleet-map-summary"]')?.textContent).toContain('7 services');
    // A lens switch never refetches the map.
    expect(map).toHaveBeenCalledTimes(2);
    // The map has no "Show inferred" control: the Expected lens hides inferred services.
    expect(target.textContent).not.toMatch(/show inferred/i);
  });

  it('only problems keeps healthy as grey context', async () => {
    const onparams = vi.fn();
    const { target, canvas } = await renderMap(mapAnswer(), { onparams });
    const toggle = button(target, 'Only problems');
    expect(toggle.getAttribute('aria-pressed')).toBe('false');
    toggle.click();
    flushSync();
    expect(onparams).toHaveBeenLastCalledWith({ only: 'problems', gaps: null, missing: null });
    expect(toggle.getAttribute('aria-pressed')).toBe('true');
    expect(target.querySelector('[data-testid="fleet-map-mode"]')?.textContent).toContain('Everything healthy stays as grey context.');
    key(canvas, 'Escape');
    flushSync();
    expect(target.querySelector('[data-testid="fleet-map-mode"]')).toBeNull();
    expect(onparams).toHaveBeenLastCalledWith({ only: null, gaps: null, missing: null });
  });

  it('gaps chips toggle each kind', async () => {
    const onparams = vi.fn();
    const { target } = await renderMap(mapAnswer(), { params: { gaps: 'all', lens: 'expected' }, onparams });
    // Gaps read in Both, whatever the lens asked.
    expect(pressed(target, 'Show')).toEqual(['Both']);
    expect(pressed(target, 'Kinds of gap')).toEqual(['Expected, not observed 1', 'Observed, not expected 1', 'Insufficient evidence 1']);
    button(target, 'Observed, not expected').click();
    flushSync();
    expect(onparams).toHaveBeenLastCalledWith({ only: null, gaps: 'expected-only,insufficient', missing: null });
    expect(pressed(target, 'Kinds of gap')).toEqual(['Expected, not observed 1', 'Insufficient evidence 1']);
    button(target, 'Show everything').click();
    flushSync();
    expect(target.querySelector('[data-testid="fleet-map-mode"]')).toBeNull();
  });

  it("environment columns in the answer's order with Previews (N)", async () => {
    const env = mapAnswer('snap-1', {
      groupBy: 'environment',
      groups: [
        { ...mapAnswer().groups![0], key: 'prod', label: 'prod', kind: 'environment', depth: 1 },
        { ...mapAnswer().groups![1], key: '(previews)', label: 'Previews (1)', kind: 'previews', preview: true, depth: -1, subs: [{ key: 'pr-7', services: 1, x: 0, y: 0, r: 5 }] },
        { ...mapAnswer().groups![1], key: 'dev', label: 'dev', kind: 'environment', depth: 0, x: 400 },
      ] as MapGroup[],
    });
    const onparams = vi.fn();
    const { target, map, store } = await renderMap(mapAnswer(), { onparams }, async (q) => (q.groupBy === 'environment' ? env : mapAnswer()));
    const select = target.querySelector('.fm-env select') as HTMLSelectElement;
    select.dispatchEvent(new FocusEvent('focus'));
    await vi.waitFor(() => expect(store.environments).toBe(env));
    flushSync();
    expect([...select.options].map((o) => o.textContent)).toEqual(['All environments', 'dev', 'prod', 'pr-7']);
    expect(select.querySelector('optgroup')?.label).toBe('Previews');
    select.value = 'prod';
    select.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();
    expect(onparams).toHaveBeenLastCalledWith({ env: 'prod' });
    await vi.waitFor(() => expect(map).toHaveBeenLastCalledWith({ groupBy: 'domain', environment: 'prod' }));
    // Grouped by environment the columns are the environments: the picker steps aside.
    button(target, 'Environment').click();
    flushSync();
    expect(target.querySelector('.fm-env')).toBeNull();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="fleet-map-summary"]')?.textContent).toContain('in 2 environments and 1 preview.'));
  });

  it('a group tooltip gives the match ring value, completeness and what the group is missing', async () => {
    // Wide bounds fit at the Groups level, where a bubble has a tooltip.
    const big = mapAnswer('snap-1', { bounds: { minX: -1500, minY: -900, maxX: 1500, maxY: 900 }, groups: mapAnswer().groups!.map((g, i) => ({ ...g, percentComplete: i ? 100 : 72, attention: { ...g.attention, worst: i ? [] : [{ category: 'non-compliant', count: 2 }] } })) as MapGroup[] });
    const onparams = vi.fn();
    const { target, canvas } = await renderMap(big, { onparams });
    const [sx, sy] = toScreen(hooks().view(), 0, 0);
    ptr(canvas, 'pointermove', 1, sx, sy);
    flushSync();
    const tip = target.querySelector('[data-testid="fleet-map-group-tip"]')!;
    expect(tip.textContent).toContain('Payments');
    expect(tip.textContent).toContain('75% match · 80% observed');
    expect(tip.textContent).toContain('72% contract completeness');
    expect(tip.textContent).toContain('Not compliant 2');
    button(tip, 'What this group is missing').click();
    flushSync();
    expect(onparams).toHaveBeenLastCalledWith({ only: null, gaps: null, missing: 'any' });
    expect(target.querySelector('[data-testid="fleet-map-mode"]')?.textContent).toContain('Services that describe every section are dimmed.');
    expect(target.querySelector('[data-testid="fleet-map-group-tip"]')).toBeNull();
  });

  it('TestFleetMapStatesNotColourOnly', async () => {
    const { target } = await renderMap();
    const items = [...target.querySelectorAll('[aria-label="Legend"] li')];
    expect(items).toHaveLength(LEGEND.length);
    for (const [i, li] of items.entries()) {
      const e = LEGEND[i];
      expect(li.textContent).toContain(e.label);
      // Each swatch draws its cue: a dash, a glyph, a double ring or a filled dot.
      const svg = li.querySelector('svg')!;
      if (e.dash?.length) expect(svg.innerHTML).toContain(`stroke-dasharray="${e.dash.join(' ')}"`);
      if (e.glyph) expect(li.textContent).toContain(e.glyph);
      if (e.shape === 'ring2') expect(svg.querySelectorAll('circle')).toHaveLength(2);
    }
  });

  const live = (t: Element) => t.querySelector('[data-testid="fleet-map-live"]')?.textContent;
  const focusBar = (t: Element) => t.querySelector('[data-testid="fleet-map-focus"]');
  async function settle() { await hooks().idle(); await new Promise((r) => setTimeout(r, 0)); flushSync(); }
  /** nodeScreen waits for the node pages and answers where payments-0 (the payments centre) is on screen. */
  async function nodeScreen(nodes: ReturnType<typeof vi.fn>): Promise<[number, number]> {
    await vi.waitFor(() => expect(nodes).toHaveBeenCalled());
    await settle();
    return toScreen(hooks().view(), 0, 0);
  }
  const tap = (canvas: Element, [x, y]: [number, number]) => { ptr(canvas, 'pointerdown', 1, x, y); ptr(canvas, 'pointerup', 1, x, y); flushSync(); };
  const focused = async (t: Element) => { await vi.waitFor(() => expect(focusBar(t)).not.toBeNull()); await settle(); };

  it('one click selects, second click opens', async () => {
    const { target, canvas, nodes } = await renderMap();
    const at = await nodeScreen(nodes);
    tap(canvas, at);
    const panel = target.querySelector('[data-testid="fleet-map-selection"]')!;
    expect(panel.textContent).toContain('payments-0');
    expect(panel.textContent).toContain('Payments');
    expect(live(target)).toBe('Selected payments-0. Enter opens it, Escape clears the selection.');
    expect(location.hash).toBe('');
    tap(canvas, at);
    expect(location.hash).toBe('#/fleet/services/payments-0');
    // Enter opens the selection too; Escape clears it.
    location.hash = '';
    key(canvas, 'Enter');
    expect(location.hash).toBe('#/fleet/services/payments-0');
    key(canvas, 'Escape');
    flushSync();
    expect(target.querySelector('[data-testid="fleet-map-selection"]')).toBeNull();
  });

  it('live region announces focus', async () => {
    const { target, focus } = await renderMap(mapAnswer(), { params: { focus: 'payments-0' } });
    await focused(target);
    expect(focus).toHaveBeenCalledWith({ service: 'payments-0', hops: 1 });
    expect(live(target)).toBe('Focus on payments-0: 2 services call it, it calls 1. Escape returns to the whole fleet.');
    // Levels do not apply to the focus diagram.
    expect(target.querySelector('[aria-label="Level of detail"]')).toBeNull();
    expect(hooks().labels().map((l) => l.text)).toEqual(expect.arrayContaining(['Calls payments-0', 'payments-0 calls']));
  });

  it('back to whole fleet restores the view', async () => {
    const { target, canvas, nodes } = await renderMap();
    key(canvas, '+');
    const before = hooks().view();
    tap(canvas, await nodeScreen(nodes));
    button(target.querySelector('[data-testid="fleet-map-selection"]')!, 'Focus').click();
    flushSync();
    await focused(target);
    expect(hooks().view()).not.toEqual(before);
    button(target, 'Back to whole fleet').click();
    flushSync();
    await settle();
    const after = hooks().view();
    expect(after.k).toBeCloseTo(before.k);
    expect(after.tx).toBeCloseTo(before.tx);
    expect(after.ty).toBeCloseTo(before.ty);
    expect(focusBar(target)).toBeNull();
  });

  it('Esc and breadcrumb exit focus', async () => {
    const onparams = vi.fn();
    let m = await renderMap(mapAnswer(), { params: { focus: 'payments-0' }, onparams });
    await focused(m.target);
    const crumbs = () => m.target.querySelector('nav[aria-label="Where you are"]')!;
    expect(crumbs().textContent).toMatch(/Whole fleet\s*›\s*Payments\s*›\s*payments-0/);
    key(m.canvas, 'Escape');
    flushSync();
    expect(onparams).toHaveBeenLastCalledWith({ focus: null, hops: null });
    expect(focusBar(m.target)).toBeNull();
    expect(live(m.target)).toMatch(/^Level/);

    done();
    m = await renderMap(mapAnswer(), { params: { focus: 'payments-0' }, onparams });
    await focused(m.target);
    button(crumbs(), 'Whole fleet').click();
    flushSync();
    expect(focusBar(m.target)).toBeNull();

    done();
    m = await renderMap(mapAnswer(), { params: { focus: 'payments-0' }, onparams });
    await focused(m.target);
    button(crumbs(), 'Payments').click();
    flushSync();
    await settle();
    expect(focusBar(m.target)).toBeNull();
    expect(onparams).toHaveBeenLastCalledWith({ focus: null, hops: null });
    // The group crumb frames the focus service's group.
    const v = hooks().view();
    const [cx] = toWorld(v, 400, 250);
    expect(Math.abs(cx)).toBeLessThan(40);
  });

  it('one more hop folds the rail away and restores it', async () => {
    const onparams = vi.fn();
    const { target, focus } = await renderMap(mapAnswer(), { params: { focus: 'payments-0' }, onparams });
    await focused(target);
    expect(target.querySelector('[data-testid="fleet-map-rail"]')).not.toBeNull();
    button(target, 'One more hop').click();
    flushSync();
    expect(onparams).toHaveBeenLastCalledWith({ focus: 'payments-0', hops: '2' });
    await vi.waitFor(() => expect(focus).toHaveBeenLastCalledWith({ service: 'payments-0', hops: 2 }));
    expect(target.querySelector('[data-testid="fleet-map-rail"]')).toBeNull();
    expect(target.querySelector('[data-testid="fleet-map-rail-open"]')).not.toBeNull();
    button(target, 'One more hop').click();
    flushSync();
    expect(onparams).toHaveBeenLastCalledWith({ focus: 'payments-0', hops: null });
    expect(target.querySelector('[data-testid="fleet-map-rail"]')).not.toBeNull();
  });

  it('the full page starts with the rail closed and opens it through the hash', async () => {
    const onparams = vi.fn();
    const { target } = await renderMap(mapAnswer(), { variant: 'full', onparams });
    expect(target.querySelector('[data-testid="fleet-map-rail"]')).toBeNull();
    (target.querySelector('[data-testid="fleet-map-rail-open"]') as HTMLButtonElement).click();
    flushSync();
    expect(onparams).toHaveBeenLastCalledWith({ rail: 'open' });
    expect(target.querySelector('[data-testid="fleet-map-rail"]')).not.toBeNull();
  });

  it('rail item focuses its service', async () => {
    vi.mocked(api.fleetAttention).mockResolvedValue({
      items: [{ entity: { kind: 'revision', key: 'payments-0@sha', label: 'payments-0 1.0.0', href: '#/fleet/revisions/x' }, service: 'payments-0', category: 'health', severity: 'error', code: 'HEALTH', label: 'x', reason: 'r', summary: 'Health check failing' }],
      total: 1, count: 1, limit: 30, offset: 0, truncated: false, categories: [], severities: { errors: 1, warnings: 0, infos: 0, unknown: 0 }, meta: {},
    } as never);
    const { target, focus } = await renderMap();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="fleet-map-rail-item"]')).not.toBeNull());
    const item = target.querySelector('[data-testid="fleet-map-rail-item"]') as HTMLButtonElement;
    item.click();
    flushSync();
    await focused(target);
    expect(focus).toHaveBeenLastCalledWith({ service: 'payments-0', hops: 1 });
    expect(item.getAttribute('aria-current')).toBe('true');
    expect(target.querySelector('nav[aria-label="Where you are"]')!.textContent).toContain('Health check failing: payments-0');
  });

  it('slash moves to Find from the map', async () => {
    const { target, canvas } = await renderMap();
    canvas.focus();
    const e = new KeyboardEvent('keydown', { key: '/', bubbles: true, cancelable: true });
    canvas.dispatchEvent(e);
    expect(e.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(target.querySelector('[data-testid="fleet-map-search"] input[type="search"]'));
  });

  it('hovering a focus edge shows its tip', async () => {
    const { target, canvas } = await renderMap(mapAnswer(), { params: { focus: 'payments-0' } });
    await focused(target);
    const labels = hooks().labels();
    const lab = labels.find((l) => l.text === 'api ^1.0')!;
    ptr(canvas, 'pointermove', 1, lab.x + lab.w / 2, lab.y + lab.h / 2);
    flushSync();
    const tip = target.querySelector('[data-testid="fleet-map-edge-tip"]');
    expect(tip?.textContent).toContain('→');
    expect(tip?.textContent).toContain('Accepts: ^1.0');
  });
});
