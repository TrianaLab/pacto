import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync, tick } from 'svelte';

const { signOutFn, sessionFn } = vi.hoisted(() => ({ signOutFn: vi.fn(), sessionFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { signOut: () => signOutFn(), session: () => sessionFn() } };
});

import { deliverySession } from '../lib/deliverySession.svelte.ts';
// @ts-expect-error — Svelte components have no declaration files
import IdentityBar from './IdentityBar.svelte';
// @ts-expect-error — Svelte components have no declaration files
import SignInScreen from './SignInScreen.svelte';

let app: ReturnType<typeof mount> | null = null;
afterEach(() => {
  if (app) unmount(app);
  app = null;
  document.body.innerHTML = '';
  delete (window as unknown as { __pactoSignIn?: unknown }).__pactoSignIn;
});
beforeEach(() => {
  signOutFn.mockReset(); sessionFn.mockReset();
  deliverySession.error = null; deliverySession.loaded = true;
  location.hash = '#/fleet/services';
});

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- component handle
function show(session: unknown, C: any = IdentityBar) {
  deliverySession.data = session as typeof deliverySession.data;
  app = mount(C, { target: document.body });
  flushSync();
  const c = document.body.cloneNode(true) as Element;
  c.querySelectorAll('[hidden]').forEach((h) => h.remove());
  return c.textContent?.replace(/\s+/g, ' ').trim() ?? '';
}
const bar = () => document.body.querySelector('[data-testid="identity"]');
const human = { user: 'https://idp#carol', class: 'human', groups: ['team-checkout', 'team-orders'] };

describe('IdentityBar', () => {
  it('names the signed-in person by the cluster username, with their groups and Sign out', () => {
    const t = show({ mode: 'shared', login: true, signedIn: true, name: 'Carol Diaz', caller: human });
    expect(t).toContain('Signed in as Carol Diaz https://idp#carol ⧉, in team-checkout, team-orders.');
    expect(bar()?.querySelector('[data-testid="sign-out"]')?.textContent).toBe('Sign out');
    // The verifiable username is printed, never only in a tooltip.
    expect(bar()?.querySelector('[title]:not(.copyable)')).toBeNull();
  });

  it('falls back to the ID token as the cluster maps it when delivery has not asked the apiserver', () => {
    const t = show({ mode: 'shared', login: true, signedIn: true, user: 'https://idp#dave', groups: ['team-shop'] });
    expect(t).toContain('Signed in as dave https://idp#dave ⧉, in team-shop.');
  });

  it('does not name a person from the ID token name alone', () => {
    const t = show({ mode: 'shared', login: true, signedIn: true, name: 'Release Bot', callerError: 'whoami: forbidden' });
    expect(t).not.toContain('Release Bot');
    expect(t).toContain('The cluster did not say who you are: whoami: forbidden');
    deliverySession.data = { mode: 'shared', login: true, signedIn: true } as typeof deliverySession.data;
    flushSync();
    expect(bar()?.textContent).toContain('The cluster did not say who you are.');
  });

  it('says a local dashboard acts as the kubeconfig user', () => {
    expect(show({ mode: 'local', login: false, signedIn: true, caller: human })).toContain('Acting as carol https://idp#carol ⧉ from this machine.');
    unmount(app!); app = null; document.body.innerHTML = '';
    expect(show({ mode: 'local', login: false, signedIn: false })).toContain('Acting as your kubeconfig user from this machine.');
  });

  it('offers sign-in back to this page when signed out', () => {
    expect(show({ mode: 'shared', login: true, signedIn: false })).toContain('You are not signed in.');
    expect(bar()?.querySelector('a')?.getAttribute('href')).toBe('/auth/login?return=%2F%23%2Ffleet%2Fservices');
  });

  it('shows nothing where nobody is identified', () => {
    show({ mode: 'none', login: false, signedIn: false });
    expect(bar()).toBeNull();
    show({ mode: 'shared', login: false, signedIn: false });
    expect(bar()).toBeNull();
    deliverySession.data = null;
    flushSync();
    expect(bar()).toBeNull();
  });

  it('signs out and reads the session again, or goes to the identity provider logout', async () => {
    signOutFn.mockResolvedValue('');
    sessionFn.mockResolvedValue({ mode: 'shared', login: true, signedIn: false });
    show({ mode: 'shared', login: true, signedIn: true, caller: human });
    (bar()?.querySelector('[data-testid="sign-out"]') as HTMLButtonElement).click();
    await vi.waitFor(() => expect(sessionFn).toHaveBeenCalled());
    await vi.waitFor(() => expect(bar()?.textContent).toContain('You are not signed in.'));

    const assign = vi.fn();
    const orig = window.location;
    Object.defineProperty(window, 'location', { configurable: true, value: { ...orig, hash: orig.hash, assign } });
    try {
      signOutFn.mockResolvedValue('https://idp/logout');
      deliverySession.data = { mode: 'shared', login: true, signedIn: true, caller: human } as typeof deliverySession.data;
      flushSync();
      (bar()?.querySelector('[data-testid="sign-out"]') as HTMLButtonElement).click();
      await vi.waitFor(() => expect(assign).toHaveBeenCalledWith('https://idp/logout'));
    } finally {
      Object.defineProperty(window, 'location', { configurable: true, value: orig });
    }
  });

  it('says when signing out failed', async () => {
    signOutFn.mockRejectedValue(new Error('offline'));
    show({ mode: 'shared', login: true, signedIn: true, caller: human });
    (bar()?.querySelector('[data-testid="sign-out"]') as HTMLButtonElement).click();
    await tick(); await Promise.resolve(); await tick();
    await vi.waitFor(() => expect(bar()?.querySelector('[role="alert"]')?.textContent).toBe('Signing out failed: offline'));
  });

  it('switches person where the host signs people in itself (the browser demo)', () => {
    const hook = vi.fn();
    (window as unknown as { __pactoSignIn: unknown }).__pactoSignIn = hook;
    show({ mode: 'shared', login: true, signedIn: true, caller: human });
    const b = bar()?.querySelector('[data-testid="sign-out"]') as HTMLButtonElement;
    expect(b.textContent).toBe('Switch person');
    b.click();
    expect(hook).toHaveBeenCalledWith('#/fleet/services', false);
    expect(signOutFn).not.toHaveBeenCalled();
  });
});

describe('SignInScreen', () => {
  it('shows the product name and one Sign in button that comes back to the page asked for', () => {
    location.hash = '#/fleet/attention';
    const t = show(null, SignInScreen);
    expect(t).toBe('Pacto Sign in');
    const links = document.body.querySelectorAll('a');
    expect(links).toHaveLength(1);
    expect(links[0].getAttribute('href')).toBe('/auth/login?return=%2F%23%2Ffleet%2Fattention');
  });

  it('comes back to the overview from the bare address', () => {
    location.hash = '';
    show(null, SignInScreen);
    expect(document.body.querySelector('a')?.getAttribute('href')).toBe('/auth/login?return=%2F%23%2Ffleet');
  });
});
