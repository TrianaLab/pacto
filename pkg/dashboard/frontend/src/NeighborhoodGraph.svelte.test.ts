/**
 * Component tests for NeighborhoodGraph's refresh strategy:
 * a same-topology refresh that changes only PRESENTATION (node status/label/kind, edge
 * reconciliation state) must restyle the existing canvas in place (patchData) rather
 * than leave it stale, and must NOT relayout; an identical refresh recreates nothing;
 * a topology change under the SAME graph query is reconciled in place (applyTopology),
 * never rebuilt -- rebuilding is what discarded the user's arrangement; and a DIFFERENT
 * graph query gets a new instance with its own saved arrangement, so spatial state can
 * never leak between two different questions. The Cytoscape engine is mocked so the
 * render/patch/reconcile calls are observable without a real canvas.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';

const { renderSpy, patchDataSpy, destroySpy, applyTopologySpy, resetLayoutSpy, restyleSpy } = vi.hoisted(() => ({
  renderSpy: vi.fn(), patchDataSpy: vi.fn(), destroySpy: vi.fn(),
  applyTopologySpy: vi.fn(), resetLayoutSpy: vi.fn(), restyleSpy: vi.fn(),
}));
vi.mock('./lib/graph.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./lib/graph.ts')>();
  return {
    ...actual,
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    renderGraph: (...args: any[]) => {
      // renderSpy can throw (to exercise the render-error path) or return custom controls;
      // otherwise the default controls stand in for the real Cytoscape engine.
      const injected = renderSpy(...args);
      return injected ?? {
        nodes: [], destroy: destroySpy, zoomIn: vi.fn(), zoomOut: vi.fn(), resetView: vi.fn(),
        fit: vi.fn(), patchData: patchDataSpy, applyTopology: applyTopologySpy,
        resetLayout: resetLayoutSpy, spatialState: vi.fn(() => ({ positions: {}, pan: { x: 0, y: 0 }, zoom: 1 })),
        applyFilter: vi.fn(), diagnostics: vi.fn(() => ({})), restyle: restyleSpy,
        applyLegendFilter: vi.fn(), focusNode: vi.fn(),
      };
    },
  };
});

// @ts-expect-error — Svelte component has no declaration file
import NeighborhoodGraph from './NeighborhoodGraph.svelte';
import { GraphRenderError } from './lib/graph.ts';
import { reactiveProps } from './testkit.svelte.ts';
import { toggleTheme } from './lib/theme.svelte.ts';

// eslint-disable-next-line @typescript-eslint/no-explicit-any
const ref = (kind: string, key: string): any => ({ kind, key, label: key });
// eslint-disable-next-line @typescript-eslint/no-explicit-any
function nb(status: string, difference?: string, extraNode = false): any {
  const nodes = [
    { ref: ref('service', 'a'), status },
    { ref: ref('service', 'b'), status: 'Compliant' },
  ];
  if (extraNode) nodes.push({ ref: ref('service', 'c'), status: 'Compliant' });
  return {
    nodes,
    edges: [{ from: ref('service', 'a'), to: ref('service', 'b'), relation: 'dependency', difference }],
  };
}

describe('NeighborhoodGraph — refresh strategy (Part 5)', () => {
  beforeEach(() => {
    for (const f of [renderSpy, patchDataSpy, destroySpy, applyTopologySpy, resetLayoutSpy, restyleSpy]) f.mockReset();
    sessionStorage.clear();
  });

  it('patches in place (no rebuild) when only a node status changes at the same topology', () => {
    const target = document.createElement('div');
    document.body.appendChild(target);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const props: any = reactiveProps({ neighborhood: nb('Compliant'), focusKey: 'a' });
    const component = mount(NeighborhoodGraph, { target, props });
    flushSync();
    expect(renderSpy).toHaveBeenCalledTimes(1);
    // Same topology, changed status -> patch, not a second renderGraph, no destroy.
    props.neighborhood = nb('NonCompliant');
    flushSync();
    expect(patchDataSpy).toHaveBeenCalledTimes(1);
    expect(renderSpy).toHaveBeenCalledTimes(1);
    expect(destroySpy).not.toHaveBeenCalled();
    unmount(component); document.body.removeChild(target);
  });

  it('patches when an edge reconciliation state changes at the same topology', () => {
    const target = document.createElement('div');
    document.body.appendChild(target);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const props: any = reactiveProps({ neighborhood: nb('Compliant', 'matched'), focusKey: 'a' });
    const component = mount(NeighborhoodGraph, { target, props });
    flushSync();
    props.neighborhood = nb('Compliant', 'observed-not-expected');
    flushSync();
    expect(patchDataSpy).toHaveBeenCalledTimes(1);
    expect(renderSpy).toHaveBeenCalledTimes(1);
    unmount(component); document.body.removeChild(target);
  });

  it('does not recreate or patch when the semantic answer is identical', () => {
    const target = document.createElement('div');
    document.body.appendChild(target);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const props: any = reactiveProps({ neighborhood: nb('Compliant', 'matched'), focusKey: 'a' });
    const component = mount(NeighborhoodGraph, { target, props });
    flushSync();
    props.neighborhood = nb('Compliant', 'matched'); // a new object, identical content
    flushSync();
    expect(renderSpy).toHaveBeenCalledTimes(1);
    expect(patchDataSpy).not.toHaveBeenCalled();
    unmount(component); document.body.removeChild(target);
  });

  it('shows an explicit render-error state (never a silent empty canvas) when the visual renderer fails', () => {
    const target = document.createElement('div');
    document.body.appendChild(target);
    // The visual renderer fails in a real browser: renderGraph throws a typed
    // GraphRenderError. The component must surface it, not swallow it into an empty canvas.
    renderSpy.mockImplementationOnce(() => { throw new GraphRenderError('no 2d context'); });
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const props: any = reactiveProps({ neighborhood: nb('Compliant'), focusKey: 'a' });
    const component = mount(NeighborhoodGraph, { target, props });
    flushSync();
    expect(target.querySelector('[data-testid="graph-render-error"]')).toBeTruthy();
    // The empty canvas is hidden rather than pretending to exist.
    expect(target.querySelector('[data-testid="neighborhood-canvas"]')?.hasAttribute('hidden')).toBe(true);
    unmount(component); document.body.removeChild(target);
  });

  it('reconciles a topology change in place, keeping the same instance', () => {
    const target = document.createElement('div');
    document.body.appendChild(target);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const props: any = reactiveProps({ neighborhood: nb('Compliant'), focusKey: 'a', queryKey: 'service|a|service|differences+expected|both|1' });
    const component = mount(NeighborhoodGraph, { target, props });
    flushSync();
    props.neighborhood = nb('Compliant', undefined, true); // extra node -> new topology
    flushSync();
    // Same question, changed answer: the instance survives so the arrangement does.
    expect(applyTopologySpy).toHaveBeenCalledTimes(1);
    expect(renderSpy).toHaveBeenCalledTimes(1);
    expect(destroySpy).not.toHaveBeenCalled();
    unmount(component); document.body.removeChild(target);
  });

  it('a Depth or Direction change reconciles in place -- same subject, same instance', () => {
    // queryKey carries depth/direction/views, so keying the instance on all of it meant
    // every toggle destroyed the canvas and re-ran the layout. Only the subject rebuilds.
    const target = document.createElement('div');
    document.body.appendChild(target);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const props: any = reactiveProps({ neighborhood: nb('Compliant'), focusKey: 'a', queryKey: 'service|a|service|expected|both|1' });
    const component = mount(NeighborhoodGraph, { target, props });
    flushSync();
    props.queryKey = 'service|a|service|expected|both|2'; // depth 1 -> 2
    props.neighborhood = nb('Compliant', undefined, true); // the deeper answer
    flushSync();
    expect(applyTopologySpy).toHaveBeenCalledTimes(1);
    expect(renderSpy).toHaveBeenCalledTimes(1);
    expect(destroySpy).not.toHaveBeenCalled();
    unmount(component); document.body.removeChild(target);
  });

  it('rebuilds for a DIFFERENT SUBJECT, so one graph`s arrangement is never another`s', () => {
    const target = document.createElement('div');
    document.body.appendChild(target);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const props: any = reactiveProps({ neighborhood: nb('Compliant'), focusKey: 'a', queryKey: 'service|a|service|expected|both|1' });
    const component = mount(NeighborhoodGraph, { target, props });
    flushSync();
    props.queryKey = 'service|b|service|expected|both|1';
    props.neighborhood = nb('Compliant', undefined, true);
    flushSync();
    expect(renderSpy).toHaveBeenCalledTimes(2);
    expect(destroySpy).toHaveBeenCalledTimes(1);
    expect(applyTopologySpy).not.toHaveBeenCalled();
    unmount(component); document.body.removeChild(target);
  });
});

describe('NeighborhoodGraph — spatial persistence wiring', () => {
  beforeEach(() => {
    for (const f of [renderSpy, patchDataSpy, destroySpy, applyTopologySpy, resetLayoutSpy, restyleSpy]) f.mockReset();
    sessionStorage.clear();
  });

  it('restores a saved arrangement for the same query and persists changes back under it', () => {
    const key = 'service|a|service|expected|both|1';
    sessionStorage.setItem(`pacto.graph.spatial.v1:${key}`, JSON.stringify({
      v: 1, positions: { a: { x: 10, y: 20 } }, pan: { x: 5, y: 6 }, zoom: 1.5,
    }));
    const target = document.createElement('div');
    document.body.appendChild(target);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const props: any = reactiveProps({ neighborhood: nb('Compliant'), focusKey: 'a', queryKey: key });
    const component = mount(NeighborhoodGraph, { target, props });
    flushSync();
    const opts = renderSpy.mock.calls[0][2];
    expect(opts.savedPositions).toEqual({ a: { x: 10, y: 20 } });
    expect(opts.savedViewport).toEqual({ pan: { x: 5, y: 6 }, zoom: 1.5 });
    // The engine reports a new arrangement; it is written back under the SAME query key.
    opts.onSpatialChange({ positions: { a: { x: 99, y: 99 } }, pan: { x: 1, y: 2 }, zoom: 2 });
    const saved = JSON.parse(sessionStorage.getItem(`pacto.graph.spatial.v1:${key}`) as string);
    expect(saved.positions).toEqual({ a: { x: 99, y: 99 } });
    expect(saved.zoom).toBe(2);
    unmount(component); document.body.removeChild(target);
  });

  it('a corrupt saved entry is ignored rather than left to break the render', () => {
    const key = 'service|a|service|expected|both|1';
    sessionStorage.setItem(`pacto.graph.spatial.v1:${key}`, '{not json');
    const target = document.createElement('div');
    document.body.appendChild(target);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const props: any = reactiveProps({ neighborhood: nb('Compliant'), focusKey: 'a', queryKey: key });
    const component = mount(NeighborhoodGraph, { target, props });
    flushSync();
    const opts = renderSpy.mock.calls[0][2];
    expect(opts.savedPositions).toBeUndefined();
    expect(opts.savedViewport).toBeNull();
    unmount(component); document.body.removeChild(target);
  });

  it('Reset layout forgets the saved arrangement immediately and relayouts (not a fit)', () => {
    const key = 'service|a|service|expected|both|1';
    sessionStorage.setItem(`pacto.graph.spatial.v1:${key}`, JSON.stringify({
      v: 1, positions: { a: { x: 10, y: 20 } }, pan: { x: 0, y: 0 }, zoom: 1,
    }));
    const target = document.createElement('div');
    document.body.appendChild(target);
    let controls: Record<string, () => void> | null = null;
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const props: any = reactiveProps({
      neighborhood: nb('Compliant'), focusKey: 'a', queryKey: key,
      oncontrols: (c: Record<string, () => void> | null) => { controls = c; },
    });
    const component = mount(NeighborhoodGraph, { target, props });
    flushSync();
    controls!.resetLayout();
    expect(resetLayoutSpy).toHaveBeenCalledTimes(1);
    // Forgotten straight away: a reload before the fresh layout settles must not
    // resurrect the arrangement the user just discarded.
    expect(sessionStorage.getItem(`pacto.graph.spatial.v1:${key}`)).toBeNull();
    unmount(component); document.body.removeChild(target);
  });
});

describe('NeighborhoodGraph — a floored fit is announced', () => {
  beforeEach(() => {
    for (const f of [renderSpy, destroySpy]) f.mockReset();
    sessionStorage.clear();
  });

  it('tells the reader the graph is cropped, and takes it back when it is not', () => {
    // A fit clamped at the legibility floor leaves part of the graph off screen, and a
    // cropped canvas looks exactly like a complete one -- so the only thing standing
    // between the reader and a silently partial answer is this line of text.
    const target = document.createElement('div');
    document.body.appendChild(target);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const props: any = reactiveProps({ neighborhood: nb('Compliant'), focusKey: 'a' });
    const component = mount(NeighborhoodGraph, { target, props });
    flushSync();
    expect(target.querySelector('[data-testid="graph-zoom-floored"]')).toBeNull();

    const onReady = renderSpy.mock.calls[0][2].onReady;
    onReady({ headless: false, nodeCount: 2, edgeCount: 1, nodesWithBox: 2, edgesRendered: 1, fitFloored: true });
    flushSync();
    const note = target.querySelector('[data-testid="graph-zoom-floored"]');
    expect(note?.textContent).toMatch(/off screen/i);
    // The acceptance seam carries the same fact as the visible note.
    expect(target.querySelector('[data-testid="neighborhood-canvas"]')?.getAttribute('data-graph-fit-floored')).toBe('true');

    onReady({ headless: false, nodeCount: 2, edgeCount: 1, nodesWithBox: 2, edgesRendered: 1, fitFloored: false });
    flushSync();
    expect(target.querySelector('[data-testid="graph-zoom-floored"]')).toBeNull();
    unmount(component); document.body.removeChild(target);
  });

  it('does not claim a crop on top of a render error -- there is no graph to crop', () => {
    const target = document.createElement('div');
    document.body.appendChild(target);
    renderSpy.mockImplementationOnce(() => { throw new GraphRenderError('no 2d context'); });
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const props: any = reactiveProps({ neighborhood: nb('Compliant'), focusKey: 'a' });
    const component = mount(NeighborhoodGraph, { target, props });
    flushSync();
    expect(target.querySelector('[data-testid="graph-render-error"]')).toBeTruthy();
    expect(target.querySelector('[data-testid="graph-zoom-floored"]')).toBeNull();
    unmount(component); document.body.removeChild(target);
  });
});

describe('NeighborhoodGraph — theme', () => {
  beforeEach(() => {
    for (const f of [renderSpy, destroySpy, restyleSpy]) f.mockReset();
    sessionStorage.clear();
  });

  it('repaints on a theme toggle without rebuilding the graph', () => {
    // Cytoscape resolves every colour from CSS custom properties ONCE, at init. Without
    // this wiring the canvas keeps the old theme's palette on the new page -- and the fix
    // must be a repaint, not a rebuild, or the toggle would also throw away the
    // arrangement and the viewport.
    const target = document.createElement('div');
    document.body.appendChild(target);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const props: any = reactiveProps({ neighborhood: nb('Compliant'), focusKey: 'a' });
    const component = mount(NeighborhoodGraph, { target, props });
    flushSync();
    expect(renderSpy).toHaveBeenCalledTimes(1);
    const before = restyleSpy.mock.calls.length;

    toggleTheme();
    flushSync();

    expect(restyleSpy.mock.calls.length).toBe(before + 1);
    expect(renderSpy).toHaveBeenCalledTimes(1);
    expect(destroySpy).not.toHaveBeenCalled();
    unmount(component); document.body.removeChild(target);
  });
});
