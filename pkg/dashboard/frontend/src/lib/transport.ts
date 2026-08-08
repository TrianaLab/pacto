/**
 * Transport seam for the generated dashboard SDK.
 *
 * There is exactly ONE fetch implementation in the frontend, and it lives here.
 * The generated openapi-fetch client is configured with `dashboardFetch`, so every
 * backend call - live HTTP, the in-browser WASM demo, and the static `pacto doc`
 * export - flows through the same generated operation definitions and the same
 * request/response types (ADR-6, requirement, item 5).
 *
 *   generated SDK (openapi-fetch, typed from OpenAPI)
 *        -> dashboardFetch (this seam)
 *             -> static route table   (globalThis.__PACTO_STATIC__)
 *             -> real fetch()         (live HTTP; the WASM demo shims window.fetch)
 *
 * The static branch matches by request PATHNAME, not the raw URL string, so query
 * ordering never breaks a lookup (requirement, item 5). A product endpoint that is
 * not fixtured resolves to a null body, which the product facade rejects with a
 * SchemaCompatibilityError rather than handing the UI a misleading typed value.
 */

import createClient from 'openapi-fetch';
import type { paths } from './generated/schema';

interface StaticExport {
  routes: Record<string, unknown>;
  service?: string;
}

function staticExport(): StaticExport | undefined {
  return (globalThis as unknown as { __PACTO_STATIC__?: StaticExport }).__PACTO_STATIC__;
}

const JSON_HEADERS = { 'content-type': 'application/json' } as const;

/**
 * staticResponse answers a request from the static export's fixtured route table,
 * keyed by pathname so query-parameter ordering is irrelevant. Fixtured routes
 * return their data; the services list degrades to an empty array so the offline
 * app stays quiet; every other route resolves to a null body (the established
 * offline-degradation contract the static single-service export relies on).
 */
function staticResponse(data: StaticExport, url: string): Response {
  const pathname = new URL(url, 'http://static.local').pathname;
  if (pathname in data.routes) {
    return new Response(JSON.stringify(data.routes[pathname]), { status: 200, headers: JSON_HEADERS });
  }
  const body = pathname === '/api/services' ? '[]' : 'null';
  return new Response(body, { status: 200, headers: JSON_HEADERS });
}

/** dashboardFetch is the single transport seam (see file header). */
export async function dashboardFetch(input: Request): Promise<Response> {
  const data = staticExport();
  if (data) {
    return staticResponse(data, input.url);
  }
  return fetch(input);
}

/**
 * baseUrl gives the generated client a VALID absolute origin for Request
 * construction in every environment: the page origin for live HTTP and the WASM
 * demo (same-origin, so requests hit the right server and the demo's window.fetch
 * shim still matches by pathname), and a sentinel for the file:// static export
 * (origin "null") where dashboardFetch short-circuits and only the pathname
 * matters. It never changes which server a live request reaches.
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
