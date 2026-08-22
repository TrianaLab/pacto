/** Shared filter state and pure filter helpers (no runes, can be unit-tested). */

import { ownerMatchesFilter, ownerKey, readinessBucket, type ReadinessBucket } from './format';

export interface FilterState {
  search: string;
  owner: string;
  category: string;
  contractStatus: string;
  readinessStatus: string;
  source: string;
  focus: string;
  group: string;
}

export const EMPTY_FILTERS: FilterState = {
  search: '',
  owner: '',
  category: '',
  contractStatus: '',
  readinessStatus: '',
  source: '',
  focus: '',
  group: '',
};

/**
 * Parse filters from hash query params.
 * Hash format: "#/path?owner=x&category=y"
 * Returns EMPTY_FILTERS if no query params present.
 */
export function readFiltersFromHash(hash: string): FilterState {
  const filters = { ...EMPTY_FILTERS };
  const qsIndex = hash.indexOf('?');
  if (qsIndex === -1) return filters;

  const qs = new URLSearchParams(hash.slice(qsIndex + 1));
  const search = qs.get('search');
  const owner = qs.get('owner');
  const category = qs.get('category');
  const contractStatus = qs.get('contractStatus');
  const readinessStatus = qs.get('readinessStatus');
  const source = qs.get('source');
  const focus = qs.get('focus');
  const group = qs.get('group');

  if (search) filters.search = search;
  if (owner) filters.owner = owner;
  if (category) filters.category = category;
  if (contractStatus) filters.contractStatus = contractStatus;
  if (readinessStatus) filters.readinessStatus = readinessStatus;
  if (source) filters.source = source;
  if (focus) filters.focus = focus;
  if (group) filters.group = group;

  return filters;
}

/**
 * Serialize filters to hash query params, preserving the path.
 * Only non-empty filter keys are serialized.
 * If no filters are active, returns just the path (no "?").
 */
export function writeFiltersToHash(hash: string, f: FilterState): string {
  const qsIndex = hash.indexOf('?');
  const path = qsIndex === -1 ? hash : hash.slice(0, qsIndex);

  const qs = new URLSearchParams();
  if (f.search) qs.set('search', f.search);
  if (f.owner) qs.set('owner', f.owner);
  if (f.category) qs.set('category', f.category);
  if (f.contractStatus) qs.set('contractStatus', f.contractStatus);
  if (f.readinessStatus) qs.set('readinessStatus', f.readinessStatus);
  if (f.source) qs.set('source', f.source);
  if (f.focus) qs.set('focus', f.focus);
  if (f.group) qs.set('group', f.group);

  const qsStr = qs.toString();
  return qsStr ? `${path}?${qsStr}` : path;
}

/** Returns true if any filter is non-empty. */
export function filtersActive(f: FilterState): boolean {
  return !!(f.search || f.owner || f.category || f.contractStatus || f.readinessStatus || f.source);
}

/**
 * Apply all active filters to a service list.
 * - search: matches name OR owner (case-insensitive)
 * - owner: matches the canonical owner key exactly (`team:NAME` / `dri:NAME`), so
 *   a team and a person of the same name filter to different services
 * - category: matches any readiness check's category
 * - contractStatus: matches svc.contractStatus
 * - readinessStatus: matches readinessBucket(svc)
 * - source: matches svc.sources array or svc.source string
 */
export function applyFilters<T extends Record<string, any>>(services: T[], f: FilterState): T[] {
  let list = services;

  if (f.search) {
    const q = f.search.toLowerCase();
    list = list.filter((s) => {
      const nameMatch = s.name?.toLowerCase().includes(q);
      const ownerMatch = ownerMatchesFilter(s.owner, q);
      return nameMatch || ownerMatch;
    });
  }

  if (f.owner) {
    list = list.filter((s) => ownerKey(s.owner) === f.owner);
  }

  if (f.category) {
    list = list.filter((s) => {
      const checks = s.readiness?.checks || [];
      return checks.some((c: any) => c.category === f.category);
    });
  }

  if (f.contractStatus) {
    list = list.filter((s) => s.contractStatus === f.contractStatus);
  }

  if (f.readinessStatus) {
    list = list.filter((s) => readinessBucket(s) === f.readinessStatus);
  }

  if (f.source) {
    list = list.filter((s) => {
      const sources = s.sources || (s.source ? [s.source] : []);
      return sources.includes(f.source);
    });
  }

  return list;
}
