/**
 * The current hash route as reactive state, so a list the client holds in full
 * can keep its page, page size and order in the URL and Back returns to them.
 */
import { hashWith, urlInt } from './paging.ts';

let current = $state(typeof location === 'undefined' ? '' : location.hash);
if (typeof window !== 'undefined') window.addEventListener('hashchange', () => { current = location.hash; });

/** hashParam reads one query value of the current hash route ('' when absent). */
export function hashParam(key: string): string {
  const i = current.indexOf('?');
  return i === -1 ? '' : new URLSearchParams(current.slice(i + 1)).get(key) ?? '';
}

/**
 * listState is the page a client-held list is on, read from the URL. prefix
 * keeps two lists on one page apart. href builds the link to another page or
 * page size, leaving a default size out of the URL.
 */
export function listState(defaultLimit: number, prefix = '') {
  const offset = urlInt(hashParam(`${prefix}offset`));
  const limit = urlInt(hashParam(`${prefix}limit`)) || defaultLimit;
  const href = (o: number, l: number) => hashWith(current, {
    [`${prefix}offset`]: o,
    [`${prefix}limit`]: l === defaultLimit ? null : l,
  });
  return { offset, limit, href };
}

/** setHashParams writes query keys onto the current route (one history entry). */
export function setHashParams(patch: Record<string, string | number | null | undefined>): void {
  location.hash = hashWith(location.hash, patch);
}
