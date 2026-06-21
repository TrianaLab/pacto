/**
 * Component render tests for FilterBar.svelte.
 * Verifies facet controls, per-facet option counts, and clear-all visibility.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, unmount } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import FilterBar from './FilterBar.svelte';
import { clearFilters } from '../lib/filters.svelte';

describe('FilterBar — facet controls and counts', () => {
  let target: HTMLElement;

  beforeEach(() => {
    target = document.createElement('div');
    document.body.appendChild(target);
    // Ensure clean filter state
    clearFilters();
  });

  afterEach(() => {
    document.body.removeChild(target);
    clearFilters();
  });

  it('renders the search input and facet controls', () => {
    const component = mount(FilterBar, {
      target,
      props: {
        services: [
          {
            name: 'svc-a',
            owner: { team: 'TeamA' },
            contractStatus: 'Compliant',
            readiness: { score: 100, minScore: 80, passing: true, checks: [{ id: 'c1', category: 'security', status: 'pass' }] },
            sources: ['k8s'],
          },
        ],
      },
    });

    // Search input
    const searchInput = target.querySelector('.filter-search input');
    expect(searchInput).toBeTruthy();
    expect(searchInput?.getAttribute('placeholder')).toContain('Filter');

    // At least one facet select (owner, category, readinessStatus, contractStatus, source)
    const selects = target.querySelectorAll('.filter-select select');
    expect(selects.length).toBeGreaterThan(0);

    unmount(component);
  });

  it('computes correct owner facet counts', () => {
    const component = mount(FilterBar, {
      target,
      props: {
        services: [
          { name: 'a', owner: { team: 'TeamA' } },
          { name: 'b', owner: { team: 'TeamA' } },
          { name: 'c', owner: { team: 'TeamB' } },
          { name: 'd', owner: { dri: 'Alice' } },
          { name: 'e' }, // unowned
        ],
      },
    });

    const ownerSelect = Array.from(target.querySelectorAll('.filter-select')).find((s) =>
      s.textContent?.includes('Owner'),
    )?.querySelector('select');
    expect(ownerSelect).toBeTruthy();

    const ownerOptions = Array.from(ownerSelect?.querySelectorAll('option') || []).filter(
      (o) => o.value !== '', // exclude "All"
    );

    // TeamA: 2, TeamB: 1, Alice: 1, (unowned): 1
    expect(ownerOptions.length).toBe(4);
    const teamAOption = ownerOptions.find((o) => o.textContent?.includes('TeamA'));
    expect(teamAOption?.textContent).toContain('(2)');

    unmount(component);
  });

  it('computes correct category facet counts (services-per-category, not checks)', () => {
    const component = mount(FilterBar, {
      target,
      props: {
        services: [
          {
            name: 'svc-a',
            readiness: {
              score: 100,
              minScore: 80,
              passing: true,
              checks: [
                { id: 'c1', category: 'security', status: 'pass' },
                { id: 'c2', category: 'security', status: 'pass' }, // 2 security checks
              ],
            },
          },
          {
            name: 'svc-b',
            readiness: {
              score: 80,
              minScore: 80,
              passing: true,
              checks: [
                { id: 'c3', category: 'security', status: 'pass' },
                { id: 'c4', category: 'observability', status: 'pass' },
              ],
            },
          },
          {
            name: 'svc-c',
            readiness: {
              score: 60,
              minScore: 80,
              passing: false,
              checks: [{ id: 'c5', category: 'observability', status: 'fail' }],
            },
          },
          {
            name: 'svc-d',
            readiness: {
              score: 50,
              minScore: 80,
              passing: false,
              checks: [{ id: 'c6', status: 'fail' }], // no category → 'other'
            },
          },
        ],
      },
    });

    const categorySelect = Array.from(target.querySelectorAll('.filter-select')).find((s) =>
      s.textContent?.includes('Category'),
    )?.querySelector('select');
    expect(categorySelect).toBeTruthy();

    const categoryOptions = Array.from(categorySelect?.querySelectorAll('option') || []).filter(
      (o) => o.value !== '', // exclude "All"
    );

    // Expected: security (2 services: a, b), observability (2 services: b, c), other (1 service: d)
    expect(categoryOptions.length).toBe(3);

    const securityOption = categoryOptions.find((o) => o.textContent?.includes('security'));
    expect(securityOption?.textContent).toContain('(2)'); // svc-a and svc-b

    const observabilityOption = categoryOptions.find((o) => o.textContent?.includes('observability'));
    expect(observabilityOption?.textContent).toContain('(2)'); // svc-b and svc-c

    const otherOption = categoryOptions.find((o) => o.textContent?.includes('other'));
    expect(otherOption?.textContent).toContain('(1)'); // svc-d

    unmount(component);
  });

  it('computes correct readinessStatus facet counts', () => {
    const component = mount(FilterBar, {
      target,
      props: {
        services: [
          { name: 'a', readiness: { score: 100, minScore: 80, passing: true } }, // ready
          { name: 'b', readiness: { score: 90, minScore: 80, passing: true } }, // ready
          { name: 'c', readiness: { score: 60, minScore: 80, passing: false } }, // partial (>= 50)
          { name: 'd' }, // unknown (no readiness)
        ],
      },
    });

    const readinessSelect = Array.from(target.querySelectorAll('.filter-select')).find((s) =>
      s.textContent?.includes('Readiness'),
    )?.querySelector('select');
    expect(readinessSelect).toBeTruthy();

    const readinessOptions = Array.from(readinessSelect?.querySelectorAll('option') || []).filter(
      (o) => o.value !== '',
    );

    // ready: 2, partial: 1, unknown: 1
    expect(readinessOptions.length).toBe(3);
    const readyOption = readinessOptions.find((o) => o.value === 'ready');
    expect(readyOption).toBeTruthy();
    expect(readyOption?.textContent).toContain('(2)');

    unmount(component);
  });

  it('computes correct contractStatus facet counts', () => {
    const component = mount(FilterBar, {
      target,
      props: {
        services: [
          { name: 'a', contractStatus: 'Compliant' },
          { name: 'b', contractStatus: 'Compliant' },
          { name: 'c', contractStatus: 'Warning' },
          { name: 'd', contractStatus: 'NonCompliant' },
        ],
      },
    });

    const contractStatusSelect = Array.from(target.querySelectorAll('.filter-select')).find((s) =>
      s.textContent?.includes('Contract Status'),
    )?.querySelector('select');
    expect(contractStatusSelect).toBeTruthy();

    const statusOptions = Array.from(contractStatusSelect?.querySelectorAll('option') || []).filter(
      (o) => o.value !== '',
    );

    expect(statusOptions.length).toBe(3);
    const compliantOption = statusOptions.find((o) => o.textContent?.includes('Compliant'));
    expect(compliantOption?.textContent).toContain('(2)');

    unmount(component);
  });

  it('computes correct source facet counts', () => {
    const component = mount(FilterBar, {
      target,
      props: {
        services: [
          { name: 'a', sources: ['k8s', 'oci'] },
          { name: 'b', sources: ['k8s'] },
          { name: 'c', sources: ['local'] },
          { name: 'd', source: 'oci' }, // legacy single source field
        ],
      },
    });

    const sourceSelect = Array.from(target.querySelectorAll('.filter-select')).find((s) =>
      s.textContent?.includes('Source'),
    )?.querySelector('select');
    expect(sourceSelect).toBeTruthy();

    const sourceOptions = Array.from(sourceSelect?.querySelectorAll('option') || []).filter(
      (o) => o.value !== '',
    );

    // k8s: 2, oci: 2, local: 1
    expect(sourceOptions.length).toBe(3);
    const k8sOption = sourceOptions.find((o) => o.textContent?.includes('k8s'));
    expect(k8sOption?.textContent).toContain('(2)');

    unmount(component);
  });

  it('does not show clear button when no filters are active', () => {
    const component = mount(FilterBar, {
      target,
      props: {
        services: [
          { name: 'a', contractStatus: 'Compliant' },
        ],
      },
    });

    const clearBtn = target.querySelector('.clear-btn');
    expect(clearBtn).toBeNull();

    unmount(component);
  });

  it('shows clear button when a filter is active', async () => {
    // Import setFilter from the reactive store
    const { setFilter } = await import('../lib/filters.svelte');

    const component = mount(FilterBar, {
      target,
      props: {
        services: [
          { name: 'a', contractStatus: 'Compliant' },
          { name: 'b', contractStatus: 'Warning' },
        ],
      },
    });

    // No clear button initially
    let clearBtn = target.querySelector('.clear-btn');
    expect(clearBtn).toBeNull();

    // Set a filter via the reactive store
    setFilter('contractStatus', 'Compliant');

    // The component should now show the clear button
    // (Need to wait for next tick in tests)
    await new Promise(resolve => setTimeout(resolve, 0));
    clearBtn = target.querySelector('.clear-btn');
    expect(clearBtn).toBeTruthy();
    expect(clearBtn?.textContent).toContain('Clear');

    unmount(component);
  });
});
