/**
 * Navbar capability gating (section 12): the navbar must not expose a capability the
 * running host has not registered. The Operational Graph tab requires the fleet
 * capability; it shows when unknown (null) or enabled, and hides when disabled.
 */
import { describe, it, expect, vi } from 'vitest';
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

function mountNavbar() {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(Navbar, {
    target,
    props: { services: [], sourcesInfo: [], capabilities: { fleet: true, impact: true }, view: 'list', version: 'x' },
  });
  return { target, component };
}

describe('Navbar — mobile drawer accessibility', () => {
  it('hidden drawer navigation is absent from the DOM (and a11y tree) until opened', () => {
    const { target, component } = mountNavbar();
    expect(target.querySelector('#mobile-drawer')).toBeNull();
    const hamburger = target.querySelector('.hamburger') as HTMLButtonElement;
    expect(hamburger.getAttribute('aria-expanded')).toBe('false');
    expect(hamburger.getAttribute('aria-controls')).toBe('mobile-drawer');
    unmount(component);
    document.body.removeChild(target);
  });

  it('opening sets aria-expanded and renders the drawer; Escape closes it and restores focus', async () => {
    const { target, component } = mountNavbar();
    const hamburger = target.querySelector('.hamburger') as HTMLButtonElement;
    hamburger.click();
    await Promise.resolve();
    const drawer = target.querySelector('#mobile-drawer') as HTMLElement;
    expect(drawer).not.toBeNull();
    expect(hamburger.getAttribute('aria-expanded')).toBe('true');
    // No incorrect role on the container; navigation is a real landmark inside.
    expect(drawer.getAttribute('role')).toBeNull();
    expect(drawer.querySelector('nav[aria-label="Primary"]')).not.toBeNull();

    drawer.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await vi.waitFor(() => {
      expect(target.querySelector('#mobile-drawer')).toBeNull();
      expect(document.activeElement).toBe(hamburger); // focus restored
    });
    unmount(component);
    document.body.removeChild(target);
  });
});

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
