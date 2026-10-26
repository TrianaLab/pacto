import { describe, it, expect } from 'vitest';
import { pageView, offsetOfPage, pageSizes, urlInt, pagedHash, pastEnd, hashWith, clientPage } from './paging.ts';

describe('pageView', () => {
  it('reads the first, a middle and the last page', () => {
    expect(pageView({ total: 120, offset: 0, limit: 50 })).toEqual({
      from: 1, to: 50, total: 120, limit: 50, page: 1, pages: 3, first: null, prev: null, next: 50, last: 100,
    });
    expect(pageView({ total: 120, offset: 50, limit: 50 })).toMatchObject({ from: 51, to: 100, page: 2, first: 0, prev: 0, next: 100, last: 100 });
    expect(pageView({ total: 120, offset: 100, limit: 50 })).toMatchObject({ from: 101, to: 120, page: 3, next: null, last: null, prev: 50 });
  });

  it('reads an empty list, a missing page and an offset off the grid', () => {
    expect(pageView({ total: 0, offset: 0, limit: 25 })).toMatchObject({ from: 0, to: 0, page: 1, pages: 1, next: null, last: null });
    expect(pageView(null)).toMatchObject({ total: 0, limit: 1, pages: 1 });
    expect(pageView({ total: 10, offset: 3, limit: 5 })).toMatchObject({ from: 4, to: 8, page: 1, prev: 0, next: 8, last: 5 });
    expect(pageView({ total: 10, offset: 99, limit: 5 })).toMatchObject({ page: 2, next: null, prev: 5, last: 5 });
  });

  it('knows a page past the end from an empty list', () => {
    expect(pastEnd({ total: 3095, offset: 5000, limit: 25 }, 0)).toBe(true);
    expect(pastEnd({ total: 0, offset: 50, limit: 25 }, 0)).toBe(false);
    expect(pastEnd({ total: 30, offset: 25, limit: 25 }, 5)).toBe(false);
    expect(pastEnd({ total: 30, offset: 0, limit: 25 }, 0)).toBe(false);
    expect(pastEnd(null, 0)).toBe(false);
  });
});

describe('page helpers', () => {
  it('clamps a jump to a page that exists', () => {
    expect(offsetOfPage(3, 5, 20)).toBe(40);
    expect(offsetOfPage(0, 5, 20)).toBe(0);
    expect(offsetOfPage(99, 5, 20)).toBe(80);
    expect(offsetOfPage(Number.NaN, 0, 20)).toBe(0);
  });

  it('always offers the current page size', () => {
    expect(pageSizes(50)).toEqual([25, 50, 100]);
    expect(pageSizes(10)).toEqual([10, 25, 50, 100]);
    expect(pageSizes(0, [5])).toEqual([5]);
  });

  it('reads whole numbers from a URL and leaves defaults out of it', () => {
    expect(urlInt('40')).toBe(40);
    expect(urlInt('-3')).toBe(0);
    expect(urlInt('x')).toBe(0);
    expect(urlInt(undefined)).toBe(0);
    expect(pagedHash('#/delivery/history', { offset: 0, q: '', limit: undefined })).toBe('#/delivery/history');
    expect(pagedHash('#/delivery/history', { namespace: 'prod', stage: 'pay', offset: 50, q: 'carol' }))
      .toBe('#/delivery/history?namespace=prod&stage=pay&offset=50&q=carol');
    expect(pagedHash('#/x', { a: null })).toBe('#/x');
  });
});

describe('limitParam', () => {
  it('leaves the default page size out of a link', async () => {
    const { limitParam } = await import('./paging.ts');
    expect(limitParam(50, '', 50)).toBeUndefined();
    expect(limitParam(50, '50', 50)).toBe(50);
    expect(limitParam(100, undefined, 50)).toBe(100);
  });
});

describe('client-held lists', () => {
  it('sets and removes query keys and keeps the rest', () => {
    expect(hashWith('#/readiness?owner=a', { offset: 50, limit: null })).toBe('#/readiness?owner=a&offset=50');
    expect(hashWith('#/readiness?owner=a&offset=50', { offset: 0 })).toBe('#/readiness?owner=a');
    expect(hashWith('#/', { offset: '' })).toBe('#/');
    expect(hashWith('', { offset: 25 })).toBe('#/?offset=25');
    expect(hashWith('#?sort=a', { offset: 25 })).toBe('#/?sort=a&offset=25');
  });

  it('pages a held list like the server pages its lists, the end clamped', () => {
    const all = Array.from({ length: 60 }, (_, i) => i);
    expect(clientPage(all, 25, 25)).toEqual({ items: all.slice(25, 50), page: { total: 60, offset: 25, limit: 25 } });
    expect(clientPage(all, 900, 25).page.offset).toBe(50);
    expect(clientPage([], 50, 25)).toEqual({ items: [], page: { total: 0, offset: 0, limit: 25 } });
    expect(clientPage(all, -5, 0).page).toEqual({ total: 60, offset: 0, limit: 1 });
  });
});
