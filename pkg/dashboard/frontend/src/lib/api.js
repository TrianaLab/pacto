/** Pacto Dashboard API client — thin typed wrapper over fetch. */

class ApiError extends Error {
  constructor(status, message) {
    super(message);
    this.status = status;
  }
}

async function request(method, path, body) {
  const opts = { method, headers: {} };
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(path, opts);
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText);
    let msg = text;
    try { msg = JSON.parse(text).detail || JSON.parse(text).title || text; } catch {}
    throw new ApiError(res.status, msg);
  }
  if (res.status === 204) return null;
  return res.json();
}

const get = (path) => request('GET', path);
const post = (path, body) => request('POST', path, body);

export const api = {
  health: () => get('/health'),
  sources: () => get('/api/sources'),
  services: () => get('/api/services'),
  service: (name) => get(`/api/services/${encodeURIComponent(name)}`),
  versions: (name) => get(`/api/services/${encodeURIComponent(name)}/versions`),
  serviceSources: (name) => get(`/api/services/${encodeURIComponent(name)}/sources`),
  dependents: (name) => get(`/api/services/${encodeURIComponent(name)}/dependents`),
  crossRefs: (name) => get(`/api/services/${encodeURIComponent(name)}/refs`),
  graph: () => get('/api/graph'),
  serviceGraph: (name) => get(`/api/services/${encodeURIComponent(name)}/graph`),
  diff: (fromName, fromVersion, toName, toVersion) =>
    get(`/api/diff?from_name=${encodeURIComponent(fromName)}&from_version=${encodeURIComponent(fromVersion || '')}&to_name=${encodeURIComponent(toName)}&to_version=${encodeURIComponent(toVersion || '')}`),
  resolve: (ref, compatibility) => post('/api/resolve', { ref, compatibility }),
  remoteVersions: (ref, fetch) => post('/api/versions', { ref, fetch }),
  debugSources: () => get('/api/debug/sources'),
};
