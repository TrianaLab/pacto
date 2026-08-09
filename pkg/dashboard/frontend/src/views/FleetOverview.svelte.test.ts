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
      { label: 'Operational targets not compliant', count: 0, view: 'attention', category: 'non-compliant', href: '/fleet/attention?category=non-compliant' },
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
    ov.entryPoints = [{ label: 'Operational targets not compliant', count: 1, view: 'attention', category: 'non-compliant', href: '/fleet/attention?category=non-compliant' }];
    overviewFn.mockResolvedValue(ov);
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('.attn-item')).toBeTruthy());
    // attention item -> exact entity href
    const entityLink = target.querySelector('.attn-item a.entity-link') as HTMLAnchorElement;
    expect(entityLink.getAttribute('href')).toBe('#/fleet/targets/prod%2Fk8s%2Fapp');
    // category tile -> exact filtered attention view
    const tile = Array.from(target.querySelectorAll('a.tile')).find((t) => t.textContent?.includes('not compliant')) as HTMLAnchorElement;
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

describe('FleetOverview — A1: an empty fleet is never "All clear"', () => {
  beforeEach(() => overviewFn.mockReset());

  // eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
  function emptyOverview(partial: boolean): any {
    const ov = baseOverview(partial);
    ov.summary.services = 0;
    ov.summary.exactTargetLinks = 0; ov.summary.inferredTargetLinks = 0;
    ov.summary.ambiguousTargetLinks = 0; ov.summary.unresolvedTargetLinks = 0;
    ov.attention = { total: 0, count: 0, truncated: false, items: [] };
    return ov;
  }

  it('case 1: complete knowledge + zero services renders a genuine empty-fleet state, not all-clear', async () => {
    overviewFn.mockResolvedValue(emptyOverview(false));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('.op-summary')).toBeTruthy());
    const text = target.textContent || '';
    expect(text).not.toMatch(/all clear/i);
    expect(text).not.toMatch(/every operational target is compliant/i);
    expect(target.querySelector('.empty-fleet')).toBeTruthy();
    expect(text).toMatch(/no services tracked/i);
    unmount(component); document.body.removeChild(target);
  });

  it('case 2: incomplete knowledge + zero services never claims health', async () => {
    overviewFn.mockResolvedValue(emptyOverview(true));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('.op-summary')).toBeTruthy());
    const text = target.textContent || '';
    expect(text).not.toMatch(/all clear/i);
    expect(text).not.toMatch(/every operational target is compliant/i);
    expect(target.querySelector('.empty-fleet')).toBeFalsy(); // incomplete: not a confirmed-empty claim either
    expect(target.querySelector('.knowledge-banner')).toBeTruthy();
    unmount(component); document.body.removeChild(target);
  });

  it('case 3: a populated healthy fleet with zero attention MAY show all-clear', async () => {
    overviewFn.mockResolvedValue(baseOverview(false)); // services: 3, targets > 0, complete
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('.all-clear')).toBeTruthy());
    expect(target.textContent).toMatch(/every operational target is compliant/i);
    unmount(component); document.body.removeChild(target);
  });

  it('case 4: a populated but incomplete fleet with zero attention does NOT show all-clear', async () => {
    overviewFn.mockResolvedValue(baseOverview(true)); // services: 3, but a source is unavailable
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('.op-summary')).toBeTruthy());
    expect(target.querySelector('.all-clear')).toBeFalsy();
    expect(target.querySelector('.knowledge-banner')).toBeTruthy();
    unmount(component); document.body.removeChild(target);
  });

  it('requirement D: an `empty`-completeness fleet is honestly empty, NOT "sources degraded"', async () => {
    // Every source healthy, no record: completeness `empty`. This is a confidently
    // empty fleet, not degraded knowledge and not an all-clear health assessment.
    const ov = baseOverview(false);
    ov.meta.completeness = 'empty';
    ov.summary.services = 0;
    ov.summary.exactTargetLinks = 0; ov.summary.inferredTargetLinks = 0;
    overviewFn.mockResolvedValue(ov);
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('.op-summary')).toBeTruthy());
    const text = target.textContent || '';
    // The honest empty-fleet message shows; the degraded-source banner and all-clear do NOT.
    expect(target.querySelector('.empty-fleet')).toBeTruthy();
    expect(target.querySelector('.knowledge-banner')).toBeFalsy();
    expect(target.querySelector('.all-clear')).toBeFalsy();
    expect(text).not.toMatch(/sources are degraded/i);
    expect(text).toMatch(/no services tracked yet/i);
    unmount(component); document.body.removeChild(target);
  });
});
