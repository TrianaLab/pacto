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
    servicesFn.mockResolvedValue([]);
    sourcesFn.mockResolvedValue({ sources: [], discovering: false });
    healthFn.mockResolvedValue({ version: 'x' });
    capabilitiesFn.mockResolvedValue({ fleet: true, impact: true });
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
