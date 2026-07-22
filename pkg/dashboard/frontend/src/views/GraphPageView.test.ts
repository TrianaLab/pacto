/**
 * Component render tests for GraphPageView.svelte.
 * Verifies deep-link bridge: hash params reflect into filter state and vice-versa.
 *
 * NOTE: The GraphPageView uses Cytoscape for the graph canvas, which requires
 * HTMLCanvasElement.getContext() — not implemented in jsdom. The graph rendering
 * is not critical to the filter deep-link contract, so these tests focus on the
 * filter store round-trip (syncFromHash + setFilter) which is the seam the view
 * actually uses. The store layer is the deep-link bridge; the view's effects are
 * just wiring.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { clearFilters, getFilters, setFilter, syncFromHash } from '../lib/filters.svelte';
import { readFiltersFromHash, writeFiltersToHash } from '../lib/filters';

describe('GraphPageView — deep-link bridge (filter store seam)', () => {
  beforeEach(() => {
    clearFilters();
    location.hash = '#/graph';
  });

  afterEach(() => {
    clearFilters();
  });

  it('reads filter state from hash via syncFromHash', () => {
    // Set a hash with filters.
    location.hash = '#/graph?contractStatus=NonCompliant&group=owner&focus=core';
    // Call syncFromHash, which the view calls on mount + hashchange.
    syncFromHash();

    const f = getFilters();
    expect(f.contractStatus).toBe('NonCompliant');
    expect(f.group).toBe('owner');
    expect(f.focus).toBe('core');
  });

  it('writes filter changes back to hash via setFilter', () => {
    location.hash = '#/graph';

    // setFilter updates the reactive store AND writes to location.hash.
    setFilter('contractStatus', 'Compliant');

    // Hash should now include the filter.
    expect(location.hash).toContain('contractStatus=Compliant');
    const f = getFilters();
    expect(f.contractStatus).toBe('Compliant');
  });

  it('writes group toggle back to hash', () => {
    location.hash = '#/graph';

    setFilter('group', 'owner');

    expect(location.hash).toContain('group=owner');
    expect(getFilters().group).toBe('owner');
  });

  it('writes focus change back to hash', () => {
    location.hash = '#/graph?group=owner';
    syncFromHash();

    setFilter('focus', 'core');

    expect(location.hash).toContain('focus=core');
    expect(getFilters().focus).toBe('core');
  });

  it('clears filter key when set to empty', () => {
    location.hash = '#/graph?contractStatus=Compliant';
    syncFromHash();

    // Clear the filter by setting to '' (the "all" sentinel).
    setFilter('contractStatus', '');

    // The hash should no longer contain contractStatus.
    expect(location.hash).not.toContain('contractStatus');
    expect(getFilters().contractStatus).toBe('');
  });

  it('round-trips all filters through hash', () => {
    location.hash = '#/graph';

    // Set multiple filters.
    setFilter('contractStatus', 'Warning');
    setFilter('source', 'k8s');
    setFilter('group', 'owner');
    setFilter('focus', 'infra');

    // Hash should contain all.
    const writtenHash = location.hash;
    expect(writtenHash).toContain('contractStatus=Warning');
    expect(writtenHash).toContain('source=k8s');
    expect(writtenHash).toContain('group=owner');
    expect(writtenHash).toContain('focus=infra');

    // Simulate a fresh page load: clear the reactive state, then re-read from hash.
    clearFilters(); // wipes reactive state
    location.hash = writtenHash; // restore the hash
    syncFromHash();

    const f = getFilters();
    expect(f.contractStatus).toBe('Warning');
    expect(f.source).toBe('k8s');
    expect(f.group).toBe('owner');
    expect(f.focus).toBe('infra');
  });
});
