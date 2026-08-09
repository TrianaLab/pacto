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

function targetDetail(): Record<string, any> {
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
      expect(target.textContent).toContain('Operational target'); // user-facing kind label
      expect(target.textContent).not.toContain('Deployment');     // Pacto observes, it never deploys
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

  it('never prints a caption and a kind chip that say the same word', async () => {
    // "SERVICE [SERVICE] app" and "OWNER [OWNER] team/platform" is what makes a page
    // read like a form. The caption names the relation; the chip is then redundant.
    const d = targetDetail();
    d.target.ownership = { owner: 'team/platform', ref: { kind: 'owner', key: 'team/platform', label: 'team/platform', href: '/fleet/owners/team%2Fplatform' } };
    detailFn.mockResolvedValue(d);
    const { target, component } = mountView('target', 'prod/k8s/app');
    await vi.waitFor(() => expect(target.textContent).toContain('team/platform'));
    for (const row of Array.from(target.querySelectorAll('.te-fact'))) {
      const caption = row.querySelector('.te-k')?.textContent?.trim().toLowerCase();
      const chip = row.querySelector('.ei-kind')?.textContent?.trim().toLowerCase();
      expect(chip, `row "${caption}" repeats its caption as a kind chip`).not.toBe(caption);
    }
    unmount(component); document.body.removeChild(target);
  });

  it('drops the contributing-sources row when it only repeats the data source', async () => {
    const one = targetDetail();
    one.target.source = 'local';
    one.target.sources = { items: ['local'], total: 1, count: 1, truncated: false };
    detailFn.mockResolvedValue(one);
    let m = mountView('target', 'prod/k8s/app');
    await vi.waitFor(() => expect(m.target.textContent).toContain('Data source'));
    expect(m.target.textContent).not.toContain('Contributing data sources');
    unmount(m.component); document.body.removeChild(m.target);

    const many = targetDetail();
    many.target.source = 'local';
    many.target.sources = { items: ['local', 'k8s'], total: 2, count: 2, truncated: false };
    detailFn.mockResolvedValue(many);
    m = mountView('target', 'prod/k8s/app');
    await vi.waitFor(() => expect(m.target.textContent).toContain('Contributing data sources'));
    unmount(m.component); document.body.removeChild(m.target);
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
  function serviceDetail(): Record<string, any> {
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

  it('names the other end of every relationship, never the service you are already on', async () => {
    // A service's edges run both ways. Rendering a fixed end made an inbound edge read
    // as the service depending on ITSELF, and two edges with the same counterpart became
    // two identical rows.
    const d = serviceDetail();
    const self = ref('service', 'domain-a/payments');
    const other = ref('service', 'domain-b/ledger');
    d.service.relationships = {
      total: 2, count: 2, truncated: false,
      items: [
        { id: 'out', from: self, to: other, relation: 'dependency', difference: 'expected-not-observed', provenance: 'declared' },
        { id: 'in', from: other, to: self, relation: 'dependency', difference: 'matched', provenance: 'declared+observed' },
      ],
    };
    detailFn.mockResolvedValue(d);
    const { target, component } = mountView('service', 'domain-a/payments');
    await vi.waitFor(() => expect(target.textContent).toContain('Observed traffic'));
    const rows = Array.from(target.querySelectorAll('.rel'));
    expect(rows.map((r) => r.querySelector('.rel-word')?.textContent)).toEqual(['Depends on', 'Used by']);
    for (const r of rows) {
      expect(r.querySelector('.ei-label')?.textContent).toBe('ledger'); // never "payments"
      // Both rows' provenance is implied by their reconciliation badge, so neither
      // repeats it -- and the raw wire token never reaches the screen.
      expect(r.querySelector('.rel-prov')).toBeNull();
    }
    expect(target.textContent).not.toContain('declared+observed');
    unmount(component); document.body.removeChild(target);
  });

  it('keeps the provenance word when the reconciliation badge does not already say it', async () => {
    const d = serviceDetail();
    d.service.relationships = {
      total: 1, count: 1, truncated: false,
      items: [{ id: 'e', from: ref('service', 'domain-a/payments'), to: ref('service', 'domain-b/ledger'), relation: 'dependency', difference: 'insufficient', provenance: 'declared' }],
    };
    detailFn.mockResolvedValue(d);
    const { target, component } = mountView('service', 'domain-a/payments');
    await vi.waitFor(() => expect(target.textContent).toContain('Observed traffic'));
    expect(target.querySelector('.rel-prov')?.textContent).toBe('Expected');
    unmount(component); document.body.removeChild(target);
  });

  it('reads an ownership conflict as a list of revisions, not a paragraph of hex', async () => {
    const digest = `sha256:${'673e8f73'.repeat(8)}`;
    const d = serviceDetail();
    d.service.ownership = {
      owner: 'team-a',
      ref: ref('owner', 'team-a'),
      conflicts: { total: 3, count: 1, truncated: true, items: [`domain-a/payments@${digest}: team-b`] },
    };
    detailFn.mockResolvedValue(d);
    const { target, component } = mountView('service', 'domain-a/payments');
    await vi.waitFor(() => expect(target.textContent).toContain('Ownership conflict'));
    const rows = Array.from(target.querySelectorAll('.se-conflict-list li'));
    expect(rows[0].textContent).toBe('domain-a/payments@673e8f73673e…: team-b');
    expect(rows[0].getAttribute('title')).toContain(digest); // exact key still available
    expect(rows[1].textContent).toBe('+2 more');             // truncation stays honest
    unmount(component); document.body.removeChild(target);
  });

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

  // 'compare' and 'impact' are two stages of ONE question ("what changed, and what does
  // that change affect?"), so they resolve to the same workspace and are offered ONCE.
  // Two buttons opening the identical screen was the legacy seam this replaces.
  it('offers the Change analysis workspace exactly once for compare AND impact', async () => {
    detailFn.mockResolvedValue(serviceDetail());
    const { target, component } = mountView('service', 'domain-a/payments');
    await vi.waitFor(() => expect(target.querySelector('.ev-action')).toBeTruthy());
    const links = Array.from(target.querySelectorAll('a.ev-action'));
    const labels = links.map((a) => a.textContent?.trim());
    expect(labels).toEqual(['Open in graph', 'Compare revisions']);
    expect(links.find((a) => a.textContent?.includes('Compare'))?.getAttribute('href'))
      .toBe('#/fleet/changes/domain-a%2Fpayments');
    unmount(component); document.body.removeChild(target);
  });

  it('breadcrumbs use the entity relationship (Overview > Services > payments)', async () => {
    detailFn.mockResolvedValue(serviceDetail());
    const { target, component } = mountView('service', 'domain-a/payments');
    // Wait for the detail to load (the entity trail replaces the loading fallback).
    await vi.waitFor(() => expect(target.textContent).toContain('Revisions'));
    const crumbs = Array.from(target.querySelectorAll('nav a, nav span')).map((n) => n.textContent?.trim());
    expect(crumbs.join(' > ')).toContain('Overview');
    expect(crumbs).not.toContain('Fleet');
    expect(crumbs).toEqual(expect.arrayContaining(['Services', 'payments']));
    unmount(component); document.body.removeChild(target);
  });
});

describe('FleetEntityView — rich revision page (E)', () => {
  beforeEach(() => detailFn.mockReset());
  function revisionDetail(identity = { retrievable: false, identityClass: 'mutable' }): Record<string, any> {
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
    expect(text).toContain('Running here (exact match)');
    // Readiness must be legible as DECLARED preparedness, never read as a runtime result.
    expect(text).toMatch(/not a measurement of the running system/i);
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

  it('names a revision with no readiness gate instead of answering triage with silence', async () => {
    const d = revisionDetail();
    d.revision.readiness = null;
    detailFn.mockResolvedValue(d);
    const { target, component } = mountView('revision', 'domain-a/payments@2.1.0');
    await vi.waitFor(() => expect(target.textContent).toContain('Readiness'));
    const text = target.textContent || '';
    expect(text).toContain('Not declared');
    // "Nothing declared" and "declared and failing" must not read as the same state.
    expect(text).toMatch(/not the same as failing/i);
    expect(text).not.toMatch(/not passing/i);
    unmount(component); document.body.removeChild(target);
  });

  it('reads a field label as English, never as the wire field name', async () => {
    const d = revisionDetail();
    d.revision.pactoVersion = '2.0';
    detailFn.mockResolvedValue(d);
    const { target, component } = mountView('revision', 'domain-a/payments@2.1.0');
    await vi.waitFor(() => expect(target.textContent).toContain('2.0'));
    const labels = Array.from(target.querySelectorAll('.re-k')).map((n) => n.textContent);
    expect(labels).toContain('Pacto version');
    expect(labels).not.toContain('pactoVersion');
  });

  it('links the owner from a revision, the same as from its service', async () => {
    // The backend emits the owner ref for both, so the trail out to "everything this
    // team owns" does not dead-end on the revision page.
    const d = revisionDetail();
    d.revision.ownership = { owner: 'team-a', ref: ref('owner', 'team-a') };
    detailFn.mockResolvedValue(d);
    const { target, component } = mountView('revision', 'domain-a/payments@2.1.0');
    await vi.waitFor(() => expect(target.textContent).toContain('team-a'));
    const href = Array.from(target.querySelectorAll('a.entity-link')).map((a) => a.getAttribute('href'));
    expect(href).toContain('#/fleet/owners/team-a');
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
    // The prose explains WHY we cannot name the revision -- it does not restate the badge.
    expect(text).toMatch(/more than one known revision matches/i);
    expect(text).toMatch(/cannot say which one is running/i);
    // ...and it never lets "we cannot see it" read as "nothing is there".
    expect(text).toMatch(/Something IS running/);
    // the "Running revision"/"Inferred revision" authoritative label must NOT appear
    expect(text).not.toContain('Running revision');
    expect(text).not.toContain('Inferred revision');
    unmount(component); document.body.removeChild(target);
  });

  it('gives an unresolved match its own reason, not the ambiguous one', async () => {
    const d = ambiguousTarget();
    d.target.linkState = 'unresolved';
    detailFn.mockResolvedValue(d);
    const { target, component } = mountView('target', 'prod/k8s/app');
    await vi.waitFor(() => expect(target.textContent).toContain('Unresolved revision'));
    const text = target.textContent || '';
    expect(text).toMatch(/nothing we observed here ties back to a known revision/i);
    expect(text).not.toMatch(/more than one/i);
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

  it('owner attention action is built from the canonical owner KEY, not the display label (F3)', async () => {
    // The owner entity's key differs from its display label; the "view all attention
    // for this owner" action must use the stable key the backend filter matches.
    detailFn.mockResolvedValue({
      meta,
      entity: { kind: 'owner', key: 'team:platform', label: 'Platform Team', href: '/fleet/owners/team%3Aplatform' },
      owner: {
        services: { total: 0, count: 0, truncated: false, items: [] },
        revisions: { total: 0, count: 0, truncated: false, items: [] },
        deployments: { total: 0, count: 0, truncated: false, items: [] },
        attention: { total: 1, count: 1, truncated: true, items: [{ severity: 'warning', category: 'stale', entity: ref('target', 'prod/k8s/a'), summary: 'stale' }] },
      },
    });
    const { target, component } = mountView('owner', 'team:platform');
    await vi.waitFor(() => expect(target.textContent).toContain('Needs attention'));
    const viewAll = Array.from(target.querySelectorAll('a')).find((a) => a.textContent?.includes('View all for this owner')) as HTMLAnchorElement;
    expect(viewAll.getAttribute('href')).toBe(`#/fleet/attention?owner=${encodeURIComponent('team:platform')}`);
    expect(viewAll.getAttribute('href')).not.toContain('Platform'); // never the display label
    unmount(component); document.body.removeChild(target);
  });

  it('source page renders health, records and contributed entities', async () => {
    detailFn.mockResolvedValue({
      // `status` mirrors the source's health, exactly as the backend emits it
      // (detail.go sets EntityDetail.Status from the source status). Health is badged
      // ONCE, in the page header, so the body never restates it.
      meta, entity: ref('source', 'kubernetes'), status: 'available',
      source: {
        kind: 'k8s', health: 'available', revisionCount: 12, targetCount: 8,
        entities: { total: 20, count: 1, truncated: true, items: [ref('target', 'prod/k8s/a')] },
        limitations: { total: 0, count: 0, truncated: false, items: [] },
      },
    });
    const { target, component } = mountView('source', 'kubernetes');
    await vi.waitFor(() => expect(target.textContent).toContain('Contributed entities'));
    const text = target.textContent || '';
    expect(text.match(/Available/g)).toHaveLength(1); // header badge only, never restated
    expect(text).toContain('12 revisions');
    expect(text).toContain('1 of 20');   // contributed-entities preview honest count
    expect(Array.from(target.querySelectorAll('nav a, nav span')).map((n) => n.textContent?.trim())).toEqual(expect.arrayContaining(['Data sources']));
    unmount(component); document.body.removeChild(target);
  });

  it('leads a degraded source with the human reason, not the machine code', async () => {
    detailFn.mockResolvedValue({
      meta, entity: ref('source', 'edge-cluster'),
      source: {
        kind: 'k8s', health: 'unavailable', revisionCount: 0, targetCount: 0,
        error: { code: 'SOURCE_UNAVAILABLE', message: 'edge cluster unreachable' },
        entities: { total: 0, count: 0, truncated: false, items: [] },
        limitations: { total: 0, count: 0, truncated: false, items: [] },
      },
    });
    const { target, component } = mountView('source', 'edge-cluster');
    await vi.waitFor(() => expect(target.textContent).toContain('edge cluster unreachable'));
    const banner = target.querySelector('.src-error') as HTMLElement;
    // The sentence starts with words, not an enum -- but the exact code is still there.
    expect(banner.textContent?.trim().startsWith('edge cluster unreachable')).toBe(true);
    expect(banner.querySelector('.src-error-code')?.textContent).toBe('SOURCE_UNAVAILABLE');
    unmount(component); document.body.removeChild(target);
  });

  it('states a source header status in the health vocabulary, not the compliance one', async () => {
    // `detail.status` for a source is HEALTH. Through the compliance badge it fell
    // through to a grey lowercase "unavailable" in the header while the fact row below
    // said "Unavailable" in red -- one source, two vocabularies, on one screen.
    detailFn.mockResolvedValue({
      meta, entity: ref('source', 'edge-cluster'), status: 'unavailable',
      source: {
        kind: 'k8s', health: 'unavailable', revisionCount: 0, targetCount: 0,
        entities: { total: 0, count: 0, truncated: false, items: [] },
        limitations: { total: 0, count: 0, truncated: false, items: [] },
      },
    });
    const { target, component } = mountView('source', 'edge-cluster');
    await vi.waitFor(() => expect(target.textContent).toContain('Contributed entities'));
    const head = target.querySelector('.ev-head') as HTMLElement;
    expect(head.querySelector('.tag')?.textContent).toBe('Unavailable');
    expect(head.querySelector('.tag')?.className).toContain('tone-err');
    expect(head.querySelector('.status-badge')).toBeNull();
    expect(head.textContent).not.toContain('unavailable'); // never the raw wire value
    unmount(component); document.body.removeChild(target);
  });

  // requirement B: a bounded preview whose exact total is UNKNOWN must never be
  // rendered as "X of X", and the RuntimePreview `scanned` lower bound is never
  // presented as the total.
  it('target observed-runtime with an unknown total never presents scanned as the total', async () => {
    detailFn.mockResolvedValue({
      meta, entity: ref('target', 'prod/k8s/app', { status: 'Compliant' }), status: 'Compliant',
      target: {
        linkState: 'exact', compliance: 'Compliant', service: ref('service', 'domain-a/app'),
        identity: { retrievable: false, identityClass: 'no-ref' }, stale: false,
        findings: { total: 0, count: 0, truncated: false, items: [] },
        // walk stopped early: total absent, scanned=400 (a lower bound), count=200.
        observedRuntime: { count: 200, scanned: 400, truncated: true, items: [{ key: 'a', value: 'b' }] },
        sources: { total: 0, count: 0, truncated: false, items: [] },
        limitations: { total: 0, count: 0, truncated: false, items: [] },
      },
    });
    const { target, component } = mountView('target', 'prod/k8s/app');
    await vi.waitFor(() => expect(target.textContent).toContain('Observed runtime'));
    const text = (target.textContent || '').replace(/\s+/g, ' ');
    expect(text).not.toContain('200 of 400'); // scanned is NOT the total
    expect(text).not.toContain('200 of 200'); // count is NOT the total
    expect(text).not.toContain('400');        // scanned is not surfaced as a total at all
    expect(text).toContain('Showing 200. More exist; total unknown.');
    unmount(component); document.body.removeChild(target);
  });

  // requirement F item 2: the revision page renders the already-available bounded
  // ownership, readiness checks, tools, skills and docs as honest previews, not bare
  // count badges.
  it('revision page renders ownership, readiness checks, tools, skills and docs previews', async () => {
    detailFn.mockResolvedValue({
      meta, entity: ref('revision', 'domain-a/app@sha256:1'), status: 'Compliant',
      revision: {
        service: ref('service', 'domain-a/app'), version: '1.2.3', valid: true,
        identity: { retrievable: true, identityClass: 'exact', digest: 'sha256:1' },
        ownership: { owner: 'platform', ref: ref('owner', 'platform'), conflicts: { total: 0, count: 0, truncated: false, items: [] } },
        readiness: {
          score: 80, minScore: 70, doneCount: 3, partialCount: 1, notDoneCount: 1, deferredCount: 0, passing: true, expired: false,
          checks: { total: 5, count: 2, truncated: true, items: [
            { id: 'has-health', status: 'done', category: 'observability', description: 'exposes a health capability' },
            { id: 'has-owner', status: 'done', category: 'ownership' },
          ] },
        },
        interfaces: 2, configurations: 1, policies: 0, capabilities: 1,
        validation: { total: 0, count: 0, truncated: false, items: [] },
        dependencies: { total: 0, count: 0, truncated: false, items: [] },
        tools: { total: 3, count: 1, truncated: true, items: [{ name: 'createOrder', method: 'post', path: '/orders', summary: 'place an order', mutating: true }] },
        skills: { total: 2, count: 2, truncated: false, items: ['summarize', 'classify'] },
        docs: { total: 1, count: 1, truncated: false, items: [{ path: 'docs/readme.md', title: 'Readme' }] },
        exactTargets: { total: 0, count: 0, truncated: false, items: [] },
        inferredTargets: { total: 0, count: 0, truncated: false, items: [] },
        limitations: { total: 0, count: 0, truncated: false, items: [] },
      },
    });
    const { target, component } = mountView('revision', 'domain-a/app@sha256:1');
    await vi.waitFor(() => expect(target.textContent).toContain('Readiness checks'));
    const text = (target.textContent || '').replace(/\s+/g, ' ');
    // Ownership rendered (not just a count).
    expect(text).toContain('platform');
    // Readiness checks preview with honest truncation.
    expect(text).toContain('has-health');
    expect(text).toContain('2 of 5');
    // Tools rendered as a real preview (path + name), not a bare "3 Tools" badge.
    expect(text).toContain('/orders');
    expect(text).toContain('createOrder');
    expect(text).not.toContain('3 Tools');
    // Skills and docs contents render.
    expect(text).toContain('summarize');
    expect(text).toContain('Readme');
    unmount(component); document.body.removeChild(target);
  });

  it('service relationships with an unknown total never says X of X', async () => {
    detailFn.mockResolvedValue({
      meta, entity: ref('service', 'domain-a/app'), status: 'Compliant',
      service: {
        domain: 'domain-a',
        revisions: { total: 0, count: 0, truncated: false, items: [] },
        deployments: { total: 0, count: 0, truncated: false, items: [] },
        dependencies: { total: 0, count: 0, truncated: false, items: [] },
        dependents: { total: 0, count: 0, truncated: false, items: [] },
        // RelationshipsPreview from an already-truncated neighborhood: total absent.
        relationships: { count: 200, truncated: true, items: [{ from: ref('service', 'domain-a/app'), to: ref('service', 'domain-a/dep'), expected: true, observed: false, difference: 'expected-not-observed' }] },
        findings: { total: 0, count: 0, truncated: false, items: [] },
        evidence: { total: 0, count: 0, truncated: false, items: [] },
        limitations: { total: 0, count: 0, truncated: false, items: [] },
      },
    });
    const { target, component } = mountView('service', 'domain-a/app');
    await vi.waitFor(() => expect(target.textContent).toContain('Observed traffic and differences'));
    const text = (target.textContent || '').replace(/\s+/g, ' ');
    expect(text).not.toContain('200 of 200');
    expect(text).toContain('Showing 200. More exist; total unknown.');
    unmount(component); document.body.removeChild(target);
  });
});
