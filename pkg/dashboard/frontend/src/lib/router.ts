/** Minimal hash router — returns a reactive route object. */

export interface Route {
  view:
    | 'list' | 'detail' | 'diff' | 'graph' | 'owners' | 'owner-detail' | 'readiness'
    // Operational-graph (fleet) product IA. 'fleet' is the legacy operational GRAPH
    // (now mounted at /fleet/graph); the Phase-2 product routes are separate.
    | 'fleet'
    | 'changes'         // /fleet/changes[/:serviceKey]  Change analysis workspace
    | 'fleet-overview'  // /fleet            operational landing page
    | 'fleet-services'  // /fleet/services   product service list
    | 'fleet-owners'    // /fleet/owners     product owner list
    | 'fleet-sources'   // /fleet/sources    product source list
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

  // #/impact?svc=&old=&new=&observed=1 — the superseded standalone impact deep link.
  // It parses to the Change analysis workspace so an old bookmark still lands on the
  // right screen; a Fleet host also canonicalizes the URL (legacyRedirectTarget).
  if (path === 'impact') {
    const params: Record<string, string> = {};
    const qs = new URLSearchParams(query);
    for (const k of ['svc', 'old', 'new', 'observed']) {
      const v = qs.get(k);
      if (v) params[k] = v;
    }
    return { view: 'changes', params };
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
 *   /fleet/changes[/:serviceKey]    -> change analysis, scoped to a service
 *   /fleet/<plural>/:key            -> unified entity detail
 * The last path segment is a single percent-escaped key (the backend escapes '/'),
 * so decodeURIComponent recovers arbitrary canonical keys.
 */
function parseFleet(path: string, query: string): Route {
  if (path === 'fleet') return { view: 'fleet-overview', params: {} };
  const rest = path.slice('fleet/'.length);

  // Operational graph: the search-first product graph. The projection/perspective,
  // knowledge views, direction, depth and advanced filters live in the query so a
  // focused graph is shareable and back/forward-restorable; an optional /:kind/:key
  // path segment focuses it (no focus -> the discovery state, never a fleet hairball).
  if (rest === 'graph' || rest.startsWith('graph/')) {
    const params: Record<string, string> = {};
    const qs = new URLSearchParams(query);
    // Only the graph state the Product Neighborhood actually consumes lives in the
    // route (requirement J): perspective, knowledge views, direction, depth and the
    // focus (kind/sel). The former domain/scope/owner/status/source/freshness params
    // were placebo URL state no view or backend consumed, so they are not parsed.
    for (const k of ['perspective', 'views', 'direction', 'depth', 'sel', 'kind']) {
      const v = qs.get(k);
      if (v) params[k] = v;
    }
    const focus = rest.match(/^graph\/([^/]+)\/(.+)$/);
    if (focus) { params.kind = decodeURIComponent(focus[1]); params.sel = decodeURIComponent(focus[2]); }
    return { view: 'fleet', params };
  }

  if (rest === 'attention') {
    const params: Record<string, string> = {};
    const qs = new URLSearchParams(query);
    // Category and offset (and the section-I triage filters) live in the URL so the
    // attention list is deep-linkable and back/forward restores the exact page.
    for (const k of ['category', 'offset', 'owner', 'source', 'severity', 'status', 'staleOnly']) {
      const v = qs.get(k);
      if (v) params[k] = v;
    }
    return { view: 'fleet-attention', params };
  }

  // Change analysis: ONE workspace answering "what changed, and what does that change
  // affect". The service is a canonical ServiceKey path segment; `name` is only ever a
  // migrated legacy compare bookmark, which the view resolves to a canonical key
  // through the Product API rather than treating a display name as an identity.
  // /fleet/impact/:key is the superseded spelling and parses here too.
  if (rest === 'changes' || rest.startsWith('changes/') || rest.startsWith('impact/')) {
    const params: Record<string, string> = {};
    const qs = new URLSearchParams(query);
    for (const k of ['name', 'old', 'new', 'observed']) {
      const v = qs.get(k);
      if (v) params[k] = v;
    }
    const svc = rest.match(/^(?:changes|impact)\/(.+)$/);
    if (svc) params.svc = decodeURIComponent(svc[1]);
    return { view: 'changes', params };
  }

  // Bare /fleet/services is the product service LIST (the backend route builder emits
  // this canonical href for EntryPointServices). It must be matched before the
  // entity-detail regex, which requires a trailing key segment. Its filters and page
  // offset live in the query so the list is deep-linkable.
  if (rest === 'services') {
    const params: Record<string, string> = {};
    const qs = new URLSearchParams(query);
    // Only the filters the product Services list actually implements live in its
    // route state -- scope (target-only in the Entities API) and source were inert
    // URL params no view consumed, so they are not parsed here (requirement F1).
    for (const k of ['text', 'owner', 'status', 'domain', 'offset']) {
      const v = qs.get(k);
      if (v) params[k] = v;
    }
    return { view: 'fleet-services', params };
  }

  // Bare /fleet/owners and /fleet/sources are the product owner/source LISTS. They
  // must be matched before the entity-detail regex (which needs a trailing key).
  if (rest === 'owners' || rest === 'sources') {
    const params: Record<string, string> = {};
    const qs = new URLSearchParams(query);
    for (const k of ['text', 'sourceHealth', 'offset']) {
      const v = qs.get(k);
      if (v) params[k] = v;
    }
    return { view: rest === 'owners' ? 'fleet-owners' : 'fleet-sources', params };
  }

  const ent = rest.match(/^(services|revisions|targets|owners|sources)\/(.+)$/);
  if (ent) {
    return { view: 'fleet-entity', params: { kind: PLURAL_KIND[ent[1]], key: decodeURIComponent(ent[2]) } };
  }

  // Unknown /fleet/* falls back to the overview, never a broken screen.
  return { view: 'fleet-overview', params: {} };
}

// replaceHash canonicalizes the current URL to `hash` WITHOUT pushing a history entry,
// so a canonicalization (a legacy->product redirect, or a graph focus/perspective
// canonicalization) does not leave the pre-canonical URL in history to bounce back to.
// It notifies the app's hashchange listener, which history.replaceState does not fire.
export function replaceHash(hash: string): void {
  const h = hash.startsWith('#') ? hash : `#${hash}`;
  if (h === location.hash) return;
  history.replaceState(null, '', h);
  window.dispatchEvent(new Event('hashchange'));
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
  // The route model names view 'fleet' the Operational GRAPH (mounted at
  // /fleet/graph) and view 'fleet-overview' the Overview (/fleet). navigate() must
  // agree with parseHash, so 'fleet' goes to the graph, not the overview.
  else if (view === 'fleet') hash = fleetUrl();
  else if (view === 'fleet-overview') hash = fleetOverviewUrl();
  else if (view === 'changes') hash = fleetChangesUrl(params.svc || '');
  else if (view === 'owners') hash = '#/owners';
  else if (view === 'owner-detail' && params.owner) hash = `#/owners/${encodeURIComponent(params.owner)}`;
  location.hash = hash;
}

// legacyRedirectTarget maps a legacy hash that has a DIRECT product equivalent to its
// canonical product hash, so a Fleet-capable host canonicalizes an old bookmark rather
// than mounting a second, competing UI for a concept already migrated (Part 1). It
// covers only the STATIC 1:1 redirects (the fleet landing, and the service/owner/graph
// LIST roots); name-bearing legacy detail URLs (#/services/:name, #/owners/:id) need a
// Product-API lookup and are handled by the migration view, so they return null here.
// It returns null for a URL already under the product IA (#/fleet/...), so those are
// never redirected.
export function legacyRedirectTarget(hash: string | null | undefined): string | null {
  const full = (hash || '').replace(/^#\/?/, '');
  const raw = full.split('?')[0];
  const qs = new URLSearchParams(full.includes('?') ? full.slice(full.indexOf('?') + 1) : '');

  // #/services/:name/diff[?from=&to=] -- a legacy same-service compare bookmark. Its
  // service is a display NAME, so it is carried as `name` for the workspace to resolve
  // to a canonical ServiceKey; the versions are dropped rather than guessed, because a
  // version string is not a RevisionKey.
  const svcDiff = raw.match(/^services\/(.+?)\/diff$/);
  if (svcDiff) return fleetChangesUrl('', { name: decodeURIComponent(svcDiff[1]) });
  // The superseded /fleet/impact/:key spelling of the same workspace.
  const oldImpact = raw.match(/^fleet\/impact\/(.+)$/);
  if (oldImpact) return fleetChangesUrl(decodeURIComponent(oldImpact[1]));

  switch (raw) {
    case '':
    case '/':
      return fleetOverviewUrl();
    case 'services':
      return fleetServicesUrl();
    case 'graph':
      return fleetGraphDiscoveryUrl();
    case 'owners':
      return fleetOwnersUrl();
    // Readiness is a DIMENSION, not a destination: it is authored contract preparedness,
    // shown on the revision that declares it and triaged as a Needs-attention category.
    // The legacy route canonicalizes to that category rather than to a third definition.
    case 'readiness':
      return fleetAttentionUrl({ category: 'readiness' });
    // Compare and Impact are two stages of ONE question ("what changed, and what does
    // that change affect"), so both legacy routes canonicalize into Change analysis.
    case 'diff':
      return fleetChangesUrl('', { name: qs.get('from_name') || qs.get('to_name') || '' });
    case 'impact':
      return fleetChangesUrl(qs.get('svc') || '', {
        old: qs.get('old') || '',
        new: qs.get('new') || '',
        observed: qs.get('observed') === '1',
      });
    default:
      return null;
  }
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

// fleetUrl is the Operational GRAPH nav link (the search-first product graph at
// /fleet/graph, so /fleet is the operational overview). The bare nav link carries no
// state; a focused, shareable graph URL is built by fleetGraphFocusUrl.
export function fleetUrl(): string {
  return '#/fleet/graph';
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

// fleetServicesUrl builds the product service-list route, preserving the backend
// filters the list implements and the page offset in the URL so a filtered/paged
// list is deep-linkable and restored by refresh/back/forward. A zero/absent offset
// is omitted (canonical page 1). scope/source are NOT accepted: scope is a
// target-only Entities filter and source was never wired into the Services list, so
// carrying them would be an inert URL filter (requirement F1).
export function fleetServicesUrl(opts: {
  text?: string; owner?: string; status?: string; domain?: string; offset?: number;
} = {}): string {
  const qs = new URLSearchParams();
  if (opts.text) qs.set('text', opts.text);
  if (opts.owner) qs.set('owner', opts.owner);
  if (opts.status) qs.set('status', opts.status);
  if (opts.domain) qs.set('domain', opts.domain);
  if (opts.offset && opts.offset > 0) qs.set('offset', String(opts.offset));
  const str = qs.toString();
  return str ? `#/fleet/services?${str}` : '#/fleet/services';
}

// fleetOwnersUrl / fleetSourcesUrl build the product owner/source list routes,
// preserving the search text, the source-health filter and the page offset.
export function fleetOwnersUrl(opts: { text?: string; offset?: number } = {}): string {
  const qs = new URLSearchParams();
  if (opts.text) qs.set('text', opts.text);
  if (opts.offset && opts.offset > 0) qs.set('offset', String(opts.offset));
  const str = qs.toString();
  return str ? `#/fleet/owners?${str}` : '#/fleet/owners';
}

export function fleetSourcesUrl(opts: { text?: string; sourceHealth?: string; offset?: number } = {}): string {
  const qs = new URLSearchParams();
  if (opts.text) qs.set('text', opts.text);
  if (opts.sourceHealth) qs.set('sourceHealth', opts.sourceHealth);
  if (opts.offset && opts.offset > 0) qs.set('offset', String(opts.offset));
  const str = qs.toString();
  return str ? `#/fleet/sources?${str}` : '#/fleet/sources';
}

// ponytail: encodeURIComponent over-escapes a few sub-delims vs Go's url.PathEscape,
// so a frontend-built key segment can differ cosmetically from the backend href for
// the same key -- both decode identically, and components prefer hashForHref(ref.href)
// anyway, so this only affects URLs we build with no backend href in hand.
export function fleetEntityUrl(kind: string, key: string): string {
  const plural = KIND_PLURAL[kind] ?? 'services';
  return `#/fleet/${plural}/${encodeURIComponent(key)}`;
}

// fleetGraphDiscoveryUrl is the search-first graph landing (no focus). It never
// carries a focus, so it opens the discovery state rather than a fleet hairball.
export function fleetGraphDiscoveryUrl(): string {
  return '#/fleet/graph';
}

// GraphState is the shareable state of the search-first Operational Graph (Q). The
// focus (kind + key) is a path segment; the projection/perspective, knowledge views,
// direction and depth are query params, so back/forward restores a meaningful graph and
// never ephemeral canvas coordinates. There are no advanced-filter params: the graph
// only carries state the Product Neighborhood actually consumes (requirement J).
export interface GraphState {
  kind?: string;
  key?: string;
  perspective?: string;
  views?: string[];
  direction?: string;
  depth?: number;
}

// fleetGraphFocusUrl builds a focused graph URL from (kind, key) plus optional graph
// state. With no key it returns the discovery landing.
// isDefaultGraphViews reports whether views are exactly the focused default
// (expected + differences), so a canonical URL omits them (kept in sync with
// graphState.DEFAULT_VIEWS; a small deliberate duplication that keeps this low-level
// route builder free of a graph-state import).
function isDefaultGraphViews(v?: string[]): boolean {
  return !!v && v.length === 2 && v.includes('expected') && v.includes('differences');
}

export function fleetGraphFocusUrl(kind: string, key: string, state: Omit<GraphState, 'kind' | 'key'> = {}): string {
  if (!key) return fleetGraphDiscoveryUrl();
  const qs = new URLSearchParams();
  if (state.perspective && state.perspective !== 'service') qs.set('perspective', state.perspective);
  if (state.views && state.views.length && !isDefaultGraphViews(state.views)) qs.set('views', state.views.join(','));
  if (state.direction && state.direction !== 'both') qs.set('direction', state.direction);
  if (state.depth && state.depth !== 1) qs.set('depth', String(state.depth));
  const base = `#/fleet/graph/${encodeURIComponent(kind)}/${encodeURIComponent(key)}`;
  const str = qs.toString();
  return str ? `${base}?${str}` : base;
}

// fleetAttentionUrl builds the attention route, preserving the category, page offset
// and the section-I triage filters in the URL so a filtered page is deep-linkable and
// restored by refresh/back/forward. A zero/absent offset is omitted (canonical page 1).
export function fleetAttentionUrl(opts: {
  category?: string; offset?: number; owner?: string; source?: string;
  severity?: string; status?: string; staleOnly?: boolean;
} = {}): string {
  const qs = new URLSearchParams();
  if (opts.category) qs.set('category', opts.category);
  if (opts.owner) qs.set('owner', opts.owner);
  if (opts.source) qs.set('source', opts.source);
  if (opts.severity) qs.set('severity', opts.severity);
  if (opts.status) qs.set('status', opts.status);
  if (opts.staleOnly) qs.set('staleOnly', '1');
  if (opts.offset && opts.offset > 0) qs.set('offset', String(opts.offset));
  const str = qs.toString();
  return str ? `#/fleet/attention?${str}` : '#/fleet/attention';
}

// fleetChangesUrl builds the Change analysis route. The service is a canonical
// ServiceKey PATH segment (never a display name); `name` is only set when migrating a
// legacy compare bookmark that had nothing but a name, and is mutually exclusive with a
// resolved key. The selected revision pair and the include-observed toggle live in the
// query, so an analysis is shareable and restored by back/forward.
export function fleetChangesUrl(
  serviceKey = '',
  opts: { name?: string; old?: string; new?: string; observed?: boolean } = {},
): string {
  const base = serviceKey ? `#/fleet/changes/${encodeURIComponent(serviceKey)}` : '#/fleet/changes';
  const qs = new URLSearchParams();
  if (!serviceKey && opts.name) qs.set('name', opts.name);
  if (opts.old) qs.set('old', opts.old);
  if (opts.new) qs.set('new', opts.new);
  if (opts.observed) qs.set('observed', '1');
  const str = qs.toString();
  return str ? `${base}?${str}` : base;
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
