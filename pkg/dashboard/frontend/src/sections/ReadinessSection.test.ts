/**
 * Component render tests for ReadinessSection.svelte.
 * Verifies the reworked readiness shape: gate/score header, assessment expiry +
 * countdown, per-check declared status + category + weight/earnedWeight, and the
 * revision-history table.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, unmount } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import ReadinessSection from './ReadinessSection.svelte';

const readiness = {
  score: 75,
  minScore: 80,
  totalWeight: 10,
  earnedWeight: 7,
  partialCredit: 1.5,
  passing: false,
  expires: '2026-12-31',
  expired: false,
  daysRemaining: 30,
  doneCount: 1,
  partialCount: 1,
  notDoneCount: 1,
  deferredCount: 0,
  revisions: [
    { date: '2026-01-15', version: '1.2.0', author: 'Ada Lovelace', description: 'Initial readiness review' },
  ],
  checks: [
    { id: 'runbook', type: 'document', category: 'operability', status: 'done', weight: 4, earnedWeight: 4, evidence: 'https://wiki/runbook', excluded: false },
    { id: 'oncall', type: 'ticket', category: 'reliability', status: 'partial', weight: 3, earnedWeight: 1.5, evidence: 'JIRA-42', excluded: false },
    { id: 'dr-plan', type: 'document', category: 'reliability', status: 'not-done', weight: 3, earnedWeight: 0, excluded: false },
  ],
};

describe('ReadinessSection — reworked readiness shape', () => {
  let target: HTMLElement;

  beforeEach(() => {
    target = document.createElement('div');
    document.body.appendChild(target);
  });

  afterEach(() => {
    document.body.removeChild(target);
  });

  function render(over = {}) {
    return mount(ReadinessSection, {
      target,
      props: { readiness: { ...readiness, ...over }, docs: [], open: true },
    });
  }

  it('renders score, gate and earned/total weight in the header', () => {
    const component = render();
    const text = target.textContent || '';
    expect(text).toContain('75');
    expect(text).toContain('FAIL');
    expect(text).toContain('80'); // minScore
    expect(text).toContain('7 / 10'); // earned / total weight
    unmount(component);
  });

  it('shows the assessment expiry date and a countdown', () => {
    const component = render();
    const text = target.textContent || '';
    expect(text).toContain('2026-12-31');
    expect(text).toContain('expires in 30 days');
    unmount(component);
  });

  it('shows an Expired state when the assessment is expired', () => {
    const component = render({ expired: true, daysRemaining: -5 });
    const text = target.textContent || '';
    expect(text).toContain('Expired');
    unmount(component);
  });

  it('renders per-check declared status and category', () => {
    const component = render();
    const text = target.textContent || '';
    // declared statuses (labels)
    expect(text).toContain('Done');
    expect(text).toContain('Partial');
    expect(text).toContain('Not done');
    // categories
    expect(text).toContain('operability');
    expect(text).toContain('reliability');
    unmount(component);
  });

  it('renders earned weight per check', () => {
    const component = render();
    // The earned column carries the per-check earnedWeight value.
    const rows = target.querySelectorAll('.readiness-table tbody tr');
    expect(rows.length).toBeGreaterThanOrEqual(3);
    unmount(component);
  });

  it('does not render the old Current/Expired/Invalid per-check columns', () => {
    const component = render();
    const headers = Array.from(target.querySelectorAll('.readiness-table thead th')).map((th) => th.textContent?.trim());
    expect(headers).toContain('Category');
    expect(headers).toContain('Earned');
    expect(headers).not.toContain('Remaining');
    unmount(component);
  });

  it('renders a revision-history row', () => {
    const component = render();
    const revTable = target.querySelector('.revision-table');
    expect(revTable).toBeTruthy();
    const text = revTable?.textContent || '';
    expect(text).toContain('1.2.0');
    expect(text).toContain('Ada Lovelace');
    expect(text).toContain('Initial readiness review');
    unmount(component);
  });

  it('omits the revision-history table when there are no revisions', () => {
    const component = render({ revisions: [] });
    expect(target.querySelector('.revision-table')).toBeNull();
    unmount(component);
  });

  it('renders nothing when there are no checks', () => {
    const component = mount(ReadinessSection, {
      target,
      props: { readiness: { ...readiness, checks: [] }, docs: [], open: true },
    });
    expect(target.querySelector('.readiness-table')).toBeNull();
    unmount(component);
  });
});
