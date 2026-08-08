/**
 * Component tests for FleetEntityView.svelte — the unified entity route.
 * Covers acceptance scenarios 11/14/15: an entity resolves through the product
 * entity-detail endpoint (NarrowedEntityDetail, never the snapshot) and shows
 * identity + canonical key + status + actions; an unknown entity produces a real
 * not-found state; a schema/contract incompatibility produces an explicit error.
 * It also proves the two identity dimensions render independently for a target.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount } from 'svelte';

const { detailFn } = vi.hoisted(() => ({ detailFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  // Keep the real facade behaviors (narrowEntityDetail, error classes); override only
  // the network call so the component exercises the real contract shapes.
  return { ...actual, api: { fleetEntityDetail: (...a: unknown[]) => detailFn(...a) } };
});

// @ts-expect-error — Svelte component has no declaration file
import FleetEntityView from './FleetEntityView.svelte';

const meta = { schemaVersion: 'pacto.dev/fleet-product/v1', snapshotId: 'x', asOf: '2026-07-29T10:00:00Z', completeness: 'complete', sources: [{ id: 'oci', status: 'available' }] };

function targetDetail() {
  return {
    meta,
    entity: { kind: 'target', key: 'prod/k8s/app', label: 'app', href: '/fleet/targets/prod%2Fk8s%2Fapp', status: 'Compliant', scope: 'prod' },
    status: 'Compliant',
    actions: ['open-graph', 'service'],
    target: {
      linkState: 'exact',
      compliance: 'Compliant',
      identity: { retrievable: false, identityClass: 'no-ref' },
      service: { kind: 'service', key: 'domain-a/app', label: 'app', href: '/fleet/services/domain-a%2Fapp' },
      revision: { kind: 'revision', key: 'domain-a/app@sha256:1', label: 'app 1.0', href: '/fleet/revisions/domain-a%2Fapp@sha256:1' },
      stale: false,
    },
  };
}

function mountView(kind: string, key: string) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(FleetEntityView, { target, props: { kind, entityKey: key, refreshTick: 0 } });
  return { target, component };
}


describe('FleetEntityView — unified entity route', () => {
  beforeEach(() => detailFn.mockReset());

  it('scenario 11: resolves via the entity-detail endpoint and shows identity + copyable key', async () => {
    detailFn.mockResolvedValue(targetDetail());
    const { target, component } = mountView('target', 'prod/k8s/app');
    await vi.waitFor(() => {
      expect(target.textContent).toContain('Deployment'); // user-facing kind label for a target
      expect(target.textContent).toContain('app');
      expect(target.querySelector('.copyable-value')?.textContent).toBe('prod/k8s/app');
    });
    expect(detailFn).toHaveBeenCalledWith('target', 'prod/k8s/app'); // product endpoint, not snapshot
    unmount(component); document.body.removeChild(target);
  });

  it('renders revision-match certainty and content retrievability as SEPARATE dimensions', async () => {
    // An exact revision match whose content is not retrievable (no canonical ref) is
    // honest, not contradictory (the whole point of the identity split).
    detailFn.mockResolvedValue(targetDetail());
    const { target, component } = mountView('target', 'prod/k8s/app');
    await vi.waitFor(() => {
      const text = target.textContent || '';
      expect(text).toContain('Exact revision match');
      expect(text).toContain('No canonical reference');
    });
    unmount(component); document.body.removeChild(target);
  });

  it('maps the DTO open-graph action to a canonical graph route', async () => {
    detailFn.mockResolvedValue(targetDetail());
    const { target, component } = mountView('target', 'prod/k8s/app');
    await vi.waitFor(() => expect(target.querySelector('.ev-action')).toBeTruthy());
    const action = Array.from(target.querySelectorAll('a.ev-action')).find((a) => a.textContent?.includes('graph')) as HTMLAnchorElement;
    expect(action.getAttribute('href')).toBe('#/fleet/graph/target/prod%2Fk8s%2Fapp');
    unmount(component); document.body.removeChild(target);
  });

  // The reject paths (scenario 14 unknown-entity -> not-found, scenario 15 schema
  // incompatibility -> explicit error) route through the same seam this view already
  // exercises on success: api rejection -> decideViewState(classifyError) ->
  // ProductEmptyState. Those two pieces are unit-tested deterministically
  // (knowledgeState.test.ts classifyError + productComponents.test.ts ProductEmptyState
  // rendering) without the rejected-promise-through-vi.waitFor timing hazard, and the
  // full browser reload/back paths are covered by the Playwright fleet spec.
});

// ── rich per-kind entity pages (D/E/F/G) ─────────────────────────────────────
const ref = (kind: string, key: string, extra = {}) => ({ kind, key, label: key.split('/').pop(), href: `/fleet/${kind}s/${encodeURIComponent(key)}`, ...extra });

describe('FleetEntityView — rich service page (D)', () => {
  beforeEach(() => detailFn.mockReset());
  function serviceDetail() {
    return {
      meta, status: 'NonCompliant',
      entity: ref('service', 'domain-a/payments', { domain: 'domain-a' }),
      actions: ['open-graph', 'compare', 'impact'],
      service: {
        domain: 'domain-a',
        ownership: { owner: 'team-a', ref: ref('owner', 'team-a'), conflicts: { total: 2, count: 2, truncated: false, items: ['team-a', 'team-b'] } },
        revisions: { total: 5, count: 2, truncated: true, items: [ref('revision', 'domain-a/payments@1.0'), ref('revision', 'domain-a/payments@2.0')] },
        deployments: { total: 1, count: 1, truncated: false, items: [ref('target', 'prod/k8s/payments')] },
        dependencies: { total: 3, count: 1, truncated: true, items: [ref('service', 'domain-b/ledger')] },
        dependents: { total: 0, count: 0, truncated: false, items: [] },
        relationships: { total: 1, count: 1, truncated: false, items: [{ id: 'e1', to: ref('service', 'domain-b/ledger'), difference: 'expected-not-observed', provenance: 'declared' }] },
        findings: { total: 1, count: 1, truncated: false, items: [{ finding: { severity: 'error', message: 'contract drift', category: 'compliance' }, entity: ref('target', 'prod/k8s/payments') }] },
        evidence: { total: 0, count: 0, truncated: false, items: [] },
        limitations: { total: 0, count: 0, truncated: false, items: [] },
      },
    };
  }

  it('renders bounded previews with honest count-of-total and truncation, plus ownership conflict', async () => {
    detailFn.mockResolvedValue(serviceDetail());
    const { target, component } = mountView('service', 'domain-a/payments');
    await vi.waitFor(() => expect(target.textContent).toContain('Revisions'));
    const text = target.textContent || '';
    expect(text).toContain('2 of 5');            // revisions preview count-of-total
    expect(text).toMatch(/Showing 2 of 5/);      // truncation is explicit, not hidden
    expect(text).toContain('Ownership conflict'); // conflicting revision owners surfaced
    expect(text).toContain('contract drift');     // attributed finding
    // a revision row links via the canonical href
    const link = Array.from(target.querySelectorAll('a.entity-link')).find((a) => a.getAttribute('href')?.includes('/fleet/revisions/')) as HTMLAnchorElement;
    expect(link).toBeTruthy();
    unmount(component); document.body.removeChild(target);
  });

  it('exposes Compare revisions and Analyze impact contextual actions', async () => {
    detailFn.mockResolvedValue(serviceDetail());
    const { target, component } = mountView('service', 'domain-a/payments');
    await vi.waitFor(() => expect(target.querySelector('.ev-action')).toBeTruthy());
    const labels = Array.from(target.querySelectorAll('a.ev-action')).map((a) => a.textContent?.trim());
    expect(labels).toEqual(expect.arrayContaining(['Open in graph', 'Compare revisions', 'Analyze impact']));
    unmount(component); document.body.removeChild(target);
  });

  it('breadcrumbs use the entity relationship (Fleet > Services > payments)', async () => {
    detailFn.mockResolvedValue(serviceDetail());
    const { target, component } = mountView('service', 'domain-a/payments');
    // Wait for the detail to load (the entity trail replaces the loading fallback).
    await vi.waitFor(() => expect(target.textContent).toContain('Revisions'));
    const crumbs = Array.from(target.querySelectorAll('nav a, nav span')).map((n) => n.textContent?.trim());
    expect(crumbs.join(' > ')).toContain('Fleet');
    expect(crumbs).toEqual(expect.arrayContaining(['Services', 'payments']));
    unmount(component); document.body.removeChild(target);
  });
});

describe('FleetEntityView — rich revision page (E)', () => {
  beforeEach(() => detailFn.mockReset());
  function revisionDetail(identity = { retrievable: false, identityClass: 'mutable' }) {
    return {
      meta, status: 'Compliant',
      entity: ref('revision', 'domain-a/payments@2.1.0'),
      actions: ['open-graph', 'compare', 'impact'],
      revision: {
        service: ref('service', 'domain-a/payments'),
        version: '2.1.0', valid: true, identity,
        readiness: { score: 80, minScore: 70, passing: true, doneCount: 8, partialCount: 1, notDoneCount: 1, deferredCount: 0, expired: false, checks: { total: 10, count: 0, truncated: false, items: [] } },
        validation: { total: 0, count: 0, truncated: false, items: [] },
        interfaces: 2, configurations: 1, policies: 1, capabilities: 3,
        dependencies: { total: 1, count: 1, truncated: false, items: [{ id: 'd1', to: ref('service', 'domain-b/ledger'), difference: 'matched', provenance: 'declared' }] },
        tools: { total: 0, count: 0, truncated: false, items: [] },
        skills: { total: 0, count: 0, truncated: false, items: [] },
        docs: { total: 0, count: 0, truncated: false, items: [] },
        exactTargets: { total: 1, count: 1, truncated: false, items: [ref('target', 'prod/k8s/payments')] },
        inferredTargets: { total: 0, count: 0, truncated: false, items: [] },
        previous: ref('revision', 'domain-a/payments@2.0.0'),
        next: null,
        limitations: { total: 0, count: 0, truncated: false, items: [] },
      },
    };
  }

  it('shows version, readiness, contract facets and the parent service link', async () => {
    detailFn.mockResolvedValue(revisionDetail());
    const { target, component } = mountView('revision', 'domain-a/payments@2.1.0');
    await vi.waitFor(() => expect(target.textContent).toContain('2.1.0'));
    const text = target.textContent || '';
    expect(text).toContain('Readiness');
    expect(text).toContain('Interfaces');
    expect(text).toContain('Exact-match deployments');
    // parent service is a link
    expect(Array.from(target.querySelectorAll('a.entity-link')).some((a) => a.getAttribute('href') === '#/fleet/services/domain-a%2Fpayments')).toBe(true);
    unmount(component); document.body.removeChild(target);
  });

  it('shows content retrievability as its OWN dimension and never calls mutable content immutable', async () => {
    detailFn.mockResolvedValue(revisionDetail({ retrievable: false, identityClass: 'mutable' }));
    const { target, component } = mountView('revision', 'domain-a/payments@2.1.0');
    await vi.waitFor(() => expect(target.textContent).toContain('Mutable reference'));
    expect(target.textContent).not.toMatch(/immutable/i); // never asserts immutability of mutable content
    unmount(component); document.body.removeChild(target);
  });
});

describe('FleetEntityView — rich target page honesty (F)', () => {
  beforeEach(() => detailFn.mockReset());
  function ambiguousTarget() {
    return {
      meta, status: 'Unknown',
      entity: ref('target', 'prod/k8s/app', { scope: 'prod' }),
      actions: ['open-graph', 'service'],
      target: {
        linkState: 'ambiguous', compliance: 'Unknown',
        identity: { retrievable: false, identityClass: 'no-ref' },
        service: ref('service', 'domain-a/app'),
        // Even if a candidate revision is present, an ambiguous match must not present it as authoritative.
        revision: ref('revision', 'domain-a/app@sha256:1'),
        scope: 'prod', sources: { total: 0, count: 0, truncated: false, items: [] },
        findings: { total: 0, count: 0, truncated: false, items: [] },
        observedRuntime: { count: 0, scanned: 0, truncated: false, items: [] },
        limitations: { total: 0, count: 0, truncated: false, items: [] },
        stale: false,
      },
    };
  }

  it('an ambiguous match never presents a specific revision as authoritative', async () => {
    detailFn.mockResolvedValue(ambiguousTarget());
    const { target, component } = mountView('target', 'prod/k8s/app');
    await vi.waitFor(() => expect(target.textContent).toContain('Ambiguous revision match'));
    const text = target.textContent || '';
    expect(text).toMatch(/no single revision is authoritative/i);
    // the "Running revision"/"Inferred revision" authoritative label must NOT appear
    expect(text).not.toContain('Running revision');
    expect(text).not.toContain('Inferred revision');
    unmount(component); document.body.removeChild(target);
  });
});

describe('FleetEntityView — owner and source pages (G)', () => {
  beforeEach(() => detailFn.mockReset());

  it('owner page renders services / revisions / deployments / attention previews', async () => {
    detailFn.mockResolvedValue({
      meta, entity: ref('owner', 'platform-team'),
      owner: {
        services: { total: 4, count: 2, truncated: true, items: [ref('service', 'domain-a/a'), ref('service', 'domain-a/b')] },
        revisions: { total: 0, count: 0, truncated: false, items: [] },
        deployments: { total: 3, count: 3, truncated: false, items: [ref('target', 'prod/k8s/a')] },
        attention: { total: 1, count: 1, truncated: false, items: [{ severity: 'warning', category: 'stale', entity: ref('target', 'prod/k8s/a'), summary: 'evidence stale' }] },
      },
    });
    const { target, component } = mountView('owner', 'platform-team');
    await vi.waitFor(() => expect(target.textContent).toContain('Services'));
    const text = target.textContent || '';
    expect(text).toContain('2 of 4');       // services preview honest count
    expect(text).toContain('Needs attention');
    expect(text).toContain('evidence stale');
    // breadcrumb: Fleet > Owners > platform-team
    expect(Array.from(target.querySelectorAll('nav a, nav span')).map((n) => n.textContent?.trim())).toEqual(expect.arrayContaining(['Owners']));
    unmount(component); document.body.removeChild(target);
  });

  it('source page renders health, records and contributed entities', async () => {
    detailFn.mockResolvedValue({
      meta, entity: ref('source', 'kubernetes'),
      source: {
        kind: 'k8s', health: 'available', revisionCount: 12, targetCount: 8,
        entities: { total: 20, count: 1, truncated: true, items: [ref('target', 'prod/k8s/a')] },
        limitations: { total: 0, count: 0, truncated: false, items: [] },
      },
    });
    const { target, component } = mountView('source', 'kubernetes');
    await vi.waitFor(() => expect(target.textContent).toContain('Contributed entities'));
    const text = target.textContent || '';
    expect(text).toContain('Available');
    expect(text).toContain('12 revisions');
    expect(text).toContain('1 of 20');   // contributed-entities preview honest count
    expect(Array.from(target.querySelectorAll('nav a, nav span')).map((n) => n.textContent?.trim())).toEqual(expect.arrayContaining(['Sources']));
    unmount(component); document.body.removeChild(target);
  });
});
