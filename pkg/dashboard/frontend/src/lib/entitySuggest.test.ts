/**
 * Deterministic tests for the shared search-as-you-type mechanics. Every search
 * surface in the product routes through this, so the guarantees are proven here once
 * instead of being re-hoped-for per component:
 *  - a keystroke does not become a request (debounce);
 *  - an abandoned search's response can never repopulate the UI (stale-response guard);
 *  - clearing and destroying invalidate what is in flight;
 *  - the bound and the kind restriction reach the backend.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { createEntitySuggest } from './entitySuggest.svelte.ts';
import { api } from './api.ts';

interface Deferred<T> { promise: Promise<T>; resolve: (v: T) => void; reject: (e: unknown) => void; }
function deferred<T>(): Deferred<T> {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}

// A minimal entity list shaped like the wire response.
const list = (...labels: string[]) => ({
  entities: labels.map((label) => ({ kind: 'service', key: label, label, href: `/fleet/services/${label}` })),
  total: labels.length,
});

// Flush microtasks so a settled fetch's .then/.catch/.finally run. Microtasks only:
// the timers here are faked, so a setTimeout-based flush would never fire.
const flush = async () => { for (let i = 0; i < 5; i++) await Promise.resolve(); };

describe('createEntitySuggest', () => {
  beforeEach(() => { vi.useFakeTimers(); });
  afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });

  it('does not turn a keystroke into a request', () => {
    const spy = vi.spyOn(api, 'fleetEntities').mockReturnValue(deferred<never>().promise);
    const s = createEntitySuggest({ debounceMs: 140 });
    for (const t of ['p', 'pa', 'pay', 'paym']) s.search(t);
    expect(spy).not.toHaveBeenCalled();
    vi.advanceTimersByTime(140);
    // One request, for the LAST thing typed -- not four.
    expect(spy).toHaveBeenCalledTimes(1);
    expect(spy.mock.calls[0][0]).toMatchObject({ text: 'paym' });
  });

  it('sends the kind restriction and the bound the caller asked for', () => {
    const spy = vi.spyOn(api, 'fleetEntities').mockReturnValue(deferred<never>().promise);
    const s = createEntitySuggest({ kinds: ['service'], limit: 8, debounceMs: 0 });
    s.search('pay');
    vi.advanceTimersByTime(0);
    expect(spy.mock.calls[0][0]).toEqual({ text: 'pay', limit: 8, kinds: ['service'] });
  });

  it('reports truncation when the backend has more than it returned', async () => {
    vi.spyOn(api, 'fleetEntities').mockResolvedValue({ ...list('a', 'b'), total: 40 } as never);
    const s = createEntitySuggest({ debounceMs: 0 });
    s.search('a');
    vi.advanceTimersByTime(0);
    await flush();
    expect(s.results.length).toBe(2);
    expect(s.total).toBe(40);
    expect(s.truncated).toBe(true);
  });

  it('drops a late response from an abandoned search', async () => {
    const first = deferred<unknown>();
    const second = deferred<unknown>();
    vi.spyOn(api, 'fleetEntities')
      .mockReturnValueOnce(first.promise as never)
      .mockReturnValueOnce(second.promise as never);

    const s = createEntitySuggest({ debounceMs: 0 });
    s.search('pay');
    vi.advanceTimersByTime(0);
    s.search('orders');
    vi.advanceTimersByTime(0);

    // The newer search lands first, then the abandoned one arrives out of order.
    second.resolve(list('orders-service'));
    await flush();
    first.resolve(list('payments-service'));
    await flush();

    expect(s.results.map((r) => r.label)).toEqual(['orders-service']);
  });

  it('drops a late FAILURE from an abandoned search', async () => {
    const first = deferred<unknown>();
    const second = deferred<unknown>();
    vi.spyOn(api, 'fleetEntities')
      .mockReturnValueOnce(first.promise as never)
      .mockReturnValueOnce(second.promise as never);

    const s = createEntitySuggest({ debounceMs: 0 });
    s.search('pay');
    vi.advanceTimersByTime(0);
    s.search('orders');
    vi.advanceTimersByTime(0);

    second.resolve(list('orders-service'));
    await flush();
    first.reject(new Error('gone'));
    await flush();

    // Good results stay; a dead search cannot turn them into an error.
    expect(s.results.map((r) => r.label)).toEqual(['orders-service']);
    expect(s.error).toBe(null);
    expect(s.loading).toBe(false);
  });

  it('clears immediately on an empty query and cancels what is pending', async () => {
    const d = deferred<unknown>();
    const spy = vi.spyOn(api, 'fleetEntities').mockReturnValue(d.promise as never);
    const s = createEntitySuggest({ debounceMs: 0 });
    s.search('pay');
    vi.advanceTimersByTime(0);
    s.search('   '); // blank: not a search
    expect(s.loading).toBe(false);
    expect(spy).toHaveBeenCalledTimes(1);

    d.resolve(list('payments-service'));
    await flush();
    expect(s.results).toEqual([]);
  });

  it('destroy() stops a pending response from writing to torn-down state', async () => {
    const d = deferred<unknown>();
    vi.spyOn(api, 'fleetEntities').mockReturnValue(d.promise as never);
    const s = createEntitySuggest({ debounceMs: 0 });
    s.search('pay');
    vi.advanceTimersByTime(0);
    s.destroy();

    d.resolve(list('payments-service'));
    await flush();
    expect(s.results).toEqual([]);
    expect(s.loading).toBe(false);
  });

  it('never fires a request that was still only debouncing when cleared', () => {
    const spy = vi.spyOn(api, 'fleetEntities').mockReturnValue(deferred<never>().promise);
    const s = createEntitySuggest({ debounceMs: 140 });
    s.search('pay');
    s.clear();
    vi.advanceTimersByTime(500);
    expect(spy).not.toHaveBeenCalled();
  });
});
