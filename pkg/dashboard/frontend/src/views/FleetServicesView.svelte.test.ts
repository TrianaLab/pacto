/**
 * Component tests for FleetServicesView.svelte — the product Services list (C / A3).
 * The list consumes /api/fleet/entities?kinds=service through the SDK facade (never
 * the legacy /api/services list or a FleetSnapshot reconstruction), shows
 * domain-qualified identity, keeps filters and the page offset in the URL, pages
 * through the backend metadata, and distinguishes filtered-empty / empty-fleet /
 * incomplete-knowledge states. `api` is mocked so only /api/fleet/entities is used.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';

const { entitiesFn } = vi.hoisted(() => ({ entitiesFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { fleetEntities: (...a: unknown[]) => entitiesFn(...a) } };
});

// @ts-expect-error — Svelte component has no declaration file
import FleetServicesView from './FleetServicesView.svelte';

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
function listResp(entities: any[], opts: { total?: number; offset?: number; nextOffset?: number; partial?: boolean } = {}): any {
  const total = opts.total ?? entities.length;
  const offset = opts.offset ?? 0;
  return {
    meta: {
      schemaVersion: 'pacto.dev/fleet-product/v1',
      completeness: opts.partial ? 'partial' : 'complete',
      sources: opts.partial ? [{ id: 'k8s', kind: 'k8s', status: 'unavailable' }] : [],
    },
    total, count: entities.length, offset, limit: 25,
    truncated: opts.nextOffset != null, nextOffset: opts.nextOffset, entities,
  };
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
function svc(domain: string): any {
  return { kind: 'service', key: `${domain}/payments`, label: 'payments', domain, href: `/fleet/services/${encodeURIComponent(`${domain}/payments`)}`, status: 'Compliant' };
}

function mountView(props: Record<string, unknown> = {}) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(FleetServicesView, { target, props: { refreshTick: 0, ...props } });
  return { target, component };
}

const rows = (t: HTMLElement) => Array.from(t.querySelectorAll('.sv-item'));

describe('FleetServicesView — product Services list (C / A3)', () => {
  beforeEach(() => { entitiesFn.mockReset(); location.hash = ''; });

  it('consumes /api/fleet/entities with kinds=service and shows domain-qualified identity', async () => {
    entitiesFn.mockResolvedValue(listResp([svc('domain-a'), svc('domain-b')], { total: 2 }));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(rows(target).length).toBe(2));
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['service'], limit: 25 }));
    // same-named services stay distinguishable by domain
    expect(rows(target)[0].textContent).toContain('domain domain-a');
    expect(rows(target)[1].textContent).toContain('domain domain-b');
    unmount(component); document.body.removeChild(target);
  });

  it('rows navigate through the canonical ProductRef href', async () => {
    entitiesFn.mockResolvedValue(listResp([svc('domain-a')], { total: 1 }));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(rows(target).length).toBe(1));
    const a = rows(target)[0].querySelector('a.entity-link') as HTMLAnchorElement;
    expect(a.getAttribute('href')).toBe('#/fleet/services/domain-a%2Fpayments');
    unmount(component); document.body.removeChild(target);
  });

  it('the status filter uses the backend query param and lives in the URL (resetting the page)', async () => {
    entitiesFn.mockResolvedValue(listResp([svc('domain-a')], { total: 1, offset: 25 }));
    const { target, component } = mountView({ offset: '25' });
    await vi.waitFor(() => expect(rows(target).length).toBe(1));
    const sel = target.querySelector('select') as HTMLSelectElement;
    sel.value = 'NonCompliant';
    sel.dispatchEvent(new Event('change', { bubbles: true }));
    expect(location.hash).toBe('#/fleet/services?status=NonCompliant'); // offset reset to page 1
    unmount(component); document.body.removeChild(target);
  });

  it('the owner filter navigates with the backend query param', async () => {
    entitiesFn.mockResolvedValue(listResp([svc('domain-a')], { total: 1 }));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(rows(target).length).toBe(1));
    const ownerInput = target.querySelector('input[aria-label="Filter by owner"]') as HTMLInputElement;
    ownerInput.value = 'team-a';
    ownerInput.dispatchEvent(new Event('change', { bubbles: true }));
    expect(location.hash).toBe('#/fleet/services?owner=team-a');
    unmount(component); document.body.removeChild(target);
  });

  it('the search box commits on submit into the URL', async () => {
    entitiesFn.mockResolvedValue(listResp([svc('domain-a')], { total: 1 }));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(rows(target).length).toBe(1));
    const search = target.querySelector('input[type="search"]') as HTMLInputElement;
    search.value = 'pay';
    search.dispatchEvent(new Event('input', { bubbles: true })); // sync bind:value
    flushSync();
    (target.querySelector('form.sv-search') as HTMLFormElement).dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    expect(location.hash).toBe('#/fleet/services?text=pay');
    unmount(component); document.body.removeChild(target);
  });

  it('paginates through the backend metadata (Next carries the next offset)', async () => {
    entitiesFn.mockResolvedValue(listResp([svc('domain-a')], { total: 60, offset: 0, nextOffset: 25 }));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('.sv-range')?.textContent).toBe('Showing 1–1 of 60'));
    expect((target.querySelector('[data-testid="svc-next"]') as HTMLAnchorElement).getAttribute('href')).toBe('#/fleet/services?offset=25');
    expect(target.querySelector('[data-testid="svc-prev"]')).toBeNull();
    unmount(component); document.body.removeChild(target);
  });

  it('distinguishes filtered-empty from empty-fleet from incomplete-knowledge', async () => {
    // filtered-empty
    entitiesFn.mockResolvedValue(listResp([], { total: 0 }));
    let m = mountView({ status: 'NonCompliant' });
    await vi.waitFor(() => expect(m.target.textContent).toMatch(/no matching services/i));
    expect(m.target.querySelector('.ps-btn')?.textContent).toMatch(/clear filters/i);
    unmount(m.component); document.body.removeChild(m.target);

    // empty-fleet (no filter, complete knowledge)
    entitiesFn.mockResolvedValue(listResp([], { total: 0 }));
    m = mountView();
    await vi.waitFor(() => expect(m.target.textContent).toMatch(/no services yet/i));
    expect(m.target.textContent).not.toMatch(/knowledge is incomplete/i);
    unmount(m.component); document.body.removeChild(m.target);

    // incomplete knowledge (no filter, a source unavailable)
    entitiesFn.mockResolvedValue(listResp([], { total: 0, partial: true }));
    m = mountView();
    await vi.waitFor(() => expect(m.target.textContent).toMatch(/no services known/i));
    expect(m.target.textContent).toMatch(/knowledge is incomplete/i);
    unmount(m.component); document.body.removeChild(m.target);
  });

  it('issues exactly ONE initial request (no onMount + effect double-fire) [requirement E]', async () => {
    entitiesFn.mockResolvedValue(listResp([svc('domain-a')], { total: 1 }));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(rows(target).length).toBe(1));
    // Let any further scheduled effects settle, then assert a single backend call.
    flushSync();
    await Promise.resolve();
    expect(entitiesFn).toHaveBeenCalledTimes(1);
    unmount(component); document.body.removeChild(target);
  });

  it('filtered-empty UNDER incomplete knowledge shows BOTH facts, never hiding either [requirement D]', async () => {
    entitiesFn.mockResolvedValue(listResp([], { total: 0, partial: true }));
    const { target, component } = mountView({ status: 'NonCompliant' });
    await vi.waitFor(() => expect(target.textContent).toMatch(/no matching services/i));
    const text = target.textContent || '';
    expect(text).toMatch(/no matching services/i);       // the filter matched nothing
    expect(text).toMatch(/this list may be incomplete/i); // AND knowledge is incomplete
    unmount(component); document.body.removeChild(target);
  });

  it('Clear filters returns to the unfiltered first page', async () => {
    entitiesFn.mockResolvedValue(listResp([svc('domain-a')], { total: 1 }));
    const { target, component } = mountView({ owner: 'team-a', status: 'NonCompliant', offset: '25' });
    await vi.waitFor(() => expect(target.querySelector('.chip')).toBeTruthy());
    (target.querySelector('.chip-clear') as HTMLButtonElement).click();
    expect(location.hash).toBe('#/fleet/services');
    unmount(component); document.body.removeChild(target);
  });
});
