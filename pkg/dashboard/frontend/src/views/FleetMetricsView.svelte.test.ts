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
  environments: [env('staging', { upstream: [], depth: 0 }), env('production', { upstream: ['staging'], parents: ['staging'], depth: 1, chainEnd: true, leadTime: { p50Seconds: 3600, p90Seconds: 7200, joined: 2, unjoined: 0 } })],
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
  it('draws one panel per environment in answer order, says what feeds them and links their counts in the table', async () => {
    statsFn.mockResolvedValue(populated());
    const { target, done } = mountView();
    await vi.waitFor(() => expect(card(target, 'health')).not.toBeNull());
    const health = card(target, 'health');
    const panels = Array.from(health.querySelectorAll('[data-testid="metrics-env-panel"]'));
    expect(panels.map((p) => p.querySelector('h3')?.textContent)).toEqual(['staging', 'production']);
    expect(panels[1].textContent).toContain('fed by staging');
    // A rate with nothing to take it over is unknown, hatched, never 0.
    const rolled = Array.from(panels[0].querySelectorAll('.hb-row')).find((r) => r.textContent?.includes('Rolled back')) as HTMLElement;
    expect(rolled.querySelector('.hb-value')?.textContent).toBe('unknown');
    expect(rolled.querySelector('.hb-fill.hatch')).not.toBeNull();
    (health.querySelector('button.cc-toggle') as HTMLButtonElement).click();
    flushSync();
    const held = health.querySelector('table a[href*="metric=held"]') as HTMLAnchorElement;
    expect(held.getAttribute('href')).toBe('#/metrics?metric=held&environment=staging');
    expect(held.getAttribute('data-lists')).toBe('2');
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

  // The fleet's two figures (509-S12) join the headline row, from the window's newest
  // coverage record, with a trend from the weekly buckets.
  const withFigures = (cov: Record<string, unknown>, pct: number | undefined) => ({
    ...cov,
    match: { servicesMatched: 6, servicesExpectedOnly: 1, servicesObservedOnly: 1, servicesInsufficient: 2, dependenciesMatched: 2, dependenciesExpectedOnly: 0, dependenciesObservedOnly: 0, dependenciesInsufficient: 0 },
    contracts: { services: 10, judged: 9, unknown: 1, fullyDescribed: 4, ...(pct == null ? {} : { percentComplete: pct }) },
  });
  const k = { services: 10, described: 6, withGaps: 1, unknown: 3, notDeployed: 2 };

  it('appends the match and completeness tiles to the headline row, with their trend', async () => {
    const base = populated();
    statsFn.mockResolvedValue(populated({
      buckets: [
        { ...base.buckets[0], coverage: withFigures(coverage(k), 50) },
        base.buckets[1],
        { since: '2026-10-25T10:00:00Z', until: '2026-11-01T10:00:00Z', days: 7, coverage: withFigures(coverage(k), 60) },
      ],
      coverageNow: withFigures(coverage(k), 60),
    }));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(tiles(target)?.querySelector('[data-testid="figure-match"]')).toBeTruthy());
    const t = tiles(target);
    expect(t.querySelector('[data-testid="figure-match-value"]')?.textContent).toBe('80%of what Pacto can observe');
    expect(t.querySelector('[data-testid="figure-match-sub"]')?.textContent).toBe('83% observed coverage · unknown never counts as matched');
    expect(t.querySelector('[data-testid="figure-completeness-value"]')?.textContent).toBe('60%4 of 9 judged services fully described, 1 not judged');
    expect(t.querySelector('[data-testid="figure-match-trend"]')).not.toBeNull();
    expect(t.querySelector('[data-testid="figure-completeness-trend"]')).not.toBeNull();
    done();
  });

  it('says unknown for a figure a bucket or the window has no record of, never 0', async () => {
    statsFn.mockResolvedValue(populated({ coverageNow: withFigures(coverage(k), undefined) }));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(tiles(target)?.querySelector('[data-testid="figure-completeness"]')).toBeTruthy());
    expect(tiles(target).querySelector('[data-testid="figure-completeness-value"]')?.textContent).toMatch(/^unknown/);
    // One of two buckets has no record: no line is drawn over a single point.
    expect(tiles(target).querySelector('[data-testid="figure-completeness-trend"]')).toBeNull();
    done();
  });

  it('tells a restricted reader why the figures have no trend', async () => {
    statsFn.mockResolvedValue(restricted());
    const { target, done } = mountView();
    await vi.waitFor(() => expect(tiles(target)?.querySelector('[data-testid="figure-match-trend-note"]')).toBeTruthy());
    expect(tiles(target).querySelector('[data-testid="figure-match-trend-note"]')?.textContent).toBe('The trend counts every service, so only a reader who can see every service sees it.');
    done();
  });

  it('says how many operation records from other clusters are not shown, in the hidden note', async () => {
    statsFn.mockResolvedValue(populated({ hidden: { present: false, otherClusters: 12 }, completeness: 'partial',
      limitations: [{ code: 'OTHER_CLUSTERS_NOT_SHOWN', message: '12 operation records from other clusters are not shown' }] }));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="metrics-hidden"]')).not.toBeNull());
    expect(target.querySelector('[data-testid="metrics-hidden"]')?.textContent).toBe('12 operation records from other clusters are not shown.');
    expect(target.querySelector('[data-testid="metrics-note"]')?.textContent ?? '').not.toContain('other clusters');
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

// A fleet of n environments for the scale tests: `depthOf` places each, its parent is the
// environment one depth up in the same column, and `extra` overrides its numbers.
type Extra = Record<string, unknown>;
const fleet = (n: number, depthOf: (i: number) => number | undefined, extra: (i: number) => Extra = () => ({})) => {
  const name = (i: number) => `env-${String(i).padStart(3, '0')}`;
  return Array.from({ length: n }, (_, i) => {
    const d = depthOf(i);
    const p = d ? Array.from({ length: i }, (_, j) => j).reverse().find((j) => depthOf(j) === d - 1) : undefined;
    return env(name(i), { ...(d == null ? {} : { depth: d }), ...(p == null ? {} : { parents: [name(p)], upstream: [name(p)] }), ...extra(i) });
  });
};
const at = (envs: Extra[], extra: Extra = {}) => populated({ environments: envs, totals: metrics({ environments: envs }), ...extra });
const groupButtons = (t: HTMLElement) => Array.from(card(t, 'health').querySelectorAll('tr.rung button')) as HTMLButtonElement[];

describe('FleetMetricsView delivery health and lead time at scale', () => {
  beforeEach(() => { location.hash = ''; });

  it('draws 5 environments as panels on one shared scale, so an over-50% rate draws longer than a 50% one', async () => {
    const envs = fleet(5, (i) => (i < 2 ? i : 2), (i) => (i === 3 ? { failed: ratio(2, 3), held: ratio(1, 2) } : {}));
    statsFn.mockResolvedValue(at(envs));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(card(target, 'health')).not.toBeNull());
    const health = card(target, 'health');
    expect(health.querySelectorAll('[data-testid="metrics-env-panel"]')).toHaveLength(5);
    expect(health.querySelector('.hm-scroll')).toBeNull();
    expect(target.querySelector('[data-testid="metrics-depths"]')).toBeNull();
    const panel = health.querySelectorAll('[data-testid="metrics-env-panel"]')[3];
    const width = (label: string) => parseFloat((Array.from(panel.querySelectorAll('.hb-row')).find((r) => r.textContent?.includes(label))!.querySelector('.hb-fill') as HTMLElement).style.width);
    expect(width('Failed')).toBeGreaterThan(width('Held'));
    expect(health.textContent).toContain('a full bar is 80%');
    expect(health.querySelector('.mx-ladder')).not.toBeNull();
    done();
  });

  it('draws 50 environments as the heatmap with every group open, in promotion order', async () => {
    statsFn.mockResolvedValue(at(fleet(50, (i) => Math.floor(i / 15))));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(card(target, 'health').querySelector('.hm-scroll')).not.toBeNull());
    expect(groupButtons(target).map((b) => b.getAttribute('aria-expanded'))).toEqual(['true', 'true', 'true', 'true']);
    expect(card(target, 'health').querySelector('.nm')?.getAttribute('title')).toBe('env-000');
    expect((card(target, 'health').querySelector('select[aria-label="Sort"], .mx-ctl select:last-of-type') as HTMLSelectElement).value).toBe('order');
    done();
  });

  it('draws 60 environments as the heatmap, groups collapsed as the approved design draws them, and says 60 of 60', async () => {
    statsFn.mockResolvedValue(at(fleet(60, (i) => Math.floor(i / 15))));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(card(target, 'health').querySelector('.hm-scroll')).not.toBeNull());
    expect(groupButtons(target).map((b) => b.getAttribute('aria-expanded'))).toEqual(['false', 'false', 'false', 'false']);
    expect(groupButtons(target).map((b) => b.querySelector('bdi')?.textContent)).toEqual(['entry', 'hop 2', 'hop 3', 'hop 4']);
    expect(card(target, 'health').querySelector('[data-testid="metrics-env-count"]')?.textContent).toBe('60 of 60 environments');
    groupButtons(target)[0].click();
    flushSync();
    // Long names are elided with the whole name in title.
    expect(card(target, 'health').querySelector('.nm')?.getAttribute('title')).toMatch(/^env-0\d\d$/);
    expect(card(target, 'lead').querySelector('.rd-name')?.getAttribute('title')).toMatch(/^env-\d{3}$/);
    done();
  });

  it('collapses 200 environments into pooled group rows with the depth filter and group-by', async () => {
    const envs = fleet(200, (i) => i % 5);
    const owner = (key: string, es: Extra[]) => ({ key, label: key, environments: es });
    statsFn.mockResolvedValue(at(envs, { environmentsBy: { owner: [owner('payments', envs.slice(0, 100)), owner('checkout', envs.slice(100))], domain: [],
      attribution: { asOf: '2026-11-15T09:00:00Z', partial: true } } }));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(card(target, 'health').querySelector('.hm-scroll')).not.toBeNull());
    expect(groupButtons(target).every((b) => b.getAttribute('aria-expanded') === 'false')).toBe(true);
    const pooled = card(target, 'health').querySelector('tr.rung .cell .visually-hidden') as HTMLElement;
    expect(pooled.textContent).toBe('Held in entry: 80 of 400 promotions, 20%');
    expect(card(target, 'health').textContent).toContain('200 of 200 environments');
    expect(card(target, 'health').textContent).toContain('Group rows show the pooled rate of the group');
    expect(target.querySelector('[data-testid="metrics-depths"]')?.getAttribute('aria-label')).toBe('Filter by promotion depth');
    expect(Array.from(card(target, 'health').querySelectorAll('select')).some((s) => Array.from(s.options).some((o) => o.value === 'owner'))).toBe(true);
    // An opened group shows its 10 worst, then pages by 20.
    groupButtons(target)[0].click();
    flushSync();
    expect(card(target, 'health').querySelectorAll('tbody tr:not(.rung):not(.more)')).toHaveLength(10);
    (Array.from(card(target, 'health').querySelectorAll('tr.more button')).find((b) => b.textContent === 'Show all 40') as HTMLButtonElement).click();
    flushSync();
    expect(card(target, 'health').querySelectorAll('tbody tr:not(.rung):not(.more)')).toHaveLength(20);
    expect(card(target, 'health').querySelector('tr.more')?.textContent).toContain('1 to 20 of 40');
    done();
  });

  it('filters delivery health, lead time and the lead tile by the depth chip, and keeps it in the URL', async () => {
    const envs = fleet(20, (i) => i % 4);
    const groups = [{ key: 'chain-end', label: 'chain end', environments: 5, leadTime: { p50Seconds: 7200, p90Seconds: 9000, joined: 4, unjoined: 0 }, buckets: [] },
      { key: 'depth-2', label: 'hop 3', depth: 2, environments: 5, leadTime: { p50Seconds: 3600, p90Seconds: 5400, joined: 3, unjoined: 0 }, buckets: [] }];
    statsFn.mockResolvedValue(at(envs, { leadTimeGroups: groups }));
    const first = mountView();
    await vi.waitFor(() => expect(first.target.querySelector('[data-testid="metrics-depths"]')).not.toBeNull());
    const chip = Array.from(first.target.querySelectorAll('[data-testid="metrics-depths"] button')).find((b) => b.textContent?.startsWith('hop 3')) as HTMLButtonElement;
    chip.click();
    expect(location.hash).toBe('#/metrics?depth=2');
    first.done();
    const { target, done } = mountView({ depth: '2' });
    await vi.waitFor(() => expect(card(target, 'health').querySelector('.hm-scroll')).not.toBeNull());
    expect(target.querySelector('[data-testid="metrics-depths"] button[aria-pressed="true"]')?.textContent).toContain('hop 3');
    expect(groupButtons(target).map((b) => b.querySelector('bdi')?.textContent)).toEqual(['hop 3']);
    expect(card(target, 'health').querySelector('[data-testid="metrics-env-count"]')?.textContent).toBe('5 of 20 environments');
    expect(card(target, 'lead').querySelectorAll('.rd-name')).toHaveLength(5);
    const tile = tiles(target).querySelectorAll('.mx-tile')[2] as HTMLElement;
    expect(tile.querySelector('.mx-tile-label')?.textContent).toBe('Lead time to hop 3');
    expect(tile.querySelector('.mx-tile-value')?.textContent?.trim()).toBe('1h');
    done();
  });

  it('lists an environment under each owner with that owner\'s numbers, and says owner is inferred', async () => {
    const envs = fleet(10, (i) => i % 2);
    const share = (held: [number, number]) => env('shared', { held: ratio(...held) });
    statsFn.mockResolvedValue(at(envs, { environmentsBy: {
      owner: [{ key: 'payments', label: 'payments', environments: [share([1, 10]), env('env-000')] }, { key: '(unowned)', label: 'unowned', environments: [share([3, 5])] }],
      domain: [], attribution: { asOf: '2026-11-15T09:00:00Z', partial: false } } }));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(card(target, 'health').querySelector('.hm-scroll')).not.toBeNull());
    const select = Array.from(card(target, 'health').querySelectorAll('select')).find((s) => Array.from(s.options).some((o) => o.value === 'owner')) as HTMLSelectElement;
    select.value = 'owner';
    select.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();
    expect(groupButtons(target).map((b) => b.querySelector('bdi')?.textContent)).toEqual(['payments', 'No owner']);
    const words = Array.from(card(target, 'health').querySelectorAll('tbody tr:not(.rung) .visually-hidden')).map((e) => e.textContent);
    expect(words).toContain('Held in shared: 1 of 10 promotions, 10%');
    expect(words).toContain('Held in shared: 3 of 5 promotions, 60%');
    expect(card(target, 'health').textContent).toContain('owner inferred from the fleet snapshot of');
    expect(card(target, 'health').querySelector('[data-testid="metrics-env-count"]')?.textContent).toBe('2 of 10 environments');
    done();
  });

  it('keeps an open group on rows after the filter shrinks it, and lets the toggle close it while filtering', async () => {
    statsFn.mockResolvedValue(at(fleet(200, (i) => i % 5)));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(card(target, 'health').querySelector('.hm-scroll')).not.toBeNull());
    const h = card(target, 'health');
    // Collapsed depth groups show their worst first whatever the order: no Sort that does nothing.
    expect(Array.from(h.querySelectorAll('select')).some((s) => Array.from(s.options).some((o) => o.value === 'worst'))).toBe(false);
    groupButtons(target)[0].click(); flushSync();
    const more = (label: string) => Array.from(h.querySelectorAll('tr.more button')).find((b) => b.textContent === label) as HTMLButtonElement;
    more('Show all 40').click(); flushSync();
    more('Next').click(); flushSync();
    expect(h.querySelector('tr.more')?.textContent).toContain('21 to 40 of 40');
    const input = h.querySelector('input[aria-label="Filter environments"]') as HTMLInputElement;
    input.value = 'env-00';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    const rows = () => card(target, 'health').querySelectorAll('tbody')[0].querySelectorAll('tr:not(.rung):not(.more)');
    expect(rows()).toHaveLength(2);
    expect(groupButtons(target)[0].getAttribute('aria-expanded')).toBe('true');
    groupButtons(target)[0].click(); flushSync();
    expect(groupButtons(target)[0].getAttribute('aria-expanded')).toBe('false');
    expect(rows()).toHaveLength(0);
    done();
  });

  it('groups by a grouping it offers when promotion depth gives one group, with no depth row', async () => {
    const envs = fleet(10, () => undefined);
    const owner = (key: string, es: Extra[]) => ({ key, label: key, environments: es });
    statsFn.mockResolvedValue(at(envs, { environmentsBy: { owner: [owner('payments', envs.slice(0, 5)), owner('checkout', envs.slice(5))], domain: [],
      attribution: { asOf: '2026-11-15T09:00:00Z', partial: false } } }));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(card(target, 'health').querySelector('.hm-scroll')).not.toBeNull());
    expect(target.querySelector('[data-testid="metrics-depths"]')).toBeNull();
    const select = Array.from(card(target, 'health').querySelectorAll('select')).find((s) => Array.from(s.options).some((o) => o.value === 'owner')) as HTMLSelectElement | undefined;
    expect(select).toBeUndefined();
    expect(groupButtons(target).map((b) => b.querySelector('bdi')?.textContent)).toEqual(['payments', 'checkout']);
    expect(card(target, 'lead').textContent).toContain('grouped by owner');
    done();
  });

  it('files a cycle under Depth unknown', async () => {
    const envs = [...fleet(9, () => 0), env('looped', { cycle: true })];
    statsFn.mockResolvedValue(at(envs));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(card(target, 'health').querySelector('.hm-scroll')).not.toBeNull());
    const unknown = card(target, 'health').querySelectorAll('tbody')[1] as HTMLElement;
    expect(unknown.querySelector('tr.rung bdi')?.textContent).toBe('Depth unknown');
    expect(unknown.querySelector('.nm')?.getAttribute('title')).toBe('looped');
    done();
  });

  it('lists the 10 slowest lead times with unknown last, and pages all of them by 20', async () => {
    const lt = (i: number) => (i < 3 ? { leadTime: { joined: 0, unjoined: 1, unknown: true } } : { leadTime: { p50Seconds: 60 * i, p90Seconds: 120 * i, joined: 2, unjoined: 0 } });
    statsFn.mockResolvedValue(at(fleet(12, () => 0, lt)));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(card(target, 'lead').querySelector('.rd-name')).not.toBeNull());
    const names = () => Array.from(card(target, 'lead').querySelectorAll('.rd-name')).map((n) => n.getAttribute('title'));
    expect(names()).toHaveLength(10);
    expect(names()[0]).toBe('env-011');
    expect(names()[9]).toMatch(/^env-00[012]$/);
    expect(card(target, 'lead').querySelectorAll('.rd-plate')).toHaveLength(1);
    expect(card(target, 'lead').textContent).toContain('The 10 slowest p50 of 12, grouped by promotion depth');
    (Array.from(card(target, 'lead').querySelectorAll('button')).find((b) => b.textContent === 'Show all 12') as HTMLButtonElement).click();
    flushSync();
    expect(names()).toHaveLength(12);
    expect(card(target, 'lead').textContent).toContain('1 to 12 of 12');
    done();
  });

  it('pages no environment chart through the URL: only the records and services pagers carry a total', async () => {
    statsFn.mockResolvedValue(at(fleet(60, (i) => Math.floor(i / 15))));
    recordsFn.mockResolvedValue({ total: 30, items: [{ record: { id: 'a', kind: 'impact', service: 'payments', recordedAt: '2026-11-10T10:00:00Z', version: '1', pactoVersion: '4' } }], limitations: [], hidden: { present: false }, skipped: {}, completeness: 'complete', asOf: '2026-11-15T10:00:00Z', provenance: 'unsigned statistics' });
    const { target, done } = mountView({ metric: 'held' });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="metrics-records-pager"]')).not.toBeNull());
    for (const c of Array.from(target.querySelectorAll('button.cc-toggle')) as HTMLButtonElement[]) c.click();
    flushSync();
    expect(Array.from(target.querySelectorAll('[data-view-total]')).map((e) => e.getAttribute('data-testid'))).toEqual(['metrics-records-pager', 'metrics-services-pager']);
    done();
  });
});
