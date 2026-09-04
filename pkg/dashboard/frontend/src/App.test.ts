/**
 * Regression test: auto-reload polling must settle to the normal cadence.
 *
 * The initial timer polls fast (2s, for discovery). Once the first load reports the
 * fleet is NOT discovering, the poll must slow to 10s. Previously the interval only
 * changed on a discovery-state transition, so a load that started already
 * not-discovering left the poll stuck at 2s. It now reconciles against the active
 * interval.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount } from 'svelte';

const servicesFn = vi.fn();
const sourcesFn = vi.fn();
const healthFn = vi.fn();
const capabilitiesFn = vi.fn();

vi.mock('./lib/api.ts', () => ({
  api: {
    services: (...a: unknown[]) => servicesFn(...a),
    sources: (...a: unknown[]) => sourcesFn(...a),
    health: (...a: unknown[]) => healthFn(...a),
    capabilities: (...a: unknown[]) => capabilitiesFn(...a),
    refresh: vi.fn().mockResolvedValue({}),
  },
}));

// @ts-expect-error — Svelte component has no declaration file
import App from './App.svelte';

describe('App — auto-reload cadence', () => {
  let target: HTMLElement;

  beforeEach(() => {
    for (const f of [servicesFn, sourcesFn, healthFn, capabilitiesFn]) f.mockReset();
    servicesFn.mockResolvedValue([]);
    sourcesFn.mockResolvedValue({ sources: [], discovering: false });
    healthFn.mockResolvedValue({ version: 'x' });
    // A non-Fleet host: the legacy list renders and polls the legacy services plane, so
    // the poll cadence is measured host-independently without the product IA redirect.
    capabilitiesFn.mockResolvedValue({ fleet: false, impact: false });
    vi.useFakeTimers();
    target = document.createElement('div');
    document.body.appendChild(target);
    location.hash = '#/';
  });

  afterEach(() => {
    vi.useRealTimers();
    document.body.removeChild(target);
  });

  it('slows the poll to 10s once not discovering (not stuck at 2s)', async () => {
    const app = mount(App, { target });

    // Settle the initial load; discovering=false must reconcile the poll to 10s.
    await vi.advanceTimersByTimeAsync(0);
    const afterInit = servicesFn.mock.calls.length;
    expect(afterInit).toBeGreaterThan(0);

    // Past the old 2s cadence — no extra poll should fire at 2s.
    await vi.advanceTimersByTimeAsync(2500);
    expect(servicesFn.mock.calls.length).toBe(afterInit);

    // Reaching 10s fires exactly one normal-cadence poll.
    await vi.advanceTimersByTimeAsync(8000);
    expect(servicesFn.mock.calls.length).toBe(afterInit + 1);

    unmount(app);
  });
});

describe('App — the global load on a large fleet', () => {
  let target: HTMLElement;

  beforeEach(() => {
    for (const f of [servicesFn, sourcesFn, healthFn, capabilitiesFn]) f.mockReset();
    servicesFn.mockResolvedValue([]);
    sourcesFn.mockResolvedValue({ sources: [], discovering: true });
    healthFn.mockResolvedValue({ version: 'x' });
    vi.useFakeTimers();
    target = document.createElement('div');
    document.body.appendChild(target);
    location.hash = '#/';
  });

  afterEach(() => {
    vi.useRealTimers();
    document.body.removeChild(target);
  });

  // The poll fires on a fixed interval whether or not the previous pass has returned.
  // On a large fleet one pass outlives the 2s discovery interval, so without a guard
  // each slow pass stacks another concurrent pass on top of it.
  it('does not stack a second pass on top of one still in flight, but never drops a manual refresh', async () => {
    capabilitiesFn.mockResolvedValue({ fleet: false, impact: false });
    // One pass over a large fleet outlives the 2s discovery interval.
    servicesFn.mockReturnValue(new Promise(() => {}));
    const app = mount(App, { target });
    await vi.advanceTimersByTimeAsync(0);
    expect(servicesFn).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(7000); // three polls the in-flight pass outlives
    expect(servicesFn).toHaveBeenCalledTimes(1);

    // The user asking is not the poll firing: a manual refresh goes through regardless.
    (target.querySelector('[aria-label="Refresh"]') as HTMLButtonElement).click();
    await vi.advanceTimersByTimeAsync(0);
    expect(servicesFn).toHaveBeenCalledTimes(2);
    unmount(app);
  });
});
