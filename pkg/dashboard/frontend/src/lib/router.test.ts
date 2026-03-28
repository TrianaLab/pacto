import { describe, it, expect } from 'vitest';
import { parseHash, serviceUrl, diffUrl, compareDiffUrl } from './router.ts';

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

  it('parses graph route', () => {
    expect(parseHash('#/graph')).toEqual({ view: 'graph', params: {} });
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
