import { api, type EntityKind, type ProductEntityRef } from './api.ts';

// The search mechanics every entity-search surface needs, in one place: a debounced
// backend Entities query, a bounded result set and a generation guard so a response
// from an abandoned search can never repopulate the UI.
//
// It exists because there are now two of those surfaces -- the global palette and the
// inline filter comboboxes on the Services workspace -- and a second hand-rolled
// debounce-plus-counter is exactly how one of them ends up with the A4 stale-request
// race the other already fixed. Presentation (modal vs combobox) stays with the
// component; only the mechanics are shared.

export interface EntitySuggestOptions {
  /** Restrict the query to these entity kinds; omit to search everything. */
  kinds?: EntityKind[];
  /** Upper bound on results asked of the backend. */
  limit?: number;
  /** Quiet period before a keystroke becomes a request. */
  debounceMs?: number;
}

export interface EntitySuggest {
  readonly results: ProductEntityRef[];
  readonly total: number;
  readonly truncated: boolean;
  readonly loading: boolean;
  readonly error: unknown;
  /** Debounced search. An empty/blank text clears immediately and cancels in flight. */
  search(text: string): void;
  /** Clear results and invalidate anything in flight, without issuing a request. */
  clear(): void;
  /** Invalidate in-flight responses so they can never write to torn-down state. */
  destroy(): void;
}

export function createEntitySuggest(opts: EntitySuggestOptions = {}): EntitySuggest {
  const { kinds, limit = 25, debounceMs = 140 } = opts;

  let results = $state<ProductEntityRef[]>([]);
  let total = $state(0);
  let truncated = $state(false);
  let loading = $state(false);
  let error = $state<unknown>(null);

  // The active-search generation. A response may touch the state only while its
  // generation still matches. Advanced on EVERY transition that ends the current
  // search -- a changed query, a clear and destroy -- so out-of-order responses are
  // dropped rather than racing each other into the UI. Deliberately not reactive.
  let seq = 0;
  let timer: ReturnType<typeof setTimeout> | null = null;

  function reset() {
    results = [];
    total = 0;
    truncated = false;
    error = null;
  }

  function clear() {
    seq++;
    if (timer) clearTimeout(timer);
    timer = null;
    loading = false;
    reset();
  }

  function search(text: string) {
    const q = (text ?? '').trim();
    if (timer) clearTimeout(timer);
    const mySeq = ++seq;
    if (!q) { timer = null; loading = false; reset(); return; }
    loading = true;
    timer = setTimeout(() => {
      api.fleetEntities({ text: q, limit, ...(kinds ? { kinds } : {}) })
        .then((res) => {
          if (mySeq !== seq) return;
          results = res.entities ?? [];
          total = res.total ?? results.length;
          truncated = total > results.length;
          error = null;
        })
        .catch((e: unknown) => {
          if (mySeq !== seq) return;
          error = e;
          results = [];
          total = 0;
          truncated = false;
        })
        .finally(() => { if (mySeq === seq) loading = false; });
    }, debounceMs);
  }

  return {
    get results() { return results; },
    get total() { return total; },
    get truncated() { return truncated; },
    get loading() { return loading; },
    get error() { return error; },
    search,
    clear,
    destroy() { clear(); },
  };
}
