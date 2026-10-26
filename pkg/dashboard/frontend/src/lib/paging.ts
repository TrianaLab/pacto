/**
 * The one paging model every long list shares: the server answers a page
 * (total, offset, limit) and the list keeps offset, limit and its filter in
 * the URL. Pager draws it; these are the numbers behind it.
 */

/** A page as the server describes it (delivery Page, fleet lists alike). */
export interface PageWire {
  total: number;
  offset: number;
  limit: number;
}

/** What a pager shows, and where each of its links goes (null: none). */
export interface PageView {
  from: number;
  to: number;
  total: number;
  limit: number;
  page: number;
  pages: number;
  first: number | null;
  prev: number | null;
  next: number | null;
  last: number | null;
}

/** pageView reads a server page: the range shown, page X of Y and the offsets to move to. */
export function pageView(p: PageWire | null | undefined): PageView {
  const total = Math.max(0, p?.total ?? 0);
  const limit = Math.max(1, p?.limit ?? 1);
  const offset = Math.max(0, p?.offset ?? 0);
  const pages = Math.max(1, Math.ceil(total / limit));
  const page = Math.min(pages, Math.floor(offset / limit) + 1);
  const lastOffset = (pages - 1) * limit;
  return {
    from: total === 0 ? 0 : offset + 1,
    to: Math.min(offset + limit, total),
    total, limit, page, pages,
    first: offset > 0 ? 0 : null,
    prev: offset > 0 ? Math.max(0, offset - limit) : null,
    next: offset + limit < total ? offset + limit : null,
    last: offset < lastOffset ? lastOffset : null,
  };
}

/** offsetOfPage is where page n (1-based, clamped to 1..pages) starts. */
export function offsetOfPage(n: number, pages: number, limit: number): number {
  const page = Math.min(Math.max(1, Math.trunc(n) || 1), Math.max(1, pages));
  return (page - 1) * limit;
}

/** The page sizes a reader may pick, with the current one always among them. */
export function pageSizes(limit: number, sizes: number[] = [25, 50, 100]): number[] {
  return [...new Set([...sizes, limit])].filter((n) => n > 0).sort((a, b) => a - b);
}

/** A non-negative whole number read from a URL value; 0 when it is not one. */
export function urlInt(v: string | number | null | undefined): number {
  const n = Math.trunc(Number(v));
  return Number.isFinite(n) && n > 0 ? n : 0;
}

/**
 * pagedHash builds a hash route with its query, leaving out what is empty or
 * zero, so page 1 with no filter is the bare route.
 */
export function pagedHash(path: string, params: Record<string, string | number | null | undefined>): string {
  const qs = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== '' && v !== 0) qs.set(k, String(v));
  }
  const s = qs.toString();
  return s ? `${path}?${s}` : path;
}

/**
 * limitParam is the limit a link carries: none when the URL named none and
 * the page size is the list's default, so a default page has a clean URL.
 */
export function limitParam(wanted: number, urlLimit: string | number | undefined, defaultLimit: number): number | undefined {
  return urlInt(urlLimit) || wanted !== defaultLimit ? wanted : undefined;
}
