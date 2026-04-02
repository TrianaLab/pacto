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
