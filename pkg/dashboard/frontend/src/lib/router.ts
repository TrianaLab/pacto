/** Minimal hash router — returns a reactive route object. */

export interface Route {
  view: 'list' | 'detail' | 'diff' | 'graph';
  params: Record<string, string>;
}

export function parseHash(hash: string | null | undefined): Route {
  const raw = (hash || '').replace(/^#\/?/, '');
  if (!raw || raw === '/') return { view: 'list', params: {} };

  // #/diff?from_name=X&from_ver=Y&to_name=Z&to_ver=W (standalone diff)
  const standaloneDiff = raw.match(/^diff(?:\?(.*))?$/);
  if (standaloneDiff) {
    const params: Record<string, string> = {};
    if (standaloneDiff[1]) {
      const qs = new URLSearchParams(standaloneDiff[1]);
      if (qs.get('from_name')) params.fromName = qs.get('from_name')!;
      if (qs.get('from_ver')) params.fromVer = qs.get('from_ver')!;
      if (qs.get('to_name')) params.toName = qs.get('to_name')!;
      if (qs.get('to_ver')) params.toVer = qs.get('to_ver')!;
    }
    return { view: 'diff', params };
  }

  // #/services/:name/diff?from=X&to=Y (legacy same-service diff)
  const diffMatch = raw.match(/^services\/(.+?)\/diff(?:\?(.*))?$/);
  if (diffMatch) {
    const name = decodeURIComponent(diffMatch[1]);
    const params: Record<string, string> = { name, fromName: name, toName: name };
    if (diffMatch[2]) {
      const qs = new URLSearchParams(diffMatch[2]);
      const from = qs.get('from');
      const to = qs.get('to');
      if (from) { params.from = from; params.fromVer = from; }
      if (to) { params.to = to; params.toVer = to; }
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

export function navigate(view: string, params: Record<string, string> = {}): void {
  let hash = '#/';
  if (view === 'detail' && params.name) hash = `#/services/${encodeURIComponent(params.name)}`;
  else if (view === 'diff' && params.name) hash = `#/services/${encodeURIComponent(params.name)}/diff`;
  else if (view === 'graph') hash = '#/graph';
  location.hash = hash;
}

export function serviceUrl(name: string): string {
  return `#/services/${encodeURIComponent(name)}`;
}

export function diffUrl(name: string, from?: string, to?: string): string {
  let url = `#/services/${encodeURIComponent(name)}/diff`;
  const qs = new URLSearchParams();
  if (from) qs.set('from', from);
  if (to) qs.set('to', to);
  const str = qs.toString();
  return str ? `${url}?${str}` : url;
}

export function compareDiffUrl(opts: { fromName?: string; fromVer?: string; toName?: string; toVer?: string } = {}): string {
  const qs = new URLSearchParams();
  if (opts.fromName) qs.set('from_name', opts.fromName);
  if (opts.fromVer) qs.set('from_ver', opts.fromVer);
  if (opts.toName) qs.set('to_name', opts.toName);
  if (opts.toVer) qs.set('to_ver', opts.toVer);
  const str = qs.toString();
  return str ? `#/diff?${str}` : '#/diff';
}
