/**
 * Availability per environment: the state, how Pacto knows it, why, how many targets
 * report and who cannot reach it. A partial environment and hidden providers are said.
 */
import { describe, it, expect } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import OfferingAvailability from './OfferingAvailability.svelte';

const ref = (kind: string, key: string) => ({ kind, key, label: key, href: `/fleet/${kind}s/${encodeURIComponent(key)}` });
const text = (el: Element | null) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim();

function render(props: Record<string, unknown>) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const c = mount(OfferingAvailability, { target, props });
  flushSync();
  return { target, done: () => { unmount(c); target.remove(); } };
}

const availability = {
  state: 'degraded', asOf: '2026-11-04T10:00:00Z', total: 3,
  byEnvironment: [
    { environment: 'production', state: 'available', provenance: 'observed', reason: '2 of 2 targets reporting', reporting: 2, targets: 2, evidenceAt: '2026-11-04T09:00:00Z', providers: [ref('service', 'postgresql')] },
    { environment: 'staging', state: 'degraded', provenance: 'inferred', reason: '1 targets reporting; orders cannot reach it; consumers you may not see are not counted', reporting: 1, targets: 1, partial: true, unreachable: [ref('service', 'orders')] },
    { environment: '', state: 'unknown', provenance: 'unknown', reason: 'no target reports it; a provider outside the observed clusters is not seen', reporting: 0, targets: 0 },
  ],
};

describe('OfferingAvailability', () => {
  it('lists each environment with its state, provenance, reason and reporters', () => {
    const { target, done } = render({ availability });
    expect(target.querySelector('h2')?.textContent).toContain('Availability');
    const rows = Array.from(target.querySelectorAll('[data-testid="availability-env"]'));
    expect(rows.map((r) => r.getAttribute('data-state'))).toEqual(['available', 'degraded', 'unknown']);
    expect(text(rows[0])).toContain('production');
    expect(rows[0].querySelector('.tone-ok')?.textContent).toBe('available');
    expect(text(rows[0])).toContain('observed');
    expect(text(rows[0])).toContain('2 of 2 targets reporting, evidence');
    expect(rows[0].querySelector('a')?.getAttribute('href')).toBe('#/fleet/services/postgresql');
    expect(rows[0].querySelector('bdi.oa-env')).toBeTruthy();
    // Partial: hatched, and the reason says what is not counted, once.
    expect(rows[1].querySelector('.oa-hatch')).toBeTruthy();
    expect(text(rows[1]).match(/consumers you may not see are not counted/g)?.length).toBe(1);
    expect(text(rows[1])).toContain('Cannot reach it: orders');
    expect(text(rows[1])).toContain('1 of 1 target reporting');
    expect(text(rows[2])).toContain('no environment label');
    expect(rows[2].querySelector('.tone-neutral')?.textContent).toBe('unknown');
    expect(target.querySelector('[data-testid="hidden-providers"]')).toBeNull();
    done();
  });

  it('adds the partial note when the reason lacks it, counts hidden providers and the environments past the bound', () => {
    const partial = { ...availability, total: 5, byEnvironment: [{ environment: 'dev', state: 'available', provenance: 'observed', reason: 'ok', reporting: 1, targets: 1, partial: true }] };
    const { target, done } = render({ availability: partial, hiddenProviders: 2 });
    expect(text(target.querySelector('[data-testid="availability-env"]'))).toContain('ok; consumers you may not see are not counted');
    expect(text(target.querySelector('[data-testid="hidden-providers"]'))).toBe('2 providers you may not see');
    expect(text(target)).toContain('+4 more environments');
    done();
  });

  it('says nothing reports it when no environment is listed', () => {
    const { target, done } = render({ availability: { state: 'unknown', asOf: '', byEnvironment: [], total: 0 } });
    expect(text(target)).toContain('No target or consumer reports this offering in any environment, so its availability is unknown.');
    done();
  });

  it('renders a compact provider form without a heading or the provider links', () => {
    const { target, done } = render({ availability, compact: true });
    expect(target.querySelector('h2')).toBeNull();
    expect(text(target)).toMatch(/^Availability production/);
    expect(target.querySelector('[data-testid="availability-env"] a[href="#/fleet/services/postgresql"]')).toBeNull();
    done();
    const empty = render({ availability: { state: 'unknown', asOf: '', byEnvironment: [] }, compact: true });
    expect(text(empty.target)).toBe('Availability unknown no environment reports it');
    empty.done();
    const none = render({ availability: null, compact: true });
    expect(none.target.textContent).toBe('');
    none.done();
  });
});
