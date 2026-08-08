/**
 * A tiny reusable product-list loader (requirement E).
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
  /** Run the fetcher iff key changed since the last sync (dedupes the initial load). */
  sync(key: string): void;
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
  // generation is a plain (non-reactive) monotonic counter: only the newest request's
  // response may touch the reactive state. It is advanced on every run and on destroy.
  let generation = 0;
  let lastKey: string | null = null;

  function run(): void {
    const gen = ++generation;
    loading = true;
    error = null;
    fetcher()
      .then((res) => { if (gen === generation) data = res; })
      .catch((e) => { if (gen === generation) error = e; })
      .finally(() => { if (gen === generation) loading = false; });
  }

  return {
    get data() { return data; },
    get loading() { return loading; },
    get error() { return error; },
    sync(key: string): void {
      if (key !== lastKey) {
        lastKey = key;
        run();
      }
    },
    refresh(): void { run(); },
    destroy(): void { generation++; },
  };
}
