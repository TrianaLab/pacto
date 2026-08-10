/**
 * The scoped inventory list (requirement 12). This is the page a bounded preview points
 * at, so the properties that matter are: it pages the SAME bounded Entities endpoint,
 * it scopes by canonical ServiceKey, it never calls a legacy name-based versions API,
 * and it renders the backend's order rather than one of its own.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';

const { entitiesFn } = vi.hoisted(() => ({ entitiesFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { fleetEntities: (...a: unknown[]) => entitiesFn(...a) } };
});

// @ts-expect-error — Svelte component has no declaration file
import FleetEntityListView from './FleetEntityListView.svelte';

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
function listResp(entities: any[], opts: { total?: number; offset?: number; nextOffset?: number; aggregate?: any } = {}): any {
  const total = opts.total ?? entities.length;
  return {
    meta: { schemaVersion: 'pacto.dev/fleet-product/v1', completeness: 'complete', sources: [] },
    total, count: entities.length, offset: opts.offset ?? 0, limit: 25,
    truncated: opts.nextOffset != null, nextOffset: opts.nextOffset, entities,
    // The backend aggregate over the whole filtered population, paging excluded.
    aggregate: opts.aggregate,
  };
}

const rev = (v: string) => ({
  kind: 'revision', key: `domain-a/payments@sha256:${v}`, label: v,
  href: `/fleet/revisions/${encodeURIComponent(`domain-a/payments@sha256:${v}`)}`,
});

function mountView(props: Record<string, unknown> = {}) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(FleetEntityListView, { target, props: { refreshTick: 0, ...props } });
  return { target, component };
}

describe('FleetEntityListView (requirement 12)', () => {
  beforeEach(() => { entitiesFn.mockReset(); location.hash = ''; });

  it('pages the bounded Entities endpoint scoped by canonical ServiceKey', async () => {
    entitiesFn.mockResolvedValue(listResp([rev('a'), rev('b')], { total: 47, nextOffset: 25 }));
    const { target, component } = mountView({ kind: 'revision', service: 'domain-a/payments' });
    await vi.waitFor(() => expect(target.querySelectorAll('.lv-item').length).toBe(2));
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({
      kinds: ['revision'], service: 'domain-a/payments', limit: 25,
    }));
    // The whole point of the page: the capped preview's "of 47" resolves here.
    expect(target.querySelector('[data-testid="entity-list-total"]')?.textContent).toBe('47 contract revisions');
    unmount(component);
  });

  // A revision key is not a service name, and there is no name-based versions endpoint
  // in this UI. The only wire call this view is allowed to make is fleetEntities.
  it('makes no legacy service-name API call', async () => {
    entitiesFn.mockResolvedValue(listResp([rev('a')], { total: 1 }));
    const { target, component } = mountView({ kind: 'revision', service: 'domain-a/payments' });
    await vi.waitFor(() => expect(target.querySelectorAll('.lv-item').length).toBe(1));
    const api = (await import('../lib/api.ts')).api as unknown as Record<string, unknown>;
    expect(Object.keys(api)).toEqual(['fleetEntities']);
    unmount(component);
  });

  it('renders the backend order verbatim rather than sorting by key', async () => {
    entitiesFn.mockResolvedValue(listResp([rev('c'), rev('a'), rev('b')], { total: 3 }));
    const { target, component } = mountView({ kind: 'revision', service: 'domain-a/payments' });
    await vi.waitFor(() => expect(target.querySelectorAll('.lv-item').length).toBe(3));
    const labels = Array.from(target.querySelectorAll('.lv-item')).map((li) => li.textContent?.trim());
    expect(labels[0]).toContain('c');
    expect(labels[1]).toContain('a');
    expect(labels[2]).toContain('b');
    unmount(component);
  });

  it('links back to the scoping service, because that is where the user came from', async () => {
    entitiesFn.mockResolvedValue(listResp([rev('a')], { total: 1 }));
    const { target, component } = mountView({ kind: 'revision', service: 'domain-a/payments' });
    await vi.waitFor(() => expect(target.querySelectorAll('.lv-item').length).toBe(1));
    const crumbs = Array.from(target.querySelectorAll('nav a')).map((a) => a.getAttribute('href'));
    expect(crumbs).toContain('#/fleet/services/domain-a%2Fpayments');
    unmount(component);
  });

  it('pages with the backend nextOffset, and disables Previous on page one', async () => {
    entitiesFn.mockResolvedValue(listResp([rev('a')], { total: 47, offset: 0, nextOffset: 25 }));
    const { target, component } = mountView({ kind: 'revision', service: 'domain-a/payments' });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="entity-list-next"]')).not.toBeNull());
    expect(target.querySelector('[data-testid="entity-list-next"]')?.getAttribute('href'))
      .toBe('#/fleet/revisions?service=domain-a%2Fpayments&offset=25');
    expect(target.querySelector('[data-testid="entity-list-prev"]')).toBeNull();
    expect(target.querySelector('.lv-range')?.textContent).toContain('of 47');
    unmount(component);
  });

  it('walks back a page from a non-zero offset', async () => {
    entitiesFn.mockResolvedValue(listResp([rev('a')], { total: 47, offset: 25 }));
    const { target, component } = mountView({ kind: 'revision', service: 'domain-a/payments', offset: '25' });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="entity-list-prev"]')).not.toBeNull());
    expect(target.querySelector('[data-testid="entity-list-prev"]')?.getAttribute('href'))
      .toBe('#/fleet/revisions?service=domain-a%2Fpayments');
    expect(target.querySelector('[data-testid="entity-list-next"]')).toBeNull();
    unmount(component);
  });

  // Targets are runtime observations, so the page says so and offers the scope filter
  // that only makes sense for them.
  it('speaks the target vocabulary and forwards the scope filter for targets only', async () => {
    entitiesFn.mockResolvedValue(listResp([], { total: 0 }));
    const { target, component } = mountView({ kind: 'target', service: 'domain-a/payments', scope: 'prod' });
    await vi.waitFor(() => expect(entitiesFn).toHaveBeenCalled());
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['target'], scope: 'prod' }));
    expect(target.textContent).toContain('A target is a runtime observation, not a contract.');
    unmount(component);
  });

  it('does not send a scope filter on the revision list, where it has no meaning', async () => {
    entitiesFn.mockResolvedValue(listResp([], { total: 0 }));
    const { component } = mountView({ kind: 'revision', scope: 'prod' });
    await vi.waitFor(() => expect(entitiesFn).toHaveBeenCalled());
    expect(entitiesFn.mock.calls[0][0].scope).toBeUndefined();
    unmount(component);
  });

  it('offers a way out of a filter that matched nothing, instead of a bare empty page', async () => {
    entitiesFn.mockResolvedValue(listResp([], { total: 0 }));
    const { target, component } = mountView({ kind: 'revision', service: 'domain-a/payments', text: 'nope' });
    await vi.waitFor(() => expect(target.querySelector('.lv-list')).toBeNull());
    expect(target.textContent).toContain('nope');
    unmount(component);
  });

  it('surfaces a load failure as an error, never as an empty inventory', async () => {
    entitiesFn.mockRejectedValue(new Error('registry unreachable'));
    const { target, component } = mountView({ kind: 'revision', service: 'domain-a/payments' });
    await vi.waitFor(() => expect(target.textContent).toContain('registry unreachable'));
    expect(target.querySelector('.lv-list')).toBeNull();
    unmount(component);
  });

  it('says the list is partial when the snapshot is, rather than implying completeness', async () => {
    const resp = listResp([rev('a')], { total: 1 });
    resp.meta.completeness = 'partial';
    resp.meta.sources = [{ id: 's1', kind: 'k8s', status: 'degraded' }];
    entitiesFn.mockResolvedValue(resp);
    const { target, component } = mountView({ kind: 'revision' });
    await vi.waitFor(() => expect(target.querySelectorAll('.lv-item').length).toBe(1));
    expect(target.querySelector('.knowledge')?.textContent).toContain('may be incomplete');
    unmount(component);
  });

  it('reloads when the refresh tick advances, keeping the same query', async () => {
    entitiesFn.mockResolvedValue(listResp([rev('a')], { total: 1 }));
    const target = document.createElement('div');
    document.body.appendChild(target);
    const props = $state({ kind: 'revision', service: 'domain-a/payments', refreshTick: 0 });
    const component = mount(FleetEntityListView, { target, props });
    await vi.waitFor(() => expect(entitiesFn).toHaveBeenCalledTimes(1));
    props.refreshTick = 1;
    flushSync();
    await vi.waitFor(() => expect(entitiesFn).toHaveBeenCalledTimes(2));
    expect(entitiesFn.mock.calls[1][0]).toEqual(entitiesFn.mock.calls[0][0]);
    unmount(component);
  });

  /**
   * Readiness is DECLARED BY a contract revision, so the unit of every readiness number
   * on this page is a revision — never a service, a target or "the fleet". A revision
   * passing its own threshold while a target running it is observed non-compliant is a
   * possible, meaningful state, so the two are never drawn as one dimension.
   */
  const READY_AGG = {
    matched: 47,
    readiness: { passing: 20, belowThreshold: 5, expired: 2, notDeclared: 20 },
    targetCompliance: { compliant: 30, nonCompliant: 4, notEvaluated: 13 },
  };

  it('charts declared readiness over the whole matching revision population', async () => {
    entitiesFn.mockResolvedValue(listResp([rev('a')], { total: 47, aggregate: READY_AGG }));
    const { target, component } = mountView({ kind: 'revision' });
    await vi.waitFor(() => expect(target.querySelectorAll('.lv-item').length).toBe(1));
    expect(target.querySelector('.lv-inventory .dist-title')?.textContent).toBe('Contract revision readiness');
    expect(Array.from(target.querySelectorAll('.lv-inventory .dist-item')).map((n) => [
      n.querySelector('.dist-label')?.textContent, n.querySelector('.dist-value')?.textContent,
    ])).toEqual([['Passing', '20'], ['Below its own threshold', '5'], ['Assessment expired', '2'], ['Not assessed', '20']]);
    // 47 revisions, one on screen: the denominator is the population, not the page.
    expect(target.querySelector('.lv-inventory .dist-scope')?.textContent).toBe('All 47 contract revisions in the snapshot.');
    expect(target.querySelector('.lv-inventory .dist-desc')?.textContent).toContain('This is not compliance');
    expect(target.querySelector('.lv-inventory .dist-legend a')?.getAttribute('href'))
      .toBe('#/fleet/revisions?readiness=passing');
    unmount(component);
  });

  it('filters revisions by declared readiness through the backend param', async () => {
    entitiesFn.mockResolvedValue(listResp([rev('a')], { total: 1 }));
    const { target, component } = mountView({ kind: 'revision', service: 'domain-a/payments' });
    await vi.waitFor(() => expect(target.querySelectorAll('.lv-item').length).toBe(1));
    const sel = target.querySelector('select[aria-label="Filter by declared readiness"]') as HTMLSelectElement;
    expect(Array.from(sel.options).map((o) => o.value))
      .toEqual(['', 'passing', 'below-threshold', 'expired', 'not-declared']);
    sel.value = 'expired';
    sel.dispatchEvent(new Event('change', { bubbles: true }));
    expect(location.hash).toBe('#/fleet/revisions?service=domain-a%2Fpayments&readiness=expired');
    unmount(component);
  });

  it('shows the readiness chip in its legend wording, not the wire value', async () => {
    entitiesFn.mockResolvedValue(listResp([rev('a')], { total: 1 }));
    const { target, component } = mountView({ kind: 'revision', readiness: 'below-threshold' });
    await vi.waitFor(() => expect(target.querySelectorAll('.lv-item').length).toBe(1));
    expect(target.querySelector('.chip')?.textContent).toContain('Below its own threshold');
    unmount(component);
  });

  // Readiness is a property of the revision that declares it. Offering the control on a
  // target list would build a query the Entities API rejects with a 422.
  it('never offers or forwards readiness on a target list', async () => {
    entitiesFn.mockResolvedValue(listResp([], { total: 0, aggregate: READY_AGG }));
    const { target, component } = mountView({ kind: 'target', readiness: 'passing' });
    await vi.waitFor(() => expect(entitiesFn).toHaveBeenCalled());
    expect(entitiesFn.mock.calls[0][0].readiness).toBeUndefined();
    expect(target.querySelector('select[aria-label="Filter by declared readiness"]')).toBeNull();
    unmount(component);
  });

  it('charts compliance, never readiness, when the unit is an operational target', async () => {
    entitiesFn.mockResolvedValue(listResp([rev('a')], { total: 47, aggregate: READY_AGG }));
    const { target, component } = mountView({ kind: 'target', service: 'domain-a/payments' });
    await vi.waitFor(() => expect(target.querySelectorAll('.lv-item').length).toBe(1));
    expect(target.querySelector('.lv-inventory .dist-title')?.textContent).toBe('Compliance');
    expect(target.textContent).not.toContain('Contract revision readiness');
    expect(target.querySelector('.lv-inventory .dist-scope')?.textContent)
      .toBe('All 47 operational targets for this service.');
    unmount(component);
  });

  it('uses the singular noun for a one-item inventory', async () => {
    entitiesFn.mockResolvedValue(listResp([rev('a')], { total: 1 }));
    const { target, component } = mountView({ kind: 'revision' });
    await vi.waitFor(() => expect(target.querySelectorAll('.lv-item').length).toBe(1));
    expect(target.querySelector('[data-testid="entity-list-total"]')?.textContent).toBe('1 contract revision');
    unmount(component);
  });
});
