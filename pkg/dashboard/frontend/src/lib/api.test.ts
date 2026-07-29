import { describe, it, expect, vi, beforeEach } from 'vitest';

// Mock fetch globally before importing the module
const mockFetch = vi.fn();
vi.stubGlobal('fetch', mockFetch);

const { api } = await import('./api.ts');

function jsonResponse(data: unknown, status = 200) {
  return {
    ok: true,
    status,
    json: () => Promise.resolve(data),
    text: () => Promise.resolve(JSON.stringify(data)),
  };
}

function errorResponse(status: number, body = '') {
  return {
    ok: false,
    status,
    statusText: 'Error',
    text: () => Promise.resolve(body),
  };
}

beforeEach(() => {
  mockFetch.mockReset();
});

describe('api.health', () => {
  it('calls GET /health', async () => {
    mockFetch.mockResolvedValue(jsonResponse({ version: '1.0.0' }));
    const result = await api.health();
    expect(result).toEqual({ version: '1.0.0' });
    expect(mockFetch).toHaveBeenCalledWith('/health', expect.objectContaining({ method: 'GET' }));
  });
});

describe('api.services', () => {
  it('calls GET /api/services', async () => {
    mockFetch.mockResolvedValue(jsonResponse([{ name: 'svc-a' }]));
    const result = await api.services();
    expect(result).toEqual([{ name: 'svc-a' }]);
  });
});

describe('api.service', () => {
  it('encodes service name in URL', async () => {
    mockFetch.mockResolvedValue(jsonResponse({ name: 'my service' }));
    await api.service('my service');
    expect(mockFetch).toHaveBeenCalledWith(
      '/api/services/my%20service',
      expect.any(Object)
    );
  });
});

describe('api.serviceAtVersion', () => {
  it('builds the versioned detail URL and encodes parts', async () => {
    mockFetch.mockResolvedValue(jsonResponse({ name: 'my service', version: '1.0.0' }));
    await api.serviceAtVersion('my service', '1.0.0+build');
    expect(mockFetch).toHaveBeenCalledWith(
      '/api/services/my%20service/versions/1.0.0%2Bbuild',
      expect.any(Object)
    );
  });
});

describe('api.diff', () => {
  it('builds correct query string', async () => {
    mockFetch.mockResolvedValue(jsonResponse({ changes: [] }));
    await api.diff('svc-a', '1.0.0', 'svc-a', '2.0.0');
    const url = mockFetch.mock.calls[0][0];
    expect(url).toContain('from_name=svc-a');
    expect(url).toContain('from_version=1.0.0');
    expect(url).toContain('to_name=svc-a');
    expect(url).toContain('to_version=2.0.0');
  });

  it('handles empty version strings', async () => {
    mockFetch.mockResolvedValue(jsonResponse({ changes: [] }));
    await api.diff('svc-a', '', 'svc-a', '');
    const url = mockFetch.mock.calls[0][0];
    expect(url).toContain('from_version=');
    expect(url).toContain('to_version=');
  });
});

describe('api.resolve', () => {
  it('sends POST with JSON body', async () => {
    mockFetch.mockResolvedValue(jsonResponse({ ok: true }));
    await api.resolve('ghcr.io/org/svc:1.0.0', 'strict');
    const [, opts] = mockFetch.mock.calls[0];
    expect(opts.method).toBe('POST');
    expect(opts.headers['Content-Type']).toBe('application/json');
    expect(JSON.parse(opts.body)).toEqual({ ref: 'ghcr.io/org/svc:1.0.0', compatibility: 'strict' });
  });
});

describe('api.fleetSnapshot', () => {
  it('calls GET /api/fleet/snapshot', async () => {
    mockFetch.mockResolvedValue(jsonResponse({ completeness: 'complete' }));
    const result = await api.fleetSnapshot();
    expect(result).toEqual({ completeness: 'complete' });
    expect(mockFetch).toHaveBeenCalledWith('/api/fleet/snapshot', expect.objectContaining({ method: 'GET' }));
  });
});

describe('api.fleetServices', () => {
  it('builds a query string from the provided params', async () => {
    mockFetch.mockResolvedValue(jsonResponse({ services: [] }));
    await api.fleetServices({ text: 'pay', owner: 'core', scope: 'prod', status: 'Compliant', source: 'k8s', limit: 10, offset: 20 });
    const url = mockFetch.mock.calls[0][0];
    expect(url).toContain('/api/fleet/services?');
    expect(url).toContain('text=pay');
    expect(url).toContain('owner=core');
    expect(url).toContain('scope=prod');
    expect(url).toContain('status=Compliant');
    expect(url).toContain('source=k8s');
    expect(url).toContain('limit=10');
    expect(url).toContain('offset=20');
  });

  it('omits the query string when no params are given', async () => {
    mockFetch.mockResolvedValue(jsonResponse({ services: [] }));
    await api.fleetServices();
    expect(mockFetch.mock.calls[0][0]).toBe('/api/fleet/services');
  });

  it('includes limit/offset of 0', async () => {
    mockFetch.mockResolvedValue(jsonResponse({ services: [] }));
    await api.fleetServices({ limit: 0, offset: 0 });
    const url = mockFetch.mock.calls[0][0];
    expect(url).toContain('limit=0');
    expect(url).toContain('offset=0');
  });
});

describe('api.fleetServiceGraph', () => {
  it('encodes the name and builds the options query string', async () => {
    mockFetch.mockResolvedValue(jsonResponse({ nodes: [] }));
    await api.fleetServiceGraph('my service', { direction: 'dependents', transitive: true, maxDepth: 3 });
    const url = mockFetch.mock.calls[0][0];
    expect(url).toContain('/api/fleet/services/my%20service/graph?');
    expect(url).toContain('direction=dependents');
    expect(url).toContain('transitive=true');
    expect(url).toContain('maxDepth=3');
  });

  it('omits the query string when no options are given', async () => {
    mockFetch.mockResolvedValue(jsonResponse({ nodes: [] }));
    await api.fleetServiceGraph('svc');
    expect(mockFetch.mock.calls[0][0]).toBe('/api/fleet/services/svc/graph');
  });
});

describe('api.fleetStatus', () => {
  it('calls GET /api/fleet/status', async () => {
    mockFetch.mockResolvedValue(jsonResponse({ items: [] }));
    await api.fleetStatus();
    expect(mockFetch).toHaveBeenCalledWith('/api/fleet/status', expect.objectContaining({ method: 'GET' }));
  });
});

describe('api.fleetImpact', () => {
  it('builds the query string and encodes refs', async () => {
    mockFetch.mockResolvedValue(jsonResponse({ classification: 'NON_BREAKING' }));
    await api.fleetImpact('oci://ghcr.io/org/svc:1.0.0', 'oci://ghcr.io/org/svc:2.0.0', true);
    const url = mockFetch.mock.calls[0][0];
    expect(url).toContain('/api/fleet/impact?');
    expect(url).toContain('old=oci%3A%2F%2Fghcr.io%2Forg%2Fsvc%3A1.0.0');
    expect(url).toContain('new=oci%3A%2F%2Fghcr.io%2Forg%2Fsvc%3A2.0.0');
    expect(url).toContain('includeObserved=true');
  });

  it('defaults includeObserved to false', async () => {
    mockFetch.mockResolvedValue(jsonResponse({ classification: 'NON_BREAKING' }));
    await api.fleetImpact('a', 'b');
    expect(mockFetch.mock.calls[0][0]).toContain('includeObserved=false');
  });
});

describe('error handling', () => {
  it('throws ApiError with status for non-ok responses', async () => {
    mockFetch.mockResolvedValue(errorResponse(404, 'not found'));
    await expect(api.service('missing')).rejects.toThrow('not found');
    try {
      await api.service('missing');
    } catch (e: unknown) {
      expect((e as { status: number }).status).toBe(404);
    }
  });

  it('extracts detail from JSON error body', async () => {
    mockFetch.mockResolvedValue(errorResponse(422, JSON.stringify({ detail: 'invalid ref' })));
    await expect(api.resolve('bad-ref')).rejects.toThrow('invalid ref');
  });

  it('extracts title from JSON error body', async () => {
    mockFetch.mockResolvedValue(errorResponse(500, JSON.stringify({ title: 'server error' })));
    await expect(api.health()).rejects.toThrow('server error');
  });

  it('falls back to raw text when JSON parsing fails', async () => {
    mockFetch.mockResolvedValue(errorResponse(500, 'plain text error'));
    await expect(api.health()).rejects.toThrow('plain text error');
  });
});

describe('204 responses', () => {
  it('returns null for 204 No Content', async () => {
    mockFetch.mockResolvedValue({ ok: true, status: 204, json: vi.fn(), text: vi.fn() });
    const result = await api.resolve('ref');
    expect(result).toBeNull();
  });
});

describe('static mode', () => {
  it('serves embedded data in static mode', async () => {
    (window as any).__PACTO_STATIC__ = { routes: { '/api/services/svc': { name: 'svc' } }, service: 'svc' };
    const res = await api.service('svc');
    expect(res).toEqual({ name: 'svc' });
    delete (window as any).__PACTO_STATIC__;
  });
});
