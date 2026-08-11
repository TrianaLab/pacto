/**
 * Component render tests for OwnerDetailView.svelte.
 * Verifies the owner gets a readiness/compliance summary (SummaryBar) and a
 * ServicesTable filtered to the owner's services, alongside the owner metadata.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import OwnerDetailView from './OwnerDetailView.svelte';

const services = [
  {
    name: 'payments',
    contractStatus: 'Compliant',
    complianceScore: 100,
    blastRadius: 2,
    owner: { team: 'core', dri: 'ada', contacts: [{ type: 'slack', value: '#core' }] },
    readiness: { score: 90, minScore: 80, passing: true, expired: false, doneCount: 2, partialCount: 0, notDoneCount: 0, deferredCount: 0, checks: [] },
  },
  {
    name: 'ledger',
    contractStatus: 'Warning',
    complianceScore: 70,
    blastRadius: 1,
    owner: { team: 'core', dri: 'ada' },
    readiness: { score: 50, minScore: 80, passing: false, expired: false, doneCount: 1, partialCount: 0, notDoneCount: 1, deferredCount: 0, checks: [] },
  },
  {
    name: 'unrelated',
    contractStatus: 'Compliant',
    complianceScore: 100,
    owner: { team: 'other' },
  },
];

describe('OwnerDetailView — readiness summary + services table', () => {
  let target: HTMLElement;

  beforeEach(() => {
    // Graph fetch fails fast and is caught; stub it so the test does not hit the network.
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new Error('no network'))));
    target = document.createElement('div');
    document.body.appendChild(target);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    document.body.removeChild(target);
  });

  it('renders a SummaryBar for the owner services', () => {
    const component = mount(OwnerDetailView, { target, props: { owner: 'team:core', services } });
    expect(target.querySelector('.summary-bar')).toBeTruthy();
    unmount(component);
  });

  it('renders a ServicesTable scoped to the owner (excludes other owners)', () => {
    const component = mount(OwnerDetailView, { target, props: { owner: 'team:core', services } });
    const rows = target.querySelectorAll('tbody tr');
    // Two services belong to "core"; the unrelated one is excluded.
    expect(rows.length).toBe(2);
    const text = target.textContent || '';
    expect(text).toContain('payments');
    expect(text).toContain('ledger');
    expect(text).not.toContain('unrelated');
    unmount(component);
  });

  it('routes on the canonical key, so a DRI never inherits a same-named team', () => {
    const named = [
      ...services,
      { name: 'oncall-notes', contractStatus: 'Compliant', complianceScore: 100, owner: { dri: 'core' } },
    ];
    const component = mount(OwnerDetailView, { target, props: { owner: 'dri:core', services: named } });
    const rows = target.querySelectorAll('tbody tr');
    expect(rows.length).toBe(1);
    const text = target.textContent || '';
    expect(text).toContain('oncall-notes');
    expect(text).not.toContain('payments');
    // The heading names the person and says which namespace they are.
    expect(target.querySelector('.owner-kind-badge')?.textContent).toBe('DRI');
    unmount(component);
  });

  it('keeps the owner metadata (team / dri / contacts)', () => {
    const component = mount(OwnerDetailView, { target, props: { owner: 'team:core', services } });
    const meta = target.querySelector('.owner-meta');
    expect(meta).toBeTruthy();
    const text = meta?.textContent || '';
    expect(text).toContain('core'); // team
    expect(text).toContain('ada'); // dri
    expect(text).toContain('#core'); // contact
    unmount(component);
  });
});
