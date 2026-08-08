/**
 * Component tests for FleetOverview.svelte — the operational landing page.
 * Covers acceptance scenarios 1-5: the overview loads from the product contract,
 * partial knowledge with zero attention NEVER shows "All clear", a genuinely
 * healthy complete state MAY, attention items and category tiles navigate to exact
 * destinations, and a degraded source is visible and navigable. `api` is mocked so
 * no network is hit and only /api/fleet/overview is consumed (never the snapshot).
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount } from 'svelte';

const { overviewFn } = vi.hoisted(() => ({ overviewFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { fleetOverview: (...a: unknown[]) => overviewFn(...a) } };
});

// @ts-expect-error — Svelte component has no declaration file
import FleetOverview from './FleetOverview.svelte';

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
function baseOverview(partial = false): any {
  return {
    meta: {
      schemaVersion: 'pacto.dev/fleet-product/v1',
      snapshotId: 'sha256:abc',
      asOf: '2026-07-29T10:00:00Z',
      completeness: partial ? 'partial' : 'complete',
      sources: partial
        ? [{ id: 'oci', kind: 'oci', status: 'available' }, { id: 'k8s', kind: 'k8s', status: 'unavailable' }]
        : [{ id: 'oci', kind: 'oci', status: 'available' }],
    },
    summary: {
      services: 3, servicesNeedingAttention: 0, exactTargetLinks: 4, inferredTargetLinks: 1,
      ambiguousTargetLinks: 0, unresolvedTargetLinks: 0, nonCompliantTargets: 0, unknownTargets: 0,
      staleTargets: 0, unresolvedRelationships: 0, observedOnlyRelationships: 0, recentEvidence: 0,
    },
    attention: { total: 0, count: 0, truncated: false, items: [] },
    recentEvidence: { total: 0, count: 0, truncated: false, items: [] },
    entryPoints: [
      { label: 'Non-compliant deployments', count: 0, view: 'attention', href: '/fleet/attention?category=non-compliant' },
    ],
  };
}

function mountView() {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(FleetOverview, { target, props: { refreshTick: 0 } });
  return { target, component };
}

describe('FleetOverview — operational landing (scenarios 1-5)', () => {
  beforeEach(() => overviewFn.mockReset());

  it('scenario 1: loads the product overview and renders the summary', async () => {
    overviewFn.mockResolvedValue(baseOverview(false));
    const { target, component } = mountView();
    await vi.waitFor(() => {
      expect(target.textContent).toContain('Operational overview');
      expect(target.textContent).toContain('Revision match');
    });
    expect(overviewFn).toHaveBeenCalledTimes(1); // consumes /api/fleet/overview, not the snapshot
    unmount(component); document.body.removeChild(target);
  });

  it('scenario 2: partial source + zero attention NEVER shows "All clear"', async () => {
    overviewFn.mockResolvedValue(baseOverview(true));
    const { target, component } = mountView();
    // Wait for the loaded content (the summary), not the always-present header.
    await vi.waitFor(() => expect(target.querySelector('.op-summary')).toBeTruthy());
    const text = target.textContent || '';
    expect(text).not.toMatch(/all clear/i);
    expect(text).toContain('Source unavailable'); // the incompleteness is shown honestly
    unmount(component); document.body.removeChild(target);
  });

  it('scenario 3: complete + zero attention MAY show an all-clear state', async () => {
    overviewFn.mockResolvedValue(baseOverview(false));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.textContent).toMatch(/all clear/i));
    unmount(component); document.body.removeChild(target);
  });

  it('scenario 4: an attention item navigates to its exact entity; a category tile to its filter', async () => {
    const ov = baseOverview(false);
    ov.summary.servicesNeedingAttention = 1;
    ov.attention = {
      total: 1, count: 1, truncated: false,
      items: [{ entity: { kind: 'target', key: 'prod/k8s/app', label: 'app', href: '/fleet/targets/prod%2Fk8s%2Fapp', status: 'NonCompliant' }, severity: 'error', category: 'non-compliant', summary: 'contract violation', label: 'app' }],
    };
    ov.entryPoints = [{ label: 'Non-compliant deployments', count: 1, view: 'attention', href: '/fleet/attention?category=non-compliant' }];
    overviewFn.mockResolvedValue(ov);
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('.attn-item')).toBeTruthy());
    // attention item -> exact entity href
    const entityLink = target.querySelector('.attn-item a.entity-link') as HTMLAnchorElement;
    expect(entityLink.getAttribute('href')).toBe('#/fleet/targets/prod%2Fk8s%2Fapp');
    // category tile -> exact filtered attention view
    const tile = Array.from(target.querySelectorAll('a.tile')).find((t) => t.textContent?.includes('Non-compliant')) as HTMLAnchorElement;
    expect(tile.getAttribute('href')).toBe('#/fleet/attention?category=non-compliant');
    unmount(component); document.body.removeChild(target);
  });

  it('scenario 5: a degraded source is visible and navigable to its detail', async () => {
    overviewFn.mockResolvedValue(baseOverview(true));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('.source-health')).toBeTruthy());
    const k8s = Array.from(target.querySelectorAll('a.sh-chip')).find((c) => c.textContent?.includes('k8s')) as HTMLAnchorElement;
    expect(k8s).toBeTruthy();
    expect(k8s.getAttribute('href')).toBe('#/fleet/sources/k8s');
    expect(k8s.textContent).toContain('Unavailable');
    unmount(component); document.body.removeChild(target);
  });
});
