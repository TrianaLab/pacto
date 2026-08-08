/**
 * Component tests for DiffView's Compare -> Product Impact canonical identity
 * (requirement A2). A Compare workflow knows a service NAME, which is not a canonical
 * ServiceKey. The impact CTA must resolve the name through the product Entities API
 * and: offer a canonical /fleet/impact/:serviceKey route for a unique match; require
 * explicit disambiguation for same-named services across domains; and NEVER fabricate
 * a route when nothing matches. `api` is mocked.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount } from 'svelte';

const { versionsFn, diffFn, capsFn, entitiesFn } = vi.hoisted(() => ({
  versionsFn: vi.fn(), diffFn: vi.fn(), capsFn: vi.fn(), entitiesFn: vi.fn(),
}));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return {
    ...actual,
    api: {
      versions: (...a: unknown[]) => versionsFn(...a),
      diff: (...a: unknown[]) => diffFn(...a),
      capabilities: (...a: unknown[]) => capsFn(...a),
      fleetEntities: (...a: unknown[]) => entitiesFn(...a),
    },
  };
});

// @ts-expect-error — Svelte component has no declaration file
import DiffView from './DiffView.svelte';

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
const svcRef = (key: string, domain: string): any => ({ kind: 'service', key, label: 'payments', domain, href: `/fleet/services/${encodeURIComponent(key)}` });

function mountView() {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(DiffView, { target, props: { name: 'payments', services: [{ name: 'payments' }] } });
  return { target, component };
}
const impactLinks = (t: HTMLElement) => Array.from(t.querySelectorAll('a')).filter((a) => (a.getAttribute('href') || '').includes('/fleet/impact/'));

describe('DiffView — Compare to Product Impact canonical identity (A2)', () => {
  beforeEach(() => {
    for (const f of [versionsFn, diffFn, capsFn, entitiesFn]) f.mockReset();
    versionsFn.mockResolvedValue([]); // no versions needed for the CTA resolution
    capsFn.mockResolvedValue({ fleet: true });
  });

  it('a unique service name offers a CANONICAL Product Impact route (never a bare name)', async () => {
    entitiesFn.mockResolvedValue({ meta: {}, entities: [svcRef('domain-a/payments', 'domain-a')] });
    const { target, component } = mountView();
    await vi.waitFor(() => expect(impactLinks(target).length).toBe(1));
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['service'], text: 'payments' }));
    expect(impactLinks(target)[0].getAttribute('href')).toBe('#/fleet/impact/domain-a%2Fpayments');
    // the legacy display-name route is never produced
    expect(target.innerHTML).not.toContain('#/impact?svc=payments');
    unmount(component); document.body.removeChild(target);
  });

  it('two same-named services in separate domains require disambiguation, no arbitrary winner', async () => {
    entitiesFn.mockResolvedValue({ meta: {}, entities: [svcRef('domain-a/payments', 'domain-a'), svcRef('domain-b/payments', 'domain-b')] });
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.textContent).toMatch(/multiple services are named/i));
    const hrefs = impactLinks(target).map((a) => a.getAttribute('href'));
    // BOTH canonical domains are offered; neither is silently collapsed to "payments".
    expect(hrefs).toContain('#/fleet/impact/domain-a%2Fpayments');
    expect(hrefs).toContain('#/fleet/impact/domain-b%2Fpayments');
    unmount(component); document.body.removeChild(target);
  });

  it('no matching service NEVER fabricates a Product Impact route', async () => {
    entitiesFn.mockResolvedValue({ meta: {}, entities: [] });
    const { target, component } = mountView();
    // give the resolution a chance to settle, then assert no impact route exists
    await vi.waitFor(() => expect(entitiesFn).toHaveBeenCalled());
    await Promise.resolve();
    expect(impactLinks(target).length).toBe(0);
    unmount(component); document.body.removeChild(target);
  });

  it('a substring-but-not-exact name match is NOT offered as this service impact', async () => {
    // A different service ("payments-legacy") whose name merely contains "payments".
    entitiesFn.mockResolvedValue({ meta: {}, entities: [{ kind: 'service', key: 'domain-a/payments-legacy', label: 'payments-legacy', domain: 'domain-a' }] });
    const { target, component } = mountView();
    await vi.waitFor(() => expect(entitiesFn).toHaveBeenCalled());
    await Promise.resolve();
    expect(impactLinks(target).length).toBe(0);
    unmount(component); document.body.removeChild(target);
  });

  it('on a non-fleet host, no Product Impact CTA is offered and no fleet query is made', async () => {
    capsFn.mockResolvedValue({ fleet: false });
    const { target, component } = mountView();
    await vi.waitFor(() => expect(capsFn).toHaveBeenCalled());
    await Promise.resolve();
    expect(entitiesFn).not.toHaveBeenCalled();
    expect(impactLinks(target).length).toBe(0);
    unmount(component); document.body.removeChild(target);
  });
});
