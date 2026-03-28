/** Pacto Dashboard API client — thin typed wrapper over fetch. */

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function request(method: string, path: string, body?: unknown): Promise<unknown> {
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
};
