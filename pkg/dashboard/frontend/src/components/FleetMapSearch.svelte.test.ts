/** Find on the fleet map: a service combobox whose choice focuses the service. */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import FleetMapSearch from './FleetMapSearch.svelte';
import { api } from '../lib/api';

const svc = (key: string, status: string, over = {}) => ({ kind: 'service', key, label: key, href: '', status, businessDomain: 'payments', owner: 'team-pay', ...over });
let done: () => void = () => {};
afterEach(() => { done(); vi.restoreAllMocks(); });

describe('FleetMapSearch', () => {
  it('search combobox arrows Enter Esc', async () => {
    const spy = vi.spyOn(api, 'fleetEntities').mockResolvedValue({ entities: [svc('payments', 'NonCompliant'), svc('payouts', 'Compliant')], total: 2 } as never);
    const onfocus = vi.fn();
    const target = document.createElement('div');
    document.body.appendChild(target);
    const c = mount(FleetMapSearch, { target, props: { onfocus } });
    done = () => { unmount(c); target.remove(); done = () => {}; };
    const root = target.querySelector('[data-testid="fleet-map-search"]')!;
    const input = root.querySelector('input[type="search"]') as HTMLInputElement;
    expect(input.getAttribute('aria-label')).toBe('Find a service on the map');
    const type = (v: string) => { input.value = v; input.dispatchEvent(new Event('input', { bubbles: true })); };
    const press = (k: string) => input.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true }));
    type('pay');
    await vi.waitFor(() => expect(target.querySelectorAll('[role="option"]')).toHaveLength(2));
    expect(spy).toHaveBeenCalledWith(expect.objectContaining({ text: 'pay', kinds: ['service'] }));
    const first = target.querySelector('[role="option"]')!;
    expect(first.querySelector('mark')?.textContent).toBe('pay');
    expect(first.textContent).toContain('✕');
    expect(first.textContent).toContain('Not compliant');
    expect(first.textContent).toContain('payments · team-pay');
    press('ArrowDown'); press('ArrowDown');
    flushSync();
    expect(input.getAttribute('aria-activedescendant')).toBe('fleet-map-find-opt-1');
    press('Enter');
    flushSync();
    expect(onfocus).toHaveBeenCalledWith('payouts');
    expect(target.querySelector('[role="listbox"]')).toBeNull();
    type('pa');
    await vi.waitFor(() => expect(target.querySelector('[role="listbox"]')).not.toBeNull());
    press('Escape');
    flushSync();
    expect(target.querySelector('[role="listbox"]')).toBeNull();
    expect(onfocus).toHaveBeenCalledTimes(1);
  });
});
