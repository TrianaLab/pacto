/**
 * Component render tests for FleetView.svelte (the Operational Graph view).
 * Verifies it renders the snapshot header + completeness, count tiles, the
 * partial-answer banner, the services table and the needs-attention section,
 * and handles the error state. The `api` module is mocked so no network is hit.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount } from 'svelte';

const { fleetSnapshotFn, fleetStatusFn } = vi.hoisted(() => ({
  fleetSnapshotFn: vi.fn(),
  fleetStatusFn: vi.fn(),
}));

vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return {
    ...actual, // keep the real ApiError so `instanceof` still works
    api: {
      fleetSnapshot: (...a: unknown[]) => fleetSnapshotFn(...a),
      fleetStatus: (...a: unknown[]) => fleetStatusFn(...a),
    },
  };
});

// @ts-expect-error — Svelte component has no declaration file
import FleetView from './FleetView.svelte';
import { ApiError } from '../lib/api.ts';

const snapshot = {
  schemaVersion: 'pacto.dev/fleet/v1',
  snapshotId: 'abc',
  generatedAt: '2026-07-29T10:00:00Z',
  completeness: 'partial',
  limitations: [{ code: 'SOURCE_UNAVAILABLE', source: 'oci', message: 'registry unreachable' }],
  services: {
    payments: { key: 'payments', name: 'payments', owner: { team: 'core' }, status: 'Compliant', revisions: ['payments@sha256:1'], targets: ['prod/k8s/payments'], sources: ['k8s', 'oci'] },
    billing: { key: 'billing', name: 'billing', owner: {}, status: 'NonCompliant', revisions: [], targets: [], sources: ['local'] },
  },
  revisions: { 'payments@sha256:1': {} },
  targets: { 'prod/k8s/payments': {} },
  relationships: [{ fromService: 'billing', to: 'payments', type: 'dependency', provenance: 'declared', resolved: true }],
  sources: [],
};

const status = {
  meta: {},
  items: [{ kind: 'service', name: 'billing', code: 'NON_COMPLIANT', reason: 'contract violation' }],
};

describe('FleetView — operational graph snapshot', () => {
  let target: HTMLElement;

  beforeEach(() => {
    fleetSnapshotFn.mockReset();
    fleetStatusFn.mockReset();
    fleetSnapshotFn.mockResolvedValue(snapshot);
    fleetStatusFn.mockResolvedValue(status);
    target = document.createElement('div');
    document.body.appendChild(target);
  });

  afterEach(() => {
    document.body.removeChild(target);
  });

  it('renders the header with a completeness badge and as-of time', async () => {
    const component = mount(FleetView, { target });
    await vi.waitFor(() => {
      const text = target.textContent || '';
      expect(text).toContain('Operational Graph');
      expect(text).toContain('Partial'); // completeness badge
      expect(text).toContain('as of');
      expect(text).toContain('Jul 29, 2026'); // formatDate(generatedAt)
    });
    unmount(component);
  });

  it('renders count tiles for services, revisions, targets and relationships', async () => {
    const component = mount(FleetView, { target });
    await vi.waitFor(() => {
      const text = target.textContent || '';
      expect(text).toContain('Services');
      expect(text).toContain('Revisions');
      expect(text).toContain('Targets');
      expect(text).toContain('Relationships');
    });
    unmount(component);
  });

  it('shows the partial-answer banner with limitations when not complete', async () => {
    const component = mount(FleetView, { target });
    await vi.waitFor(() => {
      const banner = target.querySelector('.partial-banner');
      expect(banner).toBeTruthy();
      const text = banner?.textContent || '';
      expect(text).toContain('Partial answer');
      expect(text).toContain('SOURCE_UNAVAILABLE');
      expect(text).toContain('registry unreachable');
    });
    unmount(component);
  });

  it('renders one services-table row per service with a detail link and status', async () => {
    const component = mount(FleetView, { target });
    await vi.waitFor(() => {
      const link = target.querySelector('a[href="#/services/billing"]');
      expect(link).toBeTruthy();
      expect(target.querySelector('a[href="#/services/payments"]')).toBeTruthy();
      const text = target.textContent || '';
      expect(text).toContain('Compliant'); // payments status badge
      expect(text).toContain('Non-Compliant'); // billing status badge
      expect(text).toContain('(unowned)'); // billing has no owner
    });
    unmount(component);
  });

  it('renders the needs-attention items from fleetStatus', async () => {
    const component = mount(FleetView, { target });
    await vi.waitFor(() => {
      const text = target.textContent || '';
      expect(text).toContain('Needs attention');
      expect(text).toContain('NON_COMPLIANT');
      expect(text).toContain('contract violation');
    });
    unmount(component);
  });

  it('shows an all-clear state when there are no attention items', async () => {
    fleetStatusFn.mockResolvedValue({ meta: {}, items: [] });
    const component = mount(FleetView, { target });
    await vi.waitFor(() => {
      expect(target.textContent || '').toContain('All clear');
    });
    unmount(component);
  });

  it('renders an error state with retry when the snapshot fetch fails', async () => {
    fleetSnapshotFn.mockRejectedValue(new ApiError(503, 'fleet snapshot unavailable'));
    const component = mount(FleetView, { target });
    await vi.waitFor(() => {
      const text = target.textContent || '';
      expect(text).toContain('Couldn’t load the operational graph');
      expect(text).toContain('fleet snapshot unavailable');
    });
    expect(target.querySelector('.retry-btn')).toBeTruthy();
    unmount(component);
  });
});
