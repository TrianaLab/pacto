/**
 * Component tests for the Product Impact workspace (requirement A1). The workspace
 * is product-oriented end to end: it loads bounded product service/revision data
 * (never the raw FleetSnapshot), analyzes through the POST fleetImpactByIdentity with
 * canonical ServiceKey + RevisionKeys (never the legacy GET), pages consumers through
 * the product page metadata, and handles a snapshot-mismatch (409) honestly. `api` is
 * mocked so only the product endpoints are exercised.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';

const { detailFn, entitiesFn, impactFn, rawImpactFn, snapshotFn, capsFn } = vi.hoisted(() => ({
  detailFn: vi.fn(), entitiesFn: vi.fn(), impactFn: vi.fn(),
  rawImpactFn: vi.fn(), snapshotFn: vi.fn(), capsFn: vi.fn(),
}));

vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return {
    ...actual,
    api: {
      fleetEntityDetail: (...a: unknown[]) => detailFn(...a),
      fleetEntities: (...a: unknown[]) => entitiesFn(...a),
      fleetImpactByIdentity: (...a: unknown[]) => impactFn(...a),
      fleetImpact: (...a: unknown[]) => rawImpactFn(...a),   // the legacy GET: must NEVER be called
      fleetSnapshot: (...a: unknown[]) => snapshotFn(...a),  // the raw snapshot: must NEVER be called
      capabilities: (...a: unknown[]) => capsFn(...a),
    },
  };
});

// @ts-expect-error — Svelte component has no declaration file
import ImpactView from './ImpactView.svelte';
import { ApiError } from '../lib/api.ts';

const meta = { schemaVersion: 'pacto.dev/fleet-product/v1', snapshotId: 'sha256:abc', asOf: '2026-07-29T10:00:00Z', completeness: 'complete', sources: [{ id: 'oci', status: 'available' }] };
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
const ref = (kind: string, key: string, label?: string, extra: any = {}): any => ({ kind, key, label: label ?? key, href: `/fleet/${kind}s/${encodeURIComponent(key)}`, ...extra });

const rev2 = ref('revision', 'domain-a/payments@sha256:2', 'payments 2.0.0');
const rev1 = ref('revision', 'domain-a/payments@sha256:1', 'payments 1.0.0');

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
function serviceDetail(truncated = false): any {
  return {
    meta, entity: ref('service', 'domain-a/payments', 'payments'), status: 'Compliant',
    service: {
      domain: 'domain-a',
      revisions: { total: truncated ? 5 : 2, count: 2, truncated, items: [rev2, rev1] },
      deployments: { total: 0, count: 0, truncated: false, items: [] },
      dependencies: { total: 0, count: 0, truncated: false, items: [] },
      dependents: { total: 0, count: 0, truncated: false, items: [] },
      relationships: { count: 0, truncated: false, items: [] },
      findings: { total: 0, count: 0, truncated: false, items: [] },
      evidence: { total: 0, count: 0, truncated: false, items: [] },
      limitations: { total: 0, count: 0, truncated: false, items: [] },
    },
  };
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
function impactResult(opts: { offset?: number; nextOffset?: number; match?: boolean } = {}): any {
  return {
    meta, snapshotId: 'sha256:abc', snapshotMatch: opts.match ?? true,
    service: ref('service', 'domain-a/payments', 'payments'),
    oldRevision: rev1, newRevision: rev2, classification: 'BREAKING',
    consumers: {
      total: 2, count: 2, limit: 100, offset: opts.offset ?? 0, truncated: opts.nextOffset != null, nextOffset: opts.nextOffset,
      items: [
        { service: ref('service', 'domain-a/billing', 'billing'), path: [ref('service', 'domain-a/billing', 'billing'), ref('service', 'domain-a/payments', 'payments')], pathTotal: 2, pathTruncated: false, depth: 1, direct: true, confidence: 'contractual', compatibilityVerdict: 'incompatible', owner: 'core' },
        { service: ref('service', 'domain-a/ledger', 'ledger'), path: [], pathTotal: 0, pathTruncated: false, depth: 2, direct: false, confidence: 'inferred', compatibilityVerdict: 'unknown' },
      ],
    },
    owners: { total: 1, count: 1, truncated: false, items: [ref('owner', 'core', 'core')] },
    activeTargets: { total: 1, count: 1, truncated: false, items: [ref('target', 'prod/k8s/billing', 'billing')] },
    limitations: { total: 0, count: 0, truncated: false, items: [] },
  };
}

function mountView(params: Record<string, unknown> = {}) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(ImpactView, { target, props: { params } });
  return { target, component };
}
const analyzeBtn = (t: HTMLElement) => Array.from(t.querySelectorAll('button')).find((b) => /analyze impact/i.test(b.textContent || '')) as HTMLButtonElement;

describe('ImpactView — Product Impact workspace (requirement A1)', () => {
  beforeEach(() => {
    for (const f of [detailFn, entitiesFn, impactFn, rawImpactFn, snapshotFn, capsFn]) f.mockReset();
    detailFn.mockResolvedValue(serviceDetail());
    impactFn.mockResolvedValue(impactResult());
    capsFn.mockResolvedValue({ fleet: true, impact: true, observed: false });
    location.hash = '';
  });

  it('loads revisions from the product service detail and analyzes via the POST with CANONICAL keys', async () => {
    const { target, component } = mountView({ svc: 'domain-a/payments' });
    await vi.waitFor(() => expect(target.querySelector('#impact-new-rev')).toBeTruthy());
    // selectors are populated from the product entity-detail preview (never the snapshot)
    expect(detailFn).toHaveBeenCalledWith('service', 'domain-a/payments');
    expect(snapshotFn).not.toHaveBeenCalled();
    analyzeBtn(target).click();
    await vi.waitFor(() => expect(impactFn).toHaveBeenCalled());
    // canonical ServiceKey + canonical RevisionKeys + snapshot id for mismatch rejection
    expect(impactFn).toHaveBeenCalledWith(expect.objectContaining({
      serviceKey: 'domain-a/payments',
      fromRevisionKey: 'domain-a/payments@sha256:1', // second-newest default
      toRevisionKey: 'domain-a/payments@sha256:2',   // newest default
      snapshotId: 'sha256:abc',
      includeObserved: false, limit: 100, offset: 0,
    }));
    // the legacy raw GET and the raw snapshot are never used by the product UI
    expect(rawImpactFn).not.toHaveBeenCalled();
    expect(snapshotFn).not.toHaveBeenCalled();
    unmount(component); document.body.removeChild(target);
  });

  it('does NOT treat a truncated service-detail preview as the complete revision universe', async () => {
    detailFn.mockResolvedValue(serviceDetail(true)); // revisions preview truncated
    entitiesFn.mockResolvedValue({ meta, total: 3, count: 3, limit: 200, offset: 0, truncated: false, entities: [rev2, rev1, ref('revision', 'domain-a/payments@sha256:0', 'payments 0.9.0')] });
    const { target, component } = mountView({ svc: 'domain-a/payments' });
    await vi.waitFor(() => expect(entitiesFn).toHaveBeenCalled());
    // it pages the complete revision set via the product entities API scoped to the service
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['revision'], service: 'domain-a/payments' }));
    await vi.waitFor(() => expect(Array.from(target.querySelectorAll('#impact-old-rev option')).length).toBe(4)); // 3 revs + placeholder
    expect(snapshotFn).not.toHaveBeenCalled();
    unmount(component); document.body.removeChild(target);
  });

  it('renders consumers with reach, path, verdict, confidence and owner', async () => {
    const { target, component } = mountView({ svc: 'domain-a/payments' });
    await vi.waitFor(() => expect(target.querySelector('#impact-new-rev')).toBeTruthy());
    analyzeBtn(target).click();
    await vi.waitFor(() => expect(target.textContent).toContain('Affected consumers'));
    const text = target.textContent || '';
    expect(text).toContain('billing');
    expect(text).toContain('Direct');
    expect(text).toContain('Transitive · depth 2');
    expect(text).toContain('billing → payments'); // path labels
    expect(text).toContain('incompatible');        // verdict
    expect(text).toContain('contractual');         // confidence
    expect(text).toContain('core');                // owner
    unmount(component); document.body.removeChild(target);
  });

  it('paginates consumers through the product page metadata (POST with offset)', async () => {
    impactFn.mockResolvedValueOnce(impactResult({ offset: 0, nextOffset: 100 }));
    const { target, component } = mountView({ svc: 'domain-a/payments' });
    await vi.waitFor(() => expect(target.querySelector('#impact-new-rev')).toBeTruthy());
    analyzeBtn(target).click();
    await vi.waitFor(() => expect(target.textContent).toContain('Affected consumers'));
    impactFn.mockResolvedValueOnce(impactResult({ offset: 100 }));
    const next = Array.from(target.querySelectorAll('button')).find((b) => b.textContent === 'Next') as HTMLButtonElement;
    next.click();
    await vi.waitFor(() => expect(impactFn).toHaveBeenLastCalledWith(expect.objectContaining({ offset: 100 })));
    unmount(component); document.body.removeChild(target);
  });

  it('handles a stale-snapshot 409 honestly: shows a refetch banner and retries', async () => {
    impactFn.mockRejectedValueOnce(new ApiError(409, 'snapshot mismatch'));
    const { target, component } = mountView({ svc: 'domain-a/payments' });
    await vi.waitFor(() => expect(target.querySelector('#impact-new-rev')).toBeTruthy());
    analyzeBtn(target).click();
    await vi.waitFor(() => expect(target.textContent).toMatch(/published snapshot changed/i));
    // Refresh and retry: re-loads the revision universe, then re-analyzes (now succeeds).
    impactFn.mockResolvedValueOnce(impactResult());
    (Array.from(target.querySelectorAll('button')).find((b) => /refresh and retry/i.test(b.textContent || '')) as HTMLButtonElement).click();
    await vi.waitFor(() => expect(target.textContent).toContain('Affected consumers'));
    expect(detailFn).toHaveBeenCalledTimes(2); // reloaded revisions on refetch
    unmount(component); document.body.removeChild(target);
  });

  it('shows an empty state when no consumers are affected', async () => {
    impactFn.mockResolvedValue({ ...impactResult(), consumers: { total: 0, count: 0, limit: 100, offset: 0, truncated: false, items: [] } });
    const { target, component } = mountView({ svc: 'domain-a/payments' });
    await vi.waitFor(() => expect(target.querySelector('#impact-new-rev')).toBeTruthy());
    analyzeBtn(target).click();
    await vi.waitFor(() => expect(target.textContent).toContain('No affected consumers'));
    unmount(component); document.body.removeChild(target);
  });

  it('renders a (non-409) analysis error with retry', async () => {
    impactFn.mockRejectedValue(new ApiError(422, 'unretrievable content'));
    const { target, component } = mountView({ svc: 'domain-a/payments' });
    await vi.waitFor(() => expect(target.querySelector('#impact-new-rev')).toBeTruthy());
    analyzeBtn(target).click();
    await vi.waitFor(() => {
      const text = target.textContent || '';
      expect(text).toContain('Couldn’t analyze the impact');
      expect(text).toContain('unretrievable content');
    });
    unmount(component); document.body.removeChild(target);
  });

  it('disables include-observed when no observation source exists (no placebo)', async () => {
    const { target, component } = mountView({ svc: 'domain-a/payments' });
    await vi.waitFor(() => expect((target.querySelector('input[type="checkbox"]') as HTMLInputElement)?.disabled).toBe(true));
    unmount(component); document.body.removeChild(target);
  });

  it('with no service in the route, offers a product service picker that routes to the canonical workspace', async () => {
    entitiesFn.mockResolvedValue({ meta, total: 1, count: 1, limit: 100, offset: 0, truncated: false, entities: [ref('service', 'domain-a/payments', 'payments', { domain: 'domain-a' })] });
    const { target, component } = mountView({}); // no svc
    // wait until the product service options have rendered into the picker
    await vi.waitFor(() => expect(Array.from(target.querySelectorAll('#impact-pick option')).some((o) => (o as HTMLOptionElement).value === 'domain-a/payments')).toBe(true));
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['service'] }));
    const sel = target.querySelector('#impact-pick') as HTMLSelectElement;
    sel.value = 'domain-a/payments';
    sel.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();
    expect(location.hash).toBe('#/fleet/impact/domain-a%2Fpayments');
    expect(snapshotFn).not.toHaveBeenCalled();
    unmount(component); document.body.removeChild(target);
  });
});
