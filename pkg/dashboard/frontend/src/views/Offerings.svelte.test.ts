/**
 * Component tests for the platform-offering list: one table row per service.offering
 * with its owners, lifecycle, availability, consumers, failed requests and observed
 * targets, its filters and order, and the sentence that says how to declare one.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount } from 'svelte';

const { entitiesFn } = vi.hoisted(() => ({ entitiesFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { fleetEntities: (...a: unknown[]) => entitiesFn(...a) } };
});

// @ts-expect-error — Svelte component has no declaration file
import FleetOfferingsView from './FleetOfferingsView.svelte';

const meta = { schemaVersion: 'pacto.dev/fleet-product/v1', snapshotId: 'x', asOf: '2026-11-04T10:00:00Z', completeness: 'complete', sources: [] };
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
const listResp = (entities: any[], m: any = meta): any => ({ meta: m, total: entities.length, count: entities.length, offset: 0, limit: 25, truncated: false, entities });
const offeringRef = (name: string, services: number, targets: number) => (
  { kind: 'offering', key: name, label: name, offering: name, services, targets, href: `/fleet/offerings/${name}` });

function mountView(props: Record<string, unknown> = {}) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(FleetOfferingsView, { target, props: { refreshTick: 0, ...props } });
  return { target, done: () => { unmount(component); document.body.removeChild(target); } };
}

beforeEach(() => { entitiesFn.mockReset(); location.hash = ''; });

describe('FleetOfferingsView', () => {
  it('lists offerings in a table with their owners, lifecycle, availability, consumers, failed requests and observed targets', async () => {
    entitiesFn.mockResolvedValue(listResp([
      { ...offeringRef('postgresql', 2, 3), owner: '2 owners claim it', lifecycle: 'deprecated', availability: 'unavailable', availabilityProvenance: 'inferred', consumers: 4, failedRequests: 1 },
      { ...offeringRef('shared-config', 1, 0), owner: 'team:platform', lifecycle: 'supported', availability: 'unknown', availabilityProvenance: 'unknown', consumers: 2, hiddenConsumers: 2, targetsUnknown: true },
    ]));
    const { target, done } = mountView({ text: 'sql' });
    await vi.waitFor(() => expect(target.querySelectorAll('[data-testid="offering-list"] tbody tr').length).toBe(2));
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['offering'], text: 'sql', limit: 25, sort: 'failed' }));
    expect(target.querySelector('h1')?.textContent).toBe('Platform offerings');
    expect(target.textContent).toContain('Services that platform teams publish for other teams to use, grouped by the service.offering their contracts declare.');
    expect(target.textContent).not.toContain('operational target');
    expect(Array.from(target.querySelectorAll('[data-testid="offering-list"] thead th')).map((th) => th.textContent?.trim()))
      .toEqual(['Offering', 'Owners', 'Lifecycle', 'Availability', 'Consumers', 'Failed ▼', 'Observed targets']);
    const [pg, sc] = Array.from(target.querySelectorAll('[data-testid="offering-list"] tbody tr'));
    const cell = (row: Element, label: string) => row.querySelector(`[data-label="${label}"]`)!;
    expect(pg.textContent).toContain('2 providers');
    expect(cell(pg, 'Owners').textContent).toContain('2 owners claim it');
    expect(cell(pg, 'Lifecycle').textContent?.trim()).toBe('deprecated');
    // The provenance is visible text on the badge, never only a title.
    expect(cell(pg, 'Availability').textContent?.trim()).toBe('unavailable · inferred');
    expect(cell(pg, 'Consumers').querySelector('a')?.getAttribute('href')).toBe('#/fleet/services?consumes=postgresql');
    expect(cell(pg, 'Failed').querySelector('a')?.getAttribute('href')).toBe('#/fleet/services?consumes=postgresql&phase=failed');
    expect(cell(pg, 'Observed targets').textContent?.trim()).toBe('3');
    // A withheld target count reads unknown, never 0; a restricted row never prints a bare 0.
    expect(cell(sc, 'Observed targets').textContent).toContain('unknown');
    expect(cell(sc, 'Observed targets').textContent).not.toMatch(/\b0\b/);
    expect(cell(sc, 'Availability').textContent?.trim()).toBe('unknown');
    expect(cell(sc, 'Availability').querySelector('.hatch')).not.toBeNull();
    expect(cell(sc, 'Consumers').textContent).toContain('2 consumers you may not see are not counted');
    expect(cell(sc, 'Failed').textContent).toContain('0 seen');
    expect(Array.from(target.querySelectorAll('[data-testid="offering-list"] a.entity-link')).map((a) => a.getAttribute('href')))
      .toEqual(['#/fleet/offerings/postgresql', '#/fleet/offerings/shared-config']);
    done();
  });

  it('sorts by failed requests by default and offers Consumers and Failed as sortable headers', async () => {
    entitiesFn.mockResolvedValue(listResp([offeringRef('postgresql', 1, 3)]));
    const { target, done } = mountView({ sort: 'consumers' });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="offering-list"]')).not.toBeNull());
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ sort: 'consumers' }));
    const ths = Array.from(target.querySelectorAll('[data-testid="offering-list"] thead th[aria-sort]'));
    expect(ths.map((th) => `${th.textContent?.trim()}:${th.getAttribute('aria-sort')}`)).toEqual(['Consumers ▼:descending', 'Failed:none']);
    expect(ths[1].querySelector('a')?.getAttribute('href')).toBe('#/fleet/offerings');
    expect(ths[0].querySelector('a')?.getAttribute('href')).toBe('#/fleet/offerings?sort=consumers');
    done();
  });

  it('narrows to offerings with a failed request and by lifecycle, in the URL, the request and the chips', async () => {
    entitiesFn.mockResolvedValue(listResp([offeringRef('postgresql', 1, 3)]));
    const { target, done } = mountView({ lifecycle: 'sunset', failed: '1' });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="offering-list"]')).not.toBeNull());
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ lifecycle: 'sunset', failed: true }));
    expect(target.querySelector('.filter-chips')?.textContent).toContain('Past sunset');
    expect(target.querySelector('.filter-chips')?.textContent).toContain('a failed request');
    const box = target.querySelector('[data-testid="offering-failed-filter"]') as HTMLInputElement;
    expect(box.checked).toBe(true);
    box.checked = false;
    box.dispatchEvent(new Event('change', { bubbles: true }));
    expect(location.hash).toBe('#/fleet/offerings?lifecycle=sunset');
    done();
  });

  it('writes failed=1 to the URL when the failed-request box is checked', async () => {
    entitiesFn.mockResolvedValue(listResp([offeringRef('postgresql', 1, 3)]));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="offering-list"]')).not.toBeNull());
    const box = target.querySelector('[data-testid="offering-failed-filter"]') as HTMLInputElement;
    box.checked = true;
    box.dispatchEvent(new Event('change', { bubbles: true }));
    expect(location.hash).toBe('#/fleet/offerings?failed=1');
    done();
  });

  it('draws where the requests of every matched offering stand, with the Failed station listing the failed offerings', async () => {
    const aggregate = {
      offeringCount: 2, offeringPhases: { ready: 3, failed: 2, unknown: 1 }, offeringPhaseConsumers: { ready: 2, failed: 2, unknown: 1 },
      failedByOffering: [{ type: 'postgresql', services: 2 }],
    };
    entitiesFn.mockResolvedValue({ ...listResp([offeringRef('postgresql', 1, 3), offeringRef('redis', 1, 1)]), aggregate });
    const { target, done } = mountView({ text: 'sql' });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="offerings-figure"]')).not.toBeNull(), { timeout: 5000 });
    const fig = target.querySelector('[data-testid="offerings-figure"]')!;
    expect(fig.textContent).toContain('Are the requests for platform offerings ready?');
    expect(fig.textContent).toContain('Of 6 requests across 2 offerings, 3 are ready');
    const failedLink = fig.querySelector('a[data-lists]')!;
    expect(failedLink.getAttribute('data-lists')).toBe('1');
    expect(failedLink.getAttribute('href')).toBe('#/fleet/offerings?text=sql&failed=1');
    expect(fig.querySelector('a.chip')?.getAttribute('href')).toBe('#/fleet/offerings/postgresql');
    done();
  });

  it('explains how to declare an offering when none is declared', async () => {
    entitiesFn.mockResolvedValue(listResp([]));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(target.textContent).toContain('No contract declares a platform offering yet.'));
    expect(target.textContent).toContain('Set service.offering in a contract (pactoVersion 2.4) to list it here.');
    done();
  });

  it('does not claim none is declared when knowledge is incomplete', async () => {
    entitiesFn.mockResolvedValue(listResp([], { ...meta, completeness: 'partial' }));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(entitiesFn).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 20));
    expect(target.textContent).not.toContain('No contract declares a platform offering yet.');
    done();
  });
});
