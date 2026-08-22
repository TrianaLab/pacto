/**
 * Transport seam for the generated dashboard SDK.
 *
 * There is exactly ONE fetch implementation in the frontend, and it lives here.
 * The generated openapi-fetch client is configured with `dashboardFetch`, so every
 * backend call - live HTTP, the in-browser WASM demo, and the static `pacto doc`
 * export - flows through the same generated operation definitions and the same
 * request/response types (ADR-6).
 *
 *   generated SDK (openapi-fetch, typed from OpenAPI)
 *        -> dashboardFetch (this seam)
 *             -> static route table   (globalThis.__PACTO_STATIC__)
 *             -> real fetch()         (live HTTP; the WASM demo shims window.fetch)
 *
 * The static branch matches a request by its SEMANTICS - method, pathname,
 * normalized query parameters (order-independent) and, when a fixture is
 * body-sensitive, its request body - never by a raw URL string (requirement,
 * item 1). An operation with no matching fixture fails honestly with an HTTP 501
 * the facade turns into an ApiError, rather than a misleading 200 + null. Any quiet
 * offline degradation a static export wants (e.g. an empty services list, a null
 * cross-references answer) is an EXPLICIT fixture, not a universal fallback.
 */

import createClient from 'openapi-fetch';
import type { paths } from './generated/schema';

/**
 * StaticRoute identifies the request semantics a fixture represents: the HTTP
 * method, the pathname, the normalized query parameters (order-independent), and -
 * only when the operation is body-sensitive - the request body. A fixture with no
 * `query` matches only a request with no query params; a fixture with no `body`
 * matches regardless of body (a body-insensitive operation).
 */
interface StaticRoute {
  method: string;
  path: string;
  query?: Record<string, string>;
  body?: unknown;
  response: unknown;
}

interface StaticExport {
  routes: StaticRoute[];
  service?: string;
}

function staticExport(): StaticExport | undefined {
  return (globalThis as unknown as { __PACTO_STATIC__?: StaticExport }).__PACTO_STATIC__;
}

const JSON_HEADERS = { 'content-type': 'application/json' } as const;

/** queryParams collapses a URL's search params into a plain record (last wins). */
function queryParams(url: URL): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of url.searchParams) out[k] = v;
  return out;
}

/** sameQuery compares two query records order-independently. */
function sameQuery(a: Record<string, string>, b: Record<string, string>): boolean {
  const ak = Object.keys(a);
  if (ak.length !== Object.keys(b).length) return false;
  return ak.every((k) => a[k] === b[k]);
}

/** canon recursively sorts object keys so body comparison is key-order-independent. */
function canon(v: unknown): unknown {
  if (Array.isArray(v)) return v.map(canon);
  if (v && typeof v === 'object') {
    const o = v as Record<string, unknown>;
    return Object.fromEntries(Object.keys(o).sort().map((k) => [k, canon(o[k])]));
  }
  return v;
}

function deepEqual(a: unknown, b: unknown): boolean {
  return JSON.stringify(canon(a)) === JSON.stringify(canon(b));
}

function safeParse(body: string): unknown {
  if (body === '') return undefined;
  try {
    return JSON.parse(body);
  } catch {
    return body;
  }
}

/**
 * staticResponse answers a request from the static export's request-semantic route
 * table. A fixture matches when its method, pathname and normalized query all match
 * and - when the fixture is body-sensitive - its body deep-equals the request body.
 * An unmatched operation returns HTTP 501 so the facade fails honestly instead of
 * handing the UI a misleading 200 + null.
 */
function staticResponse(data: StaticExport, req: Request, body: string): Response {
  const url = new URL(req.url, 'http://static.local');
  const method = req.method.toUpperCase();
  const query = queryParams(url);
  const parsedBody = safeParse(body);
  const match = data.routes.find(
    (r) =>
      r.method.toUpperCase() === method &&
      r.path === url.pathname &&
      sameQuery(r.query ?? {}, query) &&
      (r.body === undefined || deepEqual(r.body, parsedBody)),
  );
  if (match) {
    return new Response(JSON.stringify(match.response), { status: 200, headers: JSON_HEADERS });
  }
  return new Response(
    JSON.stringify({ title: 'Unsupported operation', detail: `no static fixture for ${method} ${url.pathname}` }),
    { status: 501, headers: JSON_HEADERS },
  );
}

/** dashboardFetch is the single transport seam (see file header). */
export async function dashboardFetch(input: Request): Promise<Response> {
  const data = staticExport();
  if (data) {
    const method = input.method.toUpperCase();
    const body = method === 'GET' || method === 'HEAD' ? '' : await input.clone().text();
    return staticResponse(data, input, body);
  }
  return fetch(input);
}

/**
 * baseUrl gives the generated client a VALID absolute origin for Request
 * construction in every environment: the page origin for live HTTP and the WASM
 * demo (same-origin, so requests hit the right server and the demo's window.fetch
 * shim still matches by pathname), and a sentinel for the file:// static export
 * (origin "null") where dashboardFetch short-circuits and only the request
 * semantics matter. It never changes which server a live request reaches.
 */
function baseUrl(): string {
  const origin = (globalThis as { location?: { origin?: string } }).location?.origin;
  return origin && origin !== 'null' ? origin : 'http://pacto.local';
}

/**
 * client is the generated, strongly typed dashboard API client. It owns every
 * backend URL, query serialization and request body from the OpenAPI contract;
 * handwritten code never reconstructs any of them.
 */
export const client = createClient<paths>({ baseUrl: baseUrl(), fetch: dashboardFetch });
