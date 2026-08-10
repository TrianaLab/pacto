import { describe, it, expect } from 'vitest';
import { initScrollRestore, type ScrollHost } from './scrollRestore.ts';

// A window stand-in with a settable page height and a real little session history,
// so "the content has not arrived yet" and "this entry has been visited before" are
// both expressible. jsdom has no layout: a real window here would report scrollY 0
// forever and prove nothing.
function makeHost(hash = '#/a') {
  const listeners: Record<string, Set<(e: Event) => void>> = {};
  const intervals = new Map<number, () => void>();
  const data: Record<string, string> = {};
  const entries: Array<{ hash: string; state: unknown }> = [{ hash, state: null }];
  let at = 0;
  let nextId = 1;

  const host = {
    location: { hash },
    history: {
      scrollRestoration: 'auto' as ScrollRestoration,
      get state() { return entries[at].state; },
      replaceState(state: unknown) { entries[at].state = state; },
    },
    scrollY: 0,
    /** How far this page can actually be scrolled right now. */
    height: 0,
    scrollTo(_x: number, y: number) {
      host.scrollY = Math.max(0, Math.min(y, host.height));
      host.emit('scroll');
    },
    addEventListener(t: string, fn: (e: Event) => void) { (listeners[t] ||= new Set()).add(fn); },
    removeEventListener(t: string, fn: (e: Event) => void) { listeners[t]?.delete(fn); },
    setInterval(fn: () => void) { const id = nextId++; intervals.set(id, fn); return id; },
    clearInterval(id: unknown) { intervals.delete(id as number); },
    sessionStorage: {
      getItem: (k: string) => (k in data ? data[k] : null),
      setItem: (k: string, v: string) => { data[k] = v; },
    } as unknown as Storage,

    emit(t: string, e?: Event) { for (const fn of [...(listeners[t] ?? [])]) fn(e ?? new Event(t)); },
    /** One poll of the pending restore. */
    tick() { for (const fn of [...intervals.values()]) fn(); },
    listenerCount(t: string) { return listeners[t]?.size ?? 0; },
    stored() { return data['pacto:scroll']; },

    // --- session history -----------------------------------------------------
    /** A deliberate navigation: a NEW entry, with no state of its own. */
    push(next: string) {
      const from = host.location.hash;
      entries.length = at + 1;
      entries.push({ hash: next, state: null });
      at = entries.length - 1;
      host.arrive(from);
    },
    /** Back or forward: an entry we have been on before, carrying its own state. */
    go(delta: number) {
      const from = host.location.hash;
      at = Math.max(0, Math.min(entries.length - 1, at + delta));
      host.arrive(from);
    },
    /** replaceHash(): same entry, new URL, notified with a BARE event. */
    replace(next: string) {
      entries[at] = { hash: next, state: null }; // history.replaceState(null, '', h)
      host.location.hash = next;
      host.emit('hashchange');
    },
    arrive(from: string) {
      host.location.hash = entries[at].hash;
      host.emit('hashchange', new HashChangeEvent('hashchange', {
        oldURL: `http://x/${from}`, newURL: `http://x/${entries[at].hash}`,
      }));
    },
    /** What a reload would hand the next page load. */
    reload() {
      host.emit('pagehide');
      const fresh = makeHost(host.location.hash);
      fresh.sessionStorage!.setItem('pacto:scroll', host.stored()!);
      fresh.history.replaceState(entries[at].state);
      return fresh;
    },
  };
  return host as typeof host & ScrollHost;
}

describe('scrollRestore', () => {
  it('restores a reloaded deep link once its async content has settled', () => {
    const host = makeHost('#/fleet/services/payments');
    // Keyed by history ENTRY plus URL: this is the first entry of a fresh load.
    host.sessionStorage!.setItem('pacto:scroll', JSON.stringify({ '0|#/fleet/services/payments': 900 }));

    const stop = initScrollRestore(host);
    // The entity request has not landed: the page cannot hold 900px yet, and this
    // is exactly where the browser's own restoration gives up at 0.
    expect(host.scrollY).toBe(0);

    host.height = 2000;
    host.tick();
    expect(host.scrollY).toBe(900);
    stop();
  });

  it('takes scroll restoration over from the browser and hands it back', () => {
    const host = makeHost();
    const stop = initScrollRestore(host);
    expect(host.history.scrollRestoration).toBe('manual');
    stop();
    expect(host.history.scrollRestoration).toBe('auto');
    expect(host.listenerCount('scroll')).toBe(0);
    expect(host.listenerCount('hashchange')).toBe(0);
  });

  it('starts a deliberate navigation at the top of the new page', () => {
    const host = makeHost();
    const stop = initScrollRestore(host);
    host.height = 2000;
    host.scrollTo(0, 800);

    host.push('#/b');

    expect(host.scrollY).toBe(0);
    stop();
  });

  it('starts at the top even when that URL was read before, because a click is not a Back', () => {
    const host = makeHost();
    const stop = initScrollRestore(host);
    host.height = 2000;
    host.scrollTo(0, 800);
    host.push('#/b');
    // Chromium announces a plain fragment link click the same way it announces a
    // traversal, so only the history ENTRY can tell them apart. Clicking a link back
    // to a page read earlier is still a fresh visit.
    host.push('#/a');

    expect(host.scrollY).toBe(0);
    host.tick();
    expect(host.scrollY).toBe(0);
    stop();
  });

  it('restores the previous page position on back, after that page settles', () => {
    const host = makeHost();
    const stop = initScrollRestore(host);
    host.height = 2000;
    host.scrollTo(0, 700);

    host.push('#/b');
    host.height = 0; // the page we come back to starts empty again

    host.go(-1);
    expect(host.scrollY).toBe(0);

    host.height = 2000;
    host.tick();
    expect(host.scrollY).toBe(700);
    stop();
  });

  it('does not settle for the height of the page being left', () => {
    const host = makeHost();
    const stop = initScrollRestore(host);
    host.height = 3000;
    host.scrollTo(0, 1358);
    host.push('#/b');

    // Back is announced while the previous page is still mounted and still tall, so
    // the offset lands immediately -- against the WRONG page.
    host.go(-1);
    expect(host.scrollY).toBe(1358);
    host.tick(); // a poll lands before the swap: the offset looks perfectly restored

    host.height = 0; // and only THEN does the incoming page render, empty
    host.tick();
    expect(host.scrollY).toBe(0);

    host.height = 3000; // and its content lands
    host.tick();
    expect(host.scrollY).toBe(1358); // the restore was still running to re-apply it
    stop();
  });

  it('does not mistake an already-collapsed page for a page at the top', () => {
    const host = makeHost();
    const stop = initScrollRestore(host);
    host.height = 2000;
    host.scrollTo(0, 700);

    // A navigation reaches us only after the router listener registered before us has
    // swapped the route, so the outgoing page is already gone and the offset already
    // reads 0. That zero is an artifact of the swap, not where the user was.
    host.height = 0;
    host.scrollY = 0;
    host.push('#/b');

    host.go(-1);
    host.height = 2000;
    host.tick();
    expect(host.scrollY).toBe(700);
    stop();
  });

  it('goes forward to where that page was left too', () => {
    const host = makeHost();
    const stop = initScrollRestore(host);
    host.height = 2000;
    host.push('#/b');
    host.scrollTo(0, 450);
    host.go(-1);
    host.go(1);

    host.tick();
    expect(host.scrollY).toBe(450);
    stop();
  });

  it('leaves the user where they are when the URL is only canonicalized', () => {
    const host = makeHost();
    const stop = initScrollRestore(host);
    host.height = 2000;
    host.scrollTo(0, 500);

    // replaceHash() rewrites the URL in place and notifies with a bare Event.
    host.replace('#/a?text=pay');
    expect(host.scrollY).toBe(500);

    // ...and Back onto that canonicalized entry is still recognized as a traversal,
    // even though replaceState wiped what we had stamped on it.
    host.push('#/b');
    host.height = 0;
    host.go(-1);
    host.height = 2000;
    host.tick();
    expect(host.scrollY).toBe(500);
    stop();
  });

  it('gives up a pending restore the moment the user scrolls themselves', () => {
    const host = makeHost('#/deep');
    host.sessionStorage!.setItem('pacto:scroll', JSON.stringify({ '0|#/deep': 900 }));
    const stop = initScrollRestore(host);

    host.emit('wheel');
    host.height = 2000;
    host.tick();

    expect(host.scrollY).toBe(0);
    stop();
  });

  it('remembers the position across a reload', () => {
    const host = makeHost('#/a');
    const stop = initScrollRestore(host);
    host.height = 2000;
    host.scrollTo(0, 640);

    const reloaded = host.reload();
    stop();
    expect(JSON.parse(host.stored()!)['0|#/a']).toBe(640);

    const stop2 = initScrollRestore(reloaded);
    reloaded.height = 2000;
    reloaded.tick();
    expect(reloaded.scrollY).toBe(640);
    stop2();
  });

  // The counterexample that forced positions off URLs and onto history entries.
  // Three entries, two of them showing #/a. Keyed by URL, the third visit's 0
  // overwrote the first visit's 800 and Back Back landed at the top of a page the
  // user had read halfway.
  it('keeps two history entries for the same URL independent (A, B, A, back, back)', () => {
    const host = makeHost('#/a');
    const stop = initScrollRestore(host);
    host.height = 2000;
    host.scrollTo(0, 800);        // entry 1: #/a, read to 800

    host.push('#/b');             // entry 2
    host.scrollTo(0, 300);
    host.push('#/a');             // entry 3: the SAME url, a fresh visit
    expect(host.scrollY).toBe(0); // and therefore a fresh scroll state

    host.go(-1);                  // back to entry 2 (#/b)
    host.tick();
    expect(host.scrollY).toBe(300);

    host.go(-1);                  // back to entry 1 (#/a) -- the halfway-read one
    host.tick();
    expect(host.scrollY).toBe(800);
    stop();
  });

  // Canonicalization renames THIS entry rather than creating one, so the position
  // travels with it and Back still finds only the entries the user really made.
  it('transfers the position to the canonical URL without creating an entry', () => {
    const host = makeHost('#/a');
    const stop = initScrollRestore(host);
    host.height = 2000;
    host.scrollTo(0, 500);
    host.replace('#/a?text=pay');

    host.push('#/b');
    host.go(-1);
    host.tick();
    expect(host.scrollY).toBe(500);

    // The old URL kept nothing behind: it is not a place the user can return to.
    host.emit('pagehide');
    const keys = Object.keys(JSON.parse(host.stored()!));
    expect(keys.some((k) => k.endsWith('|#/a'))).toBe(false);
    expect(keys.some((k) => k.endsWith('|#/a?text=pay'))).toBe(true);
    stop();
  });

  // A push truncates the forward history. An entry that later reuses a discarded
  // index is a different place and must not inherit the discarded one's position.
  it('does not let a new entry inherit a discarded forward entry position', () => {
    const host = makeHost('#/a');
    const stop = initScrollRestore(host);
    host.height = 2000;
    host.push('#/b');
    host.scrollTo(0, 600);
    host.go(-1);                  // back to #/a; #/b is now forward history
    host.push('#/b');             // a NEW entry 1, discarding the old one
    host.tick();
    expect(host.scrollY).toBe(0);
    stop();
  });

  it('bounds what it remembers', () => {
    const host = makeHost('#/a');
    const stop = initScrollRestore(host);
    host.height = 2000;
    for (let i = 0; i < 60; i++) {
      host.push(`#/p${i}`);
      host.scrollTo(0, 100 + i);
    }
    host.emit('pagehide');
    expect(Object.keys(JSON.parse(host.stored()!)).length).toBeLessThanOrEqual(30);
    stop();
  });

  it('survives an unreadable session store', () => {
    const host = makeHost('#/a');
    host.sessionStorage!.setItem('pacto:scroll', 'not json');
    expect(() => initScrollRestore(host)()).not.toThrow();
  });
});
