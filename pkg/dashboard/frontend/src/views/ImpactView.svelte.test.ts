/**
 * Component tests for the redesigned ImpactView.svelte.
 * Covers the revision-selector workflow, honest observed gating (§2.4),
 * separated breaking vs potentially-breaking (§2.3), the consumer table with
 * path/range/verdict/confidence, snapshotId parity display (§2.2), the deep-link
 * auto-run entry point (§2.1), and the empty/error states. `api` is mocked.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';

const { fleetImpactFn, fleetSnapshotFn } = vi.hoisted(() => ({
  fleetImpactFn: vi.fn(),
  fleetSnapshotFn: vi.fn(),
}));

vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return {
    ...actual,
    api: {
      fleetImpact: (...a: unknown[]) => fleetImpactFn(...a),
      fleetSnapshot: (...a: unknown[]) => fleetSnapshotFn(...a),
    },
  };
});

// @ts-expect-error — Svelte component has no declaration file
import ImpactView from './ImpactView.svelte';
import { ApiError } from '../lib/api.ts';

const snapshot = {
  snapshotId: 'sha256:abcdef0123456789',
  services: { 'domain-a/payments': { key: 'domain-a/payments', name: 'payments', domain: 'domain-a', revisions: ['domain-a/payments@sha256:2', 'domain-a/payments@sha256:1'] } },
  revisions: {
    'domain-a/payments@sha256:2': { key: 'domain-a/payments@sha256:2', version: '2.0.0', resolvedRef: 'oci://svc@sha256:2', digest: 'sha256:2' },
    'domain-a/payments@sha256:1': { key: 'domain-a/payments@sha256:1', version: '1.0.0', resolvedRef: 'oci://svc@sha256:1', digest: 'sha256:1' },
  },
  relationships: [],
};

const result = {
  schemaVersion: 'pacto.dev/impact/v1',
  snapshotId: 'sha256:abcdef0123456789',
  asOf: '2026-07-29T10:00:00Z',
  service: 'payments',
  oldVersion: '1.0.0',
  newVersion: '2.0.0',
  classification: 'BREAKING',
  completeness: 'complete',
  breakingChanges: [{ path: 'paths./pay.post', type: 'removed', reason: 'operation removed' }],
  potentiallyBreakingChanges: [{ path: 'paths./pay.get', type: 'schema-narrowed', reason: 'response field made required' }],
  consumers: [
    { service: 'billing', domain: 'domain-a', depth: 1, direct: true, path: ['billing', 'payments'], owner: 'core', required: true, compatibility: '^1.0.0', compatibilityVerdict: 'incompatible', provenance: 'declared', confidence: 'contractual', targets: ['prod/k8s/billing'] },
    { service: 'ledger', domain: 'domain-a', depth: 2, direct: false, path: ['ledger', 'billing', 'payments'], required: false, compatibilityVerdict: 'unknown', provenance: 'inferred', confidence: 'inferred' },
  ],
  owners: ['core'],
  activeTargets: ['prod/k8s/billing'],
  limitations: [],
};

function mountView(params: Record<string, unknown> = {}) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(ImpactView, { target, props: { params } });
  return { target, component };
}

describe('ImpactView — redesigned workflow', () => {
  beforeEach(() => {
    fleetImpactFn.mockReset();
    fleetSnapshotFn.mockReset();
    fleetImpactFn.mockResolvedValue(result);
    fleetSnapshotFn.mockResolvedValue(snapshot);
  });

  it('offers revision selectors populated from the snapshot (§2.1)', async () => {
    const { target, component } = mountView();
    await vi.waitFor(() => {
      const svcOpts = Array.from(target.querySelectorAll('#impact-svc option')).map((o) => o.textContent);
      expect(svcOpts.join(' ')).toContain('payments (domain-a)');
    });
    unmount(component);
    document.body.removeChild(target);
  });

  it('disables include-observed when no observed source exists (§2.4 no placebo)', async () => {
    const { target, component } = mountView();
    await vi.waitFor(() => {
      const cb = target.querySelector('input[type="checkbox"]') as HTMLInputElement;
      expect(cb.disabled).toBe(true);
    });
    unmount(component);
    document.body.removeChild(target);
  });

  it('picking a service defaults to its two most recent revisions and analyzes them', async () => {
    const { target, component } = mountView();
    await vi.waitFor(() =>
      expect(Array.from(target.querySelectorAll('#impact-svc option')).some((o) => (o as HTMLOptionElement).value === 'domain-a/payments')).toBe(true),
    );
    const svc = target.querySelector('#impact-svc') as HTMLSelectElement;
    svc.value = 'domain-a/payments';
    svc.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();
    (target.querySelector('form') as HTMLFormElement).dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await vi.waitFor(() => {
      expect(fleetImpactFn).toHaveBeenCalledWith('oci://svc@sha256:1', 'oci://svc@sha256:2', false);
    });
    unmount(component);
    document.body.removeChild(target);
  });

  it('a deep link with both refs runs the analysis immediately (§2.1 entry point)', async () => {
    const { target, component } = mountView({ old: 'oci://svc@sha256:1', new: 'oci://svc@sha256:2' });
    await vi.waitFor(() => expect(fleetImpactFn).toHaveBeenCalledWith('oci://svc@sha256:1', 'oci://svc@sha256:2', false));
    unmount(component);
    document.body.removeChild(target);
  });

  it('shows breaking and potentially-breaking changes SEPARATELY (§2.3)', async () => {
    const { target, component } = mountView({ old: 'a', new: 'b' });
    await vi.waitFor(() => {
      const text = target.textContent || '';
      expect(text).toContain('Breaking changes');
      expect(text).toContain('operation removed');
      expect(text).toContain('Potentially breaking');
      expect(text).toContain('response field made required');
      // The potentially-breaking change must NOT be under a "Breaking" label.
      const potentialSection = Array.from(target.querySelectorAll('.section')).find((s) => s.textContent?.includes('Potentially breaking'));
      expect(potentialSection?.textContent).not.toContain('operation removed');
    });
    unmount(component);
    document.body.removeChild(target);
  });

  it('renders consumers with reach, path, range, verdict, confidence and provenance (§2.3)', async () => {
    const { target, component } = mountView({ old: 'a', new: 'b' });
    await vi.waitFor(() => {
      const text = target.textContent || '';
      expect(text).toContain('billing');
      expect(text).toContain('Direct');
      expect(text).toContain('Transitive · depth 2');
      expect(text).toContain('billing → payments'); // path
      expect(text).toContain('^1.0.0'); // compatibility range
      expect(text).toContain('incompatible'); // verdict
      expect(text).toContain('contractual'); // confidence
      // Confidence legend explains the levels.
      expect(text).toContain('What do the confidence levels mean?');
    });
    unmount(component);
    document.body.removeChild(target);
  });

  it('shows snapshotId parity with the current graph (§2.2)', async () => {
    const { target, component } = mountView({ old: 'a', new: 'b' });
    await vi.waitFor(() => expect(target.textContent || '').toContain('matches graph'));
    unmount(component);
    document.body.removeChild(target);
  });

  it('renders owners and active targets', async () => {
    const { target, component } = mountView({ old: 'a', new: 'b' });
    await vi.waitFor(() => {
      const text = target.textContent || '';
      expect(text).toContain('Owners');
      expect(text).toContain('Active targets');
      expect(text).toContain('prod/k8s/billing');
    });
    unmount(component);
    document.body.removeChild(target);
  });

  it('shows an empty state when there are no affected consumers', async () => {
    fleetImpactFn.mockResolvedValue({ ...result, consumers: [], breakingChanges: [], potentiallyBreakingChanges: [] });
    const { target, component } = mountView({ old: 'a', new: 'b' });
    await vi.waitFor(() => expect(target.textContent || '').toContain('No affected consumers'));
    unmount(component);
    document.body.removeChild(target);
  });

  it('renders an error state with retry when the analysis fails', async () => {
    fleetImpactFn.mockRejectedValue(new ApiError(422, 'invalid ref'));
    const { target, component } = mountView({ old: 'a', new: 'b' });
    await vi.waitFor(() => {
      const text = target.textContent || '';
      expect(text).toContain('Couldn’t analyze the impact');
      expect(text).toContain('invalid ref');
    });
    unmount(component);
    document.body.removeChild(target);
  });
});
