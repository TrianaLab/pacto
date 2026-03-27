/** Minimal hash router — returns a reactive route object. */

export function parseHash(hash) {
  const raw = (hash || '').replace(/^#\/?/, '');
  if (!raw || raw === '/') return { view: 'list', params: {} };

  // #/services/:name/diff
  const diffMatch = raw.match(/^services\/(.+)\/diff$/);
  if (diffMatch) return { view: 'diff', params: { name: decodeURIComponent(diffMatch[1]) } };

  // #/services/:name
  const svcMatch = raw.match(/^services\/(.+)$/);
  if (svcMatch) return { view: 'detail', params: { name: decodeURIComponent(svcMatch[1]) } };

  // #/graph
  if (raw === 'graph') return { view: 'graph', params: {} };

  return { view: 'list', params: {} };
}

export function navigate(view, params = {}) {
  let hash = '#/';
  if (view === 'detail' && params.name) hash = `#/services/${encodeURIComponent(params.name)}`;
  else if (view === 'diff' && params.name) hash = `#/services/${encodeURIComponent(params.name)}/diff`;
  else if (view === 'graph') hash = '#/graph';
  location.hash = hash;
}

export function serviceUrl(name) {
  return `#/services/${encodeURIComponent(name)}`;
}

export function diffUrl(name) {
  return `#/services/${encodeURIComponent(name)}/diff`;
}
