/**
 * Component tests for FleetServicesView.svelte — the product Services list (C / A3).
 * The list consumes /api/fleet/entities?kinds=service through the SDK facade (never
 * the legacy /api/services list or a FleetSnapshot reconstruction), shows
 * domain-qualified identity, keeps filters and the page offset in the URL, pages
 * through the backend metadata, and distinguishes filtered-empty / empty-fleet /
 * incomplete-knowledge states. `api` is mocked so only /api/fleet/entities is used.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';

const { entitiesFn } = vi.hoisted(() => ({ entitiesFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { fleetEntities: (...a: unknown[]) => entitiesFn(...a) } };
});

// @ts-expect-error — Svelte component has no declaration file
import FleetServicesView from './FleetServicesView.svelte';

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
function listResp(entities: any[], opts: { total?: number; offset?: number; nextOffset?: number; partial?: boolean; aggregate?: any } = {}): any {
  const total = opts.total ?? entities.length;
  const offset = opts.offset ?? 0;
  return {
    meta: {
      schemaVersion: 'pacto.dev/fleet-product/v1',
      completeness: opts.partial ? 'partial' : 'complete',
      sources: opts.partial ? [{ id: 'k8s', kind: 'k8s', status: 'unavailable' }] : [],
    },
    total, count: entities.length, offset, limit: 25,
    truncated: opts.nextOffset != null, nextOffset: opts.nextOffset, entities,
    // The backend aggregate over every service matching the filters, paging excluded.
    // Absent by default so the tests that are not about the inventory keep it off screen.
    aggregate: opts.aggregate,
  };
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
function svc(domain: string): any {
  return { kind: 'service', key: `${domain}/payments`, label: 'payments', domain, href: `/fleet/services/${encodeURIComponent(`${domain}/payments`)}`, status: 'Compliant' };
}

function mountView(props: Record<string, unknown> = {}) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(FleetServicesView, { target, props: { refreshTick: 0, ...props } });
  return { target, component };
}

const rows = (t: HTMLElement) => Array.from(t.querySelectorAll('.sv-item'));

describe('FleetServicesView — product Services list (C / A3)', () => {
  beforeEach(() => { entitiesFn.mockReset(); location.hash = ''; });

  it('consumes /api/fleet/entities with kinds=service and shows domain-qualified identity', async () => {
    entitiesFn.mockResolvedValue(listResp([svc('domain-a'), svc('domain-b')], { total: 2 }));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(rows(target).length).toBe(2));
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['service'], limit: 25 }));
    // same-named services stay distinguishable by domain
    expect(rows(target)[0].textContent).toContain('domain domain-a');
    expect(rows(target)[1].textContent).toContain('domain domain-b');
    unmount(component); document.body.removeChild(target);
  });

  it('rows navigate through the canonical ProductRef href', async () => {
    entitiesFn.mockResolvedValue(listResp([svc('domain-a')], { total: 1 }));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(rows(target).length).toBe(1));
    const a = rows(target)[0].querySelector('a.entity-link') as HTMLAnchorElement;
    expect(a.getAttribute('href')).toBe('#/fleet/services/domain-a%2Fpayments');
    unmount(component); document.body.removeChild(target);
  });

  it('the status filter uses the backend query param and lives in the URL (resetting the page)', async () => {
    entitiesFn.mockResolvedValue(listResp([svc('domain-a')], { total: 1, offset: 25 }));
    const { target, component } = mountView({ offset: '25' });
    await vi.waitFor(() => expect(rows(target).length).toBe(1));
    const sel = target.querySelector('select[aria-label="Filter by compliance status"]') as HTMLSelectElement;
    sel.value = 'NonCompliant';
    sel.dispatchEvent(new Event('change', { bubbles: true }));
    expect(location.hash).toBe('#/fleet/services?status=NonCompliant'); // offset reset to page 1
    unmount(component); document.body.removeChild(target);
  });

  // Who owns it and whether ownership is declared at all are different questions, and
  // they compose: picking a state must not disturb the owner name already committed.
  it('the ownership-state filter is its own backend param and composes with owner', async () => {
    entitiesFn.mockResolvedValue(listResp([svc('domain-a')], { total: 1 }));
    const { target, component } = mountView({ owner: 'team-a' });
    await vi.waitFor(() => expect(rows(target).length).toBe(1));
    const sel = target.querySelector('select[aria-label="Filter by declared ownership"]') as HTMLSelectElement;
    expect(Array.from(sel.options).map((o) => o.value))
      .toEqual(['', 'consistent', 'conflicting', 'unowned']);
    sel.value = 'conflicting';
    sel.dispatchEvent(new Event('change', { bubbles: true }));
    expect(location.hash).toBe('#/fleet/services?owner=team-a&ownership=conflicting');
    unmount(component); document.body.removeChild(target);
  });

  it('shows an ownership-state chip in its legend wording, not the wire value', async () => {
    entitiesFn.mockResolvedValue(listResp([svc('domain-a')], { total: 1 }));
    const { target, component } = mountView({ ownership: 'conflicting' });
    await vi.waitFor(() => expect(rows(target).length).toBe(1));
    expect(target.querySelector('.chip')?.textContent).toContain('Revisions name different owners');
    unmount(component); document.body.removeChild(target);
  });

  it('the owner filter navigates with the backend query param', async () => {
    entitiesFn.mockResolvedValue(listResp([svc('domain-a')], { total: 1 }));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(rows(target).length).toBe(1));
    const ownerInput = target.querySelector('input[aria-label="Filter by owner"]') as HTMLInputElement;
    ownerInput.value = 'team-a';
    ownerInput.dispatchEvent(new Event('change', { bubbles: true }));
    expect(location.hash).toBe('#/fleet/services?owner=team-a');
    unmount(component); document.body.removeChild(target);
  });

  it('the search box commits on submit into the URL', async () => {
    entitiesFn.mockResolvedValue(listResp([svc('domain-a')], { total: 1 }));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(rows(target).length).toBe(1));
    const search = target.querySelector('input[type="search"]') as HTMLInputElement;
    search.value = 'pay';
    search.dispatchEvent(new Event('input', { bubbles: true })); // sync bind:value
    flushSync();
    (target.querySelector('form.sv-search') as HTMLFormElement).dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    expect(location.hash).toBe('#/fleet/services?text=pay');
    unmount(component); document.body.removeChild(target);
  });

  it('paginates through the backend metadata (Next carries the next offset)', async () => {
    entitiesFn.mockResolvedValue(listResp([svc('domain-a')], { total: 60, offset: 0, nextOffset: 25 }));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('.sv-range')?.textContent).toBe('Showing 1–1 of 60'));
    expect((target.querySelector('[data-testid="svc-next"]') as HTMLAnchorElement).getAttribute('href')).toBe('#/fleet/services?offset=25');
    expect(target.querySelector('[data-testid="svc-prev"]')).toBeNull();
    unmount(component); document.body.removeChild(target);
  });

  it('distinguishes filtered-empty from empty-fleet from incomplete-knowledge', async () => {
    // filtered-empty
    entitiesFn.mockResolvedValue(listResp([], { total: 0 }));
    let m = mountView({ status: 'NonCompliant' });
    await vi.waitFor(() => expect(m.target.textContent).toMatch(/no matching services/i));
    expect(m.target.querySelector('.ps-btn')?.textContent).toMatch(/clear filters/i);
    unmount(m.component); document.body.removeChild(m.target);

    // empty-fleet (no filter, complete knowledge)
    entitiesFn.mockResolvedValue(listResp([], { total: 0 }));
    m = mountView();
    await vi.waitFor(() => expect(m.target.textContent).toMatch(/no services yet/i));
    expect(m.target.textContent).not.toMatch(/knowledge is incomplete/i);
    unmount(m.component); document.body.removeChild(m.target);

    // incomplete knowledge (no filter, a source unavailable)
    entitiesFn.mockResolvedValue(listResp([], { total: 0, partial: true }));
    m = mountView();
    await vi.waitFor(() => expect(m.target.textContent).toMatch(/no services known/i));
    expect(m.target.textContent).toMatch(/knowledge is incomplete/i);
    unmount(m.component); document.body.removeChild(m.target);
  });

  it('issues exactly ONE initial request (no onMount + effect double-fire) [requirement E]', async () => {
    entitiesFn.mockResolvedValue(listResp([svc('domain-a')], { total: 1 }));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(rows(target).length).toBe(1));
    // Let any further scheduled effects settle, then assert a single backend call.
    flushSync();
    await Promise.resolve();
    expect(entitiesFn).toHaveBeenCalledTimes(1);
    unmount(component); document.body.removeChild(target);
  });

  it('filtered-empty UNDER incomplete knowledge shows BOTH facts, never hiding either [requirement D]', async () => {
    entitiesFn.mockResolvedValue(listResp([], { total: 0, partial: true }));
    const { target, component } = mountView({ status: 'NonCompliant' });
    await vi.waitFor(() => expect(target.textContent).toMatch(/no matching services/i));
    const text = target.textContent || '';
    expect(text).toMatch(/no matching services/i);       // the filter matched nothing
    expect(text).toMatch(/this list may be incomplete/i); // AND knowledge is incomplete
    unmount(component); document.body.removeChild(target);
  });

  /**
   * The inventory (requirement A / section 10). This page used to tally the 25 rendered rows:
   * honest about its scope in a caption and still the wrong chart to draw, because page
   * 1 of a 40-service fleet is a sample nobody chose. The aggregate is the backend's,
   * over every service the SAME filters select, with paging excluded.
   */
  const AGG = {
    matched: 40,
    serviceCompliance: { compliant: 20, nonCompliant: 5, unknown: 3, notEvaluated: 12 },
    ownership: { consistent: 30, conflicting: 2, unowned: 8 },
    byOwner: [{ owner: 'team-a', services: 18, targets: 40 }, { owner: 'team-b', services: 12, targets: 9 }],
    otherOwners: 4, distinctOwners: 5,
  };
  const dist = (t: HTMLElement, title: string) =>
    Array.from(t.querySelectorAll('.sv-inv-grid .dist')).find((f) => f.querySelector('.dist-title')?.textContent === title) as HTMLElement;

  it('charts the backend aggregate over the whole matching population, not the page', async () => {
    entitiesFn.mockResolvedValue(listResp([svc('a'), svc('b')], { total: 40, aggregate: AGG }));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(rows(target).length).toBe(2));

    const compliance = dist(target, 'Compliance');
    expect(Array.from(compliance.querySelectorAll('.dist-legend .dist-item')).map((n) => [
      n.querySelector('.dist-label')?.textContent, n.querySelector('.dist-value')?.textContent,
    ])).toEqual([['Compliant', '20'], ['Not compliant', '5'], ['Unknown', '3'], ['Not evaluated', '12']]);
    // 40 matching services, of which only 2 are on screen: the denominator is the match.
    expect(compliance.querySelector('.dist-scope')?.textContent).toBe('All 40 services in the snapshot.');
    expect(compliance.querySelector('.dist-pct')?.textContent).toBe('(50% of 40)');
    // Every bucket drills into the same list, narrowed by that status.
    expect(compliance.querySelector('.dist-legend a')?.getAttribute('href')).toBe('#/fleet/services?status=Compliant');
    unmount(component); document.body.removeChild(target);
  });

  it('says the aggregate follows the filters, and its buckets narrow the same query', async () => {
    entitiesFn.mockResolvedValue(listResp([svc('a')], { total: 40, aggregate: { ...AGG, matched: 7 } }));
    const { target, component } = mountView({ owner: 'team-a' });
    await vi.waitFor(() => expect(rows(target).length).toBe(1));
    expect(target.querySelector('#sv-inv-h')?.textContent).toBe('What these filters select');
    const ownership = dist(target, 'Declared ownership');
    expect(ownership.querySelector('.dist-scope')?.textContent).toBe('All 7 matching services, not just this page.');
    expect(Array.from(ownership.querySelectorAll('.dist-legend .dist-label')).map((n) => n.textContent))
      .toEqual(['One declared owner', 'Revisions name different owners', 'No declared owner']);
    // The owner filter survives the click; it is narrowed, not replaced.
    expect(ownership.querySelector('.dist-legend a')?.getAttribute('href'))
      .toBe('#/fleet/services?owner=team-a&ownership=consistent');
    unmount(component); document.body.removeChild(target);
  });

  /**
   * The owner ranking is a RANKING, not a partition: unowned and conflicting services
   * have no single owner to rank under and appear in no row. A page that let the reader
   * read the rows as a breakdown would quietly lose them.
   */
  it('says the owner ranking is a ranking, and what it leaves out', async () => {
    entitiesFn.mockResolvedValue(listResp([svc('a')], { total: 40, aggregate: AGG }));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(rows(target).length).toBe(1));
    const bars = Array.from(target.querySelectorAll('.sv-inv-grid .hbars'));
    const services = bars[0], targets = bars[1];
    expect(services.querySelector('.hb-title')?.textContent).toBe('Services per owner');
    expect(Array.from(services.querySelectorAll('.hb-row')).map((n) => [
      n.querySelector('.hb-label')?.textContent, n.querySelector('.hb-value')?.textContent,
    ])).toEqual([['team-a', '18 services'], ['team-b', '12 services']]);
    const note = services.querySelector('.hb-scope')?.textContent || '';
    expect(note).toContain('Top 2 of 5 declared owners by service count.');
    expect(note).toContain('The remaining 3 of 5 owners account for 4 more services.');
    expect(note).toContain('Services with no owner, or whose revisions name different owners, appear in no row here.');
    // A row counts team-a's CONSISTENTLY owned services, so its destination carries
    // ownership too: ownerKey=team-a alone also selects what team-a co-owns, which is a
    // longer list than the bar the reader clicked. And it is ownerKey, the exact owner,
    // not the free-text owner search that would also pull in team-a-platform.
    expect(services.querySelector('.hb-row a')?.getAttribute('href'))
      .toBe('#/fleet/services?ownerKey=team-a&ownership=consistent');
    expect(targets.querySelector('.hb-row a')?.getAttribute('href'))
      .toBe('#/fleet/services?ownerKey=team-a&ownership=consistent');
    // Targets per owner keeps the service-count order and says so, rather than
    // re-sorting a top-N-by-services list and presenting it as a target ranking.
    expect(Array.from(targets.querySelectorAll('.hb-label')).map((n) => n.textContent)).toEqual(['team-a', 'team-b']);
    expect(targets.querySelector('.hb-scope')?.textContent)
      .toBe('Same owners, in the same service-count order as above — this is not a ranking by target count.');
    unmount(component); document.body.removeChild(target);
  });

  /**
   * The two distributions describe the whole selection and stay on the page; the two
   * per-owner rankings answer the follow-up and cost ten touch-sized rows each, so they
   * sit behind the shared disclosure. Open, the four figures put the service list two
   * and a half screens down on a phone -- on the page a reader opens to find a service.
   */
  it('keeps the distributions in view and the per-owner rankings one disclosure away', async () => {
    entitiesFn.mockResolvedValue(listResp([svc('a')], { total: 40, aggregate: AGG }));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(rows(target).length).toBe(1));
    const more = target.querySelector('.sv-inv-more') as HTMLDetailsElement;
    expect(more.open).toBe(false);
    // Named, counted, and nothing deleted: both rankings are inside it.
    expect(more.querySelector('summary')?.textContent?.replace(/\s+/g, ' ').trim())
      .toContain('Per-owner breakdown 5 declared owners');
    expect(Array.from(more.querySelectorAll('.hb-title')).map((n) => n.textContent))
      .toEqual(['Services per owner', 'Operational targets per owner']);
    // And the distributions are NOT behind it.
    expect(Array.from(target.querySelectorAll('.sv-inventory > .sv-inv-grid .dist-title')).map((n) => n.textContent))
      .toEqual(['Compliance', 'Declared ownership']);
    unmount(component); document.body.removeChild(target);
  });

  it('draws no inventory when the filters select nothing to aggregate', async () => {
    entitiesFn.mockResolvedValue(listResp([svc('a')], { total: 1, aggregate: { ...AGG, matched: 0 } }));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(rows(target).length).toBe(1));
    expect(target.querySelector('.sv-inventory')).toBeNull();
    unmount(component); document.body.removeChild(target);
  });

  it('Clear filters returns to the unfiltered first page', async () => {
    entitiesFn.mockResolvedValue(listResp([svc('domain-a')], { total: 1 }));
    const { target, component } = mountView({ owner: 'team-a', status: 'NonCompliant', offset: '25' });
    await vi.waitFor(() => expect(target.querySelector('.chip')).toBeTruthy());
    (target.querySelector('.chip-clear') as HTMLButtonElement).click();
    expect(location.hash).toBe('#/fleet/services');
    unmount(component); document.body.removeChild(target);
  });
});

/**
 * Owner SEARCH and owner IDENTITY are two different questions and the product asks
 * them with two different parameters. The counterexample this pins down: owners
 * `team-a` and `team-a-platform` both exist, so a canonical action carrying the
 * free-text `owner=team-a` would land on a list holding services `team-a` does not
 * own. Canonical actions carry `ownerKey` (exact); the box the user types in keeps
 * carrying `owner` (substring over team, DRI and contacts), because losing generous
 * search would be a worse product than the bug.
 */
describe('FleetServicesView — owner search vs owner identity', () => {
  beforeEach(() => { entitiesFn.mockReset(); location.hash = ''; });

  // Owner suggestions and the service list share the mocked Entities endpoint; the
  // owner query is the one asking for kind `owner`.
  function twoCollidingOwners() {
    entitiesFn.mockImplementation((q: { kinds?: string[] }) => Promise.resolve(
      q.kinds?.[0] === 'owner'
        ? listResp([
          { kind: 'owner', key: 'team-a', label: 'team-a', href: '/fleet/owners/team-a' },
          { kind: 'owner', key: 'team-a-platform', label: 'team-a-platform', href: '/fleet/owners/team-a-platform' },
        ], { total: 2 })
        : listResp([svc('domain-a')], { total: 1 }),
    ));
  }

  it('picking an owner suggestion commits the exact owner, and blur does not downgrade it', async () => {
    twoCollidingOwners();
    const { target, component } = mountView();
    await vi.waitFor(() => expect(rows(target).length).toBe(1));
    const box = target.querySelector('input[aria-label="Filter by owner"]') as HTMLInputElement;
    box.value = 'team-a';
    box.dispatchEvent(new Event('input', { bubbles: true }));
    const opts = await vi.waitFor(() => {
      const found = Array.from(target.querySelectorAll('[data-testid="svc-owner-option"]'));
      expect(found.length).toBe(2);
      return found as HTMLElement[];
    });
    // The fuzzy suggestion list legitimately offers both; choosing one means THAT one.
    (opts[0] as HTMLButtonElement).click();
    flushSync();
    expect(location.hash).toBe('#/fleet/services?ownerKey=team-a');
    // The browser fires `change` on blur right after a pick, carrying the adopted
    // label. That must not re-read the exact choice as a substring search.
    box.dispatchEvent(new Event('change', { bubbles: true }));
    expect(location.hash).toBe('#/fleet/services?ownerKey=team-a');
    unmount(component); document.body.removeChild(target);
  });

  it('typing an owner keeps the generous search, and replaces an exact owner rather than adding to it', async () => {
    twoCollidingOwners();
    const { target, component } = mountView({ ownerKey: 'team-a' });
    await vi.waitFor(() => expect(rows(target).length).toBe(1));
    const box = target.querySelector('input[aria-label="Filter by owner"]') as HTMLInputElement;
    // Arriving with an exact owner, the box shows it -- the filter in force is legible.
    expect(box.value).toBe('team-a');
    box.value = 'team';
    box.dispatchEvent(new Event('change', { bubbles: true }));
    expect(location.hash).toBe('#/fleet/services?owner=team');
    unmount(component); document.body.removeChild(target);
  });

  it('sends both owner questions to the backend and labels their chips apart', async () => {
    entitiesFn.mockResolvedValue(listResp([svc('domain-a')], { total: 1 }));
    const { target, component } = mountView({ owner: 'team', ownerKey: 'team-a' });
    await vi.waitFor(() => expect(rows(target).length).toBe(1));
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ owner: 'team', ownerKey: 'team-a' }));
    // Two chips, distinguishable: a reader must be able to tell which one is loose.
    expect(Array.from(target.querySelectorAll('.chip')).map((c) => c.textContent?.replace(/\s+/g, ' ').trim()))
      .toEqual(['Owner: team-a ×', 'Owner search: team ×']);
    unmount(component); document.body.removeChild(target);
  });
});
