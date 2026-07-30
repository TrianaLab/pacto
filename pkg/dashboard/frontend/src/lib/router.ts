/** Minimal hash router — returns a reactive route object. */

export interface Route {
  view: 'list' | 'detail' | 'diff' | 'graph' | 'owners' | 'owner-detail' | 'readiness' | 'fleet' | 'impact';
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

  // For non-diff routes, strip query string from path before matching, but keep
  // the query for the routes that carry state in it (fleet graph, impact).
  const path = raw.split('?')[0];
  const query = raw.includes('?') ? raw.slice(raw.indexOf('?') + 1) : '';

  // #/services/:name/versions/:version (full detail of a specific version)
  const versionMatch = path.match(/^services\/(.+?)\/versions\/(.+)$/);
  if (versionMatch) {
    return {
      view: 'detail',
      params: {
        name: decodeURIComponent(versionMatch[1]),
        version: decodeURIComponent(versionMatch[2]),
      },
    };
  }

  // #/services/:name
  const svcMatch = path.match(/^services\/(.+)$/);
  if (svcMatch) return { view: 'detail', params: { name: decodeURIComponent(svcMatch[1]) } };

  // #/graph
  if (path === 'graph') return { view: 'graph', params: {} };

  // #/readiness
  if (path === 'readiness') return { view: 'readiness', params: {} };

  // #/fleet?perspective=&layer=&domain=&scope=&owner=&status=&source=&freshness=&sel=&kind=
  // The Operational Graph keeps its perspective, layer, filters and selection in
  // the URL so the view is deep-linkable, survives auto-refresh, and is testable.
  // `sel` is a URL-encoded domain-qualified ServiceKey/RevisionKey/TargetKey.
  if (path === 'fleet') {
    const params: Record<string, string> = {};
    const qs = new URLSearchParams(query);
    for (const k of ['perspective', 'layer', 'domain', 'scope', 'owner', 'status', 'source', 'freshness', 'sel', 'kind']) {
      const v = qs.get(k);
      if (v) params[k] = v;
    }
    return { view: 'fleet', params };
  }

  // #/impact?old=&new=&observed=1 — the impact deep link carries the two revisions
  // and the include-observed toggle so entry points (Compare, a service, a
  // revision) can launch it preconfigured.
  if (path === 'impact') {
    const params: Record<string, string> = {};
    const qs = new URLSearchParams(query);
    if (qs.get('old')) params.old = qs.get('old')!;
    if (qs.get('new')) params.new = qs.get('new')!;
    if (qs.get('observed')) params.observed = qs.get('observed')!;
    return { view: 'impact', params };
  }

  // #/owners/:id
  const ownerMatch = path.match(/^owners\/(.+)$/);
  if (ownerMatch) return { view: 'owner-detail', params: { owner: decodeURIComponent(ownerMatch[1]) } };

  // #/owners
  if (path === 'owners') return { view: 'owners', params: {} };

  return { view: 'list', params: {} };
}

export function navigate(view: string, params: Record<string, string> = {}): void {
  let hash = '#/';
  if (view === 'detail' && params.name) {
    hash = params.version
      ? `#/services/${encodeURIComponent(params.name)}/versions/${encodeURIComponent(params.version)}`
      : `#/services/${encodeURIComponent(params.name)}`;
  }
  else if (view === 'diff' && params.name) hash = `#/services/${encodeURIComponent(params.name)}/diff`;
  else if (view === 'graph') hash = '#/graph';
  else if (view === 'readiness') hash = '#/readiness';
  else if (view === 'fleet') hash = '#/fleet';
  else if (view === 'impact') hash = '#/impact';
  else if (view === 'owners') hash = '#/owners';
  else if (view === 'owner-detail' && params.owner) hash = `#/owners/${encodeURIComponent(params.owner)}`;
  location.hash = hash;
}

export function serviceUrl(name: string): string {
  return `#/services/${encodeURIComponent(name)}`;
}

export function serviceVersionUrl(name: string, version: string): string {
  return `#/services/${encodeURIComponent(name)}/versions/${encodeURIComponent(version)}`;
}

export function diffUrl(name: string, from?: string, to?: string): string {
  let url = `#/services/${encodeURIComponent(name)}/diff`;
  const qs = new URLSearchParams();
  if (from) qs.set('from', from);
  if (to) qs.set('to', to);
  const str = qs.toString();
  return str ? `${url}?${str}` : url;
}

export function graphUrl(): string {
  return '#/graph';
}

export function ownersUrl(): string {
  return '#/owners';
}

export function readinessUrl(): string {
  return '#/readiness';
}

export function fleetUrl(opts: {
  perspective?: string; layer?: string; domain?: string; scope?: string; owner?: string;
  status?: string; source?: string; freshness?: string; sel?: string; kind?: string;
} = {}): string {
  const qs = new URLSearchParams();
  for (const [k, v] of Object.entries(opts)) {
    if (v) qs.set(k, v);
  }
  const str = qs.toString();
  return str ? `#/fleet?${str}` : '#/fleet';
}

export function impactUrl(opts: { old?: string; new?: string; observed?: boolean } = {}): string {
  const qs = new URLSearchParams();
  if (opts.old) qs.set('old', opts.old);
  if (opts.new) qs.set('new', opts.new);
  if (opts.observed) qs.set('observed', '1');
  const str = qs.toString();
  return str ? `#/impact?${str}` : '#/impact';
}

export function ownerUrl(key: string): string {
  return `#/owners/${encodeURIComponent(key)}`;
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
