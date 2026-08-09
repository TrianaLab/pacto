/**
 * Navbar capability gating (section 12): the navbar must not expose a capability the
 * running host has not registered. The Operational graph tab requires the fleet
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

describe('Navbar — search affordance communicates the actual action (A6)', () => {
  function mountWithSearch(fleetSearch: boolean) {
    const target = document.createElement('div');
    document.body.appendChild(target);
    const clicks: number[] = [];
    const component = mount(Navbar, {
      target,
      props: {
        services: [], sourcesInfo: [], capabilities: { fleet: true, impact: true }, view: 'list',
        version: 'x', fleetSearch, onOpenSearch: () => clicks.push(1),
      },
    });
    const btn = target.querySelector('[data-testid="navbar-search"]') as HTMLButtonElement;
    return { target, component, btn, clicks };
  }

  // "Search the fleet" named an internal package. The affordance now names what it
  // actually searches, which is also what a first-time user would type.
  it('names the things it searches, with the "/" hint, when fleet-capable', () => {
    const { target, component, btn } = mountWithSearch(true);
    expect(btn.getAttribute('aria-label')).toBe('Search services, revisions and targets');
    expect(btn.textContent).not.toMatch(/fleet/i);
    expect(btn.querySelector('.search-kbd')?.textContent).toBe('/');
    unmount(component); document.body.removeChild(target);
  });

  it('labels the visible affordance as the command palette with the Cmd/Ctrl-K hint otherwise', () => {
    const { target, component, btn } = mountWithSearch(false);
    expect(btn.getAttribute('aria-label')).toBe('Open command palette');
    expect(btn.querySelector('.search-kbd')?.textContent).toMatch(/K$/);
    unmount(component); document.body.removeChild(target);
  });

  it('the visible affordance invokes onOpenSearch', () => {
    const { target, component, btn, clicks } = mountWithSearch(true);
    btn.click();
    expect(clicks.length).toBe(1);
    unmount(component); document.body.removeChild(target);
  });
});

describe('Navbar — the Pacto brand is the HOME affordance (Phase-2 IA residual)', () => {
  function hrefs(cap: { fleet: boolean; impact: boolean } | null): { brand: string; overview: string | null } {
    const target = document.createElement('div');
    document.body.appendChild(target);
    const component = mount(Navbar, {
      target, props: { services: [], sourcesInfo: [], capabilities: cap, view: 'list', version: 'x' },
    });
    const brand = (target.querySelector('.navbar-brand') as HTMLAnchorElement).getAttribute('href') || '';
    const overview = Array.from(target.querySelectorAll('.navbar-nav-desktop .nav-link'))
      .find((a) => a.textContent?.trim() === 'Overview')?.getAttribute('href') ?? null;
    unmount(component); document.body.removeChild(target);
    return { brand, overview };
  }

  it('points the brand to the canonical fleet Overview when fleet-capable', () => {
    expect(hrefs({ fleet: true, impact: true }).brand).toBe('#/fleet');
  });

  it('points the brand to the legacy landing when fleet is explicitly unavailable', () => {
    expect(hrefs({ fleet: false, impact: false }).brand).toBe('#/');
  });

  it('keeps the brand on the legacy landing (never a dead route) while capabilities are unresolved', () => {
    expect(hrefs(null).brand).toBe('#/');
  });

  it('brand and the Overview nav item agree on the canonical home for a fleet-capable host', () => {
    const { brand, overview } = hrefs({ fleet: true, impact: true });
    expect(overview).toBe('#/fleet');
    expect(brand).toBe(overview);
  });

  it('never returns a fleet-capable user to the legacy landing via the logo', () => {
    expect(hrefs({ fleet: true, impact: true }).brand).not.toBe('#/');
  });
});

describe('Navbar — four primary workflows (product IA)', () => {
  // state -> inventory -> relationships -> change. Owners, data sources, needs-attention
  // and readiness are DIMENSIONS of those workflows, reachable from the overview, the
  // entity pages and the command palette, so they are deliberately not primary tabs.
  it('a fleet host has exactly the four primary destinations, in order', () => {
    expect(navLabels({ fleet: true, impact: true }))
      .toEqual(['Overview', 'Services', 'Operational graph', 'Change analysis']);
  });

  it('does not promote a dimension to a primary destination', () => {
    const labels = navLabels({ fleet: true, impact: true });
    for (const dimension of ['Owners', 'Readiness', 'Compare', 'Needs attention', 'Data sources']) {
      expect(labels).not.toContain(dimension);
    }
  });

  it('the mobile drawer offers the same destinations in the same order as the desktop nav', async () => {
    const { target, component } = mountNavbar();
    (target.querySelector('.hamburger') as HTMLButtonElement).click();
    await Promise.resolve();
    const desktop = Array.from(target.querySelectorAll('.navbar-nav-desktop .nav-link')).map((a) => a.textContent?.trim());
    const mobile = Array.from(target.querySelectorAll('.mobile-nav-link')).map((a) => a.textContent?.trim());
    expect(mobile).toEqual(desktop);
    unmount(component); document.body.removeChild(target);
  });
});

describe('Navbar — capability gating', () => {
  it('shows the Operational graph tab when capabilities are unknown (null)', () => {
    expect(navLabels(null)).toContain('Operational graph');
  });

  it('shows the Operational graph tab when the fleet capability is enabled', () => {
    expect(navLabels({ fleet: true, impact: true })).toContain('Operational graph');
  });

  it('hides the Operational graph tab when the host has no fleet capability', () => {
    const labels = navLabels({ fleet: false, impact: false });
    expect(labels).not.toContain('Operational graph');
    // Capability-free tabs remain.
    expect(labels).toEqual(expect.arrayContaining(['Services', 'Owners', 'Readiness', 'Compare']));
  });
});

describe('Navbar — the active tab agrees with the breadcrumb trail', () => {
  // Every entity page shares one view id, so without the kind the nav lit "Services"
  // while the trail underneath read "Overview > Data sources > edge-cluster".
  function activeLabel(view: string, entityKind = ''): string | null {
    const target = document.createElement('div');
    document.body.appendChild(target);
    const component = mount(Navbar, {
      target,
      props: { services: [], sourcesInfo: [], capabilities: { fleet: true, impact: true }, view, entityKind, version: 'x' },
    });
    const active = Array.from(target.querySelectorAll('.navbar-nav-desktop .nav-link'))
      .filter((a) => a.getAttribute('aria-current') === 'page')
      .map((a) => a.textContent?.trim() || '');
    unmount(component); document.body.removeChild(target);
    expect(active.length).toBeLessThanOrEqual(1); // never two tabs claiming the page
    return active[0] ?? null;
  }

  it.each([
    ['service', 'Services'],
    ['revision', 'Services'],
    ['target', 'Services'],
    ['owner', 'Overview'],
    ['source', 'Overview'],
  ])('lights %s entity pages under %s', (kind, label) => {
    expect(activeLabel('fleet-entity', kind)).toBe(label);
  });

  it.each([
    ['fleet-services', 'Services'],
    ['fleet-owners', 'Overview'],
    ['fleet-sources', 'Overview'],
    ['fleet-attention', 'Overview'],
    ['fleet', 'Operational graph'],
    ['changes', 'Change analysis'],
  ])('lights the %s list view under %s', (view, label) => {
    expect(activeLabel(view)).toBe(label);
  });
});
