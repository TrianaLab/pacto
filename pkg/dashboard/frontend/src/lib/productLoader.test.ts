/**
 * Deterministic race tests for the reusable product loader. Each
 * uses controllable deferred promises to force a specific interleaving, so the
 * ordering guarantees are proven, not merely hoped for:
 *  - exactly one logical initial request (no onMount + effect double fire);
 *  - an older in-flight response can never overwrite a newer route/filter state;
 *  - destroy() invalidates any pending response;
 *  - a refresh cannot overwrite a subsequent navigation.
 */
import { describe, it, expect, vi } from 'vitest';
import { createProductLoader } from './productLoader.svelte.ts';

interface Deferred<T> { promise: Promise<T>; resolve: (v: T) => void; reject: (e: unknown) => void; }
function deferred<T>(): Deferred<T> {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}
// flush microtasks so a resolved/rejected fetcher's .then/.catch/.finally run.
const flush = () => new Promise<void>((r) => setTimeout(r, 0));

describe('createProductLoader', () => {
  it('issues exactly ONE logical initial request across a repeated key', () => {
    const fetcher = vi.fn(() => deferred<string>().promise);
    const loader = createProductLoader(fetcher);
    loader.sync('a');
    loader.sync('a'); // same key: must NOT re-fetch (dedupes mount + first effect)
    loader.sync('a');
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(loader.loading).toBe(true);
  });

  it('re-fetches only when the key changes', () => {
    const fetcher = vi.fn(() => deferred<string>().promise);
    const loader = createProductLoader(fetcher);
    loader.sync('a');
    loader.sync('b');
    loader.sync('b');
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it('an OLDER in-flight response can never overwrite a NEWER route/filter state', async () => {
    const d1 = deferred<string>();
    const d2 = deferred<string>();
    const fetcher = vi.fn().mockReturnValueOnce(d1.promise).mockReturnValueOnce(d2.promise);
    const loader = createProductLoader(fetcher);

    loader.sync('a');      // request A starts (d1)
    loader.sync('b');      // navigation to B starts (d2), supersedes A
    d2.resolve('B');       // B resolves first and renders
    await flush();
    expect(loader.data).toBe('B');
    expect(loader.loading).toBe(false);

    d1.resolve('A');       // the stale A resolves LATER
    await flush();
    expect(loader.data).toBe('B'); // A must NOT clobber B
    expect(loader.loading).toBe(false);
  });

  it('a stale error also cannot overwrite the newer state', async () => {
    const d1 = deferred<string>();
    const d2 = deferred<string>();
    const fetcher = vi.fn().mockReturnValueOnce(d1.promise).mockReturnValueOnce(d2.promise);
    const loader = createProductLoader(fetcher);
    loader.sync('a');
    loader.sync('b');
    d2.resolve('B');
    await flush();
    d1.reject(new Error('stale failure'));
    await flush();
    expect(loader.data).toBe('B');
    expect(loader.error).toBeNull();
  });

  it('destroy() invalidates any pending response (never writes to torn-down state)', async () => {
    const d1 = deferred<string>();
    const fetcher = vi.fn().mockReturnValue(d1.promise);
    const loader = createProductLoader(fetcher);
    loader.sync('a');
    loader.destroy();      // navigation away / teardown
    d1.resolve('A');
    await flush();
    expect(loader.data).toBeNull(); // the abandoned response never applied
  });

  it('a refresh cannot overwrite a subsequent navigation', async () => {
    const d1 = deferred<string>();
    const d2 = deferred<string>();
    const fetcher = vi.fn().mockReturnValueOnce(d1.promise).mockReturnValueOnce(d2.promise);
    const loader = createProductLoader(fetcher);
    loader.refresh();      // an explicit refresh starts (d1)
    loader.sync('b');      // a navigation supersedes it (d2)
    d2.resolve('B');
    await flush();
    d1.resolve('REFRESH-STALE');
    await flush();
    expect(loader.data).toBe('B');
  });

  it('a newer response applied after an older one still wins (ordering by generation, not arrival)', async () => {
    const d1 = deferred<string>();
    const d2 = deferred<string>();
    const fetcher = vi.fn().mockReturnValueOnce(d1.promise).mockReturnValueOnce(d2.promise);
    const loader = createProductLoader(fetcher);
    loader.sync('a');
    loader.sync('b');
    d1.resolve('A');       // the OLDER one resolves first this time
    await flush();
    expect(loader.data).toBeNull(); // still must not apply the stale A
    d2.resolve('B');
    await flush();
    expect(loader.data).toBe('B');
  });

  it('keeps the page on screen while another page of the same list loads, and drops it on a new question or a failure', async () => {
    const d1 = deferred<string>();
    const d2 = deferred<string>();
    const d3 = deferred<string>();
    const fetcher = vi.fn().mockReturnValueOnce(d1.promise).mockReturnValueOnce(d2.promise).mockReturnValueOnce(d3.promise);
    const loader = createProductLoader(fetcher);
    expect(loader.answer('f@0', 'f')).toEqual({ data: null, paging: false });

    loader.sync('f@0', 'f@0', 'f');
    d1.resolve('page1'); await flush();
    expect(loader.answer('f@0', 'f')).toEqual({ data: 'page1', paging: false });

    // Page 2 asked for: page 1 stays, flagged as a page turn, even before sync runs.
    expect(loader.answer('f@25', 'f')).toEqual({ data: 'page1', paging: true });
    loader.sync('f@25', 'f@25', 'f');
    expect(loader.answer('f@25', 'f')).toEqual({ data: 'page1', paging: true });
    // Another question (a filter), or a list that names no family, is not a page turn.
    expect(loader.answer('g@0', 'g')).toEqual({ data: null, paging: false });
    expect(loader.answer('f@25', undefined)).toEqual({ data: null, paging: false });

    // Page 2 fails: the old rows go, so the error is what shows.
    d2.reject(new Error('boom')); await flush();
    expect(loader.answer('f@25', 'f')).toEqual({ data: null, paging: false });

    loader.refresh();
    d3.resolve('page2'); await flush();
    expect(loader.dataTag).toBe('f@25');
    expect(loader.answer('f@25', 'f')).toEqual({ data: 'page2', paging: false });
  });
});
