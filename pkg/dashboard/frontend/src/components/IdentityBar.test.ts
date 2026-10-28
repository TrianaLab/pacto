import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync, tick } from 'svelte';

const { signOutFn, sessionFn } = vi.hoisted(() => ({ signOutFn: vi.fn(), sessionFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  const { sessionApi } = await import('../testkit.svelte.ts');
  return { ...actual, api: { signOut: () => signOutFn(), ...sessionApi(() => sessionFn(), actual.ApiError) } };
});

import { session as auth } from '../lib/session.svelte.ts';
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
  auth.error = null; auth.loaded = true;
  location.hash = '#/fleet/services';
});

// The store holds the session and delivery's caller apart; a fixture names both.
function setSession(fixture: unknown) {
  const { caller = null, ...data } = (fixture ?? {}) as Record<string, unknown>;
  const c = (caller ?? {}) as { user?: string; groups?: string[] };
  auth.data = fixture ? ({ user: c.user, groups: c.groups, ...data } as typeof auth.data) : null;
  auth.caller = caller as typeof auth.caller;
}
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- component handle
function show(session: unknown, C: any = IdentityBar) {
  setSession(session);
  app = mount(C, { target: document.body });
  flushSync();
  const c = document.body.cloneNode(true) as Element;
  c.querySelectorAll('[hidden]').forEach((h) => h.remove());
  // The word joiners that glue punctuation to the box before it are tested on their own.
  return c.textContent?.replace(/\u2060/g, '').replace(/\s+/g, ' ').trim() ?? '';
}
const bar = () => document.body.querySelector('[data-testid="identity"]');
const human = { user: 'https://idp#carol', class: 'human', groups: ['team-checkout', 'system:authenticated', 'team-orders'] };

describe('IdentityBar', () => {
  it('names the signed-in person by the cluster username, with their groups and Sign out', () => {
    const t = show({ mode: 'shared', login: true, signedIn: true, name: 'Carol Diaz', caller: human });
    expect(t).toContain('Signed in as Carol Diaz ⧉, in team-checkout, team-orders.');
    expect(bar()?.querySelector('[data-testid="sign-out"]')?.textContent).toBe('Sign out');
    // The username is in the copy button's tooltip, not printed next to the name.
    expect(bar()?.querySelector('.copyable')?.getAttribute('title')).toBe('https://idp#carol');
  });

  it('keeps the comma and the full stop on the line of the username chip before them', () => {
    show({ mode: 'shared', login: true, signedIn: true, name: 'Carol Diaz', caller: human });
    expect(bar()?.textContent).toContain('⧉\u2060, in ');
    unmount(app!); app = null; document.body.innerHTML = '';
    show({ mode: 'shared', login: true, signedIn: true, name: 'Carol Diaz', caller: { ...human, groups: [] } });
    expect(bar()?.textContent).toContain('⧉\u2060.');
  });

  it('says a person signed in with a token', () => {
    const t = show({ mode: 'shared', login: false, tokenLogin: true, signedIn: true, method: 'token', user: 'system:serviceaccount:shop:viewer' });
    expect(t).toContain('Signed in with a token as shop/viewer.');
    unmount(app!); app = null; document.body.innerHTML = '';
    show({ mode: 'shared', login: false, tokenLogin: true, signedIn: false });
    expect(bar()?.textContent).toContain('You are not signed in.');
    expect(bar()?.querySelector('a')).toBeNull();
  });

  it('names the person the session names, whatever delivery says', () => {
    const t = show({ mode: 'shared', login: true, signedIn: true, user: 'https://idp#dave', groups: ['team-shop'], caller: human });
    expect(t).toContain('Signed in as dave ⧉, in team-shop.');
    expect(t).not.toContain('carol');
  });

  it('does not name a person from the ID token name alone', () => {
    const t = show({ mode: 'shared', login: true, signedIn: true, name: 'Release Bot' });
    expect(t).not.toContain('Release Bot');
    expect(t).toContain('The cluster did not say who you are.');
  });

  it('says a local dashboard acts as the kubeconfig user', () => {
    expect(show({ mode: 'local', login: false, signedIn: true, caller: human })).toContain('Acting as carol ⧉ from this machine.');
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
    setSession(null);
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
      setSession({ mode: 'shared', login: true, signedIn: true, caller: human });
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

  it('names the person on the header button and shows the details in place in the phone menu', () => {
    show({ mode: 'shared', login: true, signedIn: true, name: 'Carol Diaz', caller: human });
    const trigger = document.body.querySelector('[data-testid="identity-trigger"]') as HTMLElement;
    expect(trigger.getAttribute('aria-label')).toBe('Who you are: Carol Diaz');
    expect(trigger.getAttribute('popovertarget')).toBe('navbar-identity');
    expect(document.body.querySelector('#navbar-identity')?.hasAttribute('popover')).toBe(true);
    unmount(app!); app = null; document.body.innerHTML = '';
    setSession({ mode: 'shared', login: true, signedIn: true, caller: human });
    app = mount(IdentityBar, { target: document.body, props: { drawer: true } });
    flushSync();
    const panel = document.body.querySelector('[data-testid="identity-drawer"]') as HTMLElement;
    expect(panel.textContent).toContain('Signed in as carol');
    expect(panel.querySelector('[popover]')).toBeNull();
    expect(bar()).toBeNull();
  });

  it('labels the header button for a local dashboard and a signed-out person', () => {
    show({ mode: 'local', login: false, signedIn: false });
    expect(document.body.querySelector('[data-testid="identity-trigger"]')?.getAttribute('aria-label')).toBe('Who you are: This machine');
    unmount(app!); app = null; document.body.innerHTML = '';
    show({ mode: 'shared', login: true, signedIn: false });
    expect(document.body.querySelector('[data-testid="identity-trigger"]')?.getAttribute('aria-label')).toBe('Who you are: Signed out');
    unmount(app!); app = null; document.body.innerHTML = '';
    show({ mode: 'shared', login: true, signedIn: true });
    expect(document.body.querySelector('[data-testid="identity-trigger"]')?.getAttribute('aria-label')).toBe('Who you are: Signed in');
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

  it('sends every Sign in link through the host that signs people in itself, and leaves a plain link alone', () => {
    show({ mode: 'shared', login: true, signedIn: false });
    const link = bar()?.querySelector('a') as HTMLAnchorElement;
    const plain = new MouseEvent('click', { bubbles: true, cancelable: true });
    link.dispatchEvent(plain);
    expect(plain.defaultPrevented).toBe(false);
    const hook = vi.fn();
    (window as unknown as { __pactoSignIn: unknown }).__pactoSignIn = hook;
    const hosted = new MouseEvent('click', { bubbles: true, cancelable: true });
    link.dispatchEvent(hosted);
    expect(hosted.defaultPrevented).toBe(true);
    expect(hook).toHaveBeenCalledWith('#/fleet/services', false);
  });
});

describe('SignInScreen', () => {
  it('takes a Kubernetes token where token sign-in is on, and says why the cluster refused one', async () => {
    const tokenFn = vi.fn();
    const { api } = await import('../lib/api.ts');
    (api as unknown as { signInWithToken: unknown }).signInWithToken = tokenFn;
    tokenFn.mockRejectedValueOnce(new Error('the cluster did not accept this token: Unauthorized'));
    show({ mode: 'shared', login: false, tokenLogin: true, signedIn: false }, SignInScreen);
    // The only link is the docs one: no identity provider to sign in with.
    expect([...document.body.querySelectorAll('a')].map((a) => a.textContent)).toEqual(['How to get a token']);
    const form = document.body.querySelector('[data-testid="token-sign-in"]') as HTMLFormElement;
    const input = form.querySelector('input') as HTMLInputElement;
    expect(input.type).toBe('password');
    input.value = 'tok'; input.dispatchEvent(new Event('input'));
    flushSync();
    form.requestSubmit();
    await vi.waitFor(() => expect(form.querySelector('[role="alert"]')?.textContent).toBe('the cluster did not accept this token: Unauthorized'));
    expect(tokenFn).toHaveBeenCalledWith('tok');

    const reload = vi.fn();
    const orig = window.location;
    Object.defineProperty(window, 'location', { configurable: true, value: { ...orig, hash: orig.hash, reload } });
    try {
      tokenFn.mockResolvedValueOnce(undefined);
      form.requestSubmit();
      await vi.waitFor(() => expect(reload).toHaveBeenCalled());
    } finally {
      Object.defineProperty(window, 'location', { configurable: true, value: orig });
    }
  });

  it('offers both where both sign-ins are on', () => {
    show({ mode: 'shared', login: true, tokenLogin: true, signedIn: false }, SignInScreen);
    expect([...document.body.querySelectorAll('a')].map((a) => a.textContent)).toEqual(['Sign in with OIDC', 'How to get a token']);
    expect(document.body.querySelector('[data-testid="token-sign-in"]')).not.toBeNull();
  });

  it('names the identity provider on the button when the dashboard is told its name', () => {
    show({ mode: 'shared', login: true, loginName: 'Keycloak', signedIn: false }, SignInScreen);
    expect(document.body.querySelector('a')?.textContent).toBe('Sign in with Keycloak');
  });

  it('says how to get a token next to the field, with a link to the docs', () => {
    show({ mode: 'shared', login: false, tokenLogin: true, signedIn: false }, SignInScreen);
    const input = document.body.querySelector('#signin-token') as HTMLInputElement;
    // Accessible name from the label, description from the hint.
    expect(document.body.querySelector('label[for="signin-token"]')?.textContent).toBe('Kubernetes token');
    const hint = document.getElementById(input.getAttribute('aria-describedby')!);
    expect(hint?.textContent).toContain('kubectl create token <service-account> -n <namespace>');
    const link = hint?.querySelector('a') as HTMLAnchorElement;
    expect(link.textContent).toBe('How to get a token');
    expect(link.getAttribute('href')).toBe('https://pacto.run/latest/integrations/kubernetes/dashboard-authentication/#token-sign-in');
    expect(link.getAttribute('target')).toBe('_blank');
    expect(link.getAttribute('rel')).toBe('noopener noreferrer');
  });

  it('shows the product name and one Sign in with OIDC button that comes back to the page asked for', () => {
    location.hash = '#/fleet/attention';
    const t = show(null, SignInScreen);
    expect(t).toBe('Pacto Sign in with OIDC');
    const links = document.body.querySelectorAll('a');
    expect(links).toHaveLength(1);
    expect(links[0].getAttribute('href')).toBe('/auth/login?return=%2F%23%2Ffleet%2Fattention');
  });

  it('follows the page asked for while it waits', () => {
    location.hash = '#/fleet/attention';
    show(null, SignInScreen);
    location.hash = '#/delivery/inbox';
    window.dispatchEvent(new HashChangeEvent('hashchange'));
    flushSync();
    expect(document.body.querySelector('a')?.getAttribute('href')).toBe('/auth/login?return=%2F%23%2Fdelivery%2Finbox');
  });

  it('comes back to the overview from the bare address', () => {
    location.hash = '';
    show(null, SignInScreen);
    expect(document.body.querySelector('a')?.getAttribute('href')).toBe('/auth/login?return=%2F%23%2Ffleet');
  });
});
