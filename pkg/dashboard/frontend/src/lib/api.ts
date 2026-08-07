/** Pacto Dashboard API client — thin typed wrapper over fetch. */

import type {
  ProductOverview,
  ProductEntityList,
  ProductEntityDetail,
  ProductNeighborhood,
  ProductAttentionList,
  ProductImpact,
  ProductMeta,
} from './productTypes';
import { checkProductSchema } from './productTypes';

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function request<T = unknown>(method: string, path: string, body?: unknown): Promise<T> {
  const staticData = (globalThis as any).__PACTO_STATIC__;
  if (staticData) {
    if (path in staticData.routes) return staticData.routes[path] as T;
    // Unknown paths (health/metrics/sources polling) resolve empty so the offline app stays quiet.
    return (path === '/api/services' ? [] : null) as T;
  }

  const opts: RequestInit = { method, headers: {} };
  if (body !== undefined) {
    (opts.headers as Record<string, string>)['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(path, opts);
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText);
    let msg = text;
    try { msg = JSON.parse(text).detail || JSON.parse(text).title || text; } catch { /* use raw text */ }
    throw new ApiError(res.status, msg);
  }
  if (res.status === 204) return null as T;
  return (await res.json()) as T;
}

const get = <T = unknown>(path: string): Promise<T> => request<T>('GET', path);
const post = <TRequest = unknown, TResponse = unknown>(path: string, body?: TRequest): Promise<TResponse> =>
  request<TResponse>('POST', path, body);

/**
 * productGet/productPost fetch a product response and validate its schema version
 * at the client boundary, so an unsupported server version raises a typed,
 * actionable SchemaCompatibilityError BEFORE the UI consumes the data.
 */
async function productGet<T extends { meta: ProductMeta }>(path: string): Promise<T> {
  const res = await get<T>(path);
  checkProductSchema(res?.meta);
  return res;
}
async function productPost<TRequest, T extends { meta: ProductMeta }>(path: string, body: TRequest): Promise<T> {
  const res = await post<TRequest, T>(path, body);
  checkProductSchema(res?.meta);
  return res;
}

export const api = {
  health: () => get('/health'),
  capabilities: () => get('/api/capabilities'),
  sources: () => get('/api/sources'),
  services: () => get('/api/services'),
  service: (name: string) => get(`/api/services/${encodeURIComponent(name)}`),
  serviceAtVersion: (name: string, version: string) =>
    get(`/api/services/${encodeURIComponent(name)}/versions/${encodeURIComponent(version)}`),
  versions: (name: string) => get(`/api/services/${encodeURIComponent(name)}/versions`),
  serviceSources: (name: string) => get(`/api/services/${encodeURIComponent(name)}/sources`),
  dependents: (name: string) => get(`/api/services/${encodeURIComponent(name)}/dependents`),
  crossRefs: (name: string) => get(`/api/services/${encodeURIComponent(name)}/refs`),
  graph: () => get('/api/graph'),
  serviceGraph: (name: string) => get(`/api/services/${encodeURIComponent(name)}/graph`),
  diff: (fromName: string, fromVersion: string, toName: string, toVersion: string) =>
    get(`/api/diff?from_name=${encodeURIComponent(fromName)}&from_version=${encodeURIComponent(fromVersion || '')}&to_name=${encodeURIComponent(toName)}&to_version=${encodeURIComponent(toVersion || '')}`),
  resolve: (ref: string, compatibility?: string) => post('/api/resolve', { ref, compatibility }),
  remoteVersions: (ref: string, fetchAll?: boolean) => post('/api/versions', { ref, fetch: fetchAll }),
  refresh: () => post('/api/refresh'),
  debugSources: () => get('/api/debug/sources'),

  // ── Operational graph (fleet) ──
  fleetSnapshot: () => get('/api/fleet/snapshot'),
  fleetServices: (
    params: {
      text?: string; owner?: string; scope?: string; status?: string;
      source?: string; limit?: number; offset?: number;
    } = {},
  ) => {
    const qs = new URLSearchParams();
    if (params.text) qs.set('text', params.text);
    if (params.owner) qs.set('owner', params.owner);
    if (params.scope) qs.set('scope', params.scope);
    if (params.status) qs.set('status', params.status);
    if (params.source) qs.set('source', params.source);
    if (params.limit != null) qs.set('limit', String(params.limit));
    if (params.offset != null) qs.set('offset', String(params.offset));
    const str = qs.toString();
    return get(`/api/fleet/services${str ? `?${str}` : ''}`);
  },
  fleetServiceGraph: (
    name: string,
    opts: { direction?: string; transitive?: boolean; maxDepth?: number } = {},
  ) => {
    const qs = new URLSearchParams();
    if (opts.direction) qs.set('direction', opts.direction);
    if (opts.transitive != null) qs.set('transitive', String(opts.transitive));
    if (opts.maxDepth != null) qs.set('maxDepth', String(opts.maxDepth));
    const str = qs.toString();
    return get(`/api/fleet/services/${encodeURIComponent(name)}/graph${str ? `?${str}` : ''}`);
  },
  fleetStatus: () => get('/api/fleet/status'),
  // Bounded lazy detail keyed by the domain-qualified ServiceKey / TargetKey.
  fleetService: (key: string) => get(`/api/fleet/service?key=${encodeURIComponent(key)}`),
  fleetTarget: (key: string) => get(`/api/fleet/target?key=${encodeURIComponent(key)}`),
  fleetImpact: (oldRef: string, newRef: string, includeObserved?: boolean) =>
    get(`/api/fleet/impact?old=${encodeURIComponent(oldRef)}&new=${encodeURIComponent(newRef)}&includeObserved=${includeObserved ? 'true' : 'false'}`),

  // ── Product-oriented operational-graph APIs (requirement 2) ──
  // These are the primary dashboard contract: bounded, versioned, strongly typed
  // answers built for product questions. Each carries canonical hrefs and is
  // schema-version-validated at the client boundary.
  fleetOverview: (): Promise<ProductOverview> => productGet<ProductOverview>('/api/fleet/overview'),
  fleetEntities: (
    params: {
      text?: string; kinds?: string[]; owner?: string; domain?: string;
      scope?: string; status?: string; source?: string; limit?: number; offset?: number;
    } = {},
  ): Promise<ProductEntityList> => {
    const qs = new URLSearchParams();
    if (params.text) qs.set('text', params.text);
    if (params.kinds && params.kinds.length) qs.set('kinds', params.kinds.join(','));
    if (params.owner) qs.set('owner', params.owner);
    if (params.domain) qs.set('domain', params.domain);
    if (params.scope) qs.set('scope', params.scope);
    if (params.status) qs.set('status', params.status);
    if (params.source) qs.set('source', params.source);
    if (params.limit != null) qs.set('limit', String(params.limit));
    if (params.offset != null) qs.set('offset', String(params.offset));
    const str = qs.toString();
    return productGet<ProductEntityList>(`/api/fleet/entities${str ? `?${str}` : ''}`);
  },
  fleetEntityDetail: (kind: string, key: string): Promise<ProductEntityDetail> =>
    productGet<ProductEntityDetail>(`/api/fleet/entities/${encodeURIComponent(kind)}?key=${encodeURIComponent(key)}`),
  fleetNeighborhood: (
    params: {
      kind: string; key: string; direction?: string; depth?: number;
      views?: string[]; maxNodes?: number; maxEdges?: number;
    },
  ): Promise<ProductNeighborhood> => {
    const qs = new URLSearchParams();
    qs.set('kind', params.kind);
    qs.set('key', params.key);
    if (params.direction) qs.set('direction', params.direction);
    if (params.depth != null) qs.set('depth', String(params.depth));
    if (params.views && params.views.length) qs.set('views', params.views.join(','));
    if (params.maxNodes != null) qs.set('maxNodes', String(params.maxNodes));
    if (params.maxEdges != null) qs.set('maxEdges', String(params.maxEdges));
    return productGet<ProductNeighborhood>(`/api/fleet/neighborhood?${qs.toString()}`);
  },
  fleetAttention: (
    params: {
      category?: string; kind?: string; key?: string; service?: string; owner?: string;
      source?: string; severity?: string; status?: string; staleOnly?: boolean; limit?: number; offset?: number;
    } = {},
  ): Promise<ProductAttentionList> => {
    const qs = new URLSearchParams();
    if (params.category) qs.set('category', params.category);
    if (params.kind) qs.set('kind', params.kind);
    if (params.key) qs.set('key', params.key);
    if (params.service) qs.set('service', params.service);
    if (params.owner) qs.set('owner', params.owner);
    if (params.source) qs.set('source', params.source);
    if (params.severity) qs.set('severity', params.severity);
    if (params.status) qs.set('status', params.status);
    if (params.staleOnly) qs.set('staleOnly', 'true');
    if (params.limit != null) qs.set('limit', String(params.limit));
    if (params.offset != null) qs.set('offset', String(params.offset));
    const str = qs.toString();
    return productGet<ProductAttentionList>(`/api/fleet/attention${str ? `?${str}` : ''}`);
  },
  // Contextual impact by canonical identity (requirement 2.6): the snapshot id is
  // sent so the server rejects analyzing a state the user is no longer viewing.
  fleetImpactByIdentity: (body: {
    snapshotId?: string; serviceKey?: string; fromRevisionKey: string;
    toRevisionKey: string; includeObserved?: boolean; limit?: number; offset?: number;
  }): Promise<ProductImpact> => productPost<typeof body, ProductImpact>('/api/fleet/impact', body),
};
