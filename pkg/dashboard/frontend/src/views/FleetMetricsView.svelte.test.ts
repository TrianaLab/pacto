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
  environmentOrder: 'name', environments: [], services: [], servicesTotal: 0, buckets: [], limitations: [], ...extra,
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
  it('reads the chosen window again and links every tile to its records', async () => {
    statsFn.mockResolvedValue(populated());
    const { target, done } = mountView({ window: '7d' });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="metrics-content"]')).not.toBeNull());
    expect(statsFn).toHaveBeenLastCalledWith(expect.objectContaining({ window: '7d' }));
    const breaking = target.querySelector('[data-testid="metrics-tiles"] a[href*="metric=breaking"]') as HTMLAnchorElement;
    expect(breaking.textContent).toBe('7');
    expect(breaking.getAttribute('data-lists')).toBe('7');
    expect(breaking.getAttribute('href')).toBe('#/metrics?window=7d&metric=breaking');
    // Every chart says how sure it is.
    expect(target.querySelectorAll('[data-testid="metrics-trend"] [data-testid="stats-strip"]').length).toBeGreaterThan(3);
    expect(target.querySelector('[data-testid="stats-strip"]')?.textContent).toContain('unsigned statistics');
    done();
  });

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

  it('keeps unreadable services in the coverage unknown row, never in deployed', async () => {
    statsFn.mockResolvedValue(populated());
    const { target, done } = mountView();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="metrics-coverage-now"]')).not.toBeNull());
    const rows = Array.from(target.querySelectorAll('[data-testid="metrics-coverage-now"] .hb-row'));
    const row = (label: string) => rows.find((r) => r.querySelector('.hb-label')?.textContent === label);
    // 10 services, 3 unreadable, 2 not deployed: 5 deployed and 3 unknown beside it.
    expect(row('Deployed')?.querySelector('.hb-value')?.textContent).toBe('5');
    expect(row('Unknown')?.querySelector('.hb-value')?.textContent).toBe('3');
    expect(row('Unknown')?.querySelector('.hb-fill')?.classList.contains('hatch')).toBe(true);
    expect(target.querySelector('[data-testid="metrics-coverage-trend"]')).not.toBeNull();
    done();
  });

  it('prints unknown when the coverage snapshot carries no knowledge tally', async () => {
    statsFn.mockResolvedValue(populated({ coverageNow: coverage(undefined) }));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="metrics-coverage-now"]')).not.toBeNull());
    expect(target.querySelector('[data-testid="metrics-coverage-now"]')?.textContent).toContain('unknown');
    done();
  });

  it('tells a restricted reader records are hidden and shows coverage now only', async () => {
    statsFn.mockResolvedValue(populated({ hidden: { present: true }, completeness: 'partial',
      limitations: [{ code: 'COVERAGE_OUTSIDE_YOUR_VIEW', message: 'coverage records count every service' }],
      buckets: populated().buckets.map((b: Record<string, unknown>) => ({ ...b, coverage: undefined })) }));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="metrics-hidden"]')).not.toBeNull());
    expect(target.querySelector('[data-testid="metrics-hidden"]')?.textContent).toBe('Some run records name services outside your view and are left out of every number.');
    expect(target.querySelector('[data-testid="metrics-coverage-trend"]')).toBeNull();
    expect(target.querySelector('[data-testid="metrics-coverage-now"]')).not.toBeNull();
    expect(target.querySelector('[data-testid="metrics-coverage-restricted"]')?.textContent).toContain('shown to people who can read every service');
    expect(target.querySelector('[data-testid="stats-strip"]')?.textContent).toContain('Partial');
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

  it('draws no 0 for a window whose every bucket is empty', async () => {
    statsFn.mockResolvedValue(populated({ buckets: populated().buckets.map((b: Record<string, unknown>) => ({ ...b, metrics: undefined, coverage: undefined })) }));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="metrics-trend"]')).not.toBeNull());
    for (const v of Array.from(target.querySelectorAll('[data-testid="metrics-trend"] .hb-value, [data-testid="metrics-coverage-trend"] .hb-value'))) {
      expect(v.textContent).toBe('no records');
    }
    expect(Array.from(target.querySelectorAll('[data-testid="metrics-trend"]')).map((t) => t.querySelector('.hb-scale'))).toEqual(
      Array.from(target.querySelectorAll('[data-testid="metrics-trend"]')).map(() => null));
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

  it('puts a lead time environment on its own line', async () => {
    statsFn.mockResolvedValue(populated());
    const { target, done } = mountView();
    await vi.waitFor(() => expect(target.querySelector('.mx-tile-sub')).not.toBeNull());
    const tile = target.querySelector('.mx-tile-sub')?.closest('.mx-tile') as HTMLElement;
    expect(tile.querySelector('.mx-tile-label')?.textContent).toBe('Lead time p50');
    expect(tile.querySelector('bdi.mx-tile-sub')?.textContent).toBe('staging');
    done();
  });

  it('draws services, described, deployed and unknown over time', async () => {
    statsFn.mockResolvedValue(populated());
    const { target, done } = mountView();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="metrics-coverage-trend"]')).not.toBeNull());
    const titles = Array.from(target.querySelectorAll('[data-testid="metrics-coverage-trend"] .hb-title')).map((h) => h.textContent);
    expect(titles).toEqual(['Services per week', 'Described services per week', 'Deployed services per week', 'Services Pacto cannot judge per week']);
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
