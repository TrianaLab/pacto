/**
 * The Metrics page shell (D12): the explainer when the host has no stats repository,
 * the error state when the read fails, the no-records sentence, and never a number it
 * does not have.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount } from 'svelte';
import { ApiError } from '../lib/api.ts';

const { statsFn } = vi.hoisted(() => ({ statsFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { stats: (...a: unknown[]) => statsFn(...a) } };
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

beforeEach(() => { statsFn.mockReset(); });

describe('FleetMetricsView', () => {
  it('explains how to turn recording on, with no number, when the host has no stats repository', async () => {
    const { target, done } = mountView({ capabilities: { fleet: true, stats: false } });
    expect(target.querySelector('h1')?.textContent).toBe('Metrics');
    const off = target.querySelector('[data-testid="metrics-off"]') as HTMLElement;
    expect(off).not.toBeNull();
    expect(off.textContent).toContain('Metrics are off');
    expect(off.textContent).toContain('Pacto records run statistics in your own registry.');
    expect(off.textContent).toContain('--stats-registry');
    expect(off.querySelector('a[href^="https://pacto.run/latest/"]')?.textContent).toBe('How recording works');
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
});
