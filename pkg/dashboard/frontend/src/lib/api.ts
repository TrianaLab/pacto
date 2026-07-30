/** Pacto Dashboard API client — thin typed wrapper over fetch. */

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function request(method: string, path: string, body?: unknown): Promise<unknown> {
  const staticData = (globalThis as any).__PACTO_STATIC__;
  if (staticData) {
    if (path in staticData.routes) return staticData.routes[path];
    // Unknown paths (health/metrics/sources polling) resolve empty so the offline app stays quiet.
    return path === '/api/services' ? [] : null;
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
  if (res.status === 204) return null;
  return res.json();
}

const get = (path: string): Promise<unknown> => request('GET', path);
const post = (path: string, body?: unknown): Promise<unknown> => request('POST', path, body);

export const api = {
  health: () => get('/health'),
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
};
