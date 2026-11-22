/** The attention rail beside the fleet map: filters, severity groups, paging and what an item does. */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import FleetMapRail from './FleetMapRail.svelte';
import { api } from '../lib/api';
import { reactiveProps } from '../testkit.svelte';

type Ref = { kind: string; key: string; label: string; href: string; parentService?: string; declaration?: string };
type Item = { entity: Ref; caller?: Ref; service?: string; category: string; severity: string; code: string; summary: string; nextStep?: string; nextAction?: { verb: string; tool?: string; cli?: string } };
const item = (kind: string, key: string, severity = 'error', over: Partial<Item> = {}): Item =>
  ({ entity: { kind, key, label: key, href: `#/fleet/${kind}s/${key}` }, category: 'health', severity, code: `C-${key}`, summary: `${key} summary`, ...over });
const answer = (items: Item[], total = items.length) =>
  ({ items, total, count: items.length, limit: 30, offset: 0, truncated: total > items.length, categories: [], severities: { errors: items.filter((i) => i.severity === 'error').length, warnings: 1, infos: 0, unknown: 0 }, meta: { completeness: 'complete' } }) as never;

let done: () => void = () => {};
beforeEach(() => { location.hash = ''; });
afterEach(() => { done(); vi.restoreAllMocks(); });

function renderRail(props: Record<string, unknown>) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const c = mount(FleetMapRail, { target, props });
  done = () => { unmount(c); target.remove(); done = () => {}; };
  return target;
}

describe('FleetMapRail', () => {
  it('rail filtered line and Clear', async () => {
    const spy = vi.spyOn(api, 'fleetAttention').mockResolvedValue(answer([]));
    const onclear = vi.fn();
    const t = renderRail({ filters: { category: 'health', severity: 'error', lens: 'expected' }, onclear });
    await vi.waitFor(() => expect(spy).toHaveBeenCalled());
    expect(spy).toHaveBeenLastCalledWith({ category: 'health', severity: 'error', limit: 30 });
    await vi.waitFor(() => expect(t.textContent).toContain('Nothing needs attention with these filters.'));
    const line = t.querySelector('[data-testid="fleet-map-rail-filtered"]')!;
    expect(line.textContent).toContain('Filtered: Category Health check failing · Severity Error');
    (line.querySelector('button') as HTMLButtonElement).click();
    expect(onclear).toHaveBeenCalled();
  });

  it('an empty list from an incomplete snapshot is no all-clear', async () => {
    const partial = { ...(answer([]) as object), meta: { completeness: 'partial', sourceCounts: { total: 2, available: 1, partial: 0, stale: 0, unavailable: 1 } } };
    vi.spyOn(api, 'fleetAttention').mockResolvedValue(partial as never);
    const t = renderRail({ filters: { category: 'health' } });
    await vi.waitFor(() => expect(t.querySelector('[data-testid="fleet-map-rail-unknown"]')).toBeTruthy());
    expect(t.textContent).toContain('Nothing found with these filters, but Pacto cannot tell for sure: 1 data source is unavailable.');
    expect(t.textContent).not.toContain('Nothing needs attention');
  });

  it('names an item\'s source and the caller with no contract', async () => {
    vi.spyOn(api, 'fleetAttention').mockResolvedValue(answer([item('service', 'payments', 'warning', { source: 'hubble', caller: { kind: 'service', key: 'k8s:shop/Deployment/cart', label: 'k8s:shop/Deployment/cart', href: '/fleet/services/k8s%3Ashop%252FDeployment%252Fcart' } } as never)]));
    const t = renderRail({});
    await vi.waitFor(() => expect(t.querySelector('[data-testid="attention-caller"]')).toBeTruthy());
    expect(t.querySelector('[data-testid="fleet-map-rail-item"] .ritem-s')?.textContent).toBe('payments summary via hubble');
    // The caller is a link of its own, beside the item: it opens its inferred service.
    const a = t.querySelector('[data-testid="attention-caller"] a') as HTMLAnchorElement;
    expect(a.textContent).toBe('k8s:shop/Deployment/cart');
    expect(a.getAttribute('href')).toBe('#/fleet/services/k8s%3Ashop%252FDeployment%252Fcart');
  });

  it('names a canonical owner by its value and kind in the filtered line', async () => {
    vi.spyOn(api, 'fleetAttention').mockResolvedValue(answer([]));
    const t = renderRail({ filters: { ownerKey: 'team:foundations' } });
    await vi.waitFor(() => expect(t.querySelector('[data-testid="fleet-map-rail-filtered"]')).toBeTruthy());
    expect(t.querySelector('[data-testid="fleet-map-rail-filtered"]')?.textContent).toContain('Filtered: Owner foundations (Team)');
  });

  it('never shows the old list under new filters while the new one loads', async () => {
    let release: (v: never) => void = () => {};
    const spy = vi.spyOn(api, 'fleetAttention').mockResolvedValueOnce(answer([item('service', 'a'), item('service', 'b', 'warning')]))
      .mockReturnValueOnce(new Promise((r) => { release = r; }) as never);
    const props = reactiveProps({ filters: {} as Record<string, string> });
    const t = renderRail(props);
    await vi.waitFor(() => expect(t.querySelectorAll('[data-testid="fleet-map-rail-item"]')).toHaveLength(2));
    props.filters = { severity: 'error' };
    flushSync();
    await vi.waitFor(() => expect(spy).toHaveBeenCalledTimes(2));
    expect(t.querySelectorAll('[data-testid="fleet-map-rail-item"]')).toHaveLength(0);
    release(answer([item('service', 'a')]));
    await vi.waitFor(() => expect(t.querySelectorAll('[data-testid="fleet-map-rail-item"]')).toHaveLength(1));
  });

  it('has no filtered line without filters', async () => {
    vi.spyOn(api, 'fleetAttention').mockResolvedValue(answer([item('service', 'a')]));
    const t = renderRail({});
    await vi.waitFor(() => expect(t.querySelector('[data-testid="fleet-map-rail-item"]')).not.toBeNull());
    expect(t.querySelector('[data-testid="fleet-map-rail-filtered"]')).toBeNull();
  });

  it('links a declare action to the inferred caller\'s Declare panel, outside the item button, and only that one', async () => {
    const caller = { kind: 'service', key: 'default/k8s:shop/Deployment/legacy-billing', label: 'legacy-billing', href: '/fleet/services/x', declaration: 'inferred' };
    const declare = { verb: 'declare', cli: 'pacto declare legacy-billing --namespace shop' };
    vi.spyOn(api, 'fleetAttention').mockResolvedValue(answer([
      item('service', 'ledger', 'error', { caller, nextAction: declare }),
      // No inferred service behind it: the declared provider is no place to declare.
      item('service', 'acme', 'error', { entity: { kind: 'service', key: 'acme', label: 'acme', href: '#/x', declaration: 'declared' }, nextAction: declare }),
    ]));
    const t = renderRail({});
    await vi.waitFor(() => expect(t.querySelectorAll('[data-testid="fleet-map-rail-item"]')).toHaveLength(2));
    const links = [...t.querySelectorAll('[data-testid="next-action-link"]')];
    expect(links).toHaveLength(1);
    expect(links[0].getAttribute('href')).toBe('#/fleet/services/default%2Fk8s%3Ashop%2FDeployment%2Flegacy-billing?declare=1');
    expect(links[0].closest('button')).toBeNull();
    const [, words] = [...t.querySelectorAll('[data-testid="fleet-map-rail-item"]')];
    expect(words.textContent).toContain(`Fix: declare the workload with ${declare.cli}`);
  });

  it('rail item focuses its service', async () => {
    vi.spyOn(api, 'fleetAttention').mockResolvedValue(answer([
      item('service', 'orders'),
      item('target', 'prod/orders', 'warning', { service: 'orders' }),
      item('revision', 'billing@sha', 'warning', { entity: { kind: 'revision', key: 'billing@sha', label: 'billing 2', href: '#/x', parentService: 'billing' } }),
      item('owner', 'team-a', 'info', { nextStep: 'Name an owner', nextAction: { verb: 'declare', cli: 'pacto declare team-a' } }),
    ]));
    const onfocus = vi.fn();
    const t = renderRail({ onfocus });
    await vi.waitFor(() => expect(t.querySelectorAll('[data-testid="fleet-map-rail-item"]')).toHaveLength(4));
    // Grouped by severity, worst first.
    expect([...t.querySelectorAll('h3')].map((h) => h.textContent?.trim())).toEqual(['✕ Error', '▲ Warning', 'i Info']);
    const [svc, tgt, rev, own] = [...t.querySelectorAll('[data-testid="fleet-map-rail-item"]')] as HTMLButtonElement[];
    svc.click(); tgt.click(); rev.click();
    expect(onfocus.mock.calls.map((c) => c[0])).toEqual(['orders', 'orders', 'billing']);
    expect(onfocus.mock.calls[1][1].entity.kind).toBe('target');
    expect(own.textContent).toContain('Next: Name an owner');
    expect(own.textContent).toContain('Fix: declare the workload with pacto declare team-a');
    own.click();
    expect(onfocus).toHaveBeenCalledTimes(3);
    expect(location.hash).toBe('#/fleet/owners/team-a');
  });

  it('shows thirty more and collapses to a counted button', async () => {
    const many = Array.from({ length: 30 }, (_, i) => item('service', `s${i}`));
    const spy = vi.spyOn(api, 'fleetAttention').mockResolvedValue(answer(many, 75));
    const ontoggle = vi.fn();
    const t = renderRail({ ontoggle });
    await vi.waitFor(() => expect(t.textContent).toContain('Show 30 more'));
    expect(t.textContent).toContain('30 items of 75 shown.');
    ([...t.querySelectorAll('button')].find((b) => b.textContent?.includes('Show 30 more')) as HTMLButtonElement).click();
    await vi.waitFor(() => expect(spy).toHaveBeenLastCalledWith({ limit: 60 }));
    ([...t.querySelectorAll('button')].find((b) => b.textContent === 'Hide') as HTMLButtonElement).click();
    expect(ontoggle).toHaveBeenLastCalledWith(false);
    done();
    const closed = renderRail({ open: false, ontoggle });
    await vi.waitFor(() => expect(closed.querySelector('[data-testid="fleet-map-rail-open"]')?.textContent).toContain('Needs attention 31'));
    (closed.querySelector('[data-testid="fleet-map-rail-open"]') as HTMLButtonElement).click();
    expect(ontoggle).toHaveBeenLastCalledWith(true);
  });

  it('says when the list could not load', async () => {
    vi.spyOn(api, 'fleetAttention').mockRejectedValue(new Error('boom'));
    const t = renderRail({});
    await vi.waitFor(() => expect(t.querySelector('[role="alert"]')?.textContent).toContain('boom'));
    flushSync();
  });
});
