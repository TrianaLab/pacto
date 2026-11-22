/**
 * Component tests for FleetEntityView.svelte — the unified entity route.
 * Covers acceptance scenarios 11/14/15: an entity resolves through the product
 * entity-detail endpoint (NarrowedEntityDetail, never the snapshot) and shows
 * identity + canonical key + status + actions; an unknown entity produces a real
 * not-found state; a schema/contract incompatibility produces an explicit error.
 * It also proves the two identity dimensions render independently for a target.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import { reactiveProps } from '../testkit.svelte.ts';

const { detailFn } = vi.hoisted(() => ({ detailFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  // Keep the real facade behaviors (narrowEntityDetail, error classes); override only
  // the network call so the component exercises the real contract shapes.
  return { ...actual, api: { fleetEntityDetail: (...a: unknown[]) => detailFn(...a), fleetEntities: () => new Promise(() => {}) } };
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

/**
 * mountView mounts the route and resolves once the ENTITY BODY exists.
 *
 * Waiting on `.ev-body` rather than on page text is load-bearing. The page-level h1
 * renders immediately, before the detail request settles, because a page that is
 * still loading is still a page and has to name itself -- and that heading contains
 * the entity key. A test that waited for "payments" to appear therefore resolved
 * against the loading shell and then asserted on a body that did not exist yet.
 */
/** A closed section mounts nothing: open every disclosure, as a reader looking would. */
function openAll(target: HTMLElement) {
  for (let pass = 0; pass < 4; pass++) {
    target.querySelectorAll('details:not([open])').forEach((d) => {
      (d as HTMLDetailsElement).open = true;
      d.dispatchEvent(new Event('toggle'));
    });
    flushSync();
  }
}

async function mountView(kind: string, key: string) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(FleetEntityView, { target, props: { kind, entityKey: key, refreshTick: 0 } });
  await vi.waitFor(() => expect(target.querySelector('.ev-body')).toBeTruthy());
  return { target, component };
}


describe('FleetEntityView — unified entity route', () => {
  beforeEach(() => detailFn.mockReset());

  it('scenario 11: resolves via the entity-detail endpoint and shows identity + copyable key', async () => {
    detailFn.mockResolvedValue(targetDetail());
    const { target, component } = await mountView('target', 'prod/k8s/app');
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
    const { target, component } = await mountView('target', 'prod/k8s/app');
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
    d.target.ownership = { owner: 'team/platform', declared: true, ref: { kind: 'owner', key: 'team/platform', label: 'team/platform', href: '/fleet/owners/team%2Fplatform' } };
    detailFn.mockResolvedValue(d);
    const { target, component } = await mountView('target', 'prod/k8s/app');
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
    let m = await mountView('target', 'prod/k8s/app');
    await vi.waitFor(() => expect(m.target.textContent).toContain('Data source'));
    expect(m.target.textContent).not.toContain('Contributing data sources');
    unmount(m.component); document.body.removeChild(m.target);

    const many = targetDetail();
    many.target.source = 'local';
    many.target.sources = { items: ['local', 'k8s'], total: 2, count: 2, truncated: false };
    detailFn.mockResolvedValue(many);
    m = await mountView('target', 'prod/k8s/app');
    await vi.waitFor(() => expect(m.target.textContent).toContain('Contributing data sources'));
    unmount(m.component); document.body.removeChild(m.target);
  });

  it('maps the DTO open-graph action to a canonical graph route', async () => {
    detailFn.mockResolvedValue(targetDetail());
    const { target, component } = await mountView('target', 'prod/k8s/app');
    await vi.waitFor(() => expect(target.querySelector('.page-hd-action')).toBeTruthy());
    const action = Array.from(target.querySelectorAll('a.page-hd-action')).find((a) => a.textContent?.includes('graph')) as HTMLAnchorElement;
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

/**
 * Background refresh must not destroy the page the user is reading.
 *
 * App.loadGlobal() advances refreshTick on every poll, and the entity route consumes
 * it. If a tick tears the entity body out of the DOM, the page collapses to a loading
 * shell, the document shortens and the browser clamps the scroll position -- a user
 * halfway down a revision page is thrown back to the top every few seconds, for no
 * reason they can see. Stale-while-revalidate is the contract: the last good answer
 * stays on screen until a newer one replaces it.
 */
describe('FleetEntityView — background refresh preserves the page (A)', () => {
  beforeEach(() => detailFn.mockReset());

  function svc(): Record<string, any> {
    return {
      meta, status: 'Compliant',
      entity: ref('service', 'domain-a/payments', { domain: 'domain-a' }),
      actions: ['open-graph'],
      service: {
        domain: 'domain-a',
        revisions: { total: 1, count: 1, truncated: false, items: [ref('revision', 'domain-a/payments@1.0')] },
        deployments: { total: 0, count: 0, truncated: false, items: [] },
        dependencies: { total: 0, count: 0, truncated: false, items: [] },
        dependents: { total: 0, count: 0, truncated: false, items: [] },
        findings: { total: 0, count: 0, truncated: false, items: [] },
        evidence: { total: 0, count: 0, truncated: false, items: [] },
        limitations: { total: 0, count: 0, truncated: false, items: [] },
      },
    };
  }

  /** A promise plus its resolvers, so a test can hold a refresh mid-flight. */
  function deferred<T>() {
    let resolve!: (v: T) => void, reject!: (e: unknown) => void;
    const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej; });
    return { promise, resolve, reject };
  }

  async function mountLive(props: Record<string, unknown>) {
    const target = document.createElement('div');
    document.body.appendChild(target);
    const component = mount(FleetEntityView, { target, props });
    await vi.waitFor(() => expect(target.querySelector('.ev-body')).toBeTruthy());
    return { target, component };
  }

  it('tells the nav a golden path page is open, and stops telling it when the page closes', async () => {
    // The Navbar lights Golden paths from currentEntity.template; this view is what sets it.
    const { currentEntity } = await import('../lib/currentEntity.svelte.ts');
    const d = svc();
    d.entity.template = true;
    detailFn.mockResolvedValueOnce(d);
    const { target, component } = await mountLive({ kind: 'service', entityKey: 'domain-a/payments', refreshTick: 0 });
    await vi.waitFor(() => expect(currentEntity.template).toBe(true));
    unmount(component); document.body.removeChild(target);
    expect(currentEntity.template).toBe(false);

    detailFn.mockResolvedValueOnce(svc());
    const plain = await mountLive({ kind: 'service', entityKey: 'domain-a/payments', refreshTick: 0 });
    expect(currentEntity.template).toBe(false);
    unmount(plain.component); document.body.removeChild(plain.target);
  });

  it('keeps the entity body rendered while a same-entity refresh is in flight', async () => {
    detailFn.mockResolvedValueOnce(svc());
    const props = reactiveProps({ kind: 'service', entityKey: 'domain-a/payments', refreshTick: 0 });
    const { target, component } = await mountLive(props);
    await vi.waitFor(() => expect(target.textContent).toContain('Revisions'));

    const pending = deferred<Record<string, any>>();
    detailFn.mockReturnValueOnce(pending.promise);
    props.refreshTick = 1;
    flushSync();
    await Promise.resolve();

    // Mid-flight: the previous answer is still the best answer we have.
    expect(target.querySelector('.ev-body'), 'the entity body vanished during a background refresh').toBeTruthy();
    expect(target.textContent, 'the entity body vanished during a background refresh').toContain('Revisions');

    pending.resolve(svc());
    await vi.waitFor(() => expect(detailFn).toHaveBeenCalledTimes(2));
    expect(target.textContent).toContain('Revisions');
    unmount(component); document.body.removeChild(target);
  });

  it('a failed background refresh keeps the good page and says the refresh failed', async () => {
    detailFn.mockResolvedValueOnce(svc());
    const props = reactiveProps({ kind: 'service', entityKey: 'domain-a/payments', refreshTick: 0 });
    const { target, component } = await mountLive(props);
    await vi.waitFor(() => expect(target.textContent).toContain('Revisions'));

    detailFn.mockRejectedValueOnce(new Error('backend went away'));
    props.refreshTick = 1;
    flushSync();

    await vi.waitFor(() => expect(target.querySelector('[data-testid="stale-refresh"]')).toBeTruthy());
    // Good data must not decay into an error screen...
    expect(target.textContent, 'a failed refresh emptied a page that had good data').toContain('Revisions');
    // ...and the failure must not be swallowed either.
    expect(target.querySelector('[data-testid="stale-refresh"]')?.textContent).toMatch(/could not be refreshed/i);
    unmount(component); document.body.removeChild(target);
  });

  it('navigating to a DIFFERENT entity never shows the previous entity under the new heading', async () => {
    detailFn.mockResolvedValueOnce(svc());
    const props = reactiveProps({ kind: 'service', entityKey: 'domain-a/payments', refreshTick: 0 });
    const { target, component } = await mountLive(props);
    await vi.waitFor(() => expect(target.textContent).toContain('payments'));

    const pending = deferred<Record<string, any>>();
    detailFn.mockReturnValueOnce(pending.promise);
    props.entityKey = 'domain-b/ledger';
    flushSync();
    await Promise.resolve();

    // A different question is being asked; the old answer is not an answer to it.
    expect(target.querySelector('.ev-body'), 'the previous entity stayed on screen after navigating away').toBeNull();
    const other = svc();
    other.entity = ref('service', 'domain-b/ledger', { domain: 'domain-b' });
    other.service.domain = 'domain-b';
    other.service.revisions = { total: 1, count: 1, truncated: false, items: [ref('revision', 'domain-b/ledger@1.0')] };
    pending.resolve(other);
    await vi.waitFor(() => expect(target.textContent).toContain('ledger'));
    expect(target.textContent).not.toContain('payments');
    unmount(component); document.body.removeChild(target);
  });
});

describe('FleetEntityView — rich service page (D)', () => {
  beforeEach(() => detailFn.mockReset());
  function serviceDetail(): Record<string, any> {
    return {
      meta, status: 'NonCompliant',
      entity: ref('service', 'domain-a/payments', { domain: 'domain-a' }),
      actions: ['open-graph', 'compare', 'impact'],
      service: {
        domain: 'domain-a',
        ownership: { owner: 'team-a', declared: true, ref: ref('owner', 'team-a'), conflicts: { total: 2, count: 2, truncated: false, items: ['team-a', 'team-b'] } },
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

  it('gives operational targets the full width and clips their names, but lets revision names wrap', async () => {
    detailFn.mockResolvedValue(serviceDetail());
    const { target, component } = await mountView('service', 'domain-a/payments');
    await vi.waitFor(() => expect(target.textContent).toContain('Operational targets'));
    const listUnder = (title: string) => Array.from(target.querySelectorAll('h2, h3')).find((h) => h.textContent?.trim().startsWith(title))!
      .closest('[data-testid="preview-section"]')!.querySelector('.ref-list')!;
    const targets = listUnder('Operational targets');
    // Full width: outside the card grids, so a long target name is not cut into four lines.
    expect(targets.closest('.se-grid')).toBeNull();
    // Targets lay out in columns (name, then environment); the columns ellipsize a long name.
    expect(targets.classList.contains('pl-list--cols')).toBe(true);
    // The whole name stays in the title of the clipped label.
    expect(targets.querySelector('.ei-label')?.getAttribute('title')).toBeTruthy();
    // A revision's version is at the end of its name, so revision lists never clip.
    const revisions = listUnder('All revisions');
    expect(revisions.classList.contains('ref-clip')).toBe(false);
    // Full width too: a long service name in a third of the page was four lines a revision.
    expect(revisions.closest('.se-grid')).toBeNull();
    unmount(component); document.body.removeChild(target);
  });

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
    const { target, component } = await mountView('service', 'domain-a/payments');
    await vi.waitFor(() => expect(target.textContent).toContain('Observed traffic'));
    openAll(target);
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
    const { target, component } = await mountView('service', 'domain-a/payments');
    await vi.waitFor(() => expect(target.textContent).toContain('Observed traffic'));
    openAll(target);
    expect(target.querySelector('.rel-prov')?.textContent).toBe('Expected');
    unmount(component); document.body.removeChild(target);
  });

  it('reads an ownership conflict as a list of revisions, not a paragraph of hex', async () => {
    const digest = `sha256:${'673e8f73'.repeat(8)}`;
    const d = serviceDetail();
    d.service.ownership = {
      owner: 'team-a',
      declared: true,
      ref: ref('owner', 'team-a'),
      conflicts: { total: 3, count: 1, truncated: true, items: [`domain-a/payments@${digest}: team-b`] },
      conflictReason: 'targets of payments run revisions that declare different owners',
    };
    detailFn.mockResolvedValue(d);
    const { target, component } = await mountView('service', 'domain-a/payments');
    await vi.waitFor(() => expect(target.textContent).toContain('Ownership conflict'));
    // The banner says why, from the answer's own reason.
    expect(target.querySelector('.se-conflicts-head')?.textContent).toContain('Targets of payments run revisions that declare different owners:');
    const rows = Array.from(target.querySelectorAll('.se-conflict-list li'));
    expect(rows[0].textContent).toBe('domain-a/payments@673e8f73673e…: team-b');
    expect(rows[0].getAttribute('title')).toContain(digest); // exact key still available
    expect(rows[1].textContent).toBe('+2 more');             // truncation stays honest
    unmount(component); document.body.removeChild(target);
  });

  it('renders bounded previews with honest count-of-total and truncation, plus ownership conflict', async () => {
    detailFn.mockResolvedValue(serviceDetail());
    const { target, component } = await mountView('service', 'domain-a/payments');
    await vi.waitFor(() => expect(target.textContent).toContain('Revisions'));
    const text = target.textContent || '';
    expect(text).toContain('2 of 5');            // revisions preview count-of-total
    expect(text).toMatch(/Showing 2 of 5/);      // truncation is explicit, not hidden
    expect(text).toContain('Ownership conflict'); // conflicting revision owners surfaced
    expect(text).toContain('contract drift');     // attributed finding
    // The page's shared detail fetch never asks for reached times (a caller-scoped delivery read).
    expect(detailFn.mock.calls[0]).toEqual(['service', 'domain-a/payments']);
    // a revision row links via the canonical href
    const link = Array.from(target.querySelectorAll('a.entity-link')).find((a) => a.getAttribute('href')?.includes('/fleet/revisions/')) as HTMLAnchorElement;
    expect(link).toBeTruthy();
    unmount(component); document.body.removeChild(target);
  });

  it('counts the neighbours Kubernetes permissions hide, without naming them', async () => {
    const sd = serviceDetail();
    sd.service.dependencies = { total: 0, count: 0, truncated: false, items: [] };
    sd.service.dependents = { total: 1, count: 1, truncated: false, items: [ref('service', 'domain-a/web')] };
    sd.service.hiddenDependencies = 2;
    sd.service.hiddenDependents = 1;
    detailFn.mockResolvedValue(sd);
    const { target, component } = await mountView('service', 'domain-a/payments');
    await vi.waitFor(() => expect(target.textContent).toContain('Expected dependencies'));
    for (const d of Array.from(target.querySelectorAll('details'))) {
      if (/Expected dependencies|Dependents/.test(d.querySelector('summary')?.textContent ?? '')) {
        d.open = true;
        d.dispatchEvent(new Event('toggle'));
      }
    }
    await vi.waitFor(() => expect(target.querySelector('[data-testid="hidden-dependents"]')).toBeTruthy());
    const text = target.textContent || '';
    expect(text).toContain('2 services hidden by your Kubernetes permissions.');
    expect(text).not.toContain('No declared dependencies.');
    expect(target.querySelector('[data-testid="hidden-dependents"]')?.textContent).toBe('Plus 1 service hidden by your Kubernetes permissions.');
    unmount(component); document.body.removeChild(target);
  });

  // 'compare' and 'impact' are two stages of ONE question ("what changed, and what does
  // that change affect?"), so they resolve to the same workspace and are offered ONCE.
  // Two buttons opening the identical screen was the legacy seam this replaces.
  it('offers the Change analysis workspace exactly once for compare AND impact', async () => {
    detailFn.mockResolvedValue(serviceDetail());
    const { target, component } = await mountView('service', 'domain-a/payments');
    await vi.waitFor(() => expect(target.querySelector('.page-hd-action')).toBeTruthy());
    const links = Array.from(target.querySelectorAll('a.page-hd-action'));
    const labels = links.map((a) => a.textContent?.trim());
    expect(labels).toEqual(['Open in graph', 'Compare revisions', 'What if it becomes unavailable?']);
    expect(links.find((a) => a.textContent?.includes('Compare'))?.getAttribute('href'))
      .toBe('#/fleet/changes/domain-a%2Fpayments');
    expect(links.find((a) => a.textContent?.includes('What if'))?.getAttribute('href'))
      .toBe('#/fleet/changes/domain-a%2Fpayments?event=unavailable');
    unmount(component); document.body.removeChild(target);
  });

  // An offering's impact action asks the event about every provider at once.
  it('asks what if a whole offering becomes unavailable from its page', async () => {
    const pv = (items: unknown[]) => ({ total: items.length, count: items.length, truncated: false, items });
    detailFn.mockResolvedValue({
      meta, entity: { kind: 'offering', key: 'postgresql', label: 'postgresql', href: '/fleet/offerings/postgresql', offering: 'postgresql' },
      actions: ['impact'], offering: { providers: pv([]), consumers: pv([]), observedConsumers: pv([]), hiddenConsumers: 0, hiddenObservedConsumers: 0 },
    });
    const { target, component } = await mountView('offering', 'postgresql');
    await vi.waitFor(() => expect(target.querySelector('a.page-hd-action')).toBeTruthy());
    const links = Array.from(target.querySelectorAll('a.page-hd-action'));
    expect(links.map((a) => a.textContent?.trim())).toEqual(['What if it becomes unavailable?']);
    expect(links[0].getAttribute('href')).toBe('#/fleet/changes?offering=postgresql&event=unavailable');
    unmount(component); document.body.removeChild(target);
  });

  // An external system's page: its class, catalog, consumers and where it was recognized.
  it('shows an external system with its catalog, consumers and sources', async () => {
    detailFn.mockResolvedValue({
      meta, entity: { kind: 'external', key: 'stripe', label: 'Stripe', href: '/fleet/externals/stripe' }, actions: ['open-graph'],
      external: {
        external: { key: 'stripe', label: 'Stripe', class: 'vendor-api', consumers: ['domain-a/checkout'], consumersTotal: 1, sourcesTotal: 0, sources: [],
          catalog: { origin: 'default', digest: `sha256:${'3e9a'.repeat(16)}`, entry: 'stripe' } },
        consumers: [{ service: { kind: 'service', key: 'domain-a/checkout', label: 'checkout', href: '/fleet/services/domain-a%2Fcheckout' }, required: true }],
        catalog: { origin: 'default', digest: `sha256:${'3e9a'.repeat(16)}`, copy: 'embedded', signature: 'unknown' },
        inferredSources: [{ service: 'domain-a/checkout', source: 'k8s', kind: 'env', where: 'deployment/checkout env STRIPE_API_BASE', host: 'api.stripe.com', confidence: 'likely' }],
        inferredSourcesTotal: 1,
      },
    });
    const { target, component } = await mountView('external', 'stripe');
    await vi.waitFor(() => expect(target.querySelector('[data-testid="external-source"]')).toBeTruthy());
    expect(target.textContent).toContain('External system');
    expect(target.querySelector('[data-testid="external-catalog"]')?.textContent?.replace(/\s+/g, ' ').trim()).toBe('Default catalog sha256:3e9a3e9a, entry stripe: embedded copy, signature unknown.');
    expect(target.querySelector('a[href="#/fleet/services/domain-a%2Fcheckout"]')).toBeTruthy();
    unmount(component); document.body.removeChild(target);
  });

  it('breadcrumbs use the entity relationship (Overview > Services > payments)', async () => {
    detailFn.mockResolvedValue(serviceDetail());
    const { target, component } = await mountView('service', 'domain-a/payments');
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
    const { target, component } = await mountView('revision', 'domain-a/payments@2.1.0');
    await vi.waitFor(() => expect(target.textContent).toContain('2.1.0'));
    const text = target.textContent || '';
    expect(text).toContain('Readiness');
    // No interfaces in this fixture: one line says so instead of an empty card.
    expect(text).toContain('Declares no interfaces');
    expect(text).toContain('Running here (exact match)');
    // Readiness must be legible as DECLARED preparedness, never read as a runtime result.
    expect(text).toMatch(/read from its contract/i);
    // parent service is a link
    expect(Array.from(target.querySelectorAll('a.entity-link')).some((a) => a.getAttribute('href') === '#/fleet/services/domain-a%2Fpayments')).toBe(true);
    unmount(component); document.body.removeChild(target);
  });

  it('shows content retrievability as its OWN dimension and never calls mutable content immutable', async () => {
    detailFn.mockResolvedValue(revisionDetail({ retrievable: false, identityClass: 'mutable' }));
    const { target, component } = await mountView('revision', 'domain-a/payments@2.1.0');
    await vi.waitFor(() => expect(target.textContent).toContain('Mutable reference'));
    expect(target.textContent).not.toMatch(/immutable/i); // never asserts immutability of mutable content
    unmount(component); document.body.removeChild(target);
  });

  it('names a revision with no readiness gate instead of answering triage with silence', async () => {
    const d = revisionDetail();
    d.revision.readiness = null;
    detailFn.mockResolvedValue(d);
    const { target, component } = await mountView('revision', 'domain-a/payments@2.1.0');
    await vi.waitFor(() => expect(target.textContent).toContain('Readiness'));
    const text = target.textContent || '';
    expect(text).toContain('Not declared');
    // "Nothing declared" and "declared and failing" must not read as the same state.
    expect(text).toMatch(/nothing to pass or fail/i);
    expect(text).not.toMatch(/not passing/i);
    unmount(component); document.body.removeChild(target);
  });

  it('reads a field label as English, never as the wire field name', async () => {
    const d = revisionDetail();
    d.revision.pactoVersion = '2.0';
    detailFn.mockResolvedValue(d);
    const { target, component } = await mountView('revision', 'domain-a/payments@2.1.0');
    await vi.waitFor(() => expect(target.textContent).toContain('2.0'));
    const labels = Array.from(target.querySelectorAll('.re-k')).map((n) => n.textContent);
    expect(labels).toContain('Pacto version');
    expect(labels).not.toContain('pactoVersion');
  });

  it('says when a revision declares a pactoVersion behind or unknown to this Pacto', async () => {
    const render = async (patch: Record<string, unknown>) => {
      const d = revisionDetail();
      Object.assign(d.revision, { pactoVersion: '2.0' }, patch);
      detailFn.mockResolvedValue(d);
      const { target, component } = await mountView('revision', 'domain-a/payments@2.1.0');
      await vi.waitFor(() => expect(target.textContent).toContain('2.0'));
      const text = target.textContent || '';
      unmount(component); document.body.removeChild(target);
      return text;
    };
    const behind = await render({ pactoVersionCurrent: '2.5' });
    expect(behind).toContain('behind 2.5');
    expect(behind).toContain('This revision declares pactoVersion 2.0. The latest version this Pacto knows is 2.5.');
    const unknown = await render({ pactoVersion: '9.9', pactoVersionCurrent: '2.5', pactoVersionUnknown: true });
    expect(unknown).toContain('unknown version');
    expect(unknown).toContain('This revision declares pactoVersion 9.9, which this Pacto does not know. The latest version it knows is 2.5.');
    expect(unknown).not.toContain('behind 2.5');
    const current = await render({});
    expect(current).not.toContain('behind');
    expect(current).not.toContain('unknown version');
  });

  it('links the owner from a revision, the same as from its service', async () => {
    // The backend emits the owner ref for both, so the trail out to "everything this
    // team owns" does not dead-end on the revision page.
    const d = revisionDetail();
    d.revision.ownership = { owner: 'team-a', declared: true, ref: ref('owner', 'team-a') };
    detailFn.mockResolvedValue(d);
    const { target, component } = await mountView('revision', 'domain-a/payments@2.1.0');
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
    const { target, component } = await mountView('target', 'prod/k8s/app');
    await vi.waitFor(() => expect(target.textContent).toContain('Ambiguous revision match'));
    const text = target.textContent || '';
    // The prose explains WHY we cannot name the revision -- it does not restate the badge.
    expect(text).toMatch(/more than one known revision matches/i);
    expect(text).toMatch(/cannot say which one is running/i);
    // ...and it never lets "we cannot see it" read as "nothing is there".
    expect(text).toMatch(/Something is running here/);
    // the "Running revision"/"Inferred revision" authoritative label must NOT appear
    expect(text).not.toContain('Running revision');
    expect(text).not.toContain('Inferred revision');
    unmount(component); document.body.removeChild(target);
  });

  it('gives an unresolved match its own reason, not the ambiguous one', async () => {
    const d = ambiguousTarget();
    d.target.linkState = 'unresolved';
    detailFn.mockResolvedValue(d);
    const { target, component } = await mountView('target', 'prod/k8s/app');
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
    const { target, component } = await mountView('owner', 'platform-team');
    await vi.waitFor(() => expect(target.textContent).toContain('Services'));
    const text = target.textContent || '';
    expect(text).toContain('2 of 4');       // services preview honest count
    expect(text).toContain('Needs attention');
    expect(text).toContain('evidence stale');
    // The category reads as its label, the same as on the domain page.
    expect(target.querySelector('.oe-cat')?.textContent).toBe('Stale evidence');
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
    const { target, component } = await mountView('owner', 'team:platform');
    await vi.waitFor(() => expect(target.textContent).toContain('Needs attention'));
    const viewAll = Array.from(target.querySelectorAll('a')).find((a) => a.textContent?.includes('View all for this owner')) as HTMLAnchorElement;
    expect(viewAll.getAttribute('href')).toBe(`#/fleet/attention?ownerKey=${encodeURIComponent('team:platform')}`);
    expect(viewAll.getAttribute('href')).not.toContain('Platform'); // never the display label
    unmount(component); document.body.removeChild(target);
  });

  // A source sent 20 records and the product owes it 25 entities, because the five
  // services were DERIVED from the twelve revisions and were sent by nobody. Both
  // numbers are true of the same source and neither is the other's total, so the page
  // has to name them as different measurements rather than print them side by side and
  // let a reader subtract. The fixture is deliberately NOT reconciled: making the two
  // agree would delete the thing being tested.
  it('a source distinguishes the records it sent from the product entities it contributed', async () => {
    detailFn.mockResolvedValue({
      // `status` mirrors the source's health, exactly as the backend emits it
      // (detail.go sets EntityDetail.Status from the source status). Health is badged
      // ONCE, in the page header; the body explains what it COSTS instead of restating it.
      meta, entity: ref('source', 'kubernetes'), status: 'available',
      source: {
        kind: 'kubernetes', health: 'available', revisionCount: 12, targetCount: 8,
        contributed: { services: 5, revisions: 12, targets: 8 },
        entities: { total: 25, count: 1, truncated: true, items: [ref('target', 'prod/k8s/a')] },
        limitations: { total: 0, count: 0, truncated: false, items: [] },
      },
    });
    const { target, component } = await mountView('source', 'kubernetes');
    await vi.waitFor(() => expect(target.textContent).toContain('Product entities contributed'));
    const text = (target.textContent || '').replace(/\s+/g, ' ');
    expect(text.match(/Available/g)).toHaveLength(1); // header badge only, never restated
    // Health is answered in a sentence about consequences, not a second badge.
    expect(text).toContain('everything it holds is in the snapshot');
    // What it sent.
    expect(text).toContain('12 contract revisions · 8 operational targets');
    // What the product owes it: more, and counted differently, with the comparison stated.
    expect(text).toContain('5 services · 12 contract revisions · 8 operational targets');
    expect(text).toContain('from 20 records it sent');
    expect(text).toContain('1 of 25'); // the preview is honest against the ENTITY total
    expect(text).not.toContain('1 of 20'); // ...never against the record count
    expect(Array.from(target.querySelectorAll('nav a, nav span')).map((n) => n.textContent?.trim())).toEqual(expect.arrayContaining(['Data sources']));
    // And an obvious way back to the complete inventory, beyond the breadcrumb.
    const all = Array.from(target.querySelectorAll('a')).find((a) => a.textContent?.trim() === 'All data sources') as HTMLAnchorElement;
    expect(all.getAttribute('href')).toBe('#/fleet/sources');
    unmount(component); document.body.removeChild(target);
  });

  it('leads a degraded source with the human reason, not the machine code', async () => {
    detailFn.mockResolvedValue({
      meta, entity: ref('source', 'edge-cluster'),
      source: {
        kind: 'observation', health: 'unavailable', revisionCount: 0, targetCount: 0,
        contributed: { services: 0, revisions: 0, targets: 0 },
        error: { code: 'SOURCE_UNAVAILABLE', message: 'edge cluster unreachable' },
        entities: { total: 0, count: 0, truncated: false, items: [] },
        limitations: { total: 0, count: 0, truncated: false, items: [] },
      },
    });
    const { target, component } = await mountView('source', 'edge-cluster');
    await vi.waitFor(() => expect(target.textContent).toContain('edge cluster unreachable'));
    const banner = target.querySelector('.se-error') as HTMLElement;
    // The sentence starts with words, not an enum -- but the exact code is still there.
    expect(banner.textContent?.trim().startsWith('edge cluster unreachable')).toBe(true);
    expect(banner.querySelector('.se-error-code')?.textContent).toBe('SOURCE_UNAVAILABLE');
    // ...under a heading, so the failure is a section of the page rather than a stray
    // red box, and the contents navigator can offer it.
    expect(target.querySelector('#sec-reported-failure')?.getAttribute('data-toc')).toBe('Reported failure');
    unmount(component); document.body.removeChild(target);
  });

  it('shows an available source message as a note, never a failure', async () => {
    detailFn.mockResolvedValue({
      meta, entity: ref('source', 'linked'),
      source: {
        kind: 'kubernetes-linked', health: 'available', revisionCount: 0, targetCount: 1,
        contributed: { services: 0, revisions: 0, targets: 1 },
        error: { code: 'LINK_DIGEST_OFF', message: 'linking by image digest is off' },
        entities: { total: 0, count: 0, truncated: false, items: [] },
        limitations: { total: 0, count: 0, truncated: false, items: [] },
      },
    });
    const { target, component } = await mountView('source', 'linked');
    await vi.waitFor(() => expect(target.textContent).toContain('linking by image digest is off'));
    expect(target.querySelector('#sec-reported-failure')?.getAttribute('data-toc')).toBe('Note');
    expect(target.querySelector('#se-fail-h')?.textContent).toBe('Note');
    expect(target.querySelector('.se-error')?.classList.contains('se-note')).toBe(true);
    unmount(component); document.body.removeChild(target);
  });

  // THE SELECTED SOURCE'S HEALTH IS NOT THE FLEET'S KNOWLEDGE COMPLETENESS.
  //
  // `local` is Available. Another source is not. The page used to badge `local`
  // Available and then, a line below, print "Source unavailable — this page may be
  // incomplete": the same three words the header would have used for a source that was
  // down, with nothing to say they were about somewhere else.
  it('an available source is never described as unavailable by the fleet caveat', async () => {
    detailFn.mockResolvedValue({
      meta: { ...meta, completeness: 'partial', sources: [{ id: 'local', status: 'available' }, { id: 'edge', status: 'unavailable' }] },
      entity: ref('source', 'local'), status: 'available',
      source: {
        kind: 'local', health: 'available', revisionCount: 4, targetCount: 2,
        contributed: { services: 3, revisions: 4, targets: 2 },
        entities: { total: 9, count: 0, truncated: false, items: [] },
        limitations: { total: 0, count: 0, truncated: false, items: [] },
      },
    });
    const { target, component } = await mountView('source', 'local');
    await vi.waitFor(() => expect(target.querySelector('.knowledge')).toBeTruthy());
    const caveat = (target.querySelector('.knowledge')?.textContent || '').replace(/\s+/g, ' ').trim();
    // It names its own subject, and the subject is the snapshot.
    expect(caveat).toContain('The fleet snapshot is missing data.');
    // It attributes the outage to a COUNT of other sources, which cannot be read as a
    // statement about the one on screen.
    expect(caveat).toContain('1 data source is unavailable');
    expect(caveat).toContain('this page may be incomplete');
    // The old fragment, which was indistinguishable from a claim about `local`.
    expect(caveat).not.toContain('Source unavailable');
    // And the page still says, in its own words, that THIS source is fine.
    expect(target.textContent).toContain('everything it holds is in the snapshot');
    unmount(component); document.body.removeChild(target);
  });

  it('states a source header status in the health vocabulary, not the compliance one', async () => {
    // `detail.status` for a source is HEALTH. Through the compliance badge it fell
    // through to a grey lowercase "unavailable" in the header while the fact row below
    // said "Unavailable" in red -- one source, two vocabularies, on one screen.
    detailFn.mockResolvedValue({
      meta, entity: ref('source', 'edge-cluster'), status: 'unavailable',
      source: {
        kind: 'observation', health: 'unavailable', revisionCount: 0, targetCount: 0,
        entities: { total: 0, count: 0, truncated: false, items: [] },
        limitations: { total: 0, count: 0, truncated: false, items: [] },
      },
    });
    const { target, component } = await mountView('source', 'edge-cluster');
    await vi.waitFor(() => expect(target.textContent).toContain('Product entities contributed'));
    const head = target.querySelector('.page-hd') as HTMLElement;
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
    const { target, component } = await mountView('target', 'prod/k8s/app');
    await vi.waitFor(() => expect(target.textContent).toContain('Observed runtime'));
    openAll(target);
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
        ownership: { owner: 'platform', declared: true, ref: ref('owner', 'platform'), conflicts: { total: 0, count: 0, truncated: false, items: [] } },
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
    const { target, component } = await mountView('revision', 'domain-a/app@sha256:1');
    await vi.waitFor(() => expect(target.textContent).toContain('Readiness checks'));
    openAll(target);
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
    const { target, component } = await mountView('service', 'domain-a/app');
    await vi.waitFor(() => expect(target.textContent).toContain('Observed traffic and differences'));
    // The section is folded on a detail page; open it as a reader would.
    target.querySelectorAll('details:not([open])').forEach((d) => { (d as HTMLDetailsElement).open = true; d.dispatchEvent(new Event('toggle')); });
    flushSync();
    const text = (target.textContent || '').replace(/\s+/g, ' ');
    expect(text).not.toContain('200 of 200');
    expect(text).toContain('Showing 200. More exist; total unknown.');
    unmount(component); document.body.removeChild(target);
  });
});

describe('FleetEntityView — cluster-qualified target keys', () => {
  // A block body: a function returned from beforeEach is a cleanup hook, and the mock would run again after the test.
  beforeEach(() => { detailFn.mockReset(); location.hash = ''; });

  it('replaces a legacy or forward key with the canonical href, without a new history entry', async () => {
    location.hash = '#/fleet/targets/c1%2Fprod%2Fk8s%2Fapp';
    const replace = vi.spyOn(history, 'replaceState');
    const push = vi.spyOn(history, 'pushState');
    detailFn.mockResolvedValue(targetDetail());
    const { target, component } = await mountView('target', 'c1/prod/k8s/app');
    await vi.waitFor(() => expect(replace).toHaveBeenCalledWith(null, '', '#/fleet/targets/prod%2Fk8s%2Fapp'));
    expect(push).not.toHaveBeenCalled();
    replace.mockRestore(); push.mockRestore();
    unmount(component); document.body.removeChild(target);
  });

  it('keeps the correlation id of the address when it replaces the key', async () => {
    location.hash = '#/fleet/targets/c1%2Fprod%2Fk8s%2Fapp?correlationID=INC-42';
    const replace = vi.spyOn(history, 'replaceState');
    detailFn.mockResolvedValue(targetDetail());
    const { target, component } = await mountView('target', 'c1/prod/k8s/app');
    await vi.waitFor(() => expect(replace).toHaveBeenCalledWith(null, '', '#/fleet/targets/prod%2Fk8s%2Fapp?correlationID=INC-42'));
    replace.mockRestore();
    unmount(component); document.body.removeChild(target);
  });

  it('leaves the location alone when the requested key is the canonical one', async () => {
    const replace = vi.spyOn(history, 'replaceState');
    detailFn.mockResolvedValue(targetDetail());
    const { target, component } = await mountView('target', 'prod/k8s/app');
    expect(replace).not.toHaveBeenCalled();
    replace.mockRestore();
    unmount(component); document.body.removeChild(target);
  });

  it('offers a chooser when a target key names targets in more than one cluster', async () => {
    const { ApiError } = await import('../lib/api.ts');
    const err = new ApiError(409, 'this target key names targets in more than one cluster; choose one');
    err.candidates = [
      { key: 'c1/prod/k8s/app', label: 'eu-prod / prod / app' },
      { key: 'c2/prod/k8s/app', label: 'us-prod / prod / app' },
    ];
    detailFn.mockRejectedValue(err);
    const target = document.createElement('div');
    document.body.appendChild(target);
    const component = mount(FleetEntityView, { target, props: { kind: 'target', entityKey: 'prod/k8s/app', refreshTick: 0 } });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="target-key-chooser"]')).toBeTruthy());
    const chooser = target.querySelector('[data-testid="target-key-chooser"]') as HTMLElement;
    expect(chooser.textContent).toContain('This target key names targets in more than one cluster; choose one.');
    const links = [...chooser.querySelectorAll('a')];
    expect(links.map((a) => a.getAttribute('href'))).toEqual(['#/fleet/targets/c1%2Fprod%2Fk8s%2Fapp', '#/fleet/targets/c2%2Fprod%2Fk8s%2Fapp']);
    expect(links[0].textContent).toContain('eu-prod / prod / app');
    expect(links[0].querySelector('bdi')?.textContent).toBe('c1/prod/k8s/app');
    expect(target.querySelector('h1')).toBeTruthy();
    unmount(component); document.body.removeChild(target);
  });

  it('chooser links open each candidate, the clusterless one included, and keep the correlation id', async () => {
    location.hash = '#/fleet/targets/prod%2Fk8s%2Fapp?correlationID=INC-42';
    const { ApiError } = await import('../lib/api.ts');
    const err = new ApiError(409, 'this target key matches one target, but a source is incomplete and may hold the same target in another cluster; choose one');
    err.candidates = [{ key: '_/prod/k8s/app', label: 'prod/k8s/app (cluster unknown)' }];
    detailFn.mockRejectedValue(err);
    const target = document.createElement('div');
    document.body.appendChild(target);
    const component = mount(FleetEntityView, { target, props: { kind: 'target', entityKey: 'prod/k8s/app', refreshTick: 0 } });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="target-key-chooser"]')).toBeTruthy());
    const chooser = target.querySelector('[data-testid="target-key-chooser"]') as HTMLElement;
    expect(chooser.textContent).toContain('a source is incomplete');
    expect([...chooser.querySelectorAll('a')].map((a) => a.getAttribute('href'))).toEqual(['#/fleet/targets/_%2Fprod%2Fk8s%2Fapp?correlationID=INC-42']);
    unmount(component); document.body.removeChild(target);
  });
});

describe('FleetEntityView — external system page', () => {
  beforeEach(() => detailFn.mockReset());
  const DIGEST = 'sha256:3e9a1b2c3d4e5f60718293a4b5c6d7e8f90123456789abcdef0123456789abcd';
  const svc = (name: string) => ({ kind: 'service', key: name, label: name, href: `/fleet/services/${name}` });
  // eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
  function externalDetail(record: Record<string, any>, extra: Record<string, any> = {}): any {
    return {
      meta,
      entity: { kind: 'external', key: record.key, label: record.label, secondary: record.class, status: 'unknown', href: `/fleet/externals/${record.key}` },
      status: 'unknown',
      actions: ['open-graph'],
      external: {
        external: { sources: [], sourcesTotal: 0, consumers: [], consumersTotal: 0, ...record },
        consumers: [],
        ...extra,
      },
    };
  }

  it('lists the consumers and a provenance panel with every source and the catalog', async () => {
    detailFn.mockResolvedValue(externalDetail({
      key: 'stripe', label: 'Stripe', class: 'vendor-api', consumersTotal: 2, sourcesTotal: 2, hiddenConsumers: 1,
      catalog: { origin: 'default', digest: DIGEST, entry: 'stripe' },
      sources: [
        { kind: 'contract', service: 'checkout', index: 0, dependency: 'Stripe', version: '2024-06', required: true, provenance: 'declared', match: 'catalog-alias' },
        { kind: 'contract', service: 'billing', index: 2, dependency: 'stripe-api', provenance: 'declared', match: 'catalog-alias', superseded: true },
      ],
    }, {
      consumers: [{ service: svc('checkout'), required: true, versions: ['2024-06'] }, { service: svc('billing') }],
      catalog: { origin: 'default', digest: DIGEST, copy: 'embedded', signature: 'unknown', reason: 'the default catalog is checked only when a source already reads ghcr.io' },
    }));
    const { target, component } = await mountView('external', 'stripe');
    expect(detailFn).toHaveBeenCalledWith('external', 'stripe');
    const rows = Array.from(target.querySelectorAll('[data-testid="external-consumers"] tbody tr'));
    expect(rows.map((r) => Array.from(r.children).map((c) => c.textContent?.trim()))).toEqual([['checkout', 'required', '2024-06'], ['billing', 'optional', 'none named']]);
    expect(target.querySelector('[data-testid="external-hidden-consumers"]')?.textContent).toBe('1 more service you cannot see declares, calls or uses it.');
    const cat = target.querySelector('[data-testid="external-catalog"]')?.textContent?.replace(/\s+/g, ' ').trim();
    expect(cat).toBe('Default catalog sha256:3e9a1b2c, entry stripe: embedded copy, signature unknown (the default catalog is checked only when a source already reads ghcr.io).');
    const sources = Array.from(target.querySelectorAll('[data-testid="external-source"]')).map((r) => [
      r.getAttribute('data-provenance'), r.querySelector('[data-cell="service"] a')?.textContent?.trim(), r.querySelector('[data-cell="where"] bdi')?.textContent?.trim()]);
    expect(sources).toEqual([
      ['declared', 'checkout', 'pacto.yaml dependencies[0] (name: Stripe)'],
      ['declared', 'billing', 'pacto.yaml dependencies[2] (name: stripe-api, not in the newest revision)'],
    ]);
    expect(target.querySelector('[data-testid="external-name-only"]')).toBeNull();
    const crumbs = Array.from(target.querySelectorAll('nav a, nav span')).map((n) => n.textContent?.trim());
    expect(crumbs).toEqual(expect.arrayContaining(['Overview', 'Services', 'External systems', 'Stripe']));
    expect(target.querySelector('.xe-caption')?.textContent).toContain('2 services declare, call or use this vendor API.');
    expect(target.querySelector('[data-testid="external-folded-into"]')).toBeNull();
    unmount(component); document.body.removeChild(target);
  });

  it('says a system joined by name only does not join other spellings', async () => {
    detailFn.mockResolvedValue(externalDetail({ key: 'parcel-carrier', label: 'parcel-carrier', class: 'unclassified', consumersTotal: 1 },
      { consumers: [{ service: svc('carrier-gateway'), required: true }] }));
    const { target, component } = await mountView('external', 'parcel-carrier');
    expect(target.querySelector('[data-testid="external-name-only"]')?.textContent).toBe('Joined by name only: consumers that spell it differently do not join.');
    expect(target.querySelector('[data-testid="external-catalog"]')).toBeNull();
    unmount(component); document.body.removeChild(target);
  });

  it('words the join from how its consumers join it, never "name only" for an identity or a host', async () => {
    for (const [joinedBy, want] of [
      ['identity', 'Joined by the identity its consumers declare; no catalog entry classifies it.'],
      ['host', 'Joined by its registrable domain; no catalog entry classifies it.'],
    ]) {
      detailFn.mockResolvedValue(externalDetail({ key: 'k', label: 'k', class: 'unclassified', consumersTotal: 1, joinedBy }));
      const { target, component } = await mountView('external', 'k');
      expect(target.querySelector('[data-testid="external-name-only"]')?.textContent).toBe(want);
      expect(target.querySelector('.xe-caption')?.textContent).toContain('this external system');
      unmount(component); document.body.removeChild(target);
    }
  });

  it('says a verified default catalog is the embedded copy matching the signed artifact, not a registry read', async () => {
    detailFn.mockResolvedValue(externalDetail({ key: 'stripe', label: 'Stripe', class: 'vendor-api', consumersTotal: 1, catalog: { origin: 'default', digest: DIGEST, entry: 'stripe' } },
      { catalog: { origin: 'default', digest: DIGEST, copy: 'registry', signature: 'verified', signer: 'release' } }));
    const { target, component } = await mountView('external', 'stripe');
    const cat = target.querySelector('[data-testid="external-catalog"]')?.textContent?.replace(/\s+/g, ' ').trim();
    expect(cat).toContain('embedded copy, matches the signed registry artifact, signature verified (release)');
    unmount(component); document.body.removeChild(target);
  });

  it('says when this answer folds the system into other egress', async () => {
    detailFn.mockResolvedValue(externalDetail({ key: 'legacy-ftp', label: 'legacy-ftp', class: 'unclassified', consumersTotal: 1 }, { foldedInto: 'other-egress' }));
    const { target, component } = await mountView('external', 'legacy-ftp');
    const note = target.querySelector('[data-testid="external-folded-into"]');
    expect(note?.textContent).toContain('This answer folds it into Other egress');
    expect(note?.querySelector('a')?.getAttribute('href')).toBe('#/fleet/externals/other-egress');
    unmount(component); document.body.removeChild(target);
  });

  it('explains the other-egress node and how many systems it folds', async () => {
    detailFn.mockResolvedValue(externalDetail({ key: 'other-egress', label: 'Other egress', class: 'other-egress', folded: 12, consumersTotal: 3 }));
    const { target, component } = await mountView('external', 'other-egress');
    expect(target.querySelector('[data-testid="external-folded"]')?.textContent).toContain('This node folds 12 external systems');
    expect(target.querySelector('.xe-caption')?.textContent).toContain('3 services declare, call or use the external systems this node folds');
    expect(target.querySelector('[data-testid="external-name-only"]')).toBeNull();
    unmount(component); document.body.removeChild(target);
  });
});

describe('FleetEntityView — inferred service (509-S1)', () => {
  beforeEach(() => detailFn.mockReset());

  it('dispatches an inferred service to its own page and gives it no posture answer', async () => {
    const empty = { total: 0, count: 0, truncated: false, items: [] };
    detailFn.mockResolvedValue({
      meta,
      entity: { kind: 'service', key: 'k8s:shop%2FDeployment%2Fcart', label: 'k8s:shop/Deployment/cart', declaration: 'inferred', scope: 'shop', secondary: 'Deployment' },
      actions: [],
      service: { workload: { source: 'k8s-workloads', namespace: 'shop', kind: 'Deployment', name: 'cart' }, calls: empty, callsObserved: false, limitations: empty, revisions: empty },
    });
    const { target, component } = await mountView('service', 'k8s:shop%2FDeployment%2Fcart');
    await vi.waitFor(() => expect(target.querySelector('[data-testid="inferred-workload"]')).toBeTruthy());
    expect(target.querySelector('[data-testid="service-targets"]')).toBeNull();
    expect(target.querySelector('[data-testid="entity-answer"]')).toBeNull();
    unmount(component); document.body.removeChild(target);
  });
});
