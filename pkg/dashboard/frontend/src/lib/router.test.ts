import { describe, it, expect, beforeEach } from 'vitest';
import {
  parseHash, navigate, serviceUrl, serviceVersionUrl, diffUrl, compareDiffUrl, ownersUrl, ownerUrl,
  readinessUrl, fleetUrl,
  hashForHref, fleetOverviewUrl, fleetServicesUrl, fleetOwnersUrl, fleetSourcesUrl,
  fleetEntityUrl, fleetGraphFocusUrl, fleetAttentionUrl, fleetChangesUrl,
  legacyRedirectTarget, replaceHash, fleetEntityListUrl,
} from './router.ts';

describe('parseHash', () => {
  it('returns list view for empty hash', () => {
    expect(parseHash('')).toEqual({ view: 'list', params: {} });
  });

  it('returns list view for #/', () => {
    expect(parseHash('#/')).toEqual({ view: 'list', params: {} });
  });

  it('returns list view for #', () => {
    expect(parseHash('#')).toEqual({ view: 'list', params: {} });
  });

  it('parses service detail route', () => {
    expect(parseHash('#/services/my-service')).toEqual({
      view: 'detail',
      params: { name: 'my-service' },
    });
  });

  it('decodes encoded service names', () => {
    expect(parseHash('#/services/my%20service')).toEqual({
      view: 'detail',
      params: { name: 'my service' },
    });
  });

  it('handles service names with slashes', () => {
    expect(parseHash('#/services/org/repo')).toEqual({
      view: 'detail',
      params: { name: 'org/repo' },
    });
  });

  it('parses versioned detail route', () => {
    expect(parseHash('#/services/my-svc/versions/1.2.0')).toEqual({
      view: 'detail',
      params: { name: 'my-svc', version: '1.2.0' },
    });
  });

  it('decodes encoded version detail parts', () => {
    expect(parseHash('#/services/my%20svc/versions/1.0.0%2Bb')).toEqual({
      view: 'detail',
      params: { name: 'my svc', version: '1.0.0+b' },
    });
  });

  it('still parses plain detail route without version', () => {
    expect(parseHash('#/services/my-svc')).toEqual({
      view: 'detail',
      params: { name: 'my-svc' },
    });
  });

  it('parses graph route', () => {
    expect(parseHash('#/graph')).toEqual({ view: 'graph', params: {} });
  });

  it('parses readiness route', () => {
    expect(parseHash('#/readiness')).toEqual({ view: 'readiness', params: {} });
  });

  it('parses the fleet overview landing route', () => {
    expect(parseHash('#/fleet')).toEqual({ view: 'fleet-overview', params: {} });
  });

  it('parses the operational graph route (migrated to /fleet/graph)', () => {
    expect(parseHash('#/fleet/graph')).toEqual({ view: 'fleet', params: {} });
  });

  // #/impact was the standalone impact screen. It is now one half of the Change
  // analysis workspace, so the old spelling still PARSES (bookmarks keep working) and
  // resolves to the same view the canonical URL does.
  it('parses the legacy impact route as change analysis', () => {
    expect(parseHash('#/impact')).toEqual({ view: 'changes', params: {} });
    expect(parseHash('#/fleet/changes')).toEqual({ view: 'changes', params: {} });
  });

  it('parses legacy diff route without query params', () => {
    expect(parseHash('#/services/my-svc/diff')).toEqual({
      view: 'diff',
      params: { name: 'my-svc', fromName: 'my-svc', toName: 'my-svc' },
    });
  });

  it('parses legacy diff route with from and to params', () => {
    const result = parseHash('#/services/my-svc/diff?from=1.0.0&to=2.0.0');
    expect(result.view).toBe('diff');
    expect(result.params.name).toBe('my-svc');
    expect(result.params.fromName).toBe('my-svc');
    expect(result.params.toName).toBe('my-svc');
    expect(result.params.fromVer).toBe('1.0.0');
    expect(result.params.toVer).toBe('2.0.0');
    // Legacy compat
    expect(result.params.from).toBe('1.0.0');
    expect(result.params.to).toBe('2.0.0');
  });

  it('parses legacy diff route with only from param', () => {
    const result = parseHash('#/services/my-svc/diff?from=1.0.0');
    expect(result.view).toBe('diff');
    expect(result.params.fromVer).toBe('1.0.0');
    expect(result.params.toVer).toBeUndefined();
  });

  it('parses standalone diff route', () => {
    const result = parseHash('#/diff?from_name=svc-a&from_ver=1.0.0&to_name=svc-b&to_ver=2.0.0');
    expect(result.view).toBe('diff');
    expect(result.params.fromName).toBe('svc-a');
    expect(result.params.fromVer).toBe('1.0.0');
    expect(result.params.toName).toBe('svc-b');
    expect(result.params.toVer).toBe('2.0.0');
  });

  it('parses standalone diff route without params', () => {
    expect(parseHash('#/diff')).toEqual({ view: 'diff', params: {} });
  });

  it('returns list view for unknown routes', () => {
    expect(parseHash('#/unknown')).toEqual({ view: 'list', params: {} });
  });

  it('handles null/undefined hash', () => {
    expect(parseHash(null)).toEqual({ view: 'list', params: {} });
    expect(parseHash(undefined)).toEqual({ view: 'list', params: {} });
  });
});

describe('serviceUrl', () => {
  it('builds service URL', () => {
    expect(serviceUrl('my-service')).toBe('#/services/my-service');
  });

  it('encodes special characters', () => {
    expect(serviceUrl('my service')).toBe('#/services/my%20service');
  });
});

describe('serviceVersionUrl', () => {
  it('builds versioned detail URL', () => {
    expect(serviceVersionUrl('my-svc', '1.2.0')).toBe('#/services/my-svc/versions/1.2.0');
  });

  it('encodes special characters', () => {
    expect(serviceVersionUrl('my svc', '1.0.0+b')).toBe('#/services/my%20svc/versions/1.0.0%2Bb');
  });
});

describe('diffUrl', () => {
  it('builds diff URL without versions', () => {
    expect(diffUrl('my-svc')).toBe('#/services/my-svc/diff');
  });

  it('builds diff URL with from and to', () => {
    expect(diffUrl('my-svc', '1.0.0', '2.0.0')).toBe(
      '#/services/my-svc/diff?from=1.0.0&to=2.0.0'
    );
  });

  it('builds diff URL with only from', () => {
    expect(diffUrl('my-svc', '1.0.0')).toBe('#/services/my-svc/diff?from=1.0.0');
  });
});

describe('compareDiffUrl', () => {
  it('builds standalone diff URL', () => {
    const url = compareDiffUrl({ fromName: 'a', fromVer: '1.0', toName: 'b', toVer: '2.0' });
    expect(url).toBe('#/diff?from_name=a&from_ver=1.0&to_name=b&to_ver=2.0');
  });

  it('builds diff URL with partial params', () => {
    const url = compareDiffUrl({ fromName: 'a' });
    expect(url).toBe('#/diff?from_name=a');
  });

  it('builds diff URL with no params', () => {
    expect(compareDiffUrl()).toBe('#/diff');
  });
});

describe('parseHash — owner routes', () => {
  it('parses owners list route', () => {
    expect(parseHash('#/owners')).toEqual({ view: 'owners', params: {} });
  });

  it('parses owner detail route', () => {
    expect(parseHash('#/owners/team-a')).toEqual({
      view: 'owner-detail',
      params: { owner: 'team-a' },
    });
  });

  it('decodes encoded owner names', () => {
    expect(parseHash('#/owners/team%2Fpayments')).toEqual({
      view: 'owner-detail',
      params: { owner: 'team/payments' },
    });
  });
});

describe('parseHash — query strings on non-diff routes', () => {
  // Regression: clicking a status badge on owner-detail appends a filter query
  // (#/owners/:id?contractStatus=Compliant). The query must be stripped from the
  // route id so the owner resolves cleanly; filters are read separately from the hash.
  it('strips the query string from an owner detail id', () => {
    expect(parseHash('#/owners/platform-foundations?contractStatus=Compliant')).toEqual({
      view: 'owner-detail',
      params: { owner: 'platform-foundations' },
    });
  });

  it('strips the query string from a service detail name', () => {
    expect(parseHash('#/services/my-svc?readinessStatus=ready')).toEqual({
      view: 'detail',
      params: { name: 'my-svc' },
    });
  });

  it('strips the query string from a versioned detail route', () => {
    expect(parseHash('#/services/my-svc/versions/1.2.0?source=oci')).toEqual({
      view: 'detail',
      params: { name: 'my-svc', version: '1.2.0' },
    });
  });

  it('strips the query string from the owners list route', () => {
    expect(parseHash('#/owners?category=security')).toEqual({ view: 'owners', params: {} });
  });

  it('strips the query string from the graph route', () => {
    expect(parseHash('#/graph?owner=team-a')).toEqual({ view: 'graph', params: {} });
  });

  it('strips the query string from the readiness route', () => {
    expect(parseHash('#/readiness?contractStatus=Warning')).toEqual({ view: 'readiness', params: {} });
  });

  it('parses search-first graph state (perspective, views, direction, depth, selection) from the query', () => {
    expect(parseHash('#/fleet/graph?perspective=target&views=observed&direction=dependencies&depth=2&sel=domain-a%2Fpayments')).toEqual({
      view: 'fleet',
      params: { perspective: 'target', views: 'observed', direction: 'dependencies', depth: '2', sel: 'domain-a/payments' },
    });
  });

  it('drops the inert graph filters no view or backend consumes (requirement J)', () => {
    // domain/scope/owner/status/source/freshness were placebo URL state; they are not
    // parsed into the graph route model any more.
    expect(parseHash('#/fleet/graph?perspective=service&domain=domain-a&scope=prod&owner=core&status=NonCompliant&source=k8s&freshness=stale')).toEqual({
      view: 'fleet',
      params: { perspective: 'service' },
    });
  });

  it('parses change-analysis deep-link params (old, new, observed) from the query', () => {
    expect(parseHash('#/impact?old=oci://x/a@sha256:1&new=oci://x/a@sha256:2&observed=1')).toEqual({
      view: 'changes',
      params: { old: 'oci://x/a@sha256:1', new: 'oci://x/a@sha256:2', observed: '1' },
    });
    expect(parseHash('#/fleet/changes/domain-a%2Fpayments?old=a&new=b&observed=1')).toEqual({
      view: 'changes',
      params: { old: 'a', new: 'b', observed: '1', svc: 'domain-a/payments' },
    });
  });

  it('strips the query string from an encoded owner id', () => {
    expect(parseHash('#/owners/team%2Fpayments?source=k8s')).toEqual({
      view: 'owner-detail',
      params: { owner: 'team/payments' },
    });
  });
});

describe('ownersUrl', () => {
  it('returns owners URL', () => {
    expect(ownersUrl()).toBe('#/owners');
  });
});

describe('readinessUrl', () => {
  it('returns readiness URL', () => {
    expect(readinessUrl()).toBe('#/readiness');
  });
});

describe('fleetUrl', () => {
  it('returns the operational graph nav URL at /fleet/graph (no inert state)', () => {
    expect(fleetUrl()).toBe('#/fleet/graph');
  });
});

describe('parseHash — fleet product IA (Phase 2)', () => {
  it('parses the unified entity-detail routes (plural segment -> singular kind)', () => {
    expect(parseHash('#/fleet/services/payments')).toEqual({ view: 'fleet-entity', params: { kind: 'service', key: 'payments' } });
    expect(parseHash('#/fleet/revisions/svc@a')).toEqual({ view: 'fleet-entity', params: { kind: 'revision', key: 'svc@a' } });
    expect(parseHash('#/fleet/targets/prod/k8s/app')).toEqual({ view: 'fleet-entity', params: { kind: 'target', key: 'prod/k8s/app' } });
    expect(parseHash('#/fleet/owners/team-a')).toEqual({ view: 'fleet-entity', params: { kind: 'owner', key: 'team-a' } });
    expect(parseHash('#/fleet/sources/k8s')).toEqual({ view: 'fleet-entity', params: { kind: 'source', key: 'k8s' } });
  });

  it('round-trips slash- and percent-bearing canonical keys through escape/decode', () => {
    for (const key of ['domain-a/payments', 'prod/k8s/app', 'oci://ghcr.io/acme/pay@sha256:ab', 'weird%2Fkey', 'a b/c%d']) {
      const url = fleetEntityUrl('service', key);
      expect(url.includes(' ')).toBe(false); // fully escaped, safe in a hash
      expect(parseHash(url)).toEqual({ view: 'fleet-entity', params: { kind: 'service', key } });
    }
  });

  it('parses the attention route with an optional category', () => {
    expect(parseHash('#/fleet/attention')).toEqual({ view: 'fleet-attention', params: {} });
    expect(parseHash('#/fleet/attention?category=stale')).toEqual({ view: 'fleet-attention', params: { category: 'stale' } });
  });

  it('parses the attention page offset and triage filters from the query (A2/I)', () => {
    expect(parseHash('#/fleet/attention?offset=50')).toEqual({ view: 'fleet-attention', params: { offset: '50' } });
    expect(parseHash('#/fleet/attention?category=stale&offset=50')).toEqual({
      view: 'fleet-attention', params: { category: 'stale', offset: '50' },
    });
    expect(parseHash('#/fleet/attention?owner=team-a&severity=error&staleOnly=1')).toEqual({
      view: 'fleet-attention', params: { owner: 'team-a', severity: 'error', staleOnly: '1' },
    });
  });

  it('parses the bare /fleet/services list route (A3 canonical services href)', () => {
    expect(parseHash('#/fleet/services')).toEqual({ view: 'fleet-services', params: {} });
  });

  it('parses the /fleet/services filters and offset from the query (A3/C)', () => {
    expect(parseHash('#/fleet/services?owner=team-a&status=NonCompliant&domain=payments&offset=100')).toEqual({
      view: 'fleet-services',
      params: { owner: 'team-a', status: 'NonCompliant', domain: 'payments', offset: '100' },
    });
  });

  it('drops the inert scope/source params the Services list does not implement (F1)', () => {
    // scope is a target-only Entities filter and source was never wired into the
    // Services list, so neither round-trips into the route state.
    expect(parseHash('#/fleet/services?scope=prod&source=k8s&owner=team-a')).toEqual({
      view: 'fleet-services',
      params: { owner: 'team-a' },
    });
    // fleetServicesUrl accepts only the implemented filters.
    expect(fleetServicesUrl({ owner: 'team-a', offset: 25 })).toBe('#/fleet/services?owner=team-a&offset=25');
  });

  it('carries declared-ownership as route state, so an aggregate finding is a link', () => {
    // "12 services have no declared owner" is only useful if it can be FOLLOWED, and a
    // followed link has to survive a copy-paste and a Back. Ownership (a state:
    // consistent / conflicting / unowned) is a different question from owner (a name),
    // so the two are separate params and compose.
    expect(parseHash('#/fleet/services?ownership=unowned')).toEqual({
      view: 'fleet-services', params: { ownership: 'unowned' },
    });
    expect(parseHash('#/fleet/services?owner=team-a&ownership=conflicting')).toEqual({
      view: 'fleet-services', params: { owner: 'team-a', ownership: 'conflicting' },
    });
    const url = fleetServicesUrl({ text: 'pay', owner: 'team-a', ownership: 'consistent', status: 'Unknown', domain: 'core', offset: 25 });
    expect(url).toBe('#/fleet/services?text=pay&owner=team-a&ownership=consistent&status=Unknown&domain=core&offset=25');
    expect(parseHash(url)).toEqual({
      view: 'fleet-services',
      params: { text: 'pay', owner: 'team-a', ownership: 'consistent', status: 'Unknown', domain: 'core', offset: '25' },
    });
  });

  // Owner SEARCH and owner IDENTITY are two questions, so they are two params and both
  // survive the round-trip. A canonical owner link carries `ownerKey`, which the
  // backend matches exactly: with only the free-text `owner`, a link built for owner
  // `team-a` would also select the services of `team-a-platform`.
  it('carries the exact owner and the free-text owner search as separate route state', () => {
    expect(fleetServicesUrl({ ownerKey: 'team-a', ownership: 'consistent' }))
      .toBe('#/fleet/services?ownerKey=team-a&ownership=consistent');
    expect(fleetAttentionUrl({ ownerKey: 'team:platform' }))
      .toBe('#/fleet/attention?ownerKey=team%3Aplatform');
    // They compose rather than override: a reader can narrow a search to one owner.
    const url = fleetServicesUrl({ owner: 'team', ownerKey: 'team-a' });
    expect(url).toBe('#/fleet/services?owner=team&ownerKey=team-a');
    expect(parseHash(url)).toEqual({
      view: 'fleet-services', params: { owner: 'team', ownerKey: 'team-a' },
    });
    expect(parseHash('#/fleet/attention?ownerKey=team-a&category=stale')).toEqual({
      view: 'fleet-attention', params: { ownerKey: 'team-a', category: 'stale' },
    });
  });

  it('a bare /fleet/services must NOT be shadowed by service detail (regression A3)', () => {
    // /fleet/services is the LIST; /fleet/services/:key is one service. The list must
    // never fall through to the entity route (which needs a key) or the overview.
    expect(parseHash('#/fleet/services').view).toBe('fleet-services');
    expect(parseHash('#/fleet/services/payments').view).toBe('fleet-entity');
  });

  it('parses the bare /fleet/owners and /fleet/sources product list routes (G)', () => {
    expect(parseHash('#/fleet/owners')).toEqual({ view: 'fleet-owners', params: {} });
    expect(parseHash('#/fleet/sources')).toEqual({ view: 'fleet-sources', params: {} });
    expect(parseHash('#/fleet/sources?sourceHealth=unavailable&offset=25')).toEqual({
      view: 'fleet-sources', params: { sourceHealth: 'unavailable', offset: '25' },
    });
    // owner/source DETAIL still needs a key and stays fleet-entity.
    expect(parseHash('#/fleet/owners/team-a').view).toBe('fleet-entity');
    expect(parseHash('#/fleet/sources/kubernetes').view).toBe('fleet-entity');
  });

  it('fleetOwnersUrl / fleetSourcesUrl build filtered/paged list URLs', () => {
    expect(fleetOwnersUrl()).toBe('#/fleet/owners');
    expect(fleetOwnersUrl({ text: 'team', offset: 25 })).toBe('#/fleet/owners?text=team&offset=25');
    expect(fleetSourcesUrl({ sourceHealth: 'stale' })).toBe('#/fleet/sources?sourceHealth=stale');
  });

  it('parses the service-scoped change-analysis route, old spelling included', () => {
    expect(parseHash('#/fleet/changes/domain-a%2Fpayments')).toEqual({ view: 'changes', params: { svc: 'domain-a/payments' } });
    expect(parseHash('#/fleet/impact/domain-a%2Fpayments')).toEqual({ view: 'changes', params: { svc: 'domain-a/payments' } });
  });

  it('parses a focused graph route (kind/key path segment)', () => {
    expect(parseHash('#/fleet/graph/target/prod%2Fk8s%2Fapp')).toEqual({ view: 'fleet', params: { kind: 'target', sel: 'prod/k8s/app' } });
  });

  it('falls back to the overview for an unknown /fleet/* route', () => {
    expect(parseHash('#/fleet/bogus/x')).toEqual({ view: 'fleet-overview', params: {} });
  });
});

describe('centralized fleet navigation builders', () => {
  it('hashForHref adopts an authoritative backend href verbatim', () => {
    expect(hashForHref('/fleet/targets/prod%2Fk8s%2Fapp')).toBe('#/fleet/targets/prod%2Fk8s%2Fapp');
    expect(hashForHref('#/fleet/services/x')).toBe('#/fleet/services/x');
    expect(hashForHref('')).toBe('#/fleet');
    expect(hashForHref(undefined)).toBe('#/fleet');
  });
  it('builds product URLs from (kind, key)', () => {
    expect(fleetOverviewUrl()).toBe('#/fleet');
    expect(fleetEntityUrl('target', 'prod/k8s/app')).toBe('#/fleet/targets/prod%2Fk8s%2Fapp');
    expect(fleetGraphFocusUrl('service', 'a/b')).toBe('#/fleet/graph/service/a%2Fb');
    expect(fleetAttentionUrl()).toBe('#/fleet/attention');
    expect(fleetAttentionUrl({ category: 'non-compliant' })).toBe('#/fleet/attention?category=non-compliant');
    expect(fleetChangesUrl('domain-a/payments')).toBe('#/fleet/changes/domain-a%2Fpayments');
  });
  it('fleetServicesUrl carries filters and a non-zero offset, dropping page 1', () => {
    expect(fleetServicesUrl()).toBe('#/fleet/services');
    expect(fleetServicesUrl({ offset: 0 })).toBe('#/fleet/services');
    expect(fleetServicesUrl({ owner: 'team-a', status: 'NonCompliant', offset: 100 }))
      .toBe('#/fleet/services?owner=team-a&status=NonCompliant&offset=100');
  });
  it('fleetAttentionUrl carries category + offset + triage filters, dropping page 1', () => {
    expect(fleetAttentionUrl({ offset: 0 })).toBe('#/fleet/attention');
    expect(fleetAttentionUrl({ category: 'stale', offset: 50 })).toBe('#/fleet/attention?category=stale&offset=50');
    expect(fleetAttentionUrl({ owner: 'team-a', staleOnly: true })).toBe('#/fleet/attention?owner=team-a&staleOnly=1');
  });
  // A service page's posture bars drill into that service's OWN backlog, by canonical
  // ServiceKey. The scope has to survive the round-trip, or the link silently widens
  // to the whole fleet and answers a question nobody asked.
  it('round-trips a service-scoped attention deep link by canonical key', () => {
    const url = fleetAttentionUrl({ service: 'domain-a/payments', category: 'stale' });
    expect(url).toBe('#/fleet/attention?category=stale&service=domain-a%2Fpayments');
    expect(parseHash(url)).toEqual({
      view: 'fleet-attention',
      params: { service: 'domain-a/payments', category: 'stale' },
    });
  });
  it('hashForHref then parseHash round-trips a backend entity href', () => {
    const href = '/fleet/revisions/' + encodeURIComponent('svc@sha256:abc');
    expect(parseHash(hashForHref(href))).toEqual({ view: 'fleet-entity', params: { kind: 'revision', key: 'svc@sha256:abc' } });
  });
});

describe('fleetChangesUrl', () => {
  it('returns the unscoped workspace URL', () => {
    expect(fleetChangesUrl()).toBe('#/fleet/changes');
  });
  it('builds a shareable deep link with both revisions and the observed toggle', () => {
    expect(fleetChangesUrl('domain-a/payments', { old: 'a', new: 'b', observed: true }))
      .toBe('#/fleet/changes/domain-a%2Fpayments?old=a&new=b&observed=1');
  });
  // A service NAME is not a canonical ServiceKey, so it is carried as a resolvable hint
  // in the query -- never promoted into the path where it would look canonical.
  it('carries an unresolved legacy name as a query hint, never as the path key', () => {
    expect(fleetChangesUrl('', { name: 'payments' })).toBe('#/fleet/changes?name=payments');
  });
});

describe('ownerUrl', () => {
  it('builds owner detail URL', () => {
    expect(ownerUrl('team-a')).toBe('#/owners/team-a');
  });

  it('encodes special characters', () => {
    expect(ownerUrl('team/payments')).toBe('#/owners/team%2Fpayments');
  });

  /**
   * An owner key is namespaced (`team:NAME` / `dri:NAME`) so a team and a person of the
   * same name are two owners with two pages. The separator is part of the identity, so
   * the route must carry it through unharmed however the name is spelled -- including
   * names that contain colons and slashes of their own.
   */
  it('round-trips a namespaced owner key, whatever the name contains', () => {
    for (const key of [
      'team:alice', 'dri:alice', 'team:team/payments', 'dri:a:b', 'team:acme co',
      'dri:ops@example.com', 'team:x%2Fy', 'team::', 'dri:100%',
    ]) {
      expect(parseHash(ownerUrl(key))).toEqual({ view: 'owner-detail', params: { owner: key } });
    }
  });

  // Go escapes the path with url.PathEscape, which leaves ':' alone; the browser's
  // encodeURIComponent escapes it to %3A. Both are the same URL, and both decode to
  // the same key -- so a link built by the backend and one built here agree.
  it('reads the backend spelling of the same key identically', () => {
    expect(parseHash('#/owners/team:alice')).toEqual(parseHash('#/owners/team%3Aalice'));
  });
});

describe('navigate — routing-helper semantics agree with parseHash (A5)', () => {
  beforeEach(() => { location.hash = ''; });

  // The route model names view 'fleet' the Operational GRAPH and 'fleet-overview'
  // the Overview. The generic navigate() helper must agree, so it can never send the
  // graph view to the overview (the semantic trap this regression guards).
  it('navigate("fleet") goes to the Operational Graph at /fleet/graph', () => {
    navigate('fleet');
    expect(location.hash).toBe('#/fleet/graph');
    // and the destination round-trips back to the graph view, not the overview.
    expect(parseHash(location.hash).view).toBe('fleet');
  });

  it('navigate("fleet-overview") goes to the Overview at /fleet', () => {
    navigate('fleet-overview');
    expect(location.hash).toBe('#/fleet');
    expect(parseHash(location.hash).view).toBe('fleet-overview');
  });
});

describe('backend-href / frontend-router contract (A3)', () => {
  // Every canonical fleet href CLASS the backend route builder (fleetroute.go) emits
  // MUST resolve through the frontend router to its intended destination. A backend
  // href that falls through to an unrelated route (e.g. the overview) is a blocking
  // failure. The href shapes below mirror fleetroute.go verbatim: routeEntity uses
  // url.PathEscape for keys, hrefForEntryPoint uses url.QueryEscape for the category,
  // hrefForGraph escapes both kind and key.
  const k = (s: string) => encodeURIComponent(s); // PathEscape-compatible for these keys
  const svcKey = 'payments-domain/payments';
  const revKey = 'payments@sha256:abc';
  const tgtKey = 'prod/k8s/payments';
  const cases: Array<{ cls: string; href: string; view: string; params?: Record<string, string> }> = [
    { cls: 'overview', href: '/fleet', view: 'fleet-overview', params: {} },
    { cls: 'attention', href: '/fleet/attention', view: 'fleet-attention', params: {} },
    { cls: 'attention category', href: '/fleet/attention?category=non-compliant', view: 'fleet-attention', params: { category: 'non-compliant' } },
    { cls: 'services list', href: '/fleet/services', view: 'fleet-services', params: {} },
    { cls: 'service detail', href: `/fleet/services/${k(svcKey)}`, view: 'fleet-entity', params: { kind: 'service', key: svcKey } },
    { cls: 'revision detail', href: `/fleet/revisions/${k(revKey)}`, view: 'fleet-entity', params: { kind: 'revision', key: revKey } },
    { cls: 'target detail', href: `/fleet/targets/${k(tgtKey)}`, view: 'fleet-entity', params: { kind: 'target', key: tgtKey } },
    { cls: 'owner detail', href: `/fleet/owners/${k('team:team-a')}`, view: 'fleet-entity', params: { kind: 'owner', key: 'team:team-a' } },
    // Go's url.PathEscape leaves ':' unescaped, so this is the byte-for-byte href the
    // backend actually emits for a namespaced owner key -- not just its equivalent.
    { cls: 'owner detail as the backend spells it', href: '/fleet/owners/dri:team-a', view: 'fleet-entity', params: { kind: 'owner', key: 'dri:team-a' } },
    { cls: 'source detail', href: `/fleet/sources/${k('kubernetes')}`, view: 'fleet-entity', params: { kind: 'source', key: 'kubernetes' } },
    { cls: 'graph focus', href: `/fleet/graph/${k('target')}/${k(tgtKey)}`, view: 'fleet', params: { kind: 'target', sel: tgtKey } },
  ];

  for (const c of cases) {
    it(`resolves the ${c.cls} href to its intended destination`, () => {
      const r = parseHash(hashForHref(c.href));
      expect(r.view).toBe(c.view);
      if (c.params) expect(r.params).toEqual(c.params);
    });
  }

  it('no non-overview canonical href silently falls through to the overview', () => {
    for (const c of cases) {
      if (c.cls === 'overview') continue;
      expect(parseHash(hashForHref(c.href)).view).not.toBe('fleet-overview');
    }
  });
});

describe('legacyRedirectTarget (Part 1: legacy -> product canonicalization)', () => {
  it('maps static 1:1 legacy roots to their canonical product routes', () => {
    expect(legacyRedirectTarget('#/')).toBe('#/fleet');
    expect(legacyRedirectTarget('')).toBe('#/fleet');
    expect(legacyRedirectTarget('#/services')).toBe('#/fleet/services');
    expect(legacyRedirectTarget('#/graph')).toBe('#/fleet/graph');
    expect(legacyRedirectTarget('#/owners')).toBe('#/fleet/owners');
  });
  it('ignores a query string on the legacy root', () => {
    expect(legacyRedirectTarget('#/services?owner=x')).toBe('#/fleet/services');
  });
  it('returns null for name-bearing legacy detail URLs (migrated via the Product API)', () => {
    expect(legacyRedirectTarget('#/services/payments')).toBeNull();
    expect(legacyRedirectTarget('#/services/payments/versions/1.0.0')).toBeNull();
    expect(legacyRedirectTarget('#/owners/team-a')).toBeNull();
  });
  // Readiness and Compare are no longer retained legacy islands on a Fleet host:
  // readiness is a property of a contract revision (so it canonicalizes to the revision
  // inventory, the whole assessed population) and Compare is the Change analysis
  // workspace. Neither mounts a superseded screen.
  //
  // Readiness deliberately does NOT land on the attention backlog's readiness category:
  // that list only ever holds revisions that FAIL, so an old "how ready are we" bookmark
  // arrived somewhere that structurally cannot contain a passing revision.
  it('canonicalizes the superseded Readiness and Compare screens', () => {
    expect(legacyRedirectTarget('#/readiness')).toBe('#/fleet/revisions');
    expect(legacyRedirectTarget('#/diff')).toBe('#/fleet/changes');
    expect(legacyRedirectTarget('#/diff?from_name=payments&to_name=payments')).toBe('#/fleet/changes?name=payments');
    expect(legacyRedirectTarget('#/services/payments/diff')).toBe('#/fleet/changes?name=payments');
  });
  it('canonicalizes the old impact spellings, preserving the selected revisions', () => {
    expect(legacyRedirectTarget('#/impact')).toBe('#/fleet/changes');
    expect(legacyRedirectTarget('#/impact?svc=domain-a%2Fpayments&old=a&new=b&observed=1'))
      .toBe('#/fleet/changes/domain-a%2Fpayments?old=a&new=b&observed=1');
    expect(legacyRedirectTarget('#/fleet/impact/domain-a%2Fpayments')).toBe('#/fleet/changes/domain-a%2Fpayments');
  });
  it('returns null for product routes (no redirect)', () => {
    expect(legacyRedirectTarget('#/fleet')).toBeNull();
    expect(legacyRedirectTarget('#/fleet/services')).toBeNull();
    expect(legacyRedirectTarget('#/fleet/changes')).toBeNull();
  });
});

describe('replaceHash', () => {
  beforeEach(() => { location.hash = '#/start'; });
  it('replaces the hash and notifies listeners without a no-op re-dispatch', () => {
    let fired = 0;
    const h = () => { fired++; };
    window.addEventListener('hashchange', h);
    replaceHash('#/fleet/services');
    expect(location.hash).toBe('#/fleet/services');
    expect(fired).toBe(1);
    // A replace to the current hash is a no-op (no event, no history churn).
    replaceHash('#/fleet/services');
    expect(fired).toBe(1);
    window.removeEventListener('hashchange', h);
  });
});

// The scoped inventory lists are the destination a bounded preview points at: "5 of 47
// revisions" has to lead somewhere that can actually show 47. They page the SAME
// bounded Entities endpoint, scoped by canonical ServiceKey.
describe('scoped inventory lists (/fleet/revisions, /fleet/targets)', () => {
  it('parses the bare list routes into a kind, and keeps them distinct from entity detail', () => {
    expect(parseHash('#/fleet/revisions')).toEqual({ view: 'fleet-entity-list', params: { kind: 'revision' } });
    expect(parseHash('#/fleet/targets')).toEqual({ view: 'fleet-entity-list', params: { kind: 'target' } });
    // A trailing key segment is still entity DETAIL, not a list.
    expect(parseHash('#/fleet/revisions/' + encodeURIComponent('a/b@sha256:c')))
      .toEqual({ view: 'fleet-entity', params: { kind: 'revision', key: 'a/b@sha256:c' } });
  });

  it('carries the canonical service scope and the list filters through the URL', () => {
    expect(parseHash('#/fleet/revisions?service=' + encodeURIComponent('domain-a/payments') + '&text=v2&offset=25'))
      .toEqual({ view: 'fleet-entity-list', params: { kind: 'revision', service: 'domain-a/payments', text: 'v2', offset: '25' } });
    expect(parseHash('#/fleet/targets?scope=prod&status=NonCompliant'))
      .toEqual({ view: 'fleet-entity-list', params: { kind: 'target', scope: 'prod', status: 'NonCompliant' } });
  });

  it('builds the list URL, encoding the ServiceKey and dropping page 1', () => {
    expect(fleetEntityListUrl('revision')).toBe('#/fleet/revisions');
    expect(fleetEntityListUrl('target', { offset: 0 })).toBe('#/fleet/targets');
    expect(fleetEntityListUrl('revision', { service: 'domain-a/payments', offset: 25 }))
      .toBe('#/fleet/revisions?service=domain-a%2Fpayments&offset=25');
    expect(fleetEntityListUrl('target', { service: 'domain-a/payments', status: 'Unknown', scope: 'prod', text: 'api' }))
      .toBe('#/fleet/targets?service=domain-a%2Fpayments&text=api&status=Unknown&scope=prod');
  });

  // Owners, sources and services already have list pages of their own; the builder
  // sends those kinds there rather than inventing a second inventory for them.
  it('routes a kind that already has a list page to that page', () => {
    expect(fleetEntityListUrl('owner')).toBe(fleetOwnersUrl());
    expect(fleetEntityListUrl('source')).toBe(fleetSourcesUrl());
    expect(fleetEntityListUrl('service')).toBe(fleetServicesUrl());
    expect(fleetEntityListUrl('nonsense')).toBe(fleetServicesUrl());
  });

  // Readiness is DECLARED AND ASSESSED PER CONTRACT REVISION. The Entities API rejects
  // the filter on any other kind, so a `readiness` param on /fleet/targets would be an
  // inert piece of URL that 422s the moment anyone acted on it. The route model refuses
  // to write it and refuses to read it, on both sides, rather than relying on the two
  // list pages to independently remember not to.
  it('carries readiness as route state for revisions, and for nothing else', () => {
    expect(parseHash('#/fleet/revisions?readiness=expired')).toEqual({
      view: 'fleet-entity-list', params: { kind: 'revision', readiness: 'expired' },
    });
    expect(parseHash('#/fleet/targets?readiness=expired')).toEqual({
      view: 'fleet-entity-list', params: { kind: 'target' },
    });
    expect(fleetEntityListUrl('revision', { service: 'domain-a/payments', readiness: 'below-threshold' }))
      .toBe('#/fleet/revisions?service=domain-a%2Fpayments&readiness=below-threshold');
    expect(fleetEntityListUrl('target', { readiness: 'passing' })).toBe('#/fleet/targets');
  });

  it('round-trips: what the builder writes is what the router reads back', () => {
    const ready = fleetEntityListUrl('revision', { service: 'domain-a/payments', readiness: 'passing', offset: 25 });
    expect(parseHash(ready)).toEqual({
      view: 'fleet-entity-list',
      params: { kind: 'revision', service: 'domain-a/payments', readiness: 'passing', offset: '25' },
    });
    const url = fleetEntityListUrl('revision', { service: 'domain-a/payments', text: 'v2', offset: 50 });
    expect(parseHash(url)).toEqual({
      view: 'fleet-entity-list',
      params: { kind: 'revision', service: 'domain-a/payments', text: 'v2', offset: '50' },
    });
  });
});
