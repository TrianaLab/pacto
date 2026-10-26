import { describe, it, expect } from 'vitest';
import { pageView, offsetOfPage, pageSizes, urlInt, pagedHash } from './paging.ts';

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
    expect(pageView({ total: 10, offset: 99, limit: 5 })).toMatchObject({ page: 2, next: null });
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
