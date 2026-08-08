import { describe, it, expect, vi, beforeEach } from 'vitest';
import { PRODUCT_SCHEMA_VERSION, SchemaCompatibilityError, ApiError } from './api.ts';

// The facade routes every call through the generated openapi-fetch client, which
// builds a Request and hands it to the single transport seam (dashboardFetch),
// which calls the global fetch. So mocking global fetch lets us assert on the
// REQUEST the generated client produced - its URL, query serialization, method and
// body - which is the property that matters: if OpenAPI declares a parameter, the
// generated SDK owns how it is serialized, and there is no handwritten query string
// to drift (requirement, item 14).

const mockFetch = vi.fn();
vi.stubGlobal('fetch', mockFetch);

const { api } = await import('./api.ts');

function jsonResponse(data: unknown, status = 200): Response {
  return new Response(JSON.stringify(data), { status, headers: { 'content-type': 'application/json' } });
}

/** lastRequest returns the Request the generated client built for the Nth call. */
function requestFor(call = 0): Request {
  return mockFetch.mock.calls[call][0] as Request;
}
function urlFor(call = 0): URL {
  return new URL(requestFor(call).url);
}

beforeEach(() => {
  mockFetch.mockReset();
});

describe('legacy endpoint serialization (generated client owns the URL)', () => {
  it('GET /health', async () => {
    mockFetch.mockResolvedValue(jsonResponse({ version: '1.0.0' }));
    expect(await api.health()).toEqual({ version: '1.0.0' });
    const req = requestFor();
    expect(urlFor().pathname).toBe('/health');
    expect(req.method).toBe('GET');
  });

  it('GET /api/services', async () => {
    mockFetch.mockResolvedValue(jsonResponse([{ name: 'svc-a' }]));
    expect(await api.services()).toEqual([{ name: 'svc-a' }]);
    expect(urlFor().pathname).toBe('/api/services');
  });

  it('encodes a path parameter', async () => {
    mockFetch.mockResolvedValue(jsonResponse({ name: 'my service' }));
    await api.service('my service');
    expect(urlFor().pathname).toBe('/api/services/my%20service');
  });

  it('encodes both path parameters for a versioned detail', async () => {
    mockFetch.mockResolvedValue(jsonResponse({ name: 'x' }));
    await api.serviceAtVersion('my service', '1.0.0+build');
    expect(urlFor().pathname).toBe('/api/services/my%20service/versions/1.0.0%2Bbuild');
  });

  it('serializes diff query parameters, including empty versions', async () => {
    mockFetch.mockResolvedValue(jsonResponse({ changes: [] }));
    await api.diff('svc-a', '1.0.0', 'svc-b', '2.0.0');
    let q = urlFor().searchParams;
    expect(q.get('from_name')).toBe('svc-a');
    expect(q.get('from_version')).toBe('1.0.0');
    expect(q.get('to_name')).toBe('svc-b');
    expect(q.get('to_version')).toBe('2.0.0');
    mockFetch.mockReset();
    mockFetch.mockResolvedValue(jsonResponse({ changes: [] }));
    await api.diff('svc-a', '', 'svc-a', '');
    q = urlFor().searchParams;
    expect(q.get('from_version')).toBe('');
    expect(q.get('to_version')).toBe('');
  });

  it('POSTs resolve with a JSON body', async () => {
    mockFetch.mockResolvedValue(jsonResponse({ ok: true }));
    await api.resolve('ghcr.io/org/svc:1.0.0', 'strict');
    const req = requestFor();
    expect(req.method).toBe('POST');
    expect(req.headers.get('content-type')).toContain('application/json');
    expect(await req.clone().json()).toEqual({ ref: 'ghcr.io/org/svc:1.0.0', compatibility: 'strict' });
  });

  it('POSTs refresh with no body', async () => {
    mockFetch.mockResolvedValue(new Response(null, { status: 204 }));
    expect(await api.refresh()).toBeNull();
    expect(requestFor().method).toBe('POST');
    expect(urlFor().pathname).toBe('/api/refresh');
  });
});

describe('fleet legacy endpoint serialization', () => {
  it('omits the query string when no params are given', async () => {
    mockFetch.mockResolvedValue(jsonResponse({ services: [] }));
    await api.fleetServices();
    expect(urlFor().search).toBe('');
    expect(urlFor().pathname).toBe('/api/fleet/services');
  });

  it('serializes fleetServices params, including limit/offset of 0', async () => {
    mockFetch.mockResolvedValue(jsonResponse({ services: [] }));
    await api.fleetServices({ text: 'pay', owner: 'core', scope: 'prod', status: 'Compliant', source: 'k8s', limit: 0, offset: 0 });
    const q = urlFor().searchParams;
    expect(q.get('text')).toBe('pay');
    expect(q.get('status')).toBe('Compliant');
    expect(q.get('limit')).toBe('0');
    expect(q.get('offset')).toBe('0');
  });

  it('serializes fleetServiceGraph path + options', async () => {
    mockFetch.mockResolvedValue(jsonResponse({ nodes: [] }));
    await api.fleetServiceGraph('my service', { direction: 'dependents', transitive: true, maxDepth: 3 });
    expect(urlFor().pathname).toBe('/api/fleet/services/my%20service/graph');
    const q = urlFor().searchParams;
    expect(q.get('direction')).toBe('dependents');
    expect(q.get('transitive')).toBe('true');
    expect(q.get('maxDepth')).toBe('3');
  });

  it('serializes fleetImpact refs and includeObserved', async () => {
    mockFetch.mockResolvedValue(jsonResponse({ classification: 'NON_BREAKING' }));
    await api.fleetImpact('oci://ghcr.io/org/svc:1.0.0', 'oci://ghcr.io/org/svc:2.0.0', true);
    const q = urlFor().searchParams;
    expect(q.get('old')).toBe('oci://ghcr.io/org/svc:1.0.0');
    expect(q.get('new')).toBe('oci://ghcr.io/org/svc:2.0.0');
    expect(q.get('includeObserved')).toBe('true');
    mockFetch.mockReset();
    mockFetch.mockResolvedValue(jsonResponse({}));
    await api.fleetImpact('a', 'b');
    expect(urlFor().searchParams.get('includeObserved')).toBe('false');
  });

  it('passes a slash-bearing key as a query param', async () => {
    mockFetch.mockResolvedValue(jsonResponse({}));
    await api.fleetService('eu/pay');
    expect(urlFor().pathname).toBe('/api/fleet/service');
    expect(urlFor().searchParams.get('key')).toBe('eu/pay');
  });
});

describe('product endpoint serialization + schema validation', () => {
  const meta = { schemaVersion: PRODUCT_SCHEMA_VERSION };
  const productResponse = (data: Record<string, unknown>) => jsonResponse({ meta, ...data });

  it('fleetOverview GET /api/fleet/overview returns a typed answer', async () => {
    mockFetch.mockResolvedValue(productResponse({ summary: {} }));
    const ov = await api.fleetOverview();
    expect(ov.meta.schemaVersion).toBe(PRODUCT_SCHEMA_VERSION);
    expect(urlFor().pathname).toBe('/api/fleet/overview');
  });

  it('fleetEntities joins kinds and serializes every filter', async () => {
    mockFetch.mockResolvedValue(productResponse({ entities: [] }));
    await api.fleetEntities({ text: 'pay', kinds: ['service', 'target'], owner: 'core', domain: 'eu', scope: 'prod', status: 'Compliant', sourceHealth: 'stale', source: 'k8s', limit: 5, offset: 10 });
    const q = urlFor().searchParams;
    expect(q.get('text')).toBe('pay');
    expect(q.get('kinds')).toBe('service,target');
    expect(q.get('domain')).toBe('eu');
    expect(q.get('sourceHealth')).toBe('stale');
    expect(q.get('limit')).toBe('5');
    expect(q.get('offset')).toBe('10');
  });

  it('fleetEntities omits the query string when empty', async () => {
    mockFetch.mockResolvedValue(productResponse({ entities: [] }));
    await api.fleetEntities();
    expect(urlFor().search).toBe('');
  });

  it('fleetEntityDetail uses the kind path param and the key query param', async () => {
    mockFetch.mockResolvedValue(productResponse({ entity: { kind: 'target' } }));
    await api.fleetEntityDetail('target', 'prod/k8s/app');
    expect(urlFor().pathname).toBe('/api/fleet/entities/target');
    expect(urlFor().searchParams.get('key')).toBe('prod/k8s/app');
  });

  it('fleetNeighborhood serializes kind, key, direction, views and bounds', async () => {
    mockFetch.mockResolvedValue(productResponse({ nodes: [] }));
    await api.fleetNeighborhood({ kind: 'service', key: 'eu/pay', direction: 'both', depth: 2, views: ['expected', 'observed'], maxNodes: 40, maxEdges: 80 });
    const q = urlFor().searchParams;
    expect(q.get('kind')).toBe('service');
    expect(q.get('key')).toBe('eu/pay');
    expect(q.get('direction')).toBe('both');
    expect(q.get('views')).toBe('expected,observed');
    expect(q.get('maxNodes')).toBe('40');
    expect(q.get('maxEdges')).toBe('80');
  });

  it('fleetAttention serializes filters and staleOnly', async () => {
    mockFetch.mockResolvedValue(productResponse({ items: [] }));
    await api.fleetAttention({ category: 'stale', severity: 'warning', staleOnly: true, limit: 3, offset: 6 });
    const q = urlFor().searchParams;
    expect(q.get('category')).toBe('stale');
    expect(q.get('severity')).toBe('warning');
    expect(q.get('staleOnly')).toBe('true');
    expect(q.get('limit')).toBe('3');
  });

  it('fleetImpactByIdentity POSTs canonical identities in the body', async () => {
    mockFetch.mockResolvedValue(productResponse({ classification: 'BREAKING' }));
    await api.fleetImpactByIdentity({ snapshotId: 'snap-1', fromRevisionKey: 'svc@a', toRevisionKey: 'svc@b', includeObserved: true });
    const req = requestFor();
    expect(urlFor().pathname).toBe('/api/fleet/impact');
    expect(req.method).toBe('POST');
    expect(await req.clone().json()).toEqual({ snapshotId: 'snap-1', fromRevisionKey: 'svc@a', toRevisionKey: 'svc@b', includeObserved: true });
  });

  it('rejects an unsupported product schema version', async () => {
    mockFetch.mockResolvedValue(jsonResponse({ meta: { schemaVersion: 'pacto.dev/fleet-product/v999' }, summary: {} }));
    await expect(api.fleetOverview()).rejects.toBeInstanceOf(SchemaCompatibilityError);
  });

  it('rejects a product response missing meta entirely', async () => {
    mockFetch.mockResolvedValue(jsonResponse({ items: [] }));
    await expect(api.fleetAttention()).rejects.toBeInstanceOf(SchemaCompatibilityError);
  });
});

describe('error handling', () => {
  it('throws ApiError carrying the status', async () => {
    mockFetch.mockResolvedValue(new Response('not found', { status: 404 }));
    await expect(api.service('missing')).rejects.toBeInstanceOf(ApiError);
    mockFetch.mockResolvedValue(new Response('not found', { status: 404 }));
    try {
      await api.service('missing');
    } catch (e) {
      expect((e as ApiError).status).toBe(404);
    }
  });

  it('extracts detail from a JSON error body', async () => {
    mockFetch.mockResolvedValue(new Response(JSON.stringify({ detail: 'invalid ref' }), { status: 422, headers: { 'content-type': 'application/json' } }));
    await expect(api.resolve('bad-ref')).rejects.toThrow('invalid ref');
  });

  it('extracts title from a JSON error body', async () => {
    mockFetch.mockResolvedValue(new Response(JSON.stringify({ title: 'server error' }), { status: 500, headers: { 'content-type': 'application/json' } }));
    await expect(api.health()).rejects.toThrow('server error');
  });
});

describe('static / WASM transport seam', () => {
  it('serves a fixtured route in static mode via the generated client', async () => {
    (globalThis as any).__PACTO_STATIC__ = { routes: { '/api/services/svc': { name: 'svc' } }, service: 'svc' };
    // The generated GET path parameter is resolved by the client; the seam matches
    // by pathname regardless of the client's absolute base or any query ordering.
    expect(await api.service('svc')).toEqual({ name: 'svc' });
    // The services list degrades to an empty array so the offline app stays quiet.
    expect(await api.services()).toEqual([]);
    // A product endpoint is not fixtured, so it resolves to a null body which the
    // product facade rejects honestly rather than returning a misleading value.
    await expect(api.fleetOverview()).rejects.toBeInstanceOf(SchemaCompatibilityError);
    // The mocked network fetch is never called in static mode.
    expect(mockFetch).not.toHaveBeenCalled();
    delete (globalThis as any).__PACTO_STATIC__;
  });

  it('serves a fixtured query-bearing GET by pathname in static mode', async () => {
    (globalThis as any).__PACTO_STATIC__ = { routes: { '/api/fleet/service': { key: 'eu/pay' } }, service: 'svc' };
    expect(await api.fleetService('eu/pay')).toEqual({ key: 'eu/pay' });
    expect(mockFetch).not.toHaveBeenCalled();
    delete (globalThis as any).__PACTO_STATIC__;
  });
});
