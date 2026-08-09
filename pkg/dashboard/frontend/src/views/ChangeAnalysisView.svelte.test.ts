/**
 * Component tests for the Change analysis workspace, the ONE screen that answers both
 * halves of a single question: what changed between two revisions of a service, and what
 * that change affects. It replaces the legacy name+version Compare screen, so it is
 * canonical end to end: bounded product service/revision data (never the raw
 * FleetSnapshot), the POST fleetImpactByIdentity with a canonical ServiceKey + two
 * RevisionKeys (never the legacy GET), product page metadata for consumer paging, an
 * honest 409 on a snapshot mismatch, and the field-level semantic diff carried in the
 * same bounded answer so both halves describe the same revision pair. `api` is mocked so
 * only the product endpoints are exercised.
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
import ChangeAnalysisView from './ChangeAnalysisView.svelte';
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
    changes: {
      total: 3, count: 3, truncated: false, breaking: 1, potential: 1, nonBreaking: 1,
      items: [
        { path: 'paths./pay.post', type: 'removed', classification: 'BREAKING', reason: 'operation removed', oldValue: 'post /pay', newValue: '', oldTruncated: false, newTruncated: false },
        { path: 'components.schemas.Pay.amount', type: 'changed', classification: 'POTENTIALLY_BREAKING', reason: 'type widened', oldValue: 'integer', newValue: 'number', oldTruncated: false, newTruncated: false },
        { path: 'paths./refund.post', type: 'added', classification: 'NON_BREAKING', reason: 'operation added', oldValue: '', newValue: 'post /refund', oldTruncated: false, newTruncated: false },
      ],
    },
    activeTargets: { total: 1, count: 1, truncated: false, items: [ref('target', 'prod/k8s/billing', 'billing')] },
    limitations: { total: 0, count: 0, truncated: false, items: [] },
  };
}

function mountView(params: Record<string, unknown> = {}) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(ChangeAnalysisView, { target, props: { params } });
  return { target, component };
}
const analyzeBtn = (t: HTMLElement) => Array.from(t.querySelectorAll('button')).find((b) => /compare revisions/i.test(b.textContent || '')) as HTMLButtonElement;

describe('ChangeAnalysisView — one workspace for what changed and what it affects', () => {
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
      expect(text).toContain('Couldn’t compare these revisions');
      expect(text).toContain('unretrievable content');
    });
    unmount(component); document.body.removeChild(target);
  });

  it('disables include-observed when no observation source exists (no placebo)', async () => {
    const { target, component } = mountView({ svc: 'domain-a/payments' });
    await vi.waitFor(() => expect((target.querySelector('input[type="checkbox"]') as HTMLInputElement)?.disabled).toBe(true));
    unmount(component); document.body.removeChild(target);
  });

  it('with no service in the route, offers a SEARCH-FIRST service picker that routes to the canonical workspace (L2)', async () => {
    entitiesFn.mockResolvedValue({ meta, total: 1, count: 1, limit: 20, offset: 0, entities: [ref('service', 'domain-a/payments', 'payments', { domain: 'domain-a' })] });
    const { target, component } = mountView({}); // no svc
    const input = target.querySelector('#impact-pick') as HTMLInputElement;
    input.value = 'pay';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await vi.waitFor(() => expect(target.querySelector('[data-testid="impact-picker-results"]')).toBeTruthy());
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['service'], text: 'pay' }));
    (target.querySelector('[data-testid="impact-picker-results"] button') as HTMLButtonElement).click();
    flushSync();
    expect(location.hash).toBe('#/fleet/changes/domain-a%2Fpayments');
    expect(snapshotFn).not.toHaveBeenCalled();
    unmount(component); document.body.removeChild(target);
  });

  it('the service picker surfaces a search failure as an error, not "no matches" (L2/K)', async () => {
    entitiesFn.mockRejectedValue(new ApiError(503, 'unavailable'));
    const { target, component } = mountView({});
    const input = target.querySelector('#impact-pick') as HTMLInputElement;
    input.value = 'pay';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await vi.waitFor(() => expect(target.querySelector('[data-testid="impact-picker-error"]')).toBeTruthy());
    expect(target.querySelector('[data-testid="impact-picker-empty"]')).toBeNull();
    unmount(component); document.body.removeChild(target);
  });

  it('surfaces an incomplete revision universe instead of claiming completeness (L1)', async () => {
    // The service-detail revisions preview truncates; paging stops at the selector
    // bound with a remaining nextOffset, so the selector is honestly incomplete.
    detailFn.mockResolvedValue({
      meta, entity: ref('service', 'domain-a/big', 'big'),
      service: { revisions: { total: 9999, count: 1, truncated: true, items: [ref('revision', 'domain-a/big@sha256:1', '1.0.0')] } },
    });
    // Every revision page reports a further page (nextOffset), so paging is bounded by
    // MAX_SELECTOR_REVISIONS and reports incomplete.
    entitiesFn.mockResolvedValue({ meta, total: 9999, count: 200, nextOffset: 200, entities: Array.from({ length: 200 }, (_, i) => ref('revision', `domain-a/big@sha256:${i}`, `1.${i}.0`)) });
    const { target, component } = mountView({ svc: 'domain-a/big' });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="impact-revisions-incomplete"]')).toBeTruthy());
    expect(snapshotFn).not.toHaveBeenCalled();
    unmount(component); document.body.removeChild(target);
  });

  // The two halves are ONE workspace but TWO claims: "the contract changed" and
  // "something running is affected" have different evidence, so they are separate,
  // labelled stages rather than one merged verdict.
  it('renders both stages of the question from a single analysis', async () => {
    const { target, component } = mountView({ svc: 'domain-a/payments' });
    await vi.waitFor(() => expect(target.querySelector('#impact-new-rev')).toBeTruthy());
    analyzeBtn(target).click();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="changes-what-changed"]')).toBeTruthy());
    expect(target.querySelector('[data-testid="changes-what-it-affects"]')).toBeTruthy();
    // one POST answered both halves; the legacy name+version diff endpoint is not used
    expect(impactFn).toHaveBeenCalledTimes(1);
    unmount(component); document.body.removeChild(target);
  });

  it('preserves the field-level semantic diff, breaking first, with per-class counts', async () => {
    const { target, component } = mountView({ svc: 'domain-a/payments' });
    await vi.waitFor(() => expect(target.querySelector('#impact-new-rev')).toBeTruthy());
    analyzeBtn(target).click();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="changes-counts"]')).toBeTruthy());
    const counts = target.querySelector('[data-testid="changes-counts"]')?.textContent || '';
    expect(counts).toContain('1 breaking');
    expect(counts).toContain('1 potentially breaking');
    expect(counts).toContain('1 non-breaking');
    const stage = target.querySelector('[data-testid="changes-what-changed"]') as HTMLElement;
    const rows = Array.from(stage.querySelectorAll('tbody tr')).map((r) => r.textContent || '');
    expect(rows.length).toBe(3);
    expect(rows[0]).toContain('paths./pay.post');            // breaking first
    expect(rows[0]).toContain('operation removed');          // the reason, not just a count
    expect(stage.textContent).toContain('components.schemas.Pay.amount');
    unmount(component); document.body.removeChild(target);
  });

  it('reports a truncated diff honestly instead of implying it is the whole change', async () => {
    impactFn.mockResolvedValue({
      ...impactResult(),
      changes: { total: 900, count: 1, truncated: true, breaking: 1, potential: 0, nonBreaking: 899, items: [{ path: 'a', type: 'removed', classification: 'BREAKING', reason: 'gone', oldValue: 'x', newValue: '', oldTruncated: false, newTruncated: false }] },
    });
    const { target, component } = mountView({ svc: 'domain-a/payments' });
    await vi.waitFor(() => expect(target.querySelector('#impact-new-rev')).toBeTruthy());
    analyzeBtn(target).click();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="changes-truncated"]')).toBeTruthy());
    expect(target.querySelector('[data-testid="changes-truncated"]')?.textContent).toContain('900');
    unmount(component); document.body.removeChild(target);
  });

  it('an identical pair of revisions says so instead of rendering an empty table', async () => {
    impactFn.mockResolvedValue({ ...impactResult(), changes: { total: 0, count: 0, truncated: false, breaking: 0, potential: 0, nonBreaking: 0, items: [] } });
    const { target, component } = mountView({ svc: 'domain-a/payments' });
    await vi.waitFor(() => expect(target.querySelector('#impact-new-rev')).toBeTruthy());
    analyzeBtn(target).click();
    await vi.waitFor(() => expect(target.textContent).toContain('No differences'));
    unmount(component); document.body.removeChild(target);
  });

  it('makes the analyzed revision pair shareable in the URL', async () => {
    const { target, component } = mountView({ svc: 'domain-a/payments' });
    await vi.waitFor(() => expect(target.querySelector('#impact-new-rev')).toBeTruthy());
    analyzeBtn(target).click();
    await vi.waitFor(() => expect(location.hash).toContain('/fleet/changes/domain-a%2Fpayments'));
    expect(location.hash).toContain('old=' + encodeURIComponent('domain-a/payments@sha256:1'));
    expect(location.hash).toContain('new=' + encodeURIComponent('domain-a/payments@sha256:2'));
    unmount(component); document.body.removeChild(target);
  });

  // A shared link promises the ANSWER the sender saw, not a pre-filled form.
  it('restores the exact revision pair from a shared link AND re-runs the comparison', async () => {
    const { target, component } = mountView({ svc: 'domain-a/payments', old: 'domain-a/payments@sha256:2', new: 'domain-a/payments@sha256:1' });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="changes-what-changed"]')).toBeTruthy());
    expect((target.querySelector('#impact-old-rev') as HTMLSelectElement).value).toBe('domain-a/payments@sha256:2');
    expect((target.querySelector('#impact-new-rev') as HTMLSelectElement).value).toBe('domain-a/payments@sha256:1');
    expect(impactFn).toHaveBeenCalledWith(expect.objectContaining({
      fromRevisionKey: 'domain-a/payments@sha256:2', toRevisionKey: 'domain-a/payments@sha256:1',
    }));
    unmount(component); document.body.removeChild(target);
  });

  // Half a link is not an answer: it must not silently analyze a pair the sender never
  // chose, so it degrades to the selectors.
  it('does not auto-run when the shared link names only one side', async () => {
    const { target, component } = mountView({ svc: 'domain-a/payments', new: 'domain-a/payments@sha256:1' });
    await vi.waitFor(() => expect(target.querySelector('#impact-new-rev')).toBeTruthy());
    await Promise.resolve();
    expect(impactFn).not.toHaveBeenCalled();
    unmount(component); document.body.removeChild(target);
  });

  it('ignores a revision key this service does not have rather than asking the backend about it', async () => {
    const { target, component } = mountView({ svc: 'domain-a/payments', old: 'domain-a/payments@sha256:deleted' });
    await vi.waitFor(() => expect(target.querySelector('#impact-old-rev')).toBeTruthy());
    expect((target.querySelector('#impact-old-rev') as HTMLSelectElement).value).toBe('domain-a/payments@sha256:1'); // the default pair
    unmount(component); document.body.removeChild(target);
  });
});

// A legacy Compare bookmark carries a service NAME, which is not a canonical ServiceKey:
// domain-a/payments and domain-b/payments are both named "payments". The migration must
// resolve the name through the Product API and never guess a domain. (These guarantees
// used to live on the legacy Compare screen's impact CTA, which this workspace replaces.)
describe('ChangeAnalysisView — migrating a legacy compare bookmark by NAME', () => {
  beforeEach(() => {
    for (const f of [detailFn, entitiesFn, impactFn, rawImpactFn, snapshotFn, capsFn]) f.mockReset();
    detailFn.mockResolvedValue(serviceDetail());
    impactFn.mockResolvedValue(impactResult());
    capsFn.mockResolvedValue({ fleet: true, impact: true, observed: false });
    location.hash = '';
  });

  it('canonicalizes a unique name to its ServiceKey URL', async () => {
    entitiesFn.mockResolvedValue({ meta, total: 1, count: 1, entities: [ref('service', 'domain-a/payments', 'payments', { domain: 'domain-a' })] });
    const { target, component } = mountView({ name: 'payments' });
    await vi.waitFor(() => expect(location.hash).toBe('#/fleet/changes/domain-a%2Fpayments'));
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['service'], text: 'payments' }));
    unmount(component); document.body.removeChild(target);
  });

  it('asks which one when several services share the name, picking no arbitrary winner', async () => {
    entitiesFn.mockResolvedValue({ meta, total: 2, count: 2, entities: [
      ref('service', 'domain-a/payments', 'payments', { domain: 'domain-a' }),
      ref('service', 'domain-b/payments', 'payments', { domain: 'domain-b' }),
    ] });
    const { target, component } = mountView({ name: 'payments' });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="changes-migrate-note"]')).toBeTruthy());
    expect(location.hash).toBe(''); // no service was chosen for the user
    const options = Array.from(target.querySelectorAll('[data-testid="impact-picker-results"] button')).map((b) => b.textContent);
    expect(options).toEqual(['payments (domain-a)', 'payments (domain-b)']);
    unmount(component); document.body.removeChild(target);
  });

  it('never resolves a substring match as the bookmarked service', async () => {
    entitiesFn.mockResolvedValue({ meta, total: 1, count: 1, entities: [ref('service', 'domain-a/payments-legacy', 'payments-legacy', { domain: 'domain-a' })] });
    const { target, component } = mountView({ name: 'payments' });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="changes-migrate-note"]')).toBeTruthy());
    expect(location.hash).toBe('');
    expect(target.querySelector('[data-testid="changes-migrate-note"]')?.textContent).toMatch(/no service named/i);
    unmount(component); document.body.removeChild(target);
  });

  it('surfaces a failed resolution as an error, never as "no such service"', async () => {
    entitiesFn.mockRejectedValue(new ApiError(503, 'unavailable'));
    const { target, component } = mountView({ name: 'payments' });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="impact-picker-error"]')).toBeTruthy());
    expect(target.querySelector('[data-testid="changes-migrate-note"]')).toBeNull();
    unmount(component); document.body.removeChild(target);
  });
});
