import { describe, it, expect } from 'vitest';
import { parseHash, serviceUrl, serviceVersionUrl, diffUrl, compareDiffUrl, ownersUrl, ownerUrl, readinessUrl, fleetUrl, impactUrl } from './router.ts';

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

  it('parses fleet route', () => {
    expect(parseHash('#/fleet')).toEqual({ view: 'fleet', params: {} });
  });

  it('parses impact route', () => {
    expect(parseHash('#/impact')).toEqual({ view: 'impact', params: {} });
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

  it('parses fleet graph state (perspective, layer, filters, selection) from the query', () => {
    expect(parseHash('#/fleet?perspective=target&layer=reconciled&domain=domain-a&owner=core&sel=domain-a%2Fpayments')).toEqual({
      view: 'fleet',
      params: { perspective: 'target', layer: 'reconciled', domain: 'domain-a', owner: 'core', sel: 'domain-a/payments' },
    });
  });

  it('parses impact deep-link params (old, new, observed) from the query', () => {
    expect(parseHash('#/impact?old=oci://x/a@sha256:1&new=oci://x/a@sha256:2&observed=1')).toEqual({
      view: 'impact',
      params: { old: 'oci://x/a@sha256:1', new: 'oci://x/a@sha256:2', observed: '1' },
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
  it('returns fleet URL', () => {
    expect(fleetUrl()).toBe('#/fleet');
  });
  it('builds a fleet URL with graph state (encoding the selected key)', () => {
    expect(fleetUrl({ perspective: 'service', layer: 'all', sel: 'domain-a/payments' }))
      .toBe('#/fleet?perspective=service&layer=all&sel=domain-a%2Fpayments');
  });
});

describe('impactUrl', () => {
  it('returns impact URL', () => {
    expect(impactUrl()).toBe('#/impact');
  });
  it('builds an impact deep link with both revisions and the observed toggle', () => {
    expect(impactUrl({ old: 'a', new: 'b', observed: true })).toBe('#/impact?old=a&new=b&observed=1');
  });
});

describe('ownerUrl', () => {
  it('builds owner detail URL', () => {
    expect(ownerUrl('team-a')).toBe('#/owners/team-a');
  });

  it('encodes special characters', () => {
    expect(ownerUrl('team/payments')).toBe('#/owners/team%2Fpayments');
  });
});
