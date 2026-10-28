import { describe, it, expect, vi } from 'vitest';

const fns = vi.hoisted(() => ({ session: vi.fn(), caller: vi.fn() }));
vi.mock('./api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./api.ts')>();
  return { ...actual, api: { session: () => fns.session(), deliveryCaller: () => fns.caller() } };
});

import { ApiError } from './api.ts';
import { session, loadSession, loginUrl, signIn, hostSignsIn, personLabel, teamGroups } from './session.svelte.ts';

describe('loadSession', () => {
  it('reads the session once for concurrent callers, and who delivery says a signed-in caller is', async () => {
    fns.session.mockResolvedValue({ mode: 'shared', login: true, signedIn: true, user: 'u' });
    fns.caller.mockResolvedValue({ user: 'u', class: 'human', requireOwnerTeam: true });
    await Promise.all([loadSession(), loadSession()]);
    expect(fns.session).toHaveBeenCalledTimes(1);
    expect([session.loaded, session.data?.user, session.caller?.class, session.callerError]).toEqual([true, 'u', 'human', '']);
  });

  it('adds nothing where delivery is not served or nobody signed in, and keeps why delivery could not say', async () => {
    fns.caller.mockRejectedValue(new ApiError(404, 'no delivery', 'DeliveryNotInstalled'));
    await loadSession();
    expect([session.caller, session.callerError]).toEqual([null, '']);
    fns.caller.mockRejectedValue(new ApiError(503, 'apiserver down', 'ClusterUnreachable'));
    await loadSession();
    expect(session.callerError).toBe('apiserver down');
    fns.caller.mockReset();
    fns.session.mockResolvedValue({ mode: 'shared', login: true, signedIn: false });
    await loadSession();
    expect([session.caller, session.callerError, fns.caller.mock.calls.length]).toEqual([null, '', 0]);
  });

  it('keeps a session it cannot read as an error, and never rejects', async () => {
    fns.session.mockRejectedValue(new Error('down'));
    await expect(loadSession()).resolves.toBeUndefined();
    expect((session.error as Error).message).toBe('down');
  });
});

describe('signing in', () => {
  it('builds the login URLs', () => {
    expect(loginUrl('#/delivery/inbox')).toBe('/auth/login?return=%2F%23%2Fdelivery%2Finbox');
    expect(loginUrl('/delivery/inbox', true)).toBe('/auth/login?return=%2F%23%2Fdelivery%2Finbox&fresh=1');
  });

  it('signs in through the host when it handles sign-in itself, else through /auth/login', () => {
    const w = window as unknown as { __pactoSignIn?: (h: string, f: boolean) => void };
    const assign = vi.fn();
    vi.stubGlobal('location', { assign });
    try {
      expect(hostSignsIn()).toBe(false);
      signIn('#/delivery/inbox', true);
      expect(assign).toHaveBeenCalledWith('/auth/login?return=%2F%23%2Fdelivery%2Finbox&fresh=1');
      const hook = vi.fn();
      w.__pactoSignIn = hook;
      expect(hostSignsIn()).toBe(true);
      signIn('#/delivery/inbox');
      expect(hook).toHaveBeenCalledWith('#/delivery/inbox', false);
      expect(assign).toHaveBeenCalledTimes(1);
    } finally {
      delete w.__pactoSignIn;
      vi.unstubAllGlobals();
    }
  });
});

describe('who people are', () => {
  it('names people: a known name, the signed-in person, a service account, a subject', () => {
    expect(personLabel('https://idp#u1', { 'https://idp#u1': 'Ann' })).toBe('Ann');
    expect(personLabel('https://idp#u1', null, { user: 'https://idp#u1', name: 'Me' })).toBe('Me');
    expect(personLabel('https://idp#u1', null, { user: 'other', name: 'Me' })).toBe('u1');
    expect(personLabel('https://idp#u1', null, { user: 'https://idp#u1' })).toBe('u1');
    expect(personLabel('system:serviceaccount:agents:pay-agent')).toBe('pay-agent');
    expect(personLabel('kind-admin')).toBe('kind-admin');
    expect(personLabel('#x')).toBe('#x');
    expect(personLabel('weird#')).toBe('weird#');
    expect(personLabel(undefined)).toBe('');
  });
});

describe('teamGroups', () => {
  it('leaves out Kubernetes system: groups, which name no team', () => {
    expect(teamGroups(['team-orders', 'system:authenticated', 'system:masters'])).toEqual(['team-orders']);
    expect(teamGroups(undefined)).toEqual([]);
  });
});
