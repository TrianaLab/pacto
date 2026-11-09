/**
 * The top bar (SPEC 20, B2): brand, search, controls and, under 768px, the Menu drawer
 * that carries the same grouped list as the sidebar. A Fleet host (or one whose
 * capabilities are not known yet) has no tabs in the bar; a legacy host keeps its own
 * four items there.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';

// @ts-expect-error — Svelte component has no declaration file
import Navbar from './Navbar.svelte';

type Caps = { fleet: boolean; impact: boolean; delivery?: boolean } | null;

/** The destinations a host offers: the drawer's list on a Fleet host, the bar on a legacy one. */
function navLabels(cap: Caps): string[] {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(Navbar, {
    target,
    props: { services: [], sourcesInfo: [], capabilities: cap, view: 'list', version: 'x' },
  });
  let links = Array.from(target.querySelectorAll('.navbar-nav-desktop .nav-link'));
  if (!links.length) {
    (target.querySelector('.hamburger') as HTMLButtonElement).click();
    flushSync();
    links = Array.from(target.querySelectorAll('#mobile-drawer nav[aria-label="Primary"] a'));
  }
  const labels = links.map((a) => (a.querySelector('.nav-label')?.firstChild?.textContent ?? a.textContent ?? '').trim());
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
    // The mockup's labelled Menu button: its name is its text (SPEC 20).
    expect(hamburger.textContent).toBe('Menu');
    expect(hamburger.hasAttribute('aria-label')).toBe(false);
    expect((target.querySelector('[data-testid="navbar-search"]') as HTMLElement).getAttribute('aria-keyshortcuts')).toBe('Control+K Meta+K');
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
    expect(btn.getAttribute('aria-label')).toBe('Search: open command palette');
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

describe('Navbar — the Pacto brand is the HOME affordance', () => {
  function hrefs(cap: { fleet: boolean; impact: boolean } | null): { brand: string; overview: string | null } {
    const target = document.createElement('div');
    document.body.appendChild(target);
    const component = mount(Navbar, {
      target, props: { services: [], sourcesInfo: [], capabilities: cap, view: 'list', version: 'x' },
    });
    const brand = (target.querySelector('.navbar-brand') as HTMLAnchorElement).getAttribute('href') || '';
    (target.querySelector('.hamburger') as HTMLButtonElement).click();
    flushSync();
    const overview = target.querySelector('#mobile-drawer a[data-nav="overview"]')?.getAttribute('href') ?? null;
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

describe('Navbar — primary destinations (SPEC 20, B2)', () => {
  it('a Fleet host has no tabs in the bar: its destinations are the grouped list', () => {
    const { target, component } = mountNavbar();
    expect(target.querySelector('.navbar-nav-desktop')).toBeNull();
    (target.querySelector('.hamburger') as HTMLButtonElement).click();
    flushSync();
    const groups = Array.from(target.querySelectorAll('#mobile-drawer .nav-group')).map((g) => ({
      label: g.querySelector('.nav-group-h')?.textContent ?? '',
      items: Array.from(g.querySelectorAll('a')).map((a) => a.getAttribute('data-nav')),
    }));
    expect(groups).toEqual([
      { label: '', items: ['overview', 'attention'] },
      { label: 'Organization', items: ['services', 'owners', 'domains', 'offerings', 'golden-paths'] },
      { label: 'Change and delivery', items: ['changes'] },
      { label: 'System', items: ['graph', 'sources'] },
    ]);
    unmount(component); document.body.removeChild(target);
  });

  it('names every destination by its label, in sidebar order', () => {
    expect(navLabels({ fleet: true, impact: true }))
      .toEqual(['Overview', 'Needs attention', 'Services', 'Owners', 'Domains', 'Offerings', 'Golden paths', 'Change analysis', 'Operational graph', 'Data sources']);
  });

  it('offers the same list while capabilities are unknown, so a Fleet host never flashes the legacy bar', () => {
    expect(navLabels(null)).toContain('Operational graph');
  });

  it('keeps the legacy host\'s own four items in the bar', () => {
    expect(navLabels({ fleet: false, impact: false })).toEqual(['Services', 'Owners', 'Readiness', 'Compare']);
  });

  it('lights the legacy item of the current view', () => {
    const target = document.createElement('div');
    document.body.appendChild(target);
    const component = mount(Navbar, { target, props: { services: [], sourcesInfo: [], capabilities: { fleet: false, impact: false }, view: 'readiness', version: 'x' } });
    const cur = Array.from(target.querySelectorAll('.navbar-nav-desktop [aria-current="page"]')).map((a) => a.textContent?.trim());
    expect(cur).toEqual(['Readiness']);
    unmount(component); document.body.removeChild(target);
  });

  it('the legacy drawer lists the same four items and closes on a link', () => {
    const target = document.createElement('div');
    document.body.appendChild(target);
    const component = mount(Navbar, { target, props: { services: [], sourcesInfo: [], capabilities: { fleet: false, impact: false }, view: 'list', version: 'x' } });
    (target.querySelector('.hamburger') as HTMLButtonElement).click();
    flushSync();
    const links = Array.from(target.querySelectorAll('#mobile-drawer .mobile-nav-link')) as HTMLAnchorElement[];
    expect(links.map((a) => a.textContent?.trim())).toEqual(['Services', 'Owners', 'Readiness', 'Compare']);
    links[1].click();
    flushSync();
    expect(target.querySelector('#mobile-drawer')).toBeNull();
    unmount(component); document.body.removeChild(target);
  });

  it('the Fleet drawer closes when a destination is followed', () => {
    const { target, component } = mountNavbar();
    (target.querySelector('.hamburger') as HTMLButtonElement).click();
    flushSync();
    (target.querySelector('#mobile-drawer a[data-nav="owners"]') as HTMLAnchorElement).click();
    flushSync();
    expect(target.querySelector('#mobile-drawer')).toBeNull();
    unmount(component); document.body.removeChild(target);
  });
});

describe('Navbar — delivery is opt-in', () => {
  it('shows Delivery only once the host confirms it', () => {
    expect(navLabels(null)).not.toContain('Delivery');
    expect(navLabels({ fleet: true, impact: true })).not.toContain('Delivery');
    expect(navLabels({ fleet: true, impact: true, delivery: false })).not.toContain('Delivery');
    expect(navLabels({ fleet: true, impact: true, delivery: true })).toContain('Delivery');
  });
});

describe('Navbar — data sources', () => {
  function mountWith(props: Record<string, unknown>) {
    const target = document.createElement('div');
    document.body.appendChild(target);
    const component = mount(Navbar, { target, props: { services: [], capabilities: { fleet: false, impact: false }, view: 'list', version: 'x', ...props } });
    return { target, done: () => { unmount(component); document.body.removeChild(target); } };
  }

  it('a legacy host groups every source behind one Data sources control, so the search keeps its room', () => {
    const { target, done } = mountWith({ sourcesInfo: [{ type: 'kubernetes', enabled: true }, { type: 'oci', enabled: true }, { type: 'local', enabled: false }] });
    const trigger = target.querySelector('[data-testid="navbar-sources"]') as HTMLButtonElement;
    expect(trigger.textContent?.trim()).toBe('Data sources');
    expect(trigger.getAttribute('popovertarget')).toBe('navbar-sources');
    const panel = target.querySelector('#navbar-sources') as HTMLElement;
    expect(panel.hasAttribute('popover')).toBe(true);
    expect(Array.from(panel.querySelectorAll('li strong')).map((n) => n.textContent)).toEqual(['Kubernetes', 'OCI Registry']);
    expect(panel.querySelector('li .sources-pop-desc')?.textContent).toBe('live cluster runtime data');
    done();
  });

  // The sidebar carries source health on a Fleet host; the control comes back only
  // where there is no sidebar.
  it('a Fleet host has no sources control in the bar', () => {
    const { target, done } = mountWith({ capabilities: { fleet: true, impact: true }, sourcesInfo: [{ type: 'kubernetes', enabled: true }] });
    expect(target.querySelector('[data-testid="navbar-sources"]')).toBeNull();
    done();
  });

  it('closes the sources popover on any navigation', () => {
    const { target, done } = mountWith({ sourcesInfo: [{ type: 'kubernetes', enabled: true }] });
    const panel = target.querySelector('#navbar-sources') as HTMLElement & { hidePopover?: () => void };
    let hidden = 0;
    panel.hidePopover = () => { hidden++; };
    window.dispatchEvent(new HashChangeEvent('hashchange'));
    expect(hidden).toBe(1);
    done();
  });

  it('says it is still discovering, and offers no control when there is nothing to list', () => {
    let m = mountWith({ sourcesInfo: [], discovering: true });
    expect(m.target.querySelector('[data-testid="navbar-sources"]')?.textContent).toContain('discovering');
    expect(m.target.querySelector('#navbar-sources')?.textContent).toContain('Still discovering');
    m.done();
    m = mountWith({ sourcesInfo: [] });
    expect(m.target.querySelector('[data-testid="navbar-sources"]')).toBeNull();
    m.done();
  });
});
