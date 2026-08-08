/** Minimal hash router — returns a reactive route object. */

export interface Route {
  view:
    | 'list' | 'detail' | 'diff' | 'graph' | 'owners' | 'owner-detail' | 'readiness'
    // Operational-graph (fleet) product IA. 'fleet' is the legacy operational GRAPH
    // (now mounted at /fleet/graph); the Phase-2 product routes are separate.
    | 'fleet' | 'impact'
    | 'fleet-overview'  // /fleet            operational landing page
    | 'fleet-entity'    // /fleet/<plural>/:key   unified entity detail
    | 'fleet-attention';// /fleet/attention  attention list
  params: Record<string, string>;
}

// Entity kind <-> URL segment. The backend route builder (fleetroute.go) uses the
// plural segment; the product entity-detail facade takes the singular kind. These
// two maps are the ONE place the frontend translates between them.
const KIND_PLURAL: Record<string, string> = {
  service: 'services', revision: 'revisions', target: 'targets', owner: 'owners', source: 'sources',
};
const PLURAL_KIND: Record<string, string> = {
  services: 'service', revisions: 'revision', targets: 'target', owners: 'owner', sources: 'source',
};

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

  // Operational-graph (fleet) product IA (Phase 2). The backend route builder
  // (fleetroute.go) emits exactly these paths as authoritative hrefs; parseFleet is
  // their frontend counterpart. Keys are percent-escaped path segments, so they
  // round-trip slash-, percent-, OCI- and domain-qualified identities.
  if (path === 'fleet' || path.startsWith('fleet/')) {
    return parseFleet(path, query);
  }

  // #/impact?old=&new=&observed=1 — the impact deep link carries the two revisions
  // and the include-observed toggle so entry points (Compare, a service, a
  // revision) can launch it preconfigured.
  if (path === 'impact') {
    const params: Record<string, string> = {};
    const qs = new URLSearchParams(query);
    if (qs.get('svc')) params.svc = qs.get('svc')!;
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

/**
 * parseFleet routes the /fleet/* product IA:
 *   /fleet                          -> operational overview (landing)
 *   /fleet/graph[/:kind/:key][?...] -> operational graph (legacy FleetView)
 *   /fleet/attention[?category=]    -> attention list
 *   /fleet/impact/:serviceKey       -> impact, scoped to a service
 *   /fleet/<plural>/:key            -> unified entity detail
 * The last path segment is a single percent-escaped key (the backend escapes '/'),
 * so decodeURIComponent recovers arbitrary canonical keys.
 */
function parseFleet(path: string, query: string): Route {
  if (path === 'fleet') return { view: 'fleet-overview', params: {} };
  const rest = path.slice('fleet/'.length);

  // Operational graph: perspective/layer/filters/selection live in the query so the
  // graph is deep-linkable; an optional /:kind/:key path segment focuses it.
  if (rest === 'graph' || rest.startsWith('graph/')) {
    const params: Record<string, string> = {};
    const qs = new URLSearchParams(query);
    for (const k of ['perspective', 'layer', 'domain', 'scope', 'owner', 'status', 'source', 'freshness', 'sel', 'kind']) {
      const v = qs.get(k);
      if (v) params[k] = v;
    }
    const focus = rest.match(/^graph\/([^/]+)\/(.+)$/);
    if (focus) { params.kind = decodeURIComponent(focus[1]); params.sel = decodeURIComponent(focus[2]); }
    return { view: 'fleet', params };
  }

  if (rest === 'attention') {
    const params: Record<string, string> = {};
    const cat = new URLSearchParams(query).get('category');
    if (cat) params.category = cat;
    return { view: 'fleet-attention', params };
  }

  const imp = rest.match(/^impact\/(.+)$/);
  if (imp) return { view: 'impact', params: { svc: decodeURIComponent(imp[1]) } };

  const ent = rest.match(/^(services|revisions|targets|owners|sources)\/(.+)$/);
  if (ent) {
    return { view: 'fleet-entity', params: { kind: PLURAL_KIND[ent[1]], key: decodeURIComponent(ent[2]) } };
  }

  // Unknown /fleet/* falls back to the overview, never a broken screen.
  return { view: 'fleet-overview', params: {} };
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

// fleetUrl builds the Operational GRAPH link (legacy FleetView), now mounted at
// /fleet/graph so /fleet is the operational overview. Graph state stays in the query.
export function fleetUrl(opts: {
  perspective?: string; layer?: string; domain?: string; scope?: string; owner?: string;
  status?: string; source?: string; freshness?: string; sel?: string; kind?: string;
} = {}): string {
  const qs = new URLSearchParams();
  for (const [k, v] of Object.entries(opts)) {
    if (v) qs.set(k, v);
  }
  const str = qs.toString();
  return str ? `#/fleet/graph?${str}` : '#/fleet/graph';
}

// ── centralized fleet product navigation (Phase 2) ───────────────────────────
// Every /fleet/* URL is built here; components never assemble a fleet path inline.
// Prefer hashForHref(ref.href) when a ProductRef already carries its authoritative
// backend href; use these builders when only (kind, key) is known.

/** hashForHref turns an authoritative backend product href ("/fleet/...") into a
 *  hash-router location. This is the primary navigator: ProductRef, entry points and
 *  edges all carry a canonical href the backend built from the exact key. */
export function hashForHref(href: string | null | undefined): string {
  if (!href) return '#/fleet';
  return href.startsWith('#') ? href : `#${href}`;
}

export function fleetOverviewUrl(): string {
  return '#/fleet';
}

// ponytail: encodeURIComponent over-escapes a few sub-delims vs Go's url.PathEscape,
// so a frontend-built key segment can differ cosmetically from the backend href for
// the same key -- both decode identically, and components prefer hashForHref(ref.href)
// anyway, so this only affects URLs we build with no backend href in hand.
export function fleetEntityUrl(kind: string, key: string): string {
  const plural = KIND_PLURAL[kind] ?? 'services';
  return `#/fleet/${plural}/${encodeURIComponent(key)}`;
}

export function fleetGraphFocusUrl(kind: string, key: string): string {
  return `#/fleet/graph/${encodeURIComponent(kind)}/${encodeURIComponent(key)}`;
}

export function fleetAttentionUrl(category?: string): string {
  return category ? `#/fleet/attention?category=${encodeURIComponent(category)}` : '#/fleet/attention';
}

export function fleetImpactUrl(serviceKey: string): string {
  return `#/fleet/impact/${encodeURIComponent(serviceKey)}`;
}

export function impactUrl(opts: { svc?: string; old?: string; new?: string; observed?: boolean } = {}): string {
  const qs = new URLSearchParams();
  if (opts.svc) qs.set('svc', opts.svc);
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
