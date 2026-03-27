class ApiError extends Error {
  constructor(message, status) {
    super(message);
    this.status = status;
  }
}

async function get(path) {
  const r = await fetch('/api' + path);
  if (!r.ok) throw new ApiError('API ' + r.status, r.status);
  return r.json();
}

async function post(path, body) {
  const r = await fetch('/api' + path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!r.ok) {
    const data = await r.json().catch(() => null);
    const msg = data?.detail || data?.title || 'API ' + r.status;
    throw new ApiError(msg, r.status);
  }
  return r.json();
}

export const api = {
  listServices: () => get('/services'),
  getService: (name) => get('/services/' + encodeURIComponent(name)),
  getVersions: (name) => get('/services/' + encodeURIComponent(name) + '/versions'),
  getServiceSources: (name) => get('/services/' + encodeURIComponent(name) + '/sources'),
  getDependents: (name) => get('/services/' + encodeURIComponent(name) + '/dependents'),
  getGraph: () => get('/graph'),
  getServiceGraph: (name) => get('/services/' + encodeURIComponent(name) + '/graph'),
  getSources: () => get('/sources'),
  getCrossRefs: (name) => get('/services/' + encodeURIComponent(name) + '/refs'),
  getDiff: (from, to) =>
    get(
      '/diff?from_name=' + encodeURIComponent(from.name) +
      '&from_version=' + encodeURIComponent(from.version) +
      '&to_name=' + encodeURIComponent(to.name) +
      '&to_version=' + encodeURIComponent(to.version)
    ),
  getDebugSources: () => get('/debug/sources'),
  getHealth: () => fetch('/health').then((r) => r.json()),
  resolveRef: (ref, compatibility) => {
    const payload = { ref };
    if (compatibility) payload.compatibility = compatibility;
    return post('/resolve', payload);
  },
  listRemoteVersions: (ref, fetchAll) =>
    post('/versions', { ref, fetch: !!fetchAll }),
};
