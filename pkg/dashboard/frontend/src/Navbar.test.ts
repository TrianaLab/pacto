/**
 * Navbar capability gating (§12): the navbar must not expose a capability the
 * running host has not registered. The Operational Graph tab requires the fleet
 * capability; it shows when unknown (null) or enabled, and hides when disabled.
 */
import { describe, it, expect } from 'vitest';
import { mount, unmount } from 'svelte';

// @ts-expect-error — Svelte component has no declaration file
import Navbar from './Navbar.svelte';

function navLabels(cap: { fleet: boolean; impact: boolean } | null): string[] {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(Navbar, {
    target,
    props: { services: [], sourcesInfo: [], capabilities: cap, view: 'list', version: 'x' },
  });
  const labels = Array.from(target.querySelectorAll('.navbar-nav-desktop .nav-link')).map((a) => a.textContent?.trim() || '');
  unmount(component);
  document.body.removeChild(target);
  return labels;
}

describe('Navbar — capability gating', () => {
  it('shows the Operational Graph tab when capabilities are unknown (null)', () => {
    expect(navLabels(null)).toContain('Operational Graph');
  });

  it('shows the Operational Graph tab when the fleet capability is enabled', () => {
    expect(navLabels({ fleet: true, impact: true })).toContain('Operational Graph');
  });

  it('hides the Operational Graph tab when the host has no fleet capability', () => {
    const labels = navLabels({ fleet: false, impact: false });
    expect(labels).not.toContain('Operational Graph');
    // Capability-free tabs remain.
    expect(labels).toEqual(expect.arrayContaining(['Services', 'Owners', 'Readiness', 'Compare']));
  });
});
