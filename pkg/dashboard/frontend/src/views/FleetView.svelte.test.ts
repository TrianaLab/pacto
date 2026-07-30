/**
 * Component tests for FleetView.svelte — the redesigned Operational Graph.
 * Verifies the real topology controls (perspective, layer, filters), the honest
 * unavailable semantics (source states, a status FAILURE is NOT "all clear"),
 * bounded lazy selection detail, and the error state. `api` is mocked so no
 * network is hit; the Cytoscape canvas renders headless in jsdom.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';

const { fleetSnapshotFn, fleetStatusFn, fleetServiceFn, fleetTargetFn } = vi.hoisted(() => ({
  fleetSnapshotFn: vi.fn(),
  fleetStatusFn: vi.fn(),
  fleetServiceFn: vi.fn(),
  fleetTargetFn: vi.fn(),
}));

vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return {
    ...actual, // keep the real ApiError so `instanceof` still works
    api: {
      fleetSnapshot: (...a: unknown[]) => fleetSnapshotFn(...a),
      fleetStatus: (...a: unknown[]) => fleetStatusFn(...a),
      fleetService: (...a: unknown[]) => fleetServiceFn(...a),
      fleetTarget: (...a: unknown[]) => fleetTargetFn(...a),
    },
  };
});

// @ts-expect-error — Svelte component has no declaration file
import FleetView from './FleetView.svelte';
import { ApiError } from '../lib/api.ts';

const snapshot = {
  schemaVersion: 'pacto.dev/fleet/v1',
  snapshotId: 'sha256:0123456789abcdef0123456789abcdef',
  generatedAt: '2026-07-29T10:00:00Z',
  completeness: 'partial',
  limitations: [{ code: 'SOURCE_UNAVAILABLE', source: 'k8s', message: 'cluster unreachable' }],
  services: {
    'domain-a/payments': { key: 'domain-a/payments', name: 'payments', domain: 'domain-a', owner: { team: 'core' }, status: 'Compliant', revisions: ['domain-a/payments@sha256:1'], targets: ['prod/k8s/payments'], sources: ['oci'] },
    'domain-a/billing': { key: 'domain-a/billing', name: 'billing', domain: 'domain-a', owner: {}, status: 'NonCompliant', revisions: [], targets: [], sources: ['local'] },
  },
  revisions: { 'domain-a/payments@sha256:1': { key: 'domain-a/payments@sha256:1', serviceKey: 'domain-a/payments', service: 'payments', version: '2.0.0', valid: true } },
  targets: { 'prod/k8s/payments': { key: 'prod/k8s/payments', serviceKey: 'domain-a/payments', service: 'payments', name: 'payments', scope: 'prod', compliance: 'Compliant', stale: false } },
  relationships: [{ fromService: 'domain-a/billing', fromRevision: '', toService: 'domain-a/payments', type: 'dependency', provenance: 'declared', resolved: true }],
  sources: [
    { id: 'oci', kind: 'oci', status: 'available' },
    { id: 'k8s', kind: 'k8s', status: 'unavailable' },
  ],
};

const status = { meta: {}, items: [{ kind: 'service', name: 'billing', code: 'NON_COMPLIANT', reason: 'contract violation' }] };

const serviceView = {
  meta: {},
  service: { key: 'domain-a/payments', name: 'payments', domain: 'domain-a', status: 'Compliant' },
  revisions: [{ key: 'domain-a/payments@sha256:1', version: '2.0.0', digest: 'sha256:deadbeefdeadbeef', valid: true }],
  targets: [{ name: 'payments', scope: 'prod', kind: 'k8s', compliance: 'Compliant' }],
  dependencies: [{ toService: 'domain-a/ledger', type: 'dependency', provenance: 'declared', required: true, compatibility: '^2', resolved: true }],
  dependents: ['domain-a/billing'],
};

function mountView(overrides: Record<string, unknown> = {}) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  // A reactive props holder so tests can update props (Svelte 5 removed $set).
  const props = $state({ params: {}, refreshTick: 0, ...overrides });
  const component = mount(FleetView, { target, props });
  return { target, component, props };
}

describe('FleetView — redesigned operational graph', () => {
  beforeEach(() => {
    fleetSnapshotFn.mockReset();
    fleetStatusFn.mockReset();
    fleetServiceFn.mockReset();
    fleetTargetFn.mockReset();
    fleetSnapshotFn.mockResolvedValue(snapshot);
    fleetStatusFn.mockResolvedValue(status);
    fleetServiceFn.mockResolvedValue(serviceView);
    fleetTargetFn.mockResolvedValue({ meta: {}, target: { key: 'prod/k8s/payments', name: 'payments', service: 'payments', compliance: 'Compliant' } });
  });

  it('renders the header with completeness, as-of and a snapshot id', async () => {
    const { target, component } = mountView();
    await vi.waitFor(() => {
      const text = target.textContent || '';
      expect(text).toContain('Operational Graph');
      expect(text).toContain('Partial');
      expect(text).toContain('as of');
      expect(text).toContain('Jul 29, 2026');
      expect(text).toContain('snapshot');
    });
    unmount(component);
    document.body.removeChild(target);
  });

  it('exposes perspective and layer controls, disabling observed when no observed edges exist', async () => {
    const { target, component } = mountView();
    await vi.waitFor(() => {
      const labels = Array.from(target.querySelectorAll('.seg')).map((b) => b.textContent?.trim());
      expect(labels).toEqual(expect.arrayContaining(['Services', 'Revisions', 'Targets', 'All', 'Declared']));
      // Observed has no data → its segment is disabled (no placebo control).
      const observed = Array.from(target.querySelectorAll('.seg')).find((b) => b.textContent?.includes('Observed')) as HTMLButtonElement;
      expect(observed?.disabled).toBe(true);
    });
    unmount(component);
    document.body.removeChild(target);
  });

  it('target perspective explains instances link to dependency services, not peer instances', async () => {
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('.controls')).toBeTruthy());
    const targetsBtn = Array.from(target.querySelectorAll('.seg')).find((b) => b.textContent?.trim() === 'Targets') as HTMLButtonElement;
    targetsBtn.click();
    await vi.waitFor(() => {
      const note = target.querySelector('[data-testid="target-note"]');
      expect(note?.textContent).toContain('instances');
      expect(note?.textContent).toContain('not to specific peer instances');
    });
    unmount(component);
    document.body.removeChild(target);
  });

  it('renders the filter selectors populated from snapshot values', async () => {
    const { target, component } = mountView();
    await vi.waitFor(() => {
      expect(target.querySelector('select[aria-label="Domain"]')).toBeTruthy();
      expect(target.querySelector('select[aria-label="Owner"]')).toBeTruthy();
      const domainOpts = Array.from(target.querySelectorAll('select[aria-label="Domain"] option')).map((o) => o.textContent);
      expect(domainOpts).toContain('domain-a');
    });
    unmount(component);
    document.body.removeChild(target);
  });

  it('shows the snapshot source states, including unavailable ones', async () => {
    const { target, component } = mountView();
    await vi.waitFor(() => {
      const chips = target.querySelector('[data-testid="source-states"]');
      expect(chips?.textContent).toContain('oci');
      expect(chips?.textContent).toContain('available');
      expect(chips?.textContent).toContain('k8s');
      expect(chips?.textContent).toContain('unavailable');
    });
    unmount(component);
    document.body.removeChild(target);
  });

  it('shows the partial-answer banner with limitations', async () => {
    const { target, component } = mountView();
    await vi.waitFor(() => {
      const banner = target.querySelector('[data-testid="partial-banner"]');
      expect(banner?.textContent).toContain('Partial answer');
      expect(banner?.textContent).toContain('SOURCE_UNAVAILABLE');
      expect(banner?.textContent).toContain('cluster unreachable');
    });
    unmount(component);
    document.body.removeChild(target);
  });

  it('renders needs-attention items from fleetStatus', async () => {
    const { target, component } = mountView();
    await vi.waitFor(() => {
      const text = target.textContent || '';
      expect(text).toContain('Needs attention');
      expect(text).toContain('NON_COMPLIANT');
    });
    unmount(component);
    document.body.removeChild(target);
  });

  it('shows all-clear only when the status report loaded with no items', async () => {
    fleetStatusFn.mockResolvedValue({ meta: {}, items: [] });
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.textContent || '').toContain('All clear'));
    unmount(component);
    document.body.removeChild(target);
  });

  it('a status FAILURE renders "unavailable", never "All clear" (§1.3)', async () => {
    fleetStatusFn.mockRejectedValue(new ApiError(503, 'status backend down'));
    const { target, component } = mountView();
    await vi.waitFor(() => {
      const text = target.textContent || '';
      expect(text).toContain('Attention report unavailable');
      expect(text).not.toContain('All clear');
    });
    unmount(component);
    document.body.removeChild(target);
  });

  it('lazily loads bounded detail for a deep-linked selection (§1.2)', async () => {
    const { target, component } = mountView({ params: { sel: 'domain-a/payments' } });
    await vi.waitFor(() => {
      expect(fleetServiceFn).toHaveBeenCalledWith('domain-a/payments');
      const panel = target.querySelector('[data-testid="detail-panel"]');
      const text = panel?.textContent || '';
      expect(text).toContain('payments');
      expect(text).toContain('2.0.0'); // revision version
      expect(text).toContain('domain-a/ledger'); // dependency edge target
      expect(text).toContain('Analyze impact');
    });
    unmount(component);
    document.body.removeChild(target);
  });

  it('renders an error state with retry when the snapshot fetch fails', async () => {
    fleetSnapshotFn.mockRejectedValue(new ApiError(503, 'fleet snapshot unavailable'));
    const { target, component } = mountView();
    await vi.waitFor(() => {
      const text = target.textContent || '';
      expect(text).toContain('Couldn’t load the operational graph');
      expect(text).toContain('fleet snapshot unavailable');
    });
    unmount(component);
    document.body.removeChild(target);
  });

  it('reloads when refreshTick changes (§1.3 auto-refresh)', async () => {
    const { target, component, props } = mountView();
    await vi.waitFor(() => expect(fleetSnapshotFn).toHaveBeenCalledTimes(1));
    props.refreshTick = 1; // a global refresh / auto-reload tick
    flushSync();
    await vi.waitFor(() => expect(fleetSnapshotFn.mock.calls.length).toBe(2));
    unmount(component);
    document.body.removeChild(target);
  });
});
