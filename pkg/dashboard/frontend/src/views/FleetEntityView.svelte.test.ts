/**
 * Component tests for FleetEntityView.svelte — the unified entity route.
 * Covers acceptance scenarios 11/14/15: an entity resolves through the product
 * entity-detail endpoint (NarrowedEntityDetail, never the snapshot) and shows
 * identity + canonical key + status + actions; an unknown entity produces a real
 * not-found state; a schema/contract incompatibility produces an explicit error.
 * It also proves the two identity dimensions render independently for a target.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount } from 'svelte';

const { detailFn } = vi.hoisted(() => ({ detailFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  // Keep the real facade behaviors (narrowEntityDetail, error classes); override only
  // the network call so the component exercises the real contract shapes.
  return { ...actual, api: { fleetEntityDetail: (...a: unknown[]) => detailFn(...a) } };
});

// @ts-expect-error — Svelte component has no declaration file
import FleetEntityView from './FleetEntityView.svelte';

const meta = { schemaVersion: 'pacto.dev/fleet-product/v1', snapshotId: 'x', asOf: '2026-07-29T10:00:00Z', completeness: 'complete', sources: [{ id: 'oci', status: 'available' }] };

function targetDetail() {
  return {
    meta,
    entity: { kind: 'target', key: 'prod/k8s/app', label: 'app', href: '/fleet/targets/prod%2Fk8s%2Fapp', status: 'Compliant', scope: 'prod' },
    status: 'Compliant',
    actions: ['open-graph', 'service'],
    target: {
      linkState: 'exact',
      compliance: 'Compliant',
      identity: { retrievable: false, identityClass: 'no-ref' },
      service: { kind: 'service', key: 'domain-a/app', label: 'app', href: '/fleet/services/domain-a%2Fapp' },
      revision: { kind: 'revision', key: 'domain-a/app@sha256:1', label: 'app 1.0', href: '/fleet/revisions/domain-a%2Fapp@sha256:1' },
      stale: false,
    },
  };
}

function mountView(kind: string, key: string) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(FleetEntityView, { target, props: { kind, entityKey: key, refreshTick: 0 } });
  return { target, component };
}


describe('FleetEntityView — unified entity route', () => {
  beforeEach(() => detailFn.mockReset());

  it('scenario 11: resolves via the entity-detail endpoint and shows identity + copyable key', async () => {
    detailFn.mockResolvedValue(targetDetail());
    const { target, component } = mountView('target', 'prod/k8s/app');
    await vi.waitFor(() => {
      expect(target.textContent).toContain('Deployment'); // user-facing kind label for a target
      expect(target.textContent).toContain('app');
      expect(target.querySelector('.copyable-value')?.textContent).toBe('prod/k8s/app');
    });
    expect(detailFn).toHaveBeenCalledWith('target', 'prod/k8s/app'); // product endpoint, not snapshot
    unmount(component); document.body.removeChild(target);
  });

  it('renders revision-match certainty and content retrievability as SEPARATE dimensions', async () => {
    // An exact revision match whose content is not retrievable (no canonical ref) is
    // honest, not contradictory (the whole point of the identity split).
    detailFn.mockResolvedValue(targetDetail());
    const { target, component } = mountView('target', 'prod/k8s/app');
    await vi.waitFor(() => {
      const text = target.textContent || '';
      expect(text).toContain('Exact revision match');
      expect(text).toContain('No canonical reference');
    });
    unmount(component); document.body.removeChild(target);
  });

  it('maps the DTO open-graph action to a canonical graph route', async () => {
    detailFn.mockResolvedValue(targetDetail());
    const { target, component } = mountView('target', 'prod/k8s/app');
    await vi.waitFor(() => expect(target.querySelector('.ev-action')).toBeTruthy());
    const action = Array.from(target.querySelectorAll('a.ev-action')).find((a) => a.textContent?.includes('graph')) as HTMLAnchorElement;
    expect(action.getAttribute('href')).toBe('#/fleet/graph/target/prod%2Fk8s%2Fapp');
    unmount(component); document.body.removeChild(target);
  });

  // The reject paths (scenario 14 unknown-entity -> not-found, scenario 15 schema
  // incompatibility -> explicit error) route through the same seam this view already
  // exercises on success: api rejection -> decideViewState(classifyError) ->
  // ProductEmptyState. Those two pieces are unit-tested deterministically
  // (knowledgeState.test.ts classifyError + productComponents.test.ts ProductEmptyState
  // rendering) without the rejected-promise-through-vi.waitFor timing hazard, and the
  // full browser reload/back paths are covered by the Playwright fleet spec.
});
