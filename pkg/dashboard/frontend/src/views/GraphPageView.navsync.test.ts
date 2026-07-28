/**
 * Regression test: back/forward navigation must not be reverted by the filter
 * sync effects.
 *
 * GraphPageView mirrors the shared filter store into local StatsBar bindables with
 * a pair of effects per filter. Previously both directions tracked both values, so
 * when the store changed externally (back/forward nav) the local->store effect
 * re-ran with a stale local value and wrote it back, clobbering the navigation. The
 * effects now untrack their comparison target so each reacts only to its own source.
 *
 * The graph canvas (Cytoscape) needs getContext, unavailable in jsdom, so api.graph
 * returns an empty graph — the empty-state branch renders (no GraphPanel) while the
 * filter-sync effects still run.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount } from 'svelte';
import { clearFilters, getFilters, setFilter } from '../lib/filters.svelte';

vi.mock('../lib/api.ts', () => ({
  api: { graph: vi.fn().mockResolvedValue({ nodes: [], edges: [] }) },
}));

// @ts-expect-error — Svelte component has no declaration file
import GraphPageView from './GraphPageView.svelte';

const flush = () => new Promise(resolve => setTimeout(resolve, 0));

describe('GraphPageView — back/forward nav does not revert filters', () => {
  let target: HTMLElement;

  beforeEach(() => {
    clearFilters();
    location.hash = '#/graph';
    target = document.createElement('div');
    document.body.appendChild(target);
  });

  afterEach(() => {
    clearFilters();
    document.body.removeChild(target);
  });

  it('keeps an externally-cleared search filter cleared', async () => {
    const component = mount(GraphPageView, { target, props: { services: [] } });
    await flush();

    // A search filter is active (mirrored into the local StatsBar bindable).
    setFilter('search', 'foo');
    await flush();
    expect(getFilters().search).toBe('foo');

    // Simulate back/forward navigation clearing the filter in the store.
    setFilter('search', '');
    await flush();

    // The local->store effect must NOT push the stale 'foo' back into the store.
    expect(getFilters().search).toBe('');

    unmount(component);
  });
});
