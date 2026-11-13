/** Provisioning badges: an unsigned Ready reads as unsigned, never as a green Ready. */
import { describe, it, expect, afterEach } from 'vitest';
import { mount, unmount } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import ProvisioningBadges from './ProvisioningBadges.svelte';

let cleanup: (() => void) | null = null;
afterEach(() => { cleanup?.(); cleanup = null; });
function render(statuses: unknown[]) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const c = mount(ProvisioningBadges, { target, props: { statuses } });
  cleanup = () => { unmount(c); target.remove(); };
  return target;
}
const ready = { phase: 'Ready', provenance: 'observed', environment: 'production', source: 'targetstate', asOf: '2026-11-12T10:00:00Z' };

describe('ProvisioningBadges', () => {
  it('draws an unsigned Ready with the info tone, no check and the words inline', () => {
    const el = render([{ ...ready, verified: false }]);
    const badge = el.querySelector('[data-testid="provisioning-badge"]')!;
    expect(badge.textContent).toContain('production: Ready (unsigned)');
    expect(badge.querySelector('svg')!.getAttribute('class')).toContain('pb-info');
    expect(badge.querySelector('.pb-ok')).toBeNull();
  });
  it('draws a signed Ready as ok', () => {
    const el = render([{ ...ready, verified: true, source: 'evidence' }]);
    const badge = el.querySelector('[data-testid="provisioning-badge"]')!;
    expect(badge.textContent).toContain('production: Ready');
    expect(badge.textContent).not.toContain('unsigned');
    expect(badge.querySelector('svg')!.getAttribute('class')).toContain('pb-ok');
  });
});
