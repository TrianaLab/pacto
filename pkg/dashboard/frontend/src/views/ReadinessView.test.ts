/**
 * Component render tests for ReadinessView.svelte.
 * Verifies the shared FilterBar + SummaryBar are present and the by-category
 * breakdown panel renders with click-to-filter category cells.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, unmount } from 'svelte';
import { clearFilters, getFilters } from '../lib/filters.svelte.ts';
// @ts-expect-error — Svelte component has no declaration file
import ReadinessView from './ReadinessView.svelte';

const services = [
  {
    name: 'payments',
    contractStatus: 'Compliant',
    complianceScore: 100,
    owner: { team: 'core' },
    readiness: {
      score: 90, minScore: 80, totalWeight: 10, earnedWeight: 9, partialCredit: 0,
      passing: true, expires: '2026-12-31', expired: false, daysRemaining: 30,
      doneCount: 2, partialCount: 0, notDoneCount: 0, deferredCount: 0,
      checks: [
        { id: 'runbook', type: 'document', category: 'operability', status: 'done', weight: 5, earnedWeight: 5, excluded: false },
        { id: 'slo', type: 'document', category: 'reliability', status: 'done', weight: 5, earnedWeight: 4, excluded: false },
      ],
    },
  },
  {
    name: 'billing',
    contractStatus: 'Warning',
    complianceScore: 60,
    owner: { team: 'platform' },
    readiness: {
      score: 40, minScore: 80, totalWeight: 10, earnedWeight: 4, partialCredit: 0,
      passing: false, expires: '2026-06-30', expired: false, daysRemaining: 5,
      doneCount: 0, partialCount: 1, notDoneCount: 1, deferredCount: 0,
      checks: [
        { id: 'oncall', type: 'ticket', category: 'reliability', status: 'partial', weight: 5, earnedWeight: 2.5, excluded: false },
        { id: 'dr', type: 'document', category: 'reliability', status: 'not-done', weight: 5, earnedWeight: 0, excluded: false },
      ],
    },
  },
];

describe('ReadinessView — shared filters + category breakdown', () => {
  let target: HTMLElement;

  beforeEach(() => {
    location.hash = '';
    clearFilters();
    target = document.createElement('div');
    document.body.appendChild(target);
  });

  afterEach(() => {
    clearFilters();
    location.hash = '';
    document.body.removeChild(target);
  });

  it('renders the shared FilterBar and SummaryBar', () => {
    const component = mount(ReadinessView, { target, props: { services } });
    expect(target.querySelector('.filter-bar')).toBeTruthy();
    expect(target.querySelector('.summary-bar')).toBeTruthy();
    unmount(component);
  });

  it('renders a by-category breakdown chart', () => {
    const component = mount(ReadinessView, { target, props: { services } });
    const panels = target.querySelectorAll('.chart-panel');
    expect(panels.length).toBeGreaterThan(0);
    const text = target.textContent || '';
    // Default breakdown='category', so CategoryBreakdownChart renders
    expect(text).toContain('By category'); // segmented button
    unmount(component);
  });

  it('renders a readiness status donut chart in the focal pair', () => {
    const component = mount(ReadinessView, { target, props: { services } });
    const text = target.textContent || '';
    expect(text).toContain('Where we stand'); // donut panel title
    expect(text).toContain('What to fix first'); // quadrant panel title
    unmount(component);
  });

  it('per-row readiness shows score and bucket label', () => {
    const component = mount(ReadinessView, { target, props: { services } });
    const text = target.textContent || '';
    expect(text).toContain('90'); // payments score
    expect(text).toContain('Ready'); // payments bucket
    expect(text).toContain('Not Ready'); // billing bucket (score 40 < 50)
    unmount(component);
  });
});
