/**
 * Resolving a legacy display NAME to ONE canonical entity over a BOUNDED page.
 *
 * The Product entities API answers with a bounded page, and a page that truncated is
 * not the population (Fleet.EntityList.truncated). Two claims a caller draws from such
 * a page are therefore unsound unless the page WAS the whole match set: "nothing here
 * is named this" — an exact match may sit on a page nobody read — and "exactly one
 * thing is named this", because so may a second, and canonicalizing to the first
 * silently opens the wrong entity. Both legacy-URL migrations (the redirect view and
 * the Change analysis picker) have to obey that rule identically, so it lives here.
 */
import type { ProductEntityList, ProductEntityRef } from './api.ts';

/** The Product entities API's per-page maximum (fleet.MaxEntityLimit). A name lookup
 *  asks for it outright, so the page it reads is the whole match set for any realistic
 *  fleet — and when it is not, [BoundedMatches.complete] says so instead of guessing. */
export const NAME_LOOKUP_LIMIT = 500;

export interface BoundedMatches {
  /** The matching entities ON THE PAGE that was read. */
  matches: ProductEntityRef[];
  /** True when the page carried every match, so `matches` can be reasoned about as
   *  the complete set: none means absent, exactly one means unique. */
  complete: boolean;
}

/**
 * boundedMatches picks the entities matching `match` out of one bounded entity page and
 * reports whether that page was the whole match set. A caller may only conclude "absent"
 * or "unique" from the result when `complete` is true.
 */
export function boundedMatches(
  page: ProductEntityList | null | undefined,
  match: (e: ProductEntityRef) => boolean,
): BoundedMatches {
  return {
    matches: (page?.entities ?? []).filter(match),
    complete: !page?.truncated,
  };
}
