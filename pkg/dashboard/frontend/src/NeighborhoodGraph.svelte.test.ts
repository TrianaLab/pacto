/**
 * Component tests for NeighborhoodGraph's refresh strategy (Part 5): a same-topology
 * refresh that changes only PRESENTATION (node status/label/kind, edge reconciliation
 * state) must restyle the existing canvas in place (patchData) rather than leave it
 * stale, and must NOT relayout; an identical refresh recreates nothing; a topology
 * change rebuilds. The Cytoscape engine is mocked so the render/patch calls are
 * observable without a real canvas.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';

const { renderSpy, patchDataSpy, destroySpy } = vi.hoisted(() => ({
  renderSpy: vi.fn(), patchDataSpy: vi.fn(), destroySpy: vi.fn(),
}));
vi.mock('./lib/graph.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./lib/graph.ts')>();
  return {
    ...actual,
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    renderGraph: (...args: any[]) => {
      renderSpy(...args);
      return { nodes: [], destroy: destroySpy, zoomIn: vi.fn(), zoomOut: vi.fn(), resetView: vi.fn(), fit: vi.fn(), patchData: patchDataSpy, applyFilter: vi.fn() };
    },
  };
});

// @ts-expect-error — Svelte component has no declaration file
import NeighborhoodGraph from './NeighborhoodGraph.svelte';
import { reactiveProps } from './testkit.svelte.ts';

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
  beforeEach(() => { for (const f of [renderSpy, patchDataSpy, destroySpy]) f.mockReset(); });

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

  it('rebuilds when the topology changes (a node is added)', () => {
    const target = document.createElement('div');
    document.body.appendChild(target);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const props: any = reactiveProps({ neighborhood: nb('Compliant'), focusKey: 'a' });
    const component = mount(NeighborhoodGraph, { target, props });
    flushSync();
    props.neighborhood = nb('Compliant', undefined, true); // extra node -> new topology
    flushSync();
    expect(renderSpy).toHaveBeenCalledTimes(2);
    expect(destroySpy).toHaveBeenCalledTimes(1);
    unmount(component); document.body.removeChild(target);
  });
});
