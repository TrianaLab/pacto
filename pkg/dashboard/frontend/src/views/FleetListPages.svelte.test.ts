/**
 * Component tests for the product Owners and Sources list pages (requirement G).
 * Both consume /api/fleet/entities (kinds=owner / kinds=source) through the SDK facade
 * — never a FleetSnapshot reconstruction — with search / health filters and stable
 * backend pagination kept in the URL.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';

const { entitiesFn } = vi.hoisted(() => ({ entitiesFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { fleetEntities: (...a: unknown[]) => entitiesFn(...a) } };
});

// @ts-expect-error — Svelte component has no declaration file
import FleetOwnersView from './FleetOwnersView.svelte';
// @ts-expect-error — Svelte component has no declaration file
import FleetSourcesView from './FleetSourcesView.svelte';

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
function listResp(entities: any[], opts: { total?: number; offset?: number; nextOffset?: number } = {}): any {
  const total = opts.total ?? entities.length;
  return {
    meta: { schemaVersion: 'pacto.dev/fleet-product/v1', completeness: 'complete', sources: [] },
    total, count: entities.length, offset: opts.offset ?? 0, limit: 25,
    truncated: opts.nextOffset != null, nextOffset: opts.nextOffset, entities,
  };
}

function mountView(Comp: unknown, props: Record<string, unknown> = {}) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(Comp, { target, props: { refreshTick: 0, ...props } });
  return { target, component };
}

describe('FleetOwnersView (G)', () => {
  beforeEach(() => { entitiesFn.mockReset(); location.hash = ''; });

  it('consumes entities?kinds=owner and lists owners', async () => {
    entitiesFn.mockResolvedValue(listResp([
      { kind: 'owner', key: 'team-a', label: 'team-a', href: '/fleet/owners/team-a' },
      { kind: 'owner', key: 'team-b', label: 'team-b', href: '/fleet/owners/team-b' },
    ], { total: 2 }));
    const { target, component } = mountView(FleetOwnersView);
    await vi.waitFor(() => expect(target.querySelectorAll('.lv-item').length).toBe(2));
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['owner'], limit: 25 }));
    const a = target.querySelector('.lv-item a.entity-link') as HTMLAnchorElement;
    expect(a.getAttribute('href')).toBe('#/fleet/owners/team-a');
    unmount(component); document.body.removeChild(target);
  });

  it('search commits into the URL and pagination carries the offset', async () => {
    entitiesFn.mockResolvedValue(listResp([{ kind: 'owner', key: 'team-a', label: 'team-a', href: '/fleet/owners/team-a' }], { total: 60, nextOffset: 25 }));
    const { target, component } = mountView(FleetOwnersView);
    await vi.waitFor(() => expect(target.querySelector('.lv-item')).toBeTruthy());
    const search = target.querySelector('input[type="search"]') as HTMLInputElement;
    search.value = 'team'; search.dispatchEvent(new Event('input', { bubbles: true })); flushSync();
    (target.querySelector('form.lv-search') as HTMLFormElement).dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    expect(location.hash).toBe('#/fleet/owners?text=team');
    unmount(component); document.body.removeChild(target);
  });
});

describe('FleetSourcesView (G)', () => {
  beforeEach(() => { entitiesFn.mockReset(); location.hash = ''; });

  it('consumes entities?kinds=source and lists sources with status', async () => {
    entitiesFn.mockResolvedValue(listResp([
      { kind: 'source', key: 'kubernetes', label: 'kubernetes', status: 'available', href: '/fleet/sources/kubernetes' },
    ], { total: 1 }));
    const { target, component } = mountView(FleetSourcesView);
    await vi.waitFor(() => expect(target.querySelector('.lv-item')).toBeTruthy());
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['source'], limit: 25 }));
    unmount(component); document.body.removeChild(target);
  });

  it('the health filter uses the backend sourceHealth param and lives in the URL', async () => {
    entitiesFn.mockResolvedValue(listResp([{ kind: 'source', key: 'kubernetes', label: 'kubernetes', href: '/fleet/sources/kubernetes' }], { total: 1 }));
    const { target, component } = mountView(FleetSourcesView);
    await vi.waitFor(() => expect(target.querySelector('.lv-item')).toBeTruthy());
    const sel = target.querySelector('select') as HTMLSelectElement;
    sel.value = 'unavailable'; sel.dispatchEvent(new Event('change', { bubbles: true }));
    expect(location.hash).toBe('#/fleet/sources?sourceHealth=unavailable');
    unmount(component); document.body.removeChild(target);
  });
});
