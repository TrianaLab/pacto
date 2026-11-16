import { describe, it, expect, vi, beforeEach } from 'vitest';
import { takeSignInLink, consumeSignInLink, jwtExpired } from './signInLink.ts';

const jwt = (payload: object) => `h.${btoa(JSON.stringify(payload)).replace(/=+$/, '').replace(/\+/g, '-').replace(/\//g, '_')}.s`;
const now = Math.floor(Date.now() / 1000);

// A location and history pair the module can rewrite, like the browser's.
function fake(hash: string, search = '') {
  const calls: string[] = [];
  const loc = { hash, search, pathname: '/', reload: vi.fn() } as unknown as Location & { reload: ReturnType<typeof vi.fn> };
  const hist = {
    state: null,
    replaceState: vi.fn((_s: unknown, _t: string, url: string) => { calls.push(url); loc.hash = ''; }),
  } as unknown as History & { replaceState: ReturnType<typeof vi.fn> };
  const win = new EventTarget() as unknown as Window;
  Object.defineProperty(win, 'sessionStorage', { value: sessionStorage });
  return { loc, hist, win, calls };
}

beforeEach(() => {
  sessionStorage.clear();
  consumeSignInLink();
});

describe('sign-in link', () => {
  it('removes the fragment with replaceState and keeps the token once', () => {
    const f = fake('#token=abc', '?x=1');
    takeSignInLink(f.loc, f.hist, f.win);
    expect(f.calls).toEqual(['/?x=1']);
    expect(consumeSignInLink()).toEqual({ token: 'abc', expired: false });
    expect(consumeSignInLink()).toBeNull();
  });

  it('never reads a token from the query', () => {
    const f = fake('', '?token=abc');
    takeSignInLink(f.loc, f.hist, f.win);
    expect(f.hist.replaceState).not.toHaveBeenCalled();
    expect(consumeSignInLink()).toBeNull();
  });

  it('leaves a route hash alone', () => {
    const f = fake('#/fleet/services');
    takeSignInLink(f.loc, f.hist, f.win);
    expect(f.hist.replaceState).not.toHaveBeenCalled();
    expect(f.loc.hash).toBe('#/fleet/services');
    expect(consumeSignInLink()).toBeNull();
  });

  it('says an expired token is expired', () => {
    const f = fake(`#token=${jwt({ exp: now - 60 })}`);
    takeSignInLink(f.loc, f.hist, f.win);
    expect(consumeSignInLink()?.expired).toBe(true);
    expect(jwtExpired(jwt({ exp: now + 600 }))).toBe(false);
    expect(jwtExpired('not-a-jwt')).toBe(false);
    expect(jwtExpired(jwt({ sub: 'x' }))).toBe(false);
  });

  it('strips a link opened in an open tab, keeps it across the reload and takes it back', () => {
    const f = fake('#/fleet');
    takeSignInLink(f.loc, f.hist, f.win);
    f.loc.hash = '#/fleet/services';
    f.win.dispatchEvent(new Event('hashchange'));
    expect(f.loc.reload).not.toHaveBeenCalled();
    f.loc.hash = '#token=xyz';
    f.win.dispatchEvent(new Event('hashchange'));
    expect(f.calls).toEqual(['/']);
    expect(f.loc.reload).toHaveBeenCalledTimes(1);
    expect(sessionStorage.getItem('pacto.signInLink')).toBe('xyz');
    consumeSignInLink();
    const next = fake('');
    takeSignInLink(next.loc, next.hist, next.win);
    expect(sessionStorage.getItem('pacto.signInLink')).toBeNull();
    expect(consumeSignInLink()).toEqual({ token: 'xyz', expired: false });
  });
});
