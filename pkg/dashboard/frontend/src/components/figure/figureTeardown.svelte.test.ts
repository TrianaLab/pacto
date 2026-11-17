import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, tick } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import ImpactFigure from './ImpactFigure.svelte';

// A figure unmounted mid-narration (a route change while it plays) finishes its
// animations on destroy; their settling must not reach the stage it no longer has.
vi.mock('../../lib/narrate.ts', async (orig) => {
  const real = await orig<typeof import('../../lib/narrate.ts')>();
  return {
    ...real,
    claimAutoplay: () => true,
    finalMode: () => false,
    play: () => {
      let resolve = () => {};
      const finished = new Promise<void>((r) => { resolve = r; });
      return [{ finished, cancel() {}, finish: () => resolve() }];
    },
  };
});

afterEach(() => { vi.unstubAllGlobals(); document.body.innerHTML = ''; });

describe('Figure teardown', () => {
  it('raises nothing when it is unmounted while its narration plays', async () => {
    vi.stubGlobal('matchMedia', (q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }));
    vi.stubGlobal('IntersectionObserver', class {
      cb: IntersectionObserverCallback;
      constructor(cb: IntersectionObserverCallback) { this.cb = cb; }
      observe() { this.cb([{ isIntersecting: true } as IntersectionObserverEntry], this as unknown as IntersectionObserver); }
      disconnect() {}
    });
    const errors: unknown[] = [];
    const onErr = (e: PromiseRejectionEvent | ErrorEvent) => errors.push(e);
    window.addEventListener('error', onErr);
    const onRej = (e: unknown) => errors.push(e);
    process.on('unhandledRejection', onRej);
    process.on('uncaughtException', onRej);
    const target = document.createElement('div');
    document.body.appendChild(target);
    const impact = {
      meta: { completeness: 'complete', asOf: '2026-11-07T14:04:00Z', sourceCounts: { total: 1, available: 1, partial: 0, stale: 0, unavailable: 0 } },
      service: { kind: 'service', key: 's', label: 's', href: '' },
      oldRevision: { kind: 'revision', key: 's@1', label: 's 1', version: '1', href: '' },
      newRevision: { kind: 'revision', key: 's@2', label: 's 2', version: '2', href: '' },
      classification: 'BREAKING', gate: 'blocked', gateReason: 'r', snapshotId: 'x', snapshotMatch: true,
      changes: { total: 1, count: 1, breaking: 1, potential: 0, nonBreaking: 0, truncated: false, items: [] },
      consumers: { total: 1, direct: 1, hidden: 0, refused: 0, count: 0, limit: 100, offset: 0, truncated: false, items: [], byVerdict: [], byConfidence: [], byOutcome: [{ key: 'incompatible', count: 1 }], byOwner: [], beyondRanking: 0, noOwner: 0, owners: 0, highlights: [] },
    };
    const c = mount(ImpactFigure, { target, props: { impact, autoplay: true } });
    for (let i = 0; i < 5; i++) await tick();
    expect(target.querySelector('[data-fig]')?.getAttribute('data-narration')).toBe('playing');
    unmount(c);
    await new Promise((r) => setTimeout(r, 0));
    window.removeEventListener('error', onErr);
    process.off('unhandledRejection', onRej);
    process.off('uncaughtException', onRej);
    expect(errors).toEqual([]);
  });
});
