/**
 * The delivery views appear only where the host serves delivery: anywhere else
 * a delivery URL says why there is nothing, and never mounts a view that would
 * call an endpoint the host does not have.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, tick, flushSync } from 'svelte';

const fns = vi.hoisted(() => ({ capabilities: vi.fn(), inbox: vi.fn(), session: vi.fn() }));
vi.mock('./lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./lib/api.ts')>();
  return {
    ...actual,
    api: {
      services: vi.fn().mockResolvedValue([]), sources: vi.fn().mockResolvedValue({ sources: [] }),
      health: vi.fn().mockResolvedValue({ version: 'x' }), refresh: vi.fn().mockResolvedValue({}),
      capabilities: () => fns.capabilities(), deliveryInbox: () => fns.inbox(), session: () => fns.session(),
    },
  };
});

// @ts-expect-error — Svelte component has no declaration file
import App from './App.svelte';

async function renderAt(hash: string) {
  location.hash = hash;
  const target = document.createElement('div');
  document.body.appendChild(target);
  const c = mount(App, { target });
  for (let i = 0; i < 8; i++) { await tick(); await Promise.resolve(); }
  flushSync();
  return { target, destroy: () => { unmount(c); target.remove(); } };
}

beforeEach(() => {
  fns.inbox.mockReset();
  fns.session.mockResolvedValue({ mode: 'shared', login: true, signedIn: true, fresh: false, caller: { user: 'u', class: 'human', groups: [] } });
  fns.inbox.mockResolvedValue({ asOf: 'x', completeness: 'complete', held: [], frozen: [], errored: [] });
});

describe('App — delivery gate', () => {
  it('says delivery is not available where the host does not serve it', async () => {
    fns.capabilities.mockResolvedValue({ fleet: true, impact: true, observed: false, delivery: false, auth: { mode: 'none', login: false, signedIn: false } });
    const { target, destroy } = await renderAt('#/delivery/inbox');
    expect(target.querySelector('[data-testid="delivery-unavailable"]')).not.toBeNull();
    expect(fns.inbox).not.toHaveBeenCalled();
    destroy();
  });

  it('asks again on a refresh, and mounts the inbox once delivery is served', async () => {
    fns.capabilities.mockResolvedValueOnce({ fleet: true, impact: true, observed: false, delivery: false, auth: { mode: 'shared', login: true, signedIn: true } })
      .mockResolvedValue({ fleet: true, impact: true, observed: false, delivery: true, auth: { mode: 'shared', login: true, signedIn: true } });
    const { target, destroy } = await renderAt('#/delivery/inbox');
    expect(target.querySelector('[data-testid="delivery-unavailable"]')).not.toBeNull();
    (target.querySelector('button[aria-label="Refresh"]') as HTMLButtonElement).click();
    // The delivery views load on first use.
    await vi.waitFor(() => expect(fns.inbox).toHaveBeenCalled());
    destroy();
  });

  it('mounts the inbox where it does', async () => {
    fns.capabilities.mockResolvedValue({ fleet: true, impact: true, observed: false, delivery: true, auth: { mode: 'shared', login: true, signedIn: true } });
    const { target, destroy } = await renderAt('#/delivery/inbox');
    await vi.waitFor(() => expect(target.querySelector('[data-testid="inbox-empty"]')).not.toBeNull());
    expect(fns.inbox).toHaveBeenCalled();
    destroy();
  });
});
