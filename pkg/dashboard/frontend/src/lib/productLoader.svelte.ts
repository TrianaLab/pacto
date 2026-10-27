/**
 * A tiny reusable product-list loader.
 *
 * Every product-list view (Services, Attention, Owners, Sources) shared two defects:
 *
 *  1. a DUPLICATE initial request -- `onMount(load)` plus a reactive effect that also
 *     called `load`, so the first render fired the backend query twice; and
 *  2. an unguarded STALE-RESPONSE race -- overlapping URL/filter/refresh requests had
 *     no ordering protection, so an older in-flight response could overwrite a newer
 *     route/filter state (request A starts, navigation changes to B, B renders, then
 *     A resolves and clobbers B).
 *
 * This loader removes both with one mechanism, mirroring EntitySearch's generation
 * token. `sync(key)` runs the fetcher only when the key CHANGES, so mount plus the
 * first reactive run collapse to ONE logical request. A monotonic generation token
 * guards every write: a response applies its data/error/loading transition only while
 * it is still the newest request, so navigation, a filter change or a refresh can
 * never be overwritten by a request that started earlier. `destroy()` invalidates any
 * pending response on teardown so it can never write to torn-down state.
 *
 * The fetcher is a closure over the caller's reactive params; it is invoked at request
 * time, so it always reads the CURRENT param values.
 */
export interface ProductLoader<T> {
  readonly data: T | null;
  readonly loading: boolean;
  readonly error: unknown;
  /** The `tag` of the request that produced the CURRENT data. The loader deliberately
   *  keeps the previous data while the next request is in flight, so a refresh does not
   *  blank the screen -- but a caller whose key encodes more than a refresh (a different
   *  question entirely) needs to know which question the data on hand answers. */
  readonly dataTag: unknown;
  /** Run the fetcher iff key changed since the last sync (dedupes the initial load).
   *  `tag` is recorded against the resulting data and exposed as `dataTag`; `family`
   *  is the same question without its page, so `answer` can tell a page turn apart. */
  sync(key: string, tag?: unknown, family?: unknown): void;
  /** What to draw for the question `tag` (of list `family`): its own answer, or -- while
   *  only the page changed -- the page on screen, with `paging` set so the view can say
   *  the next one is loading. A user decision: turning a page keeps the rows in place
   *  instead of flashing a skeleton. A failed page drops them, so the error shows. */
  answer(tag: unknown, family: unknown): { data: T | null; paging: boolean };
  /** Force a reload under a fresh generation (e.g. an explicit retry), still guarded
   *  so it cannot overwrite a subsequent navigation. */
  refresh(): void;
  /** Abandon any in-flight response (call on component destroy). */
  destroy(): void;
}

export function createProductLoader<T>(fetcher: () => Promise<T>): ProductLoader<T> {
  let data = $state<T | null>(null);
  let loading = $state(true);
  let error = $state<unknown>(null);
  // Written in the SAME synchronous step as `data`, so a consumer never sees new data
  // under the previous tag (or vice versa) for even one render.
  let dataTag = $state<unknown>(undefined);
  let dataFamily = $state<unknown>(undefined);
  // generation is a plain (non-reactive) monotonic counter: only the newest request's
  // response may touch the reactive state. It is advanced on every run and on destroy.
  let generation = 0;
  let lastKey: string | null = null;
  let lastTag: unknown = undefined;
  let lastFamily: unknown = undefined;

  function run(tag: unknown, family: unknown): void {
    const gen = ++generation;
    loading = true;
    error = null;
    fetcher()
      .then((res) => { if (gen === generation) { data = res; dataTag = tag; dataFamily = family; } })
      .catch((e) => { if (gen === generation) error = e; })
      .finally(() => { if (gen === generation) loading = false; });
  }

  return {
    get data() { return data; },
    get loading() { return loading; },
    get error() { return error; },
    get dataTag() { return dataTag; },
    sync(key: string, tag?: unknown, family?: unknown): void {
      if (key !== lastKey) {
        lastKey = key;
        lastTag = tag;
        lastFamily = family;
        run(tag, family);
      }
    },
    answer(tag: unknown, family: unknown) {
      if (dataTag === tag) return { data, paging: false };
      const paging = data !== null && family !== undefined && dataFamily === family && error == null;
      return { data: paging ? data : null, paging };
    },
    refresh(): void { run(lastTag, lastFamily); },
    destroy(): void { generation++; },
  };
}
