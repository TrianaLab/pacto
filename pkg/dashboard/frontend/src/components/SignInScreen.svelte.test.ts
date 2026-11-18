import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';

const { tokenFn } = vi.hoisted(() => ({ tokenFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { ...actual.api, signInWithToken: (...a: unknown[]) => tokenFn(...a) } };
});

import { session as auth } from '../lib/session.svelte.ts';
import { takeSignInLink, consumeSignInLink } from '../lib/signInLink.ts';
// @ts-expect-error — Svelte components have no declaration files
import SignInScreen from './SignInScreen.svelte';

const jwt = (exp: number) => `h.${btoa(JSON.stringify({ exp })).replace(/=+$/, '')}.s`;
const now = Math.floor(Date.now() / 1000);

let app: ReturnType<typeof mount> | null = null;
const orig = window.location;
let reload: ReturnType<typeof vi.fn>;

beforeEach(() => {
  tokenFn.mockReset();
  consumeSignInLink();
  auth.error = null; auth.loaded = true;
  auth.data = { mode: 'shared', login: false, tokenLogin: true, signedIn: false } as typeof auth.data;
  reload = vi.fn();
  // jsdom cannot navigate; capture where the screen sends the browser.
  Object.defineProperty(window, 'location', { configurable: true, value: { ...orig, pathname: '/', search: '', hash: '', reload } });
});
afterEach(() => {
  if (app) unmount(app);
  app = null;
  document.body.innerHTML = '';
  Object.defineProperty(window, 'location', { configurable: true, value: orig });
});

// A sign-in link as the browser opens it: main.js takes the fragment first.
function openLink(token: string) {
  const hist = { state: null, replaceState: vi.fn() } as unknown as History;
  takeSignInLink({ hash: `#token=${token}`, pathname: '/', search: '' } as Location, hist, window);
}
function show() {
  app = mount(SignInScreen, { target: document.body });
  flushSync();
}
const alertText = () => document.body.querySelector('[role="alert"]')?.textContent;

describe('SignInScreen sign-in link', () => {
  it('says an expired link is expired and posts nothing', () => {
    openLink(jwt(now - 60));
    show();
    expect(document.body.querySelector('[data-testid="sign-in-link-expired"]')?.textContent).toBe('link expired, run the command again');
    expect(tokenFn).not.toHaveBeenCalled();
  });

  it('signs in once with a valid link, saying it came from a link, and opens the fleet', async () => {
    tokenFn.mockResolvedValue(undefined);
    const tok = jwt(now + 600);
    openLink(tok);
    show();
    const push = vi.spyOn(history, 'replaceState');
    await vi.waitFor(() => expect(reload).toHaveBeenCalled());
    expect(push).toHaveBeenCalledWith(null, '', '/#/fleet');
    push.mockRestore();
    expect(tokenFn).toHaveBeenCalledTimes(1);
    expect(tokenFn).toHaveBeenCalledWith(tok, { link: true });
    expect(document.body.querySelector('[data-testid="sign-in-link-expired"]')).toBeNull();
  });

  it('shows the server refusal of a link', async () => {
    tokenFn.mockRejectedValue(new Error('SignInLinkNeedsDashboardToken: a sign-in link carries only a token minted for the dashboard'));
    openLink('person-token');
    show();
    await vi.waitFor(() => expect(alertText()).toContain('SignInLinkNeedsDashboardToken'));
    expect(reload).not.toHaveBeenCalled();
  });

  it('says a link that carried no token carried none and posts nothing', () => {
    openLink('');
    show();
    expect(document.body.querySelector('[data-testid="sign-in-link-notice"]')?.textContent).toBe('the sign-in link carried no token: run the command again');
    expect(tokenFn).not.toHaveBeenCalled();
  });

  it('says a dashboard without token sign-in takes no links, and posts nothing', () => {
    auth.data = { mode: 'shared', login: true, loginName: 'Okta', tokenLogin: false, signedIn: false } as typeof auth.data;
    openLink(jwt(now - 60));
    show();
    expect(document.body.querySelector('[data-testid="sign-in-link-notice"]')?.textContent).toBe('This dashboard takes no sign-in links: sign in with Okta.');
    expect(tokenFn).not.toHaveBeenCalled();
  });

  it('posts a pasted token without the link flag', async () => {
    tokenFn.mockResolvedValue(undefined);
    show();
    expect(tokenFn).not.toHaveBeenCalled();
    const form = document.body.querySelector('[data-testid="token-sign-in"]') as HTMLFormElement;
    const input = form.querySelector('input') as HTMLInputElement;
    input.value = 'pasted'; input.dispatchEvent(new Event('input'));
    flushSync();
    form.requestSubmit();
    await vi.waitFor(() => expect(reload).toHaveBeenCalled());
    expect(tokenFn).toHaveBeenCalledWith('pasted');
    expect(form.querySelector('#signin-token-hint')?.textContent).toContain('--audience pacto-dashboard');
  });
});
