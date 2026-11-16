/**
 * The Metrics page shell (D12): the explainer when the host has no stats repository,
 * the error state when the read fails, the no-records sentence, and never a number it
 * does not have.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import { ApiError } from '../lib/api.ts';

const { statsFn, recordsFn } = vi.hoisted(() => ({ statsFn: vi.fn(), recordsFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { stats: (...a: unknown[]) => statsFn(...a), statsRecords: (...a: unknown[]) => recordsFn(...a) } };
});

// @ts-expect-error — Svelte component has no declaration file
import FleetMetricsView from './FleetMetricsView.svelte';

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
const answer = (extra: any = {}): any => ({
  asOf: '2026-11-15T10:00:00Z', window: { name: '30d' }, recordsRead: 0, runs: 0, distinctChanges: 0,
  completeness: 'empty', provenance: 'unsigned statistics', truncated: '', skipped: { count: 0 }, hidden: { present: false },
  environmentOrder: 'name', environments: [], services: [], servicesTotal: 0, buckets: [], limitations: [],
  environmentsBy: { owner: [], domain: [] }, leadTimeGroups: [], ...extra,
});

function mountView(props: Record<string, unknown> = {}) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(FleetMetricsView, { target, props: { capabilities: { fleet: true, stats: true }, refreshTick: 0, ...props } });
  return { target, done: () => { unmount(component); document.body.removeChild(target); } };
}
const body = (t: HTMLElement) => t.querySelector('.product-page') as HTMLElement;

beforeEach(() => { statsFn.mockReset(); recordsFn.mockReset(); });

describe('FleetMetricsView', () => {
  it('explains how to turn recording on, with no number, when the host has no stats repository', async () => {
    const { target, done } = mountView({ capabilities: { fleet: true, stats: false } });
    expect(target.querySelector('h1')?.textContent).toBe('Metrics');
    const off = target.querySelector('[data-testid="metrics-off"]') as HTMLElement;
    expect(off).not.toBeNull();
    expect(off.textContent).toContain('Metrics are off');
    expect(off.textContent).toContain('Pacto records run statistics in your own registry.');
    expect(off.textContent).toContain('--stats-registry');
    expect(off.querySelector('a[href="https://pacto.run/latest/measure-what-pacto-does/"]')?.textContent).toBe('How recording works');
    expect(body(target).textContent).not.toMatch(/\d/);
    expect(statsFn).not.toHaveBeenCalled();
    done();
  });

  it('shows the read error with a retry, never the empty state', async () => {
    statsFn.mockRejectedValueOnce(new ApiError(503, 'reading the stats repository failed: unauthorized'));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(target.querySelector('[role="alert"]')).not.toBeNull());
    expect(target.textContent).toContain('reading the stats repository failed: unauthorized');
    expect(target.querySelector('[data-testid="metrics-none"]')).toBeNull();
    statsFn.mockResolvedValueOnce(answer({ limitations: [{ code: 'NO_RUN_RECORDS', message: 'no run records in this window' }] }));
    (target.querySelector('[role="alert"] button') as HTMLButtonElement).click();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="metrics-none"]')).not.toBeNull());
    done();
  });

  it('says there are no run records, with the CI line, and asks for the URL window', async () => {
    statsFn.mockResolvedValue(answer({ limitations: [{ code: 'NO_RUN_RECORDS', message: 'no run records in this window: recording may be off for these jobs, or the window holds none' }] }));
    const { target, done } = mountView({ window: '7d', servicesOffset: '100' });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="metrics-none"]')).not.toBeNull());
    expect(statsFn).toHaveBeenCalledWith(expect.objectContaining({ window: '7d', servicesOffset: 100 }));
    const none = target.querySelector('[data-testid="metrics-none"]') as HTMLElement;
    expect(none.textContent).toContain('No run records in this window');
    expect(none.textContent).toContain('--stats-registry');
    done();
  });

  it('draws the coverage of a window that holds only coverage records, with no CI number', async () => {
    const cov = { services: 4, uncontracted: 0, links: { exact: 1, inferred: 0, ambiguous: 0, unresolved: 0 }, snapshotAsOf: '2026-11-15T09:00:00Z', completeness: 'complete',
      knowledge: { services: 4, described: 3, withGaps: 1, unknown: 0, notDeployed: 0 } };
    statsFn.mockResolvedValue(answer({ recordsRead: 1, completeness: 'complete', coverage: cov,
      buckets: [{ since: '2026-11-14T10:00:00Z', until: '2026-11-15T10:00:00Z', days: 1, coverage: cov }],
      limitations: [{ code: 'NO_CI_OR_DELIVERY_RUN_RECORDS', message: 'the window holds coverage records and no CI or delivery run records' }] }));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="metrics-content"]')).not.toBeNull());
    expect(target.querySelector('[data-testid="metrics-none"]')).toBeNull();
    expect(target.querySelector('[data-testid="metrics-coverage-trend"]')).not.toBeNull();
    expect(target.textContent).toContain('no CI or delivery run records');
    done();
  });
});

const ratio = (count: number, of: number) => (of ? { count, of } : { count: 0, of: 0, unknown: true });
const env = (environment: string, extra: Record<string, unknown> = {}) => ({
  environment, source: 'context', operations: 10, held: ratio(2, 10), failed: ratio(0, 10), rolledBack: ratio(0, 0),
  verificationFailed: ratio(1, 8), acceptedUnknown: 0, leadTime: { joined: 0, unjoined: 0, unknown: true }, upstream: [], ...extra,
});
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
const metrics = (extra: Record<string, unknown> = {}): any => ({
  changes: 20, breakingCaught: { distinct: 7, runs: 19 }, consumersProtected: { count: 12, unknown: 0 },
  gates: { clear: 30, blocked: 4, unverified: 0 }, environments: [env('staging'), env('production')], ...extra,
});
const coverage = (knowledge: Record<string, number> | undefined) => ({
  services: 10, uncontracted: 2, knowledge, links: { exact: 5, inferred: 2, ambiguous: 1, unresolved: 0 },
  snapshotAsOf: '2026-11-15T09:00:00Z', completeness: 'complete',
});
const populated = (extra: Record<string, unknown> = {}) => answer({
  recordsRead: 40, runs: 30, distinctChanges: 20, completeness: 'complete',
  window: { since: '2026-10-16T10:00:00Z', until: '2026-11-15T10:00:00Z' }, totals: metrics(),
  environmentOrder: 'stages',
  environments: [env('staging', { upstream: [] }), env('production', { upstream: ['staging'], leadTime: { p50Seconds: 3600, p90Seconds: 7200, joined: 2, unjoined: 0 } })],
  services: [
    { label: 'payments', href: '/fleet/services/payments', metrics: metrics() },
    { label: 'ghost', note: 'not in the current snapshot', metrics: metrics() },
    { label: 'twin', note: 'ambiguous: more than one service has this name', metrics: metrics() },
  ],
  servicesTotal: 60,
  buckets: [
    { since: '2026-10-16T10:00:00Z', until: '2026-10-18T10:00:00Z', days: 2, partial: true },
    { since: '2026-10-18T10:00:00Z', until: '2026-10-25T10:00:00Z', days: 7, metrics: metrics(),
      coverage: coverage({ services: 10, described: 6, withGaps: 1, unknown: 3, notDeployed: 2 }) },
  ],
  coverageNow: coverage({ services: 10, described: 6, withGaps: 1, unknown: 3, notDeployed: 2 }),
  ...extra,
});

describe('FleetMetricsView content', () => {
  it('keeps the answer order of environments, says what feeds them and links their counts', async () => {
    statsFn.mockResolvedValue(populated());
    const { target, done } = mountView();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="metrics-environments"]')).not.toBeNull());
    const env = target.querySelector('[data-testid="metrics-environments"]') as HTMLElement;
    expect(env.querySelector('h2')?.textContent).toBe('Environments, in the order their Stages promote');
    const rows = Array.from(env.querySelectorAll('[data-testid="metrics-env-row"]'));
    expect(rows.map((r) => r.querySelector('th a')?.textContent)).toEqual(['staging', 'production']);
    expect(rows[1].textContent).toContain('fed by staging');
    expect(rows[1].textContent).toContain('1h');
    const held = rows[0].querySelector('a[href*="metric=held"]') as HTMLAnchorElement;
    expect(held.getAttribute('href')).toBe('#/metrics?metric=held&environment=staging');
    expect(held.getAttribute('data-lists')).toBe('2');
    // A rate with nothing to take it over is unknown, never a link and never 0.
    expect(rows[0].querySelector('a[href*="metric=rolledBack"]')).toBeNull();
    expect(rows[0].querySelector('td[data-label="Rolled back"]')?.textContent?.trim()).toBe('unknown');
    done();
  });

  it('titles the environments by name when no Stage graph orders them', async () => {
    statsFn.mockResolvedValue(populated({ environmentOrder: 'name' }));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="metrics-environments"] h2')).not.toBeNull());
    expect(target.querySelector('[data-testid="metrics-environments"] h2')?.textContent).toBe('Environments, listed by name: no Stage graph to order by');
    done();
  });

  it('prints unresolved and ambiguous services as text, and pages services in the URL', async () => {
    statsFn.mockResolvedValue(populated());
    const { target, done } = mountView();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="metrics-services"] tbody')).not.toBeNull());
    const rows = Array.from(target.querySelectorAll('[data-testid="metrics-service-row"]'));
    expect(rows[0].querySelector('a')?.getAttribute('href')).toBe('#/fleet/services/payments');
    expect(rows[1].querySelector('a')).toBeNull();
    expect(rows[1].textContent).toContain('not in the current snapshot');
    expect(rows[2].querySelector('a')).toBeNull();
    expect(rows[2].textContent).toContain('ambiguous: more than one service has this name');
    const pager = target.querySelector('[data-testid="metrics-services-pager"]') as HTMLElement;
    expect(pager.getAttribute('data-view-total')).toBe('60');
    expect(pager.querySelector('[data-testid="metrics-services-pager-next"]')?.getAttribute('href')).toBe('#/metrics?servicesOffset=25');
    done();
  });

  it('keeps the coverage trend for a reader who sees every service, even when records are hidden', async () => {
    statsFn.mockResolvedValue(populated({ hidden: { present: true }, completeness: 'partial' }));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="metrics-hidden"]')).not.toBeNull());
    expect(target.querySelector('[data-testid="metrics-coverage-trend"]')).not.toBeNull();
    expect(target.querySelector('[data-testid="metrics-coverage-restricted"]')).toBeNull();
    done();
  });

  it('lists the records behind a number with their links', async () => {
    statsFn.mockResolvedValue(populated());
    recordsFn.mockResolvedValue({
      asOf: '2026-11-15T10:00:00Z', window: { since: '2026-10-16T10:00:00Z', until: '2026-11-15T10:00:00Z' }, provenance: 'unsigned statistics',
      recordsRead: 40, skipped: { malformed: 0, oversized: 0, unreadable: 0 }, truncated: '', hidden: { present: false }, completeness: 'complete', limitations: [],
      total: 30,
      items: [
        { record: { id: 'a', kind: 'impact', service: 'payments', oldRevision: 'sha256:aaa', newRevision: 'sha256:bbb', gate: { verdict: 'blocked' }, recordedAt: '2026-11-10T10:00:00Z', version: '1', pactoVersion: '4' },
          service: { label: 'payments', href: '/fleet/services/payments' }, oldRevision: { label: 'sha256:aaa', href: '/fleet/revisions/payments%40a' },
          change: { label: 'change analysis', href: '/fleet/changes/payments?old=a&new=b' }, note: 'not in the current snapshot' },
        { record: { id: 'b', kind: 'operation', type: 'promote', outcome: 'Held', service: 'payments', recordedAt: '2026-11-09T10:00:00Z', version: '1', pactoVersion: '4', environment: 'prod' },
          stage: { namespace: 'team-a', name: 'prod-eu', environment: 'prod' } },
      ],
    });
    const { target, done } = mountView({ metric: 'blocked', offset: '0' });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="metrics-record"]')).not.toBeNull());
    expect(recordsFn).toHaveBeenCalledWith(expect.objectContaining({ metric: 'blocked' }));
    const panel = target.querySelector('[data-testid="metrics-records"]') as HTMLElement;
    expect(panel.querySelector('h2')?.textContent).toBe('Records behind gates blocked');
    const [gate, op] = Array.from(panel.querySelectorAll('[data-testid="metrics-record"]'));
    expect(gate.querySelector('a[href="#/fleet/services/payments"]')).not.toBeNull();
    expect(gate.querySelector('a[href="#/fleet/revisions/payments%40a"]')?.textContent).toBe('sha256:aaa');
    // The new revision the snapshot does not hold is text.
    expect(Array.from(gate.querySelectorAll('a')).some((a) => a.textContent === 'sha256:bbb')).toBe(false);
    expect(gate.textContent).toContain('sha256:bbb');
    expect(gate.querySelector('a[href^="#/fleet/changes/payments"]')?.textContent).toBe('Change analysis');
    expect(op.querySelector('a[href*="/delivery/history"]')?.textContent).toBe('prod-eu');
    expect(op.querySelector('a[href*="/delivery/environments"]')?.textContent).toBe('prod');
    expect(panel.querySelector('[data-testid="metrics-records-pager"]')?.getAttribute('data-view-total')).toBe('30');
    // The records panel comes before the services table, so its total is the first one.
    expect(target.querySelector('[data-view-total]')?.getAttribute('data-testid')).toBe('metrics-records-pager');
    done();
  });

  it('keeps the numbers and the open records on screen through a refresh, and says when one fails', async () => {
    statsFn.mockResolvedValue(populated());
    recordsFn.mockResolvedValue({ asOf: '2026-11-15T10:00:00Z', window: { since: '2026-10-16T10:00:00Z', until: '2026-11-15T10:00:00Z' }, provenance: 'unsigned statistics',
      recordsRead: 1, skipped: { malformed: 0, oversized: 0, unreadable: 0 }, truncated: '', hidden: { present: false }, completeness: 'complete', limitations: [], total: 1,
      items: [{ record: { id: 'a', kind: 'impact', service: 'payments', gate: { verdict: 'blocked' }, recordedAt: '2026-11-10T10:00:00Z', version: '1', pactoVersion: '4' } }] });
    const props = $state({ capabilities: { fleet: true, stats: true }, refreshTick: 0, metric: 'blocked' });
    const target = document.createElement('div');
    document.body.appendChild(target);
    const component = mount(FleetMetricsView, { target, props });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="metrics-record"]')).not.toBeNull());
    let release = () => {};
    recordsFn.mockReturnValueOnce(new Promise((r) => { release = () => r(undefined); }));
    statsFn.mockRejectedValueOnce(new ApiError(503, 'registry down'));
    props.refreshTick = 1;
    flushSync();
    // The poll in flight never collapses the records into a skeleton.
    expect(target.querySelector('[data-testid="metrics-record"]')).not.toBeNull();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="metrics-refresh-error"]')).not.toBeNull());
    expect(target.querySelector('[data-testid="metrics-content"]')).not.toBeNull();
    expect(target.querySelector('[data-testid="metrics-error"]')).toBeNull();
    release();
    unmount(component);
    document.body.removeChild(target);
  });

  it('keeps the records panel open when the window changes', async () => {
    statsFn.mockResolvedValue(populated());
    recordsFn.mockResolvedValue({ total: 0, items: [], limitations: [], hidden: { present: false }, skipped: {}, completeness: 'complete', asOf: '2026-11-15T10:00:00Z', provenance: 'unsigned statistics' });
    const { target, done } = mountView({ metric: 'failed', environment: 'prod-eu' });
    await vi.waitFor(() => expect(target.querySelector('select')).not.toBeNull());
    const select = target.querySelector('select') as HTMLSelectElement;
    select.value = '90d';
    select.dispatchEvent(new Event('change', { bubbles: true }));
    expect(location.hash).toBe('#/metrics?window=90d&metric=failed&environment=prod-eu');
    location.hash = '';
    done();
  });

  it('qualifies a lower-bound consumers count and draws a service with no CI record as unknown', async () => {
    statsFn.mockResolvedValue(populated({ services: [
      { label: 'payments', href: '/fleet/services/payments', metrics: metrics({ consumersProtected: { count: 3, unknown: 5, partial: 2 } }) },
      { label: 'delivered', href: '/fleet/services/delivered', metrics: metrics({ changes: 0 }) },
    ] }));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="metrics-service-row"]')).not.toBeNull());
    const [qualified, noCi] = Array.from(target.querySelectorAll('[data-testid="metrics-service-row"]'));
    const cell = qualified.querySelector('td[data-label="Consumers protected"]') as HTMLElement;
    expect(cell.textContent?.trim()).toBe('3 or more, 5 unknown');
    expect(cell.querySelector('.hatch')).not.toBeNull();
    expect(noCi.querySelector('td[data-label="Breaking caught"]')?.textContent?.trim()).toBe('no CI records');
    done();
  });

});


const tiles = (t: HTMLElement) => t.querySelector('section.mx-tiles') as HTMLElement;
const card = (t: HTMLElement, id: string) => t.querySelector(`[data-testid="metrics-${id}"]`) as HTMLElement;
const restricted = () => populated({ hidden: { present: true }, completeness: 'partial',
  limitations: [{ code: 'COVERAGE_OUTSIDE_YOUR_VIEW', message: 'coverage records count every service' }],
  buckets: populated().buckets.map((b: Record<string, unknown>) => ({ ...b, coverage: undefined })) });

describe('FleetMetricsView headline and weekly charts', () => {
  it('reads the chosen window again and links every tile number to its records', async () => {
    statsFn.mockResolvedValue(populated());
    const { target, done } = mountView({ window: '7d' });
    await vi.waitFor(() => expect(tiles(target)).not.toBeNull());
    expect(statsFn).toHaveBeenLastCalledWith(expect.objectContaining({ window: '7d' }));
    const section = tiles(target);
    expect(section.hasAttribute('data-fig')).toBe(true);
    expect(section.getAttribute('aria-label')).toBe('Headline numbers');
    expect(section.querySelectorAll('.mx-tile')).toHaveLength(3);
    const breaking = section.querySelector('a[href*="metric=breaking"]') as HTMLAnchorElement;
    expect(breaking.textContent).toBe('7');
    expect(breaking.getAttribute('data-lists')).toBe('7');
    expect(breaking.getAttribute('href')).toBe('#/metrics?window=7d&metric=breaking');
    const blocked = section.querySelector('a[href*="metric=blocked"]') as HTMLAnchorElement;
    expect(blocked.textContent).toBe('4');
    expect(blocked.getAttribute('data-lists')).toBe('4');
    expect(section.textContent).toContain('blocked of 34');
    done();
  });

  it('reads window totals, never a sum of the buckets', async () => {
    // The same 7 changes ran in two weeks: each week counts them, the window counts them once.
    const b = populated().buckets;
    statsFn.mockResolvedValue(populated({ buckets: [b[0], b[1], { ...b[1], since: '2026-10-25T10:00:00Z', until: '2026-11-01T10:00:00Z' }] }));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(tiles(target)).not.toBeNull());
    expect(tiles(target).querySelector('a[href*="metric=breaking"]')?.textContent).toBe('7');
    expect(card(target, 'stopped').textContent).toContain('A change with runs in two weeks counts in each');
    done();
  });

  it('qualifies a lower-bound consumers protected count in the tile', async () => {
    statsFn.mockResolvedValue(populated({ totals: metrics({ consumersProtected: { count: 3, unknown: 5, partial: 2 } }) }));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(tiles(target)).not.toBeNull());
    const sub = tiles(target).querySelector('.mx-tile .mx-tile-sub') as HTMLElement;
    expect(sub.textContent).toContain('3 or more, 5 unknown');
    done();
  });

  it('says on one provenance line what was read, skipped and cut, and puts the main limitation on the note', async () => {
    statsFn.mockResolvedValue(populated({ completeness: 'partial', skipped: { malformed: 2, oversized: 1, unreadable: 0 }, truncated: 'more than 1000 run records',
      limitations: [{ code: 'RUN_RECORDS_SKIPPED', message: '3 run records could not be read' },
        { code: 'LEAD_TIME_UNJOINED', message: '2 promotions have no publish record with their digest, so their lead time is unknown.' }] }));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(tiles(target)).not.toBeNull());
    const strip = target.querySelector('[data-testid="metrics-prov"] [data-testid="stats-strip"]') as HTMLElement;
    expect(strip.textContent).toContain('3 skipped');
    expect(strip.textContent).toContain('cut at more than 1000 run records');
    expect(strip.textContent).toContain('Partial');
    expect(strip.textContent).toContain('unsigned statistics');
    const note = target.querySelector('[data-testid="metrics-note"]') as HTMLElement;
    expect(note.textContent).toContain('so their lead time is unknown');
    expect(note.querySelector('.hatch')).not.toBeNull();
    expect(target.querySelector('[data-testid="metrics-limitations"]')?.textContent).toContain('3 run records could not be read');
    done();
  });

  it('draws the gates bar with icons, labels and the dot texture for unverified', async () => {
    const gates = { gates: { clear: 30, blocked: 4, unverified: 2 } };
    const b = populated().buckets;
    statsFn.mockResolvedValue(populated({ totals: metrics(gates), buckets: [b[0], { ...b[1], metrics: metrics(gates) }] }));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(tiles(target)).not.toBeNull());
    const tile = tiles(target).querySelectorAll('.mx-tile')[1] as HTMLElement;
    expect(tile.querySelector('.dist-seg.dots')).not.toBeNull();
    expect(tile.querySelector('.dist-seg.hatch')).toBeNull();
    expect(Array.from(tile.querySelectorAll('.dist-icon')).map((i) => i.textContent)).toEqual(['✓', '✕', '?']);
    expect(Array.from(tile.querySelectorAll('.dist-label')).map((i) => i.textContent)).toEqual(['Clear', 'Blocked', 'Unverified']);
    // The stacked weekly columns use the same texture.
    expect(card(target, 'gates').querySelector('path.cc-col[data-series="unverified"]')).not.toBeNull();
    done();
  });

  it('names the lead time basis: chain-end promotions, p90 and its sparkline', async () => {
    const group = { key: 'chain-end', label: 'chain end', environments: 1, leadTime: { p50Seconds: 3600, p90Seconds: 7200, joined: 2, unjoined: 0 },
      buckets: [{ joined: 0, unjoined: 0, unknown: true }, { p50Seconds: 3600, p90Seconds: 7200, joined: 2, unjoined: 0 }] };
    const staged = [env('staging', { depth: 0 }), env('production', { depth: 1, upstream: ['staging'], parents: ['staging'], chainEnd: true })];
    statsFn.mockResolvedValue(populated({ leadTimeGroups: [group], environments: staged }));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(tiles(target)).not.toBeNull());
    const tile = tiles(target).querySelectorAll('.mx-tile')[2] as HTMLElement;
    expect(tile.querySelector('.mx-tile-label')?.textContent).toBe('Lead time to production');
    expect(tile.querySelector('.mx-tile-value')?.textContent?.trim()).toBe('1h');
    expect(tile.querySelector('.mx-tile-sub')?.textContent).toBe('p50 of 2 promotions into 1 environment at the end of their promotion chain, p90 2h');
    expect(tile.querySelector('.lc-line')).not.toBeNull();
    expect(tile.querySelector('.lc-unknown')).not.toBeNull();
    done();
  });

  it('says the lead time is unknown, never 0, when no promotion joined', async () => {
    statsFn.mockResolvedValue(populated());
    const { target, done } = mountView();
    await vi.waitFor(() => expect(tiles(target)).not.toBeNull());
    const tile = tiles(target).querySelectorAll('.mx-tile')[2] as HTMLElement;
    expect(tile.querySelector('.mx-tile-label')?.textContent).toBe('Lead time to any environment');
    expect(tile.querySelector('.mx-tile-value')?.textContent?.trim()).toBe('unknown');
    expect(tile.querySelector('.mx-tile-value .hatch')).not.toBeNull();
    done();
  });

  it('draws no 0 for a window whose every bucket is empty', async () => {
    statsFn.mockResolvedValue(populated({ buckets: populated().buckets.map((b: Record<string, unknown>) => ({ ...b, metrics: undefined, coverage: undefined })) }));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(card(target, 'stopped')).not.toBeNull());
    for (const id of ['stopped', 'gates']) {
      const c = card(target, id);
      expect(c.querySelectorAll('.cc-plate').length).toBeGreaterThan(0);
      for (const p of Array.from(c.querySelectorAll('.cc-plate'))) expect(p.textContent).toBe('no records');
      expect(c.querySelector('.cc-col, .cc-stub, .cc-val')).toBeNull();
    }
    // The sparkline is hatched too, never a 0 column.
    expect(tiles(target).querySelector('.cc-col, .cc-stub')).toBeNull();
    expect(tiles(target).querySelectorAll('.cc-unknown').length).toBe(2);
    done();
  });

  it('says a bucket with no diff or impact run has no CI records', async () => {
    const b = populated().buckets;
    statsFn.mockResolvedValue(populated({ buckets: [{ ...b[1], metrics: metrics({ changes: 0 }) }] }));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(card(target, 'stopped')).not.toBeNull());
    expect(card(target, 'stopped').querySelector('.cc-plate')?.textContent).toBe('no CI records');
    expect(card(target, 'gates').querySelector('.cc-plate')?.textContent).toBe('no CI records');
    done();
  });

  it('shows each weekly chart as a table, with unknown cells saying why', async () => {
    statsFn.mockResolvedValue(populated());
    const { target, done } = mountView();
    await vi.waitFor(() => expect(card(target, 'stopped')).not.toBeNull());
    for (const id of ['stopped', 'gates', 'coverage']) {
      const c = card(target, id);
      const btn = c.querySelector('button.cc-toggle') as HTMLButtonElement;
      expect(btn.getAttribute('aria-pressed')).toBe('false');
      btn.click();
      flushSync();
      expect(btn.getAttribute('aria-pressed')).toBe('true');
      expect(btn.textContent?.trim()).toBe('Show as chart');
      const first = c.querySelector('table tbody tr') as HTMLElement;
      expect(first.querySelector('th')?.textContent).toBe('16 to 17 Oct (2 days)');
      expect(first.querySelector('td.cc-unk')?.textContent).toBe('no records');
    }
    done();
  });

  it('draws coverage over time with both lines, a legend and the links of now', async () => {
    statsFn.mockResolvedValue(populated());
    const { target, done } = mountView();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="metrics-coverage-trend"]')).not.toBeNull());
    const c = card(target, 'coverage');
    expect(c.querySelector('h2')?.textContent).toBe('Coverage over time');
    expect(c.querySelectorAll('.lc-line')).toHaveLength(2);
    expect(c.textContent).toContain('Services observed');
    expect(c.textContent).toContain('Contracts described');
    expect(c.textContent).toContain('Pacto cannot judge');
    expect(c.textContent).toContain('5 exact, 2 inferred, 1 ambiguous and 0 unresolved');
    expect(target.querySelector('[data-testid="metrics-coverage-restricted"]')).toBeNull();
    done();
  });

  it('tells a restricted reader records are hidden and shows coverage now only, with the note', async () => {
    statsFn.mockResolvedValue(restricted());
    const { target, done } = mountView();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="metrics-hidden"]')).not.toBeNull());
    expect(target.querySelector('[data-testid="metrics-hidden"]')?.textContent).toBe('Some run records name services outside your view and are left out of every number.');
    expect(target.querySelector('[data-testid="metrics-coverage-trend"]')).toBeNull();
    expect(card(target, 'coverage').querySelector('button.cc-toggle')).toBeNull();
    expect(target.querySelector('[data-testid="metrics-coverage-restricted"]')?.textContent).toContain('shown to people who can read every service');
    // 10 services, 3 unreadable, 2 not deployed: 5 deployed and 3 unknown beside it.
    const rows = Array.from(target.querySelectorAll('[data-testid="metrics-coverage-now"] .hb-row'));
    const row = (label: string) => rows.find((r) => r.querySelector('.hb-label')?.textContent === label);
    expect(row('Deployed')?.querySelector('.hb-value')?.textContent).toBe('5');
    expect(row('Unknown')?.querySelector('.hb-fill')?.classList.contains('hatch')).toBe(true);
    expect(target.querySelector('[data-testid="stats-strip"]')?.textContent).toContain('Partial');
    done();
  });

  it('prints unknown when a restricted reader\'s coverage snapshot carries no knowledge tally', async () => {
    statsFn.mockResolvedValue({ ...restricted(), coverageNow: coverage(undefined) });
    const { target, done } = mountView();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="metrics-coverage-now"]')).not.toBeNull());
    expect(target.querySelector('[data-testid="metrics-coverage-now"]')?.textContent).toContain('unknown');
    done();
  });

  it('mounts the page\'s one chart tooltip', async () => {
    statsFn.mockResolvedValue(populated());
    const { target, done } = mountView();
    await vi.waitFor(() => expect(tiles(target)).not.toBeNull());
    expect(target.querySelectorAll('#chart-tip')).toHaveLength(1);
    done();
  });
});

describe('FleetMetricsView zero that another limitation explains', () => {
  for (const [name, limitation, extra] of [
    ['records outside the view', { code: 'OUTSIDE_YOUR_VIEW', message: 'some run records name services outside your view' }, { hidden: { present: true } }],
    ['skipped records', { code: 'RUN_RECORDS_SKIPPED', message: '3 run records could not be read' }, {}],
    ['a cut read', { code: 'RUN_RECORDS_TRUNCATED', message: 'the read was cut at more than 1000 run records' }, { truncated: 'more than 1000 run records' }],
  ] as const) {
    it(`says ${name}, never that recording may be off`, async () => {
      statsFn.mockResolvedValue(answer({ completeness: 'partial', ...extra,
        limitations: [limitation, { code: 'NO_RUN_RECORDS', message: 'no run record you can count in this window' }] }));
      const { target, done } = mountView();
      await vi.waitFor(() => expect(target.querySelector('[data-testid="metrics-content"]')).not.toBeNull());
      expect(target.querySelector('[data-testid="metrics-none"]')).toBeNull();
      expect(target.textContent).toContain(limitation.message);
      expect(target.textContent).toContain('no run record you can count in this window');
      expect(target.textContent).not.toContain('recording may be off');
      expect(target.querySelector('[data-testid="stats-strip"]')?.textContent).toContain('Partial');
      if ('hidden' in extra) expect(target.querySelector('[data-testid="metrics-hidden"]')).not.toBeNull();
      done();
    });
  }
});
