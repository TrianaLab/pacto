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
    expect(target.querySelector('[data-testid="delivery-unavailable-reason"]')?.textContent).toContain('has no signed-in user');
    expect(fns.inbox).not.toHaveBeenCalled();
    destroy();
  });

  it('says the cluster lacks delivery when the caller is known', async () => {
    fns.capabilities.mockResolvedValue({ fleet: true, impact: true, observed: false, delivery: false, auth: { mode: 'local', login: false, signedIn: true } });
    const { target, destroy } = await renderAt('#/delivery/inbox');
    expect(target.querySelector('[data-testid="delivery-unavailable-reason"]')?.textContent).toContain('does not serve the delivery API');
    destroy();
  });

  it('offers a retry when the capabilities probe fails, instead of loading for ever', async () => {
    fns.capabilities.mockRejectedValueOnce(new Error('down'))
      .mockResolvedValue({ fleet: true, impact: true, observed: false, delivery: true, auth: { mode: 'shared', login: true, signedIn: true } });
    const { target, destroy } = await renderAt('#/delivery/inbox');
    const err = target.querySelector('[data-testid="capabilities-error"]');
    expect(err).not.toBeNull();
    (err!.querySelector('button') as HTMLButtonElement).click();
    await vi.waitFor(() => expect(fns.inbox).toHaveBeenCalled(), { timeout: 10_000 });
    destroy();
  }, 15_000);

  it('asks again on a refresh, and mounts the inbox once delivery is served', async () => {
    fns.capabilities.mockResolvedValueOnce({ fleet: true, impact: true, observed: false, delivery: false, auth: { mode: 'shared', login: true, signedIn: true } })
      .mockResolvedValue({ fleet: true, impact: true, observed: false, delivery: true, auth: { mode: 'shared', login: true, signedIn: true } });
    const { target, destroy } = await renderAt('#/delivery/inbox');
    expect(target.querySelector('[data-testid="delivery-unavailable"]')).not.toBeNull();
    (target.querySelector('button[aria-label="Refresh"]') as HTMLButtonElement).click();
    // The delivery views load on first use.
    await vi.waitFor(() => expect(fns.inbox).toHaveBeenCalled(), { timeout: 10_000 });
    destroy();
  }, 15_000);

  it('mounts the inbox where it does', async () => {
    fns.capabilities.mockResolvedValue({ fleet: true, impact: true, observed: false, delivery: true, auth: { mode: 'shared', login: true, signedIn: true } });
    const { target, destroy } = await renderAt('#/delivery/inbox');
    await vi.waitFor(() => expect(target.querySelector('[data-testid="inbox-empty"]')).not.toBeNull(), { timeout: 10_000 });
    expect(fns.inbox).toHaveBeenCalled();
    destroy();
  }, 15_000);
});
