/**
 * Component tests for the product Owners and Sources list pages (requirement G).
 * Both consume /api/fleet/entities (kinds=owner / kinds=source) through the SDK facade
 * — never a FleetSnapshot reconstruction — with search / health filters and stable
 * backend pagination kept in the URL.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';

const { entitiesFn } = vi.hoisted(() => ({ entitiesFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { fleetEntities: (...a: unknown[]) => entitiesFn(...a) } };
});

// @ts-expect-error — Svelte component has no declaration file
import FleetOwnersView from './FleetOwnersView.svelte';
// @ts-expect-error — Svelte component has no declaration file
import FleetSourcesView from './FleetSourcesView.svelte';

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
function listResp(entities: any[], opts: { total?: number; offset?: number; nextOffset?: number; aggregate?: any } = {}): any {
  const total = opts.total ?? entities.length;
  return {
    meta: { schemaVersion: 'pacto.dev/fleet-product/v1', completeness: 'complete', sources: [] },
    total, count: entities.length, offset: opts.offset ?? 0, limit: 25,
    truncated: opts.nextOffset != null, nextOffset: opts.nextOffset, entities,
    aggregate: opts.aggregate,
  };
}

// The backend's ownership aggregate over the COMPLETE service population: 40 services,
// 5 declared owners of which the top 2 are ranked. Deliberately not a partition —
// 18 + 12 ranked + 4 other == 34 consistent, and the 6 conflicting/unowned services
// belong to no owner row.
const SERVICE_AGG = {
  matched: 40, services: 40,
  ownership: { consistent: 34, conflicting: 2, unowned: 4 },
  byOwner: [{ owner: 'team-a', services: 18, targets: 22 }, { owner: 'team-b', services: 12, targets: 9 }],
  otherOwners: 4, distinctOwners: 5,
};

const owners = (n: number) => Array.from({ length: n }, (_, i) => (
  { kind: 'owner', key: `team-${i}`, label: `team-${i}`, href: `/fleet/owners/team-${i}` }));

/** Answers the owner inventory and the service aggregate as the two questions they are. */
function respondByKind(ownerList: unknown, aggregate: unknown = SERVICE_AGG) {
  entitiesFn.mockImplementation((o: { kinds?: string[] }) => Promise.resolve(
    o?.kinds?.[0] === 'service' ? listResp([], { total: 40, aggregate }) : ownerList));
}

const aggregateOf = (target: HTMLElement) => target.querySelector('[data-testid="owners-aggregate"]');
// The summary section is always present -- it has its own loading, error and stale
// states -- so "the aggregate arrived" is the drawn distribution, not the section.
const aggregateDrawn = (target: HTMLElement) => aggregateOf(target)?.querySelector('.dist-legend');
const legend = (target: HTMLElement) => Array.from(
  aggregateOf(target)?.querySelectorAll('.dist-legend a') ?? [],
).map((a) => [
  a.querySelector('.dist-label')?.textContent,
  a.querySelector('.dist-value')?.textContent,
  a.getAttribute('href'),
]);

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- Svelte components have no declaration files here
function mountView(Comp: any, props: Record<string, unknown> = {}) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(Comp, { target, props: { refreshTick: 0, ...props } });
  return { target, component };
}

describe('FleetOwnersView (G)', () => {
  beforeEach(() => { entitiesFn.mockReset(); location.hash = ''; });

  it('consumes entities?kinds=owner and lists owners', async () => {
    entitiesFn.mockResolvedValue(listResp([
      { kind: 'owner', key: 'team-a', label: 'team-a', href: '/fleet/owners/team-a' },
      { kind: 'owner', key: 'team-b', label: 'team-b', href: '/fleet/owners/team-b' },
    ], { total: 2 }));
    const { target, component } = mountView(FleetOwnersView);
    await vi.waitFor(() => expect(target.querySelectorAll('.lv-item').length).toBe(2));
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['owner'], limit: 25 }));
    const a = target.querySelector('.lv-item a.entity-link') as HTMLAnchorElement;
    expect(a.getAttribute('href')).toBe('#/fleet/owners/team-a');
    unmount(component); document.body.removeChild(target);
  });

  it('search commits into the URL and pagination carries the offset', async () => {
    entitiesFn.mockResolvedValue(listResp([{ kind: 'owner', key: 'team-a', label: 'team-a', href: '/fleet/owners/team-a' }], { total: 60, nextOffset: 25 }));
    const { target, component } = mountView(FleetOwnersView);
    await vi.waitFor(() => expect(target.querySelector('.lv-item')).toBeTruthy());
    const search = target.querySelector('input[type="search"]') as HTMLInputElement;
    search.value = 'team'; search.dispatchEvent(new Event('input', { bubbles: true })); flushSync();
    (target.querySelector('form.lv-search') as HTMLFormElement).dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    expect(location.hash).toBe('#/fleet/owners?text=team');
    unmount(component); document.body.removeChild(target);
  });

  /**
   * A list of owners cannot answer the two questions a reader opens this page with. A
   * service nobody claims has no owner row; a service two teams claim has two, and is
   * counted under neither. So the page asks a SECOND question about the service
   * population, and every bucket it draws drills into exactly the population it counted.
   */
  it('summarizes ownership over the complete service population, not the owner page', async () => {
    respondByKind(listResp(owners(2), { total: 2 }));
    const { target, component } = mountView(FleetOwnersView);
    await vi.waitFor(() => expect(aggregateDrawn(target)).toBeTruthy());
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['service'], limit: 1 }));
    // The buckets are the backend's 40 services, not the 2 owner rows below them.
    expect(legend(target)).toEqual([
      ['One declared owner', '34', '#/fleet/services?ownership=consistent'],
      ['Revisions name different owners', '2', '#/fleet/services?ownership=conflicting'],
      ['No declared owner', '4', '#/fleet/services?ownership=unowned'],
    ]);
    expect(aggregateOf(target)?.querySelector('.dist-scope')?.textContent)
      .toBe('All 40 services in the snapshot, whatever this page is filtered or paged to.');
    unmount(component); document.body.removeChild(target);
  });

  /**
   * A ranking row counts the services CONSISTENTLY owned by that team, so its
   * destination has to say so. `ownerKey=team-a` alone also selects what team-a co-owns
   * with somebody else -- a longer list than the row the reader clicked.
   */
  it('ranks owners into the exact population each row counted', async () => {
    respondByKind(listResp(owners(2), { total: 2 }));
    const { target, component } = mountView(FleetOwnersView);
    await vi.waitFor(() => expect(aggregateDrawn(target)).toBeTruthy());
    const bars = Array.from(target.querySelectorAll('.ow-sum-grid .hbars'));
    expect(Array.from(bars[0].querySelectorAll('.hb-row')).map((n) => [
      n.querySelector('.hb-label')?.textContent, n.querySelector('.hb-value')?.textContent,
      n.querySelector('a')?.getAttribute('href'),
    ])).toEqual([
      ['team-a', '18 services', '#/fleet/services?ownerKey=team-a&ownership=consistent'],
      ['team-b', '12 services', '#/fleet/services?ownerKey=team-b&ownership=consistent'],
    ]);
    // And it says what it is not: 18 + 12 + 4 == the 34 consistent services, so the six
    // conflicting and unowned ones are in no row here.
    const note = bars[0].querySelector('.hb-scope')?.textContent || '';
    expect(note).toContain('Top 2 of 5 declared owners by service count.');
    expect(note).toContain('The remaining 3 of 5 owners account for 4 more services.');
    expect(note).toContain('Services with no owner, or whose revisions name different owners, appear in no row here.');
    unmount(component); document.body.removeChild(target);
  });

  /**
   * Searching and paging the owner INVENTORY must not redraw the summary above it into a
   * different population -- otherwise "ownership" would mean something new on every page.
   */
  it('keeps the ownership summary whole while the owner list is searched and paged', async () => {
    respondByKind(listResp(owners(1), { total: 60, offset: 25, nextOffset: 50 }));
    const { target, component } = mountView(FleetOwnersView, { text: 'team', offset: '25' });
    await vi.waitFor(() => expect(aggregateDrawn(target)).toBeTruthy());
    // The owner question carries the search and the offset; the ownership question does not.
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['owner'], text: 'team', offset: 25 }));
    expect(entitiesFn).toHaveBeenCalledWith({ kinds: ['service'], limit: 1 });
    expect(legend(target)[0]).toEqual(['One declared owner', '34', '#/fleet/services?ownership=consistent']);
    unmount(component); document.body.removeChild(target);
  });
});

/**
 * The Owners page asks TWO backend questions, so it has two fates. The ownership
 * summary is a separate request from the owner roster, and until now its failure had
 * nowhere to appear: the section was drawn only when its buckets summed above zero, so
 * a failed aggregate rendered as no section at all -- indistinguishable from a fleet
 * with nothing to summarize, and, on a refresh, indistinguishable from a current
 * picture that had simply stopped moving.
 */
describe('FleetOwnersView — the ownership summary has its own state', () => {
  beforeEach(() => { entitiesFn.mockReset(); location.hash = ''; });

  /** Roster succeeds, aggregate fails: two answers, two fates, both stated. */
  function rosterOkAggregateFails(err: Error) {
    entitiesFn.mockImplementation((o: { kinds?: string[] }) => (
      o?.kinds?.[0] === 'service' ? Promise.reject(err) : Promise.resolve(listResp(owners(2), { total: 2 }))));
  }

  it('says the summary is unavailable on first load, and keeps the owner roster usable', async () => {
    rosterOkAggregateFails(new Error('aggregate down'));
    const { target, component } = mountView(FleetOwnersView);
    await vi.waitFor(() => expect(target.querySelectorAll('.lv-item').length).toBe(2));
    const section = aggregateOf(target) as HTMLElement;
    // The heading stays: the question is still on the page, only the answer is missing.
    expect(section.querySelector('h2')?.textContent).toBe('Ownership across every service');
    expect(section.textContent).toContain('Can’t reach the Pacto backend');
    expect(section.textContent).toContain('aggregate down');
    // Not a picture drawn from nothing, and not silence either.
    expect(aggregateDrawn(target)).toBeFalsy();
    // The roster is untouched -- one failed question does not close the page.
    expect(target.querySelector('.lv-item a.entity-link')?.getAttribute('href')).toBe('#/fleet/owners/team-0');
    unmount(component); document.body.removeChild(target);
  });

  it('retries only the summary from its own error state', async () => {
    rosterOkAggregateFails(new Error('aggregate down'));
    const { target, component } = mountView(FleetOwnersView);
    await vi.waitFor(() => expect(aggregateOf(target)?.textContent).toContain('aggregate down'));
    respondByKind(listResp(owners(2), { total: 2 }));
    (aggregateOf(target)?.querySelector('button') as HTMLButtonElement).click();
    await vi.waitFor(() => expect(aggregateDrawn(target)).toBeTruthy());
    expect(legend(target)[0]).toEqual(['One declared owner', '34', '#/fleet/services?ownership=consistent']);
    unmount(component); document.body.removeChild(target);
  });

  it('keeps a previous summary through a failed refresh, and says the refresh failed', async () => {
    respondByKind(listResp(owners(2), { total: 2 }));
    const target = document.createElement('div');
    document.body.appendChild(target);
    const props = $state({ refreshTick: 0 });
    const component = mount(FleetOwnersView, { target, props });
    await vi.waitFor(() => expect(aggregateDrawn(target)).toBeTruthy());

    rosterOkAggregateFails(new Error('poll failed'));
    props.refreshTick = 1;
    flushSync();
    await vi.waitFor(() => expect(aggregateOf(target)?.querySelector('[data-testid="stale-refresh"]')).toBeTruthy());
    // Stale-while-revalidate is only honest if the page says the revalidation failed.
    expect(aggregateOf(target)?.querySelector('[data-testid="stale-refresh"]')?.textContent)
      .toContain('This ownership summary could not be refreshed');
    // And the last answer we did receive is still readable, rather than thrown away.
    expect(legend(target)[0]).toEqual(['One declared owner', '34', '#/fleet/services?ownership=consistent']);
    unmount(component); document.body.removeChild(target);
  });

  /**
   * The denominator is the backend's service count, never the sum of the buckets it
   * sent. If the two ever disagree, a bar drawn against its own sum reads as a complete
   * partition and hides the gap in whitespace; drawn against the authoritative count,
   * the gap is a visible slice.
   */
  it('draws ownership against the authoritative service count, so a gap is visible', async () => {
    respondByKind(listResp(owners(1), { total: 1 }), {
      matched: 10, services: 10,
      ownership: { consistent: 5, conflicting: 2, unowned: 1 },
      byOwner: [], otherOwners: 0, distinctOwners: 0,
    });
    const { target, component } = mountView(FleetOwnersView);
    await vi.waitFor(() => expect(aggregateDrawn(target)).toBeTruthy());
    const rows = Array.from(aggregateOf(target)?.querySelectorAll('.dist-item') ?? []).map((n) => [
      n.querySelector('.dist-label')?.textContent, n.querySelector('.dist-value')?.textContent,
      n.querySelector('.dist-pct')?.textContent,
    ]);
    // 5 + 2 + 1 == 8 of 10: the missing two are a bucket, not rounding.
    expect(rows).toEqual([
      ['One declared owner', '5', '(50% of 10)'],
      ['Revisions name different owners', '2', '(20% of 10)'],
      ['No declared owner', '1', '(10% of 10)'],
      ['Unclassified', '2', '(20% of 10)'],
    ]);
    unmount(component); document.body.removeChild(target);
  });
});

describe('FleetSourcesView (G)', () => {
  beforeEach(() => { entitiesFn.mockReset(); location.hash = ''; });

  it('consumes entities?kinds=source and lists sources with status', async () => {
    entitiesFn.mockResolvedValue(listResp([
      { kind: 'source', key: 'kubernetes', label: 'kubernetes', status: 'available', href: '/fleet/sources/kubernetes' },
    ], { total: 1 }));
    const { target, component } = mountView(FleetSourcesView);
    await vi.waitFor(() => expect(target.querySelector('.lv-item')).toBeTruthy());
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['source'], limit: 25 }));
    unmount(component); document.body.removeChild(target);
  });

  it('the health filter uses the backend sourceHealth param and lives in the URL', async () => {
    entitiesFn.mockResolvedValue(listResp([{ kind: 'source', key: 'kubernetes', label: 'kubernetes', href: '/fleet/sources/kubernetes' }], { total: 1 }));
    const { target, component } = mountView(FleetSourcesView);
    await vi.waitFor(() => expect(target.querySelector('.lv-item')).toBeTruthy());
    const sel = target.querySelector('select') as HTMLSelectElement;
    sel.value = 'unavailable'; sel.dispatchEvent(new Event('change', { bubbles: true }));
    expect(location.hash).toBe('#/fleet/sources?sourceHealth=unavailable');
    unmount(component); document.body.removeChild(target);
  });
});
