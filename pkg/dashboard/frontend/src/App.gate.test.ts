/**
 * The sign-in gate: a dashboard people sign in to shows only its sign-in screen
 * until they do and reads nothing from the fleet; a session that ends brings the
 * screen back; a person whose RBAC reads no namespace is told so with their groups.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount } from 'svelte';

const { fns } = vi.hoisted(() => ({
  fns: { session: vi.fn(), capabilities: vi.fn(), sources: vi.fn(), health: vi.fn(), overview: vi.fn() },
}));
vi.mock('./lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./lib/api.ts')>();
  return {
    ...actual,
    api: {
      session: () => fns.session(),
      capabilities: () => fns.capabilities(),
      sources: () => fns.sources(),
      health: () => fns.health(),
      services: async () => [],
      refresh: async () => ({}),
      fleetOverview: () => fns.overview(),
    },
  };
});

import { session as auth } from './lib/session.svelte.ts';
// @ts-expect-error — Svelte component has no declaration file
import App from './App.svelte';

const meta = { schemaVersion: 'pacto.dev/fleet-product/v1', sources: [], completeness: 'complete' };
let target: HTMLElement;
let app: ReturnType<typeof mount> | null = null;

beforeEach(() => {
  for (const f of Object.values(fns)) f.mockReset();
  auth.data = null; auth.error = null; auth.loaded = false;
  fns.capabilities.mockResolvedValue({ fleet: true, impact: true, auth: { hidden: false } });
  fns.sources.mockResolvedValue({ sources: [], discovering: false });
  fns.health.mockResolvedValue({ version: 'x' });
  fns.overview.mockResolvedValue({ meta, summary: { services: 1 }, attention: { total: 0, count: 0, truncated: false, items: [] },
    recentEvidence: { total: 0, count: 0, truncated: false, items: [] }, entryPoints: [] });
  location.hash = '#/fleet';
  target = document.createElement('div');
  document.body.appendChild(target);
});
afterEach(() => {
  if (app) unmount(app);
  app = null;
  target.remove();
});

describe('App — the sign-in gate', () => {
  it('shows only the sign-in screen to a signed-out person and reads nothing from the fleet', async () => {
    fns.session.mockResolvedValue({ mode: 'shared', login: true, signedIn: false });
    app = mount(App, { target });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="sign-in"]')).not.toBeNull());
    expect(target.querySelector('.navbar')).toBeNull();
    expect(target.querySelector('[data-testid="identity"]')).toBeNull();
    expect(fns.capabilities).not.toHaveBeenCalled();
    expect(fns.sources).not.toHaveBeenCalled();
    expect(fns.overview).not.toHaveBeenCalled();
  });

  it('gates a dashboard where only tokens sign in', async () => {
    fns.session.mockResolvedValue({ mode: 'shared', login: false, tokenLogin: true, signedIn: false });
    app = mount(App, { target });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="token-sign-in"]')).not.toBeNull());
    expect(fns.capabilities).not.toHaveBeenCalled();
  });

  it('shows the fleet and who you are once signed in, and the sign-in screen again when the session ends', async () => {
    fns.session.mockResolvedValue({ mode: 'shared', login: true, signedIn: true, user: 'https://idp#carol', groups: ['team-checkout'] });
    app = mount(App, { target });
    await vi.waitFor(() => expect(target.querySelector('.navbar')).not.toBeNull());
    await vi.waitFor(() => expect(fns.capabilities).toHaveBeenCalled());
    expect(target.querySelector('[data-testid="identity"]')?.textContent).toContain('team-checkout');
    expect(target.querySelector('[data-testid="sign-in"]')).toBeNull();

    fns.session.mockResolvedValue({ mode: 'shared', login: true, signedIn: false });
    window.dispatchEvent(new Event('pacto:unauthorized'));
    await vi.waitFor(() => expect(target.querySelector('[data-testid="sign-in"]')).not.toBeNull());
    expect(target.querySelector('.navbar')).toBeNull();
  });

  it('shows the fleet with no sign-in screen on a local dashboard and where the session cannot be read', async () => {
    fns.session.mockResolvedValue({ mode: 'local', login: false, signedIn: false });
    app = mount(App, { target });
    await vi.waitFor(() => expect(target.querySelector('.navbar')).not.toBeNull());
    expect(target.querySelector('[data-testid="sign-in"]')).toBeNull();
    unmount(app);

    auth.data = null; auth.loaded = false;
    fns.session.mockRejectedValue(new Error('no session here'));
    app = mount(App, { target });
    await vi.waitFor(() => expect(target.querySelector('.navbar')).not.toBeNull());
    expect(target.querySelector('[data-testid="sign-in"]')).toBeNull();
  });

  it('tells a person whose RBAC reads no namespace that they see nothing, with their groups', async () => {
    fns.session.mockResolvedValue({ mode: 'shared', login: true, signedIn: true, user: 'https://idp#erin', name: 'Erin', groups: ['team-a', 'system:authenticated', 'team-b'] });
    fns.capabilities.mockResolvedValue({ fleet: true, impact: true, auth: { hidden: true, nothingVisible: true } });
    app = mount(App, { target });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="nothing-visible"]')).not.toBeNull());
    const t = target.querySelector('[data-testid="nothing-visible"]')?.textContent?.replace(/\s+/g, ' ');
    expect(t).toContain('You can see no services');
    expect(t).toContain('signed in as Erin ⧉, in team-a, team-b.');
    expect(target.querySelector('[data-testid="hidden-by-permissions"]')).toBeNull();

    unmount(app);
    auth.data = null; auth.loaded = false;
    fns.session.mockResolvedValue({ mode: 'shared', login: true, signedIn: true, user: 'https://idp#erin' });
    app = mount(App, { target });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="nothing-visible"]')?.textContent).toContain('in no groups'));
  });
});
