/**
 * Route-scoped scroll restoration.
 *
 * The product is a hash-routed SPA whose content arrives asynchronously, so the
 * browser's own restoration cannot work: at the moment it restores, the page is
 * a spinner with no height, the offset is clamped to 0 and the user's place is
 * gone. We take restoration over (`manual`) and re-apply the saved offset until
 * the content is tall enough to hold it.
 *
 * The rule follows what the USER did, not what changed on screen:
 *
 *   back / forward, and a hard reload  -> restore that ENTRY's last position
 *   a deliberate navigation (push)     -> start at the top of the new page
 *   a canonicalization (replace)       -> leave the page exactly where it is
 *
 * A position belongs to a history ENTRY, not to a URL. The same URL can occupy
 * several entries -- read A halfway, open B, come back to A through a link
 * rather than Back -- and those are different places in the user's history, each
 * with its own reading position. Keying by URL alone made the third visit
 * overwrite the first, so Back Back landed at the top of a page the user had
 * read halfway. Positions are therefore keyed by the entry index we stamp on
 * `history.state` PLUS the URL, and a push starts a genuinely fresh one.
 *
 * A background data refresh is not a navigation and never reaches here. Keeping
 * the user's place through one is the loader's job -- stale-while-revalidate, so
 * the DOM never collapses in the first place -- and an unconditional scrollTo()
 * would only paper over a page that had already lost its content.
 */

/** Where a hard reload picks the position back up from. */
const STORE_KEY = 'pacto:scroll';

/** Positions remembered per history entry. Bounded: a convenience, not a log. */
const MAX_ENTRIES = 30;

/** How long we keep re-applying an offset while async content settles. */
const SETTLE_MS = 3000;
const POLL_MS = 50;

/**
 * How long the offset must HOLD before we call the restore done. Reaching it once
 * proves nothing: when a history traversal is announced the page being left is
 * still mounted, so the offset lands against ITS height and is then clamped away
 * a frame later, when the incoming page renders short and empty.
 */
const HOLD_MS = 400;

/** Close enough: sub-pixel and rounding differences are not a failed restore. */
const TOLERANCE = 2;

/** Anything the user does that means "I am driving now" cancels a pending restore. */
const USER_INTENT = ['wheel', 'touchstart', 'keydown'];

/** The marker we stamp on a history entry the first time we see it. */
const IDX = 'pactoScrollIdx';

/**
 * A window-shaped host. Injectable so the behaviour is testable without a real
 * browser (jsdom has no layout, so scrollTo is a no-op there).
 */
export interface ScrollHost {
  location: { hash: string };
  history: { scrollRestoration: ScrollRestoration; state: unknown; replaceState(state: unknown, unused: string): void };
  scrollY: number;
  scrollTo(x: number, y: number): void;
  addEventListener(type: string, fn: (e: Event) => void, opts?: unknown): void;
  removeEventListener(type: string, fn: (e: Event) => void, opts?: unknown): void;
  setInterval(fn: () => void, ms: number): unknown;
  clearInterval(id: unknown): void;
  sessionStorage?: Storage;
}

function load(win: ScrollHost): Array<[string, number]> {
  try {
    const raw = win.sessionStorage?.getItem(STORE_KEY);
    const obj = raw ? JSON.parse(raw) : null;
    if (!obj || typeof obj !== 'object') return [];
    return Object.entries(obj).filter(([, v]) => typeof v === 'number') as Array<[string, number]>;
  } catch {
    return []; // a poisoned or unavailable store just means "no memory"
  }
}

/** The index we stamped on the current history entry, or -1 if it is brand new. */
function entryIndex(win: ScrollHost): number {
  const s = win.history.state as Record<string, unknown> | null;
  const n = s && typeof s === 'object' ? s[IDX] : undefined;
  return typeof n === 'number' ? n : -1;
}

function stamp(win: ScrollHost, idx: number): void {
  const s = win.history.state;
  const next = { ...(s && typeof s === 'object' ? s : {}), [IDX]: idx };
  try { win.history.replaceState(next, ''); } catch { /* opaque origin */ }
}

/**
 * The storage key for one history entry showing one URL. Only the text before the
 * FIRST separator is ever read back as the index, and the index is always written
 * first, so a hash containing the separator itself cannot confuse the two.
 */
function posKey(idx: number, hash: string): string {
  return `${idx}|${hash}`;
}

/** The entry index encoded in a storage key, or -1 if the key is malformed. */
function keyIndex(key: string): number {
  const n = Number(key.slice(0, key.indexOf('|')));
  return Number.isInteger(n) ? n : -1;
}

/**
 * initScrollRestore takes over scroll restoration for the hash router. Returns a
 * teardown.
 */
export function initScrollRestore(win: ScrollHost = window as unknown as ScrollHost): () => void {
  const positions = new Map<string, number>(load(win));
  let currentHash = win.location.hash;
  let cancel: (() => void) | null = null;

  // Push or traversal is decided by the history ENTRY, never by which event fired:
  // Chromium fires popstate for a plain fragment link click too, so an event-order
  // discriminator calls every navigation a Back. An entry we have never stamped is
  // new (a push); one that comes back carrying its stamp is a traversal.
  //
  // currentIdx is THIS entry; highWater is the furthest entry we have handed out.
  // They differ after a Back, which is exactly why a re-stamp must write currentIdx:
  // stamping the high-water mark onto an earlier entry would give two entries the
  // same identity and merge their positions back together.
  let currentIdx = Math.max(entryIndex(win), 0);
  let highWater = currentIdx;
  stamp(win, currentIdx);

  const currentKey = () => posKey(currentIdx, currentHash);

  const remember = () => {
    const key = currentKey();
    positions.delete(key);
    positions.set(key, win.scrollY);
    while (positions.size > MAX_ENTRIES) {
      const oldest = positions.keys().next().value;
      if (oldest === undefined) break;
      positions.delete(oldest);
    }
  };

  // A push truncates the forward history, so every entry at or beyond the new index
  // is gone. Their remembered positions go with them: a future entry that happens to
  // land on the same index is a different place, and must not inherit one.
  const dropForwardOf = (idx: number) => {
    for (const key of [...positions.keys()]) {
      if (keyIndex(key) >= idx) positions.delete(key);
    }
  };

  /**
   * Apply `y` and keep applying it until the page has held it for a moment, the
   * deadline passes, or the user takes over. Scrolling once is not enough: the
   * entity body only exists after its request lands.
   */
  const restoreTo = (y: number) => {
    cancel?.();
    win.scrollTo(0, y);
    if (y <= 0) return;

    const deadline = Date.now() + SETTLE_MS;
    let timer: unknown = null;
    let heldSince = 0;
    const stop = () => {
      if (timer !== null) win.clearInterval(timer);
      timer = null;
      for (const t of USER_INTENT) win.removeEventListener(t, stop);
      cancel = null;
    };
    const attempt = () => {
      win.scrollTo(0, y);
      const now = Date.now();
      if (Math.abs(win.scrollY - y) > TOLERANCE) heldSince = 0;
      else if (heldSince === 0) heldSince = now;
      if ((heldSince && now - heldSince >= HOLD_MS) || now > deadline) stop();
    };
    cancel = stop;
    // Never fight the user: their own scroll wins over a pending restore.
    for (const t of USER_INTENT) win.addEventListener(t, stop, { passive: true });
    timer = win.setInterval(attempt, POLL_MS);
  };

  const onScroll = () => remember();

  const onHashChange = (e: Event) => {
    const prevKey = currentKey();
    const next = win.location.hash;
    const changed = next !== currentHash;
    currentHash = next;

    // The position we are LEAVING is whatever the last scroll event reported -- not
    // whatever scrollY says now. By the time this listener runs, an earlier listener
    // has already swapped the route, the outgoing page is gone and the offset has
    // been clamped to 0; reading it here would overwrite the user's real place with
    // that zero, and Back would land at the top of a page they had read halfway.

    // replaceHash() canonicalizes the URL in place and notifies listeners with a
    // BARE Event; a genuine hash navigation always carries a HashChangeEvent. A
    // canonicalization did not move the user, so neither do we -- the position just
    // moves with the URL that now names this page -- SAME entry, renamed. It does
    // call replaceState, which drops our stamp, so put that back.
    if (!('oldURL' in e)) {
      const held = positions.get(prevKey);
      if (changed && held !== undefined) { positions.delete(prevKey); positions.set(currentKey(), held); }
      stamp(win, currentIdx);
      return;
    }
    if (!changed) return;

    const idx = entryIndex(win);
    if (idx < 0) {
      // A history entry we have never seen: a deliberate navigation. It gets the
      // next index of its own and starts at the top of its new page, even when the
      // URL is one an earlier entry already shows.
      currentIdx = ++highWater;
      dropForwardOf(currentIdx);
      stamp(win, currentIdx);
      restoreTo(0);
    } else {
      currentIdx = idx;
      highWater = Math.max(highWater, idx);
      restoreTo(positions.get(currentKey()) ?? 0);
    }
  };

  const onPageHide = () => {
    remember();
    try {
      win.sessionStorage?.setItem(STORE_KEY, JSON.stringify(Object.fromEntries(positions)));
    } catch { /* storage full or blocked: the position is a nicety, not data */ }
  };

  const prior = win.history.scrollRestoration;
  try { win.history.scrollRestoration = 'manual'; } catch { /* not supported */ }

  win.addEventListener('scroll', onScroll, { passive: true });
  win.addEventListener('hashchange', onHashChange);
  win.addEventListener('pagehide', onPageHide);

  // A hard reload lands here with the URL already set and the page empty: this is
  // the case the browser cannot do for us. history.state survives the reload, so the
  // entry stamp identifies WHICH visit to this URL is being reloaded.
  restoreTo(positions.get(currentKey()) ?? 0);

  return () => {
    cancel?.();
    win.removeEventListener('scroll', onScroll);
    win.removeEventListener('hashchange', onHashChange);
    win.removeEventListener('pagehide', onPageHide);
    try { win.history.scrollRestoration = prior; } catch { /* not supported */ }
  };
}
