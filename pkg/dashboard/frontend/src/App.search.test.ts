/**
 * A6 integration: the primary search affordance opens the global fleet EntitySearch
 * on fleet-capable hosts and the command palette otherwise; Cmd/Ctrl-K always opens
 * the command palette; and a non-fleet host never opens a dead fleet search. `api` is
 * mocked (spreading the real module so its typed error classes remain) and the route
 * is the fleet overview so only lightweight children mount.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount } from 'svelte';

const { caps } = vi.hoisted(() => ({ caps: { value: { fleet: true, impact: true } as { fleet: boolean; impact: boolean } } }));
vi.mock('./lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./lib/api.ts')>();
  const overview = {
    meta: { schemaVersion: 'pacto.dev/fleet-product/v1', sources: [], completeness: 'complete' },
    summary: { services: 1, servicesNeedingAttention: 0, exactTargetLinks: 1 },
    attention: { total: 0, count: 0, truncated: false, items: [] },
    recentEvidence: { total: 0, count: 0, truncated: false, items: [] },
    entryPoints: [],
  };
  return {
    ...actual,
    api: {
      sources: async () => ({ sources: [] }),
      health: async () => ({ version: 'x' }),
      capabilities: async () => caps.value,
      services: async () => [],
      refresh: async () => ({}),
      fleetOverview: async () => overview,
      fleetEntities: async () => ({ meta: { schemaVersion: 'pacto.dev/fleet-product/v1' }, total: 0, count: 0, entities: [] }),
    },
  };
});

// @ts-expect-error — Svelte component has no declaration file
import App from './App.svelte';

function mountApp() {
  location.hash = '#/fleet';
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(App, { target });
  return { target, component };
}

const esOpen = (t: HTMLElement) => !!t.querySelector('.es-panel');
const cpOpen = (t: HTMLElement) => !!t.querySelector('.cp-panel');
const cmdK = () => window.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', metaKey: true, bubbles: true }));
const slash = () => window.dispatchEvent(new KeyboardEvent('keydown', { key: '/', bubbles: true }));

describe('App — primary search affordance (A6)', () => {
  beforeEach(() => { caps.value = { fleet: true, impact: true }; location.hash = ''; });
  afterEach(() => { location.hash = ''; });

  it('fleet-capable: the visible Search button and "/" open the fleet EntitySearch; Cmd/Ctrl-K opens the palette', async () => {
    caps.value = { fleet: true, impact: true };
    const { target, component } = mountApp();
    // Wait for capabilities to resolve so the affordance knows it is fleet search ("/").
    await vi.waitFor(() => expect((target.querySelector('.search-kbd') as HTMLElement)?.textContent).toBe('/'));

    (target.querySelector('[data-testid="navbar-search"]') as HTMLButtonElement).click();
    await vi.waitFor(() => expect(esOpen(target)).toBe(true));
    expect(cpOpen(target)).toBe(false);
    (target.querySelector('.es-panel input') as HTMLElement)?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await vi.waitFor(() => expect(esOpen(target)).toBe(false));

    slash();
    await vi.waitFor(() => expect(esOpen(target)).toBe(true));
    (target.querySelector('.es-panel input') as HTMLElement)?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await vi.waitFor(() => expect(esOpen(target)).toBe(false));

    cmdK();
    await vi.waitFor(() => expect(cpOpen(target)).toBe(true));
    expect(esOpen(target)).toBe(false);

    unmount(component); document.body.removeChild(target);
  });

  it('non-fleet host: the visible Search and "/" open the command palette, never a dead fleet search', async () => {
    caps.value = { fleet: false, impact: false };
    const { target, component } = mountApp();
    await vi.waitFor(() => expect((target.querySelector('.search-kbd') as HTMLElement)?.textContent).toMatch(/K$/));

    (target.querySelector('[data-testid="navbar-search"]') as HTMLButtonElement).click();
    await vi.waitFor(() => expect(cpOpen(target)).toBe(true));
    expect(esOpen(target)).toBe(false);
    (target.querySelector('.cp-panel input') as HTMLElement)?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await vi.waitFor(() => expect(cpOpen(target)).toBe(false));

    slash();
    await vi.waitFor(() => expect(cpOpen(target)).toBe(true));
    expect(esOpen(target)).toBe(false); // never a dead fleet search

    unmount(component); document.body.removeChild(target);
  });
});
