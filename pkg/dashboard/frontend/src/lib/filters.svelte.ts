/** Reactive filter store using Svelte 5 runes, synced to URL hash. */

import { readFiltersFromHash, writeFiltersToHash, EMPTY_FILTERS, type FilterState } from './filters';

// Reactive state object backed by $state
let filters = $state<FilterState>({ ...EMPTY_FILTERS });

// Initialize from current hash on module load
if (typeof window !== 'undefined') {
  filters = readFiltersFromHash(location.hash);
}

/** Get the current filter state (reactive). */
export function getFilters(): FilterState {
  return filters;
}

/** Set a single filter key and sync to URL. */
export function setFilter<K extends keyof FilterState>(key: K, value: string): void {
  filters[key] = value;
  if (typeof window !== 'undefined') {
    location.hash = writeFiltersToHash(location.hash, filters);
  }
}

/** Toggle a filter: set to value if different, clear if already equal. */
export function toggleFilter<K extends keyof FilterState>(key: K, value: string): void {
  if (filters[key] === value) {
    filters[key] = '';
  } else {
    filters[key] = value;
  }
  if (typeof window !== 'undefined') {
    location.hash = writeFiltersToHash(location.hash, filters);
  }
}

/** Clear all filters and sync to URL. */
export function clearFilters(): void {
  filters = { ...EMPTY_FILTERS };
  if (typeof window !== 'undefined') {
    location.hash = writeFiltersToHash(location.hash, filters);
  }
}
