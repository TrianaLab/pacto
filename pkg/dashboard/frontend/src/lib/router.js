/** Minimal hash router — returns a reactive route object. */

export function parseHash(hash) {
  const raw = (hash || '').replace(/^#\/?/, '');
  if (!raw || raw === '/') return { view: 'list', params: {} };

  // #/services/:name/diff?from=X&to=Y
  const diffMatch = raw.match(/^services\/(.+?)\/diff(?:\?(.*))?$/);
  if (diffMatch) {
    const params = { name: decodeURIComponent(diffMatch[1]) };
    if (diffMatch[2]) {
      const qs = new URLSearchParams(diffMatch[2]);
      if (qs.get('from')) params.from = qs.get('from');
      if (qs.get('to')) params.to = qs.get('to');
    }
    return { view: 'diff', params };
  }

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

export function diffUrl(name, from, to) {
  let url = `#/services/${encodeURIComponent(name)}/diff`;
  const qs = new URLSearchParams();
  if (from) qs.set('from', from);
  if (to) qs.set('to', to);
  const str = qs.toString();
  return str ? `${url}?${str}` : url;
}
