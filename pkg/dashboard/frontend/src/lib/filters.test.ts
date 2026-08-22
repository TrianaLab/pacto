import { describe, it, expect } from 'vitest';
import {
  readFiltersFromHash,
  writeFiltersToHash,
  EMPTY_FILTERS,
  filtersActive,
  applyFilters,
  type FilterState,
} from './filters';

describe('filters hash', () => {
  it('round-trips through the hash, preserving the path', () => {
    const f: FilterState = { ...EMPTY_FILTERS, owner: 'team/x', category: 'security' };
    const h = writeFiltersToHash('#/readiness', f);
    expect(h).toContain('#/readiness?');
    expect(readFiltersFromHash(h)).toEqual(f);
  });

  it('serializes only non-empty keys', () => {
    expect(writeFiltersToHash('#/', EMPTY_FILTERS)).toBe('#/');
    expect(writeFiltersToHash('#/owners', EMPTY_FILTERS)).toBe('#/owners');
  });

  it('parses filters from hash query params', () => {
    const f = readFiltersFromHash('#/services?search=pay&owner=team%2Fpay&contractStatus=Compliant');
    expect(f.search).toBe('pay');
    expect(f.owner).toBe('team/pay');
    expect(f.contractStatus).toBe('Compliant');
    expect(f.category).toBe('');
  });

  it('ignores unknown query params', () => {
    const f = readFiltersFromHash('#/services?search=x&unknown=y');
    expect(f.search).toBe('x');
    expect(f).not.toHaveProperty('unknown');
  });

  it('handles hash without query params', () => {
    expect(readFiltersFromHash('#/services')).toEqual(EMPTY_FILTERS);
    expect(readFiltersFromHash('#/')).toEqual(EMPTY_FILTERS);
  });

  it('preserves path when writing filters', () => {
    const f: FilterState = { ...EMPTY_FILTERS, search: 'test' };
    expect(writeFiltersToHash('#/services', f)).toContain('#/services?');
    expect(writeFiltersToHash('#/owners/team%2Fpay', f)).toContain('#/owners/team%2Fpay?');
  });

  it('round-trips graph view-state keys focus and group', () => {
    const f: FilterState = { ...EMPTY_FILTERS, contractStatus: 'NonCompliant', group: 'owner', focus: 'payments' };
    const h = writeFiltersToHash('#/graph', f);
    expect(h).toContain('#/graph?');
    expect(readFiltersFromHash(h)).toEqual(f);
  });

  it('excludes focus and group from filtersActive (they are view state)', () => {
    expect(filtersActive({ ...EMPTY_FILTERS, group: 'owner' })).toBe(false);
    expect(filtersActive({ ...EMPTY_FILTERS, focus: 'payments' })).toBe(false);
  });
});

describe('filtersActive', () => {
  it('returns false for EMPTY_FILTERS', () => {
    expect(filtersActive(EMPTY_FILTERS)).toBe(false);
  });

  it('returns true when any filter is non-empty', () => {
    expect(filtersActive({ ...EMPTY_FILTERS, search: 'x' })).toBe(true);
    expect(filtersActive({ ...EMPTY_FILTERS, owner: 'team/x' })).toBe(true);
    expect(filtersActive({ ...EMPTY_FILTERS, category: 'security' })).toBe(true);
  });
});

describe('applyFilters', () => {
  const svcs = [
    {
      name: 'pay',
      owner: { team: 'team/pay' },
      contractStatus: 'Compliant',
      sources: ['k8s', 'oci'],
      readiness: { score: 100, minScore: 80, passing: true, totalWeight: 1, currentWeight: 1, currentCount: 1, expiredCount: 0, checks: [{ id: '1', type: 'manual', status: 'Current', weight: 1, expires: '2027-01-01', category: 'security' }] },
    },
    {
      name: 'auth',
      owner: { team: 'team/auth' },
      contractStatus: 'Warning',
      sources: ['oci'],
      readiness: { score: 60, minScore: 80, passing: false, totalWeight: 1, currentWeight: 0.6, currentCount: 1, expiredCount: 0, checks: [{ id: '2', type: 'manual', status: 'Current', weight: 1, expires: '2027-01-01', category: 'compliance' }] },
    },
    {
      name: 'billing',
      owner: { team: 'team/pay' },
      contractStatus: 'NonCompliant',
      source: 'local',
      readiness: { score: 0, minScore: 80, passing: false, totalWeight: 1, currentWeight: 0, currentCount: 0, expiredCount: 1, checks: [{ id: '3', type: 'manual', status: 'Expired', weight: 1, expires: '2025-01-01', category: 'security' }] },
    },
    {
      name: 'search',
      owner: 'team/search',
      contractStatus: 'Reference',
      sources: ['k8s'],
    },
  ];

  it('filters by search matching name', () => {
    const f: FilterState = { ...EMPTY_FILTERS, search: 'billing' };
    const result = applyFilters(svcs, f);
    expect(result).toHaveLength(1);
    expect(result[0].name).toBe('billing');
  });

  it('filters by search matching owner', () => {
    const f: FilterState = { ...EMPTY_FILTERS, search: 'auth' };
    const result = applyFilters(svcs, f);
    expect(result).toHaveLength(1);
    expect(result[0].name).toBe('auth');
  });

  it('filters by owner key', () => {
    const f: FilterState = { ...EMPTY_FILTERS, owner: 'team:team/pay' };
    const result = applyFilters(svcs, f);
    expect(result).toHaveLength(2);
    expect(result.map(s => s.name).sort()).toEqual(['billing', 'pay']);
  });

  it('filters by category', () => {
    const f: FilterState = { ...EMPTY_FILTERS, category: 'security' };
    const result = applyFilters(svcs, f);
    expect(result).toHaveLength(2);
    expect(result.map(s => s.name).sort()).toEqual(['billing', 'pay']);
  });

  it('filters by contractStatus', () => {
    const f: FilterState = { ...EMPTY_FILTERS, contractStatus: 'Compliant' };
    const result = applyFilters(svcs, f);
    expect(result).toHaveLength(1);
    expect(result[0].name).toBe('pay');
  });

  it('filters by readinessStatus', () => {
    const fReady: FilterState = { ...EMPTY_FILTERS, readinessStatus: 'ready' };
    expect(applyFilters(svcs, fReady)).toHaveLength(1);
    expect(applyFilters(svcs, fReady)[0].name).toBe('pay');

    const fPartial: FilterState = { ...EMPTY_FILTERS, readinessStatus: 'partial' };
    expect(applyFilters(svcs, fPartial)).toHaveLength(1);
    expect(applyFilters(svcs, fPartial)[0].name).toBe('auth');

    const fNotReady: FilterState = { ...EMPTY_FILTERS, readinessStatus: 'not-ready' };
    expect(applyFilters(svcs, fNotReady)).toHaveLength(1);
    expect(applyFilters(svcs, fNotReady)[0].name).toBe('billing');

    const fUnknown: FilterState = { ...EMPTY_FILTERS, readinessStatus: 'unknown' };
    expect(applyFilters(svcs, fUnknown)).toHaveLength(1);
    expect(applyFilters(svcs, fUnknown)[0].name).toBe('search');
  });

  it('filters by source (sources array)', () => {
    const f: FilterState = { ...EMPTY_FILTERS, source: 'k8s' };
    const result = applyFilters(svcs, f);
    expect(result).toHaveLength(2);
    expect(result.map(s => s.name).sort()).toEqual(['pay', 'search']);
  });

  it('filters by source (single source field)', () => {
    const f: FilterState = { ...EMPTY_FILTERS, source: 'local' };
    const result = applyFilters(svcs, f);
    expect(result).toHaveLength(1);
    expect(result[0].name).toBe('billing');
  });

  it('applies multiple filters (AND logic)', () => {
    const f: FilterState = { ...EMPTY_FILTERS, owner: 'team:team/pay', contractStatus: 'Compliant' };
    const result = applyFilters(svcs, f);
    expect(result).toHaveLength(1);
    expect(result[0].name).toBe('pay');
  });

  it('returns all services when no filters are active', () => {
    expect(applyFilters(svcs, EMPTY_FILTERS)).toHaveLength(4);
  });
});
