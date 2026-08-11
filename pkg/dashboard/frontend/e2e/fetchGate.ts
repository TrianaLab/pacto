import { type Page } from '@playwright/test';

/*
 * NOTE ON THE FILENAME: `fetchGate.ts`, not `*.spec.ts`, so Playwright's default
 * testMatch does not collect it. It is a helper shared by the specs that need to hold a
 * request open or fail one -- swr.spec.ts and owners.spec.ts -- which run as separate
 * files and cannot share a local function.
 */

/**
 * The seam every API call passes through: `window.fetch`, which the demo's boot.js
 * shadows to route into the wasm engine. Playwright's own network interception never
 * sees a wasm-served fetch, so a browser test that needs a slow or failing backend has
 * to wrap the shim instead of the network.
 */
export interface Gate {
  hold: string | null;
  fail: string | null;
  waiters: Array<() => void>;
  held: number;
  polls: number;
}

/**
 * Wraps `window.fetch` so a test can hold matching requests open, or fail them, without
 * touching the engine underneath. Installed BEFORE boot.js: the getter hands boot.js the
 * native fetch (so its own captured `realFetch` is not our wrapper, which would recurse),
 * and the setter captures boot.js's shim as the real implementation everything else is
 * routed through.
 */
export function installFetchGate(): void {
  const w = window as unknown as { __gate: Gate };
  w.__gate = { hold: null, fail: null, waiters: [], held: 0, polls: 0 };
  const native = window.fetch.bind(window);
  let real: typeof window.fetch | null = null;

  const wrapped: typeof window.fetch = (input, init) => {
    const url = typeof input === 'string' ? input : input instanceof Request ? input.url : String(input);
    const g = w.__gate;
    // /health is fetched exactly once per App.loadGlobal(), so it counts refresh ticks --
    // automatic and explicit alike -- and lets a test wait for a REAL poll.
    if (url.includes('/health')) g.polls++;
    const run = () => (real ?? native)(input, init);
    if (g.fail && url.includes(g.fail)) {
      return Promise.resolve(new Response(JSON.stringify({ title: 'Service Unavailable', detail: 'the backend went away' }), {
        status: 503, headers: { 'Content-Type': 'application/problem+json' },
      }));
    }
    if (g.hold && url.includes(g.hold)) {
      g.held++;
      return new Promise<Response>((resolve, reject) => { g.waiters.push(() => run().then(resolve, reject)); });
    }
    return run();
  };

  Object.defineProperty(window, 'fetch', {
    configurable: true,
    get() { return real ? wrapped : native; },
    set(v: typeof window.fetch) { real = v; },
  });
}

const T = 30_000;

/** The test-side handle on the gate installed by {@link installFetchGate}. */
export function gate(page: Page) {
  return {
    /** Hold every request whose URL contains `frag` until released. */
    async hold(frag: string) {
      await page.evaluate((f) => {
        const g = (window as unknown as { __gate: Gate }).__gate;
        g.hold = f; g.held = 0; g.waiters.length = 0;
      }, frag);
    },
    /** Wait until at least `n` matching requests are actually being held. */
    async awaitHeld(n = 1) {
      await page.waitForFunction((k) => (window as unknown as { __gate: Gate }).__gate.held >= k, n, { timeout: T });
    },
    heldCount: () => page.evaluate(() => (window as unknown as { __gate: Gate }).__gate.held),
    /** Let the held requests through. `lifo` resolves the NEWEST first, which is how an
     *  older in-flight response ends up racing a newer one. */
    async release(lifo = false) {
      await page.evaluate((rev) => {
        const g = (window as unknown as { __gate: Gate }).__gate;
        g.hold = null;
        const ws = g.waiters.splice(0);
        if (rev) ws.reverse();
        for (const w of ws) w();
      }, lifo);
    },
    /** Make every matching request fail with a 503 (or stop, with null). */
    async fail(frag: string | null) {
      await page.evaluate((f) => { (window as unknown as { __gate: Gate }).__gate.fail = f; }, frag);
    },
    polls: () => page.evaluate(() => (window as unknown as { __gate: Gate }).__gate.polls),
    async awaitPoll() {
      const before = await page.evaluate(() => (window as unknown as { __gate: Gate }).__gate.polls);
      await page.waitForFunction((n) => (window as unknown as { __gate: Gate }).__gate.polls > n, before, { timeout: T });
    },
  };
}
