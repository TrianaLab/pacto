/**
 * Data sources' Clusters block (514-S2): one chip per cluster with its status word and
 * the reason in its HelpTip, shown only with two or more clusters or an access mark.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount } from 'svelte';

const { entitiesFn } = vi.hoisted(() => ({ entitiesFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { fleetEntities: (...a: unknown[]) => entitiesFn(...a), fleetProviders: () => Promise.resolve({ providers: [] }) } };
});

// @ts-expect-error — Svelte component has no declaration file
import FleetSourcesView from './FleetSourcesView.svelte';

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
function resp(clusters?: any[]): any {
  return {
    meta: { schemaVersion: 'pacto.dev/fleet-product/v1', completeness: 'complete', sources: [], clusters },
    total: 1, count: 1, offset: 0, limit: 25, truncated: false,
    entities: [{ kind: 'source', key: 'k8s', label: 'k8s', href: '/fleet/sources/k8s' }],
  };
}

async function render(clusters?: unknown[]) {
  entitiesFn.mockResolvedValue(resp(clusters));
  const target = document.createElement('div');
  document.body.appendChild(target);
  const c = mount(FleetSourcesView, { target, props: { refreshTick: 0 } });
  await vi.waitFor(() => expect(target.querySelector('[data-testid="source-list"]')).not.toBeNull());
  return { target, done: () => { unmount(c); target.remove(); } };
}

describe('FleetSourcesView Clusters block', () => {
  beforeEach(() => entitiesFn.mockReset());

  it('is hidden with one cluster and no access mark', async () => {
    const { target, done } = await render([{ id: 'uid-eu', name: 'eu-prod', status: 'available', targets: 2 }]);
    expect(target.querySelector('[data-testid="source-clusters"]')).toBeNull();
    done();
  });

  it('lists every cluster with its word when there are two', async () => {
    const { target, done } = await render([
      { id: 'uid-eu', name: 'eu-prod', status: 'stale', targets: 2 },
      { id: 'uid-us', name: 'us-prod', status: 'available', access: 'review-failed', targets: 0 },
    ]);
    const chips = Array.from(target.querySelectorAll('[data-testid="source-cluster"]'));
    expect(chips.map((c) => c.querySelector('.sc-word')?.textContent)).toEqual(['stale', 'review failed']);
    expect(chips[1].querySelector('bdi')?.getAttribute('title')).toBe('us-prod');
    done();
  });

  it('carries the reason in the HelpTip', async () => {
    const { target, done } = await render([{ id: 'uid-us', name: 'us-prod', status: 'available', access: 'no-access', targets: 0 }]);
    const tip = target.querySelector('[data-testid="source-cluster"] [role="note"]');
    expect(tip?.textContent).toBe('You may read no namespace in this cluster, so its services are hidden.');
    done();
  });
});
