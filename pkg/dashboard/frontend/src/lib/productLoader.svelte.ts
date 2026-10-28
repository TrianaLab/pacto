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
import { decideViewState, snapshotKnowledge, type SnapshotKnowledge, type ViewState } from './knowledgeState.ts';

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

/** What a product list view renders, derived once from its loader. */
export interface ProductListState<T> {
  /** The answer to the current question, or the page on screen while only the page changed. */
  readonly data: T | null;
  /** Only the page changed and the next one is loading over the rows on screen. */
  readonly paging: boolean;
  readonly knowledge: SnapshotKnowledge;
  /** Rows in the current answer. */
  readonly count: number;
  readonly state: ViewState;
  /** A poll that failed over rows we can still show. decideViewState keeps the rows;
   *  this is the half that keeps it honest, so a frozen list never reads as a live one. */
  readonly refreshError: unknown;
  /** A poll in flight over data already on screen, for the header to say in words. */
  readonly revalidating: boolean;
  /** How many skeleton rows to draw: the shape of the rows last on screen, else a page. */
  readonly skeletonRows: number;
}

/**
 * productListState is the list-view half every product list shared word for word:
 * which answer to draw, its knowledge, and the view state decided from it.
 * `rows` picks the list's rows out of an answer; `query` reads the caller's reactive
 * question (`tag` and `family` as passed to `sync`), whether a filter is on and the page
 * size. Call it during component initialisation, like a rune.
 */
export function productListState<T extends { meta?: Parameters<typeof snapshotKnowledge>[0] }>(
  loader: ProductLoader<T>,
  rows: (data: T) => readonly unknown[] | null | undefined,
  query: () => { tag: unknown; family: unknown; filtered: boolean; pageLimit: number },
): ProductListState<T> {
  const q = $derived(query());
  const answer = $derived(loader.answer(q.tag, q.family));
  const knowledge = $derived(snapshotKnowledge(answer.data?.meta));
  const count = $derived((answer.data && rows(answer.data)?.length) || 0);
  const state = $derived(decideViewState({ loading: loader.loading, error: loader.error, itemCount: count, filtered: q.filtered, knowledge }));
  // `loader.data`, not the answer: the answer is nulled the instant the query changes,
  // which is exactly when the skeleton renders, and 0 rows is no shape at all.
  const skeletonRows = $derived((loader.data && rows(loader.data)?.length) || q.pageLimit);
  return {
    get data() { return answer.data; },
    get paging() { return answer.paging; },
    get knowledge() { return knowledge; },
    get count() { return count; },
    get state() { return state; },
    get refreshError() { return state.kind === 'ready' ? state.refreshError : null; },
    get revalidating() { return state.kind === 'ready' && !!state.revalidating; },
    get skeletonRows() { return skeletonRows; },
  };
}
