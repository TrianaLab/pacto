/**
 * The legacy dependency graph at fleet scale: past MAX_LAYOUT_NODES it asks for a
 * focus instead of laying everything out (which hung the tab), and the connections
 * table pages and joins dependencies as a sentence.
 */
import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount } from 'svelte';
import { clearFilters } from '../lib/filters.svelte';

const nodes = Array.from({ length: 200 }, (_, i) => ({
  id: `svc-${i}`, serviceName: `svc-${i}`, status: 'Compliant',
  edges: i === 0 ? [{ targetName: 'svc-1', required: true }, { targetName: 'svc-2' }] : [],
}));
vi.mock('../lib/api.ts', () => ({ api: { graph: vi.fn(() => Promise.resolve({ nodes, edges: [] })) } }));

// @ts-expect-error — Svelte component has no declaration file
import GraphPageView from './GraphPageView.svelte';

const flush = () => new Promise((r) => setTimeout(r, 0));
let target: HTMLElement;
afterEach(() => { clearFilters(); target?.remove(); location.hash = ''; });

describe('GraphPageView — a fleet too big to lay out', () => {
  it('asks for a focus, pages the connections and joins dependencies with a space', async () => {
    location.hash = '#/graph';
    target = document.createElement('div');
    document.body.appendChild(target);
    const c = mount(GraphPageView, { target, props: { services: [] } });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="graph-too-big"]')).not.toBeNull());
    expect(target.querySelector('[data-testid="graph-too-big"]')?.textContent).toContain('200 services are too many');
    expect(target.querySelectorAll('table.data-table tbody tr').length).toBe(50);
    expect(target.querySelector('[data-testid="connections-pager-range"]')?.textContent?.replace(/\s+/g, ' ').trim()).toBe('Showing 1–50 of 200 services');
    const deps = target.querySelector('table.data-table tbody tr td:last-child ul')?.textContent?.replace(/\s+/g, ' ');
    expect(deps).toBe('svc-1 req, svc-2');
    await flush();
    unmount(c);
  });
});
