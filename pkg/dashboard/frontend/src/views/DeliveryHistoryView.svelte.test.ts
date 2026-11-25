/**
 * Delivery history (#459): each digest links to its fleet revision, a record's
 * pacto.impact verdict reads as recorded (apart from today's analysis), and As of
 * answers what each Stage ran at a past time. `api` is mocked.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync, tick } from 'svelte';

const fns = vi.hoisted(() => ({ session: vi.fn(), history: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  const { sessionApi } = await import('../testkit.svelte.ts');
  return { ...actual, api: { ...sessionApi(() => fns.session(), actual.ApiError), deliveryHistory: (...a: unknown[]) => fns.history(...a) } };
});

import { loadSession } from '../lib/session.svelte.ts';
// @ts-expect-error — Svelte component has no declaration file
import DeliveryHistoryView from './DeliveryHistoryView.svelte';

const D1 = 'sha256:' + '1'.repeat(64);
const D2 = 'sha256:' + '2'.repeat(64);
const D3 = 'sha256:' + '3'.repeat(64);
const REC_A = 'sha256:' + 'a'.repeat(64);
const SVC = 'registry.example/story/payments';
const listing = { asOf: '2026-11-10T10:00:00Z', completeness: 'complete' };
const impact = { gate: 'pacto.impact', phase: 'pre', result: 'Pass', message: 'no consumer breaks' };

async function settle() {
  for (let i = 0; i < 6; i++) { await tick(); await Promise.resolve(); }
  flushSync();
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- component handle
function render(props: Record<string, unknown> = {}) {
  loadSession();
  const target = document.createElement('div');
  document.body.appendChild(target);
  const c = mount(DeliveryHistoryView, { target, props });
  return { target, destroy: () => { unmount(c); target.remove(); } };
}

const stageHistory = (verified: boolean, entries: Record<string, unknown>[]) => ({
  ...listing,
  versions: { [D1]: '1.0.0', [D2]: '2.0.0', [D3]: '3.0.0' },
  revisions: { [D1]: `${SVC}@${D1}`, [D2]: `${SVC}@${D2}` },
  histories: [{ namespace: 'prod', asOf: listing.asOf, completeness: 'complete', source: 'records', verified, note: '', stage: { name: 'pay', pointer: D2 }, entries }],
});
const promote = (over: Record<string, unknown> = {}) => ({ seq: 2, kind: 'operation', operation: 'op2', type: 'delivery.pacto.trianalab.io/promote', from: D1, to: D2,
  outcome: 'Succeeded', finished: '2026-11-10T14:02:00Z', record: 'rec-2', impact, ...over });

beforeEach(() => {
  fns.session.mockResolvedValue({ mode: 'local', login: false, signedIn: false });
  fns.history.mockReset();
});

describe('delivery history revisions and recorded impact', () => {
  it('links each digest to the revision its fleet holds and leaves an unknown one as text', async () => {
    fns.history.mockResolvedValue(stageHistory(true, [promote({ from: D3, to: D2 })]));
    const { target, destroy } = render({ namespace: 'prod', stage: 'pay' });
    await settle();
    const cell = target.querySelector('[data-label="Version"]') as HTMLElement;
    const links = [...cell.querySelectorAll('a')];
    expect(links.map((a) => a.getAttribute('href'))).toEqual([`#/fleet/revisions/${encodeURIComponent(`${SVC}@${D2}`)}`]);
    expect(links[0].textContent).toBe('2.0.0');
    expect(cell.textContent).toContain('3.0.0');
    destroy();
  });

  it('says the verdict a signed record carries, apart from the link to analyse it against today', async () => {
    fns.history.mockResolvedValue(stageHistory(true, [promote()]));
    const { target, destroy } = render({ namespace: 'prod', stage: 'pay' });
    await settle();
    const rec = target.querySelector('[data-testid="history-impact"]') as HTMLElement;
    expect(rec.textContent?.trim()).toBe('Impact recorded 2026-11-10 14:02 UTC: Pass no consumer breaks');
    expect(rec.textContent).not.toContain("against today's graph");
    const analyse = target.querySelector('[data-testid="history-analyse"]') as HTMLAnchorElement;
    expect(analyse.textContent).toBe("Analyse against today's graph");
    expect(analyse.textContent).not.toContain('recorded');
    expect(analyse.contains(rec) || rec.contains(analyse)).toBe(false);
    expect(analyse.getAttribute('href')).toBe(`#/fleet/changes/${encodeURIComponent(SVC)}?old=${encodeURIComponent(`${SVC}@${D1}`)}&new=${encodeURIComponent(`${SVC}@${D2}`)}`);
    expect(analyse.getAttribute('aria-label')).toBe("Analyse against today's graph: 1.0.0 to 2.0.0");
    destroy();
  });

  it('gives every analyse link its own label', async () => {
    fns.history.mockResolvedValue(stageHistory(true, [promote({ from: D2, to: D1, seq: 3, operation: 'op3', type: 'delivery.pacto.trianalab.io/rollback' }), promote()]));
    const { target, destroy } = render({ namespace: 'prod', stage: 'pay' });
    await settle();
    const labels = [...target.querySelectorAll('[data-testid="history-analyse"]')].map((a) => a.getAttribute('aria-label'));
    expect(labels).toHaveLength(2);
    expect(new Set(labels).size).toBe(2);
    destroy();
  });

  it('says a verdict on an unverified history is not checked, and offers no analysis without both revisions', async () => {
    fns.history.mockResolvedValue(stageHistory(false, [promote({ from: D3 })]));
    const { target, destroy } = render({ namespace: 'prod', stage: 'pay' });
    await settle();
    expect(target.querySelector('[data-testid="history-impact"]')?.textContent?.trim()).toBe('Impact recorded, signature not checked: Pass no consumer breaks');
    expect(target.querySelector('[data-testid="history-analyse"]')).toBeNull();
    destroy();
  });

  it('says nothing about impact on a line read from status', async () => {
    fns.history.mockResolvedValue(stageHistory(true, [promote({ record: undefined, impact: undefined, applied: '2026-11-10T14:00:00Z' })]));
    const { target, destroy } = render({ namespace: 'prod', stage: 'pay' });
    await settle();
    expect(target.querySelector('[data-testid="history-impact"]')).toBeNull();
    expect(target.querySelector('[data-testid="history-analyse"]')).toBeNull();
    expect(target.textContent).not.toContain('Impact');
    expect(target.textContent).toContain('from status, not signed');
    destroy();
  });
});

describe('delivery history as of a time', () => {
  const at = (over: Record<string, unknown> = {}) => ({
    ...listing, histories: [], page: { total: 2, offset: 0, limit: 25 },
    at: { at: '2026-11-10T12:00:00Z', asOf: listing.asOf, completeness: 'partial', note: '2 Stages read, 1 of them known at this time', stages: [
      { namespace: 'prod', stage: 'pay', service: 'payments', state: 'known', digest: D1, since: '2026-11-09T08:00:00Z', record: 'rec-1', version: '1.0.0',
        revision: `${SVC}@${D1}`, owner: 'Payments', ownerKey: 'payments-team', businessDomain: 'payments' },
      { namespace: 'test', stage: 'pay', service: 'payments', state: 'unknown', reason: 'no record before this time' },
    ], ...over },
  });

  it('asks for the time in the URL and lists what each Stage ran then', async () => {
    fns.history.mockResolvedValue(at());
    const { target, destroy } = render({ at: '2026-11-10T12:00:00Z' });
    await settle();
    expect(fns.history.mock.calls[0][2]).toMatchObject({ at: '2026-11-10T12:00:00Z' });
    const region = target.querySelector('[data-testid="history-at"]') as HTMLElement;
    expect(region.getAttribute('role')).toBe('region');
    expect(region.getAttribute('aria-label')).toBe('What each Stage ran at 2026-11-10 12:00 UTC');
    expect(region.getAttribute('tabindex')).toBe('0');
    expect(target.querySelector('[data-testid="history-at-caption"]')?.textContent?.trim()).toBe('As of 2026-11-10 12:00 UTC: from signed records, complete for 1 of 2 Stages.');
    const rows = [...target.querySelectorAll('[data-testid="history-at-row"]')] as HTMLElement[];
    expect(rows).toHaveLength(2);
    expect(rows[0].querySelector(`a[href="#/fleet/owners/payments-team"]`)?.textContent).toBe('Payments');
    expect([...rows[0].querySelectorAll('a')].some((a) => a.getAttribute('href') === `#/fleet/revisions/${encodeURIComponent(`${SVC}@${D1}`)}`)).toBe(true);
    expect(rows[0].querySelector('[data-testid="history-at-state"]')?.textContent?.trim()).toBe('known');
    expect(rows[1].querySelector('[data-testid="history-at-state"]')?.textContent?.trim()).toBe('unknown');
    expect(rows[1].textContent).toContain('no record before this time');
    // Narrow form: every cell names its column.
    expect([...rows[0].querySelectorAll('[role="cell"]')].map((c) => c.getAttribute('data-label')))
      .toEqual(['Stage', 'Service', 'Version', 'Owner', 'Business domain', 'Since', 'State']);
    // The control shows the time asked, in UTC.
    expect((target.querySelector('[data-testid="history-at-input"]') as HTMLInputElement).value).toBe('2026-11-10T12:00');
    destroy();
  });

  it('says a partial Stage why and names the namespaces it could not read', async () => {
    fns.history.mockResolvedValue(at({ unreadable: ['staging', 'qa'], note: '2 Stages read, 1 of them known at this time; others were not read',
      stages: [{ namespace: 'prod', stage: 'pay', service: 'payments', state: 'partial', digest: D1, version: '1.0.0', reason: 'a move after it is not signed' }] }));
    const { target, destroy } = render({ at: '2h' });
    await settle();
    const caption = target.querySelector('[data-testid="history-at-caption"]')?.textContent ?? '';
    expect(caption).toContain('complete for 0 of 1 Stage');
    expect(caption).toContain('staging and qa could not be read');
    expect(caption).toContain('other Stages were not read');
    const row = target.querySelector('[data-testid="history-at-row"]') as HTMLElement;
    expect(row.querySelector('[data-testid="history-at-state"]')?.textContent?.trim()).toBe('partial');
    expect(row.textContent).toContain('a move after it is not signed');
    destroy();
  });

  it('commits a typed time only on Ask, as UTC, and drops the filters it cannot take', async () => {
    fns.history.mockResolvedValue(at());
    location.hash = '#/delivery/history?q=pay';
    const { target, destroy } = render({ q: 'pay' });
    await settle();
    const input = target.querySelector('[data-testid="history-at-input"]') as HTMLInputElement;
    // A date field changes on each digit typed: none of them navigates.
    for (const v of ['0002-11-01T09:30', '0020-11-01T09:30', '0202-11-01T09:30', '2026-11-01T09:30']) {
      input.value = v;
      input.dispatchEvent(new Event('input', { bubbles: true }));
      input.dispatchEvent(new Event('change', { bubbles: true }));
    }
    expect(location.hash).toBe('#/delivery/history?q=pay');
    expect(input.value).toBe('2026-11-01T09:30');
    (target.querySelector('[data-testid="history-at-ask"]') as HTMLButtonElement).click();
    expect(location.hash).toBe('#/delivery/history?q=pay&at=2026-11-01T09%3A30%3A00Z');
    expect(input.max).toMatch(/^\d{4}-\d\d-\d\dT\d\d:\d\d$/);
    destroy();
  });

  it('draws one Stage as of a time as a window that ends then, never now', async () => {
    const end = '2026-01-01T00:00:00Z';
    const buckets = Array.from({ length: 12 }, (_, i) => ({ start: new Date(Date.parse(end) - (12 - i) * 7_200_000).toISOString(), end: new Date(Date.parse(end) - (11 - i) * 7_200_000).toISOString(),
      released: i === 11 ? 1 : 0, failed: 0, held: 0, missing: 0 }));
    buckets[11].end = end;
    const timeline = { start: buckets[0].start, end, buckets, totals: { released: 1, failed: 0, held: 0, missing: 0 }, untimed: 0 };
    const h = stageHistory(true, [promote()]);
    fns.history.mockResolvedValue({ ...h, histories: [{ ...h.histories[0], timeline }], at: { ...at().at, at: end } });
    const { target, destroy } = render({ namespace: 'prod', stage: 'pay', at: end });
    await settle();
    const fig = target.querySelector('[data-testid="history-figure"]') as HTMLElement;
    expect(fig.textContent).toContain('What happened in the 24 hours before 2026-01-01 00:00 UTC on this Stage');
    expect(fig.textContent).not.toContain('now,');
    const last = [...fig.querySelectorAll('a')].map((a) => a.getAttribute('href') ?? '').find((u) => u.includes(encodeURIComponent(buckets[11].start)));
    expect(last).toContain(`until=${encodeURIComponent(end)}`);
    expect(last).not.toContain('at=');
    destroy();
  });

  it('shows no As of table without at', async () => {
    fns.history.mockResolvedValue({ ...listing, histories: [], page: { total: 0, offset: 0, limit: 25 } });
    const { target, destroy } = render({});
    await settle();
    expect(fns.history.mock.calls[0][2].at).toBeUndefined();
    expect(target.querySelector('[data-testid="history-at"]')).toBeNull();
    destroy();
  });

  it('marks how an approval was signed even when it carries nothing else', async () => {
    fns.history.mockResolvedValue(stageHistory(true, [promote({ approvals: [{ by: 'alice@acme.com', decision: 'approve',
      record: REC_A, signer: { kind: 'person' } }] })]));
    const { target, destroy } = render({ namespace: 'prod', stage: 'pay' });
    await settle();
    const cell = target.querySelector('[data-testid="history-decided"]') as HTMLElement;
    expect(cell.querySelector('button[aria-label="How this approval was signed"]')).not.toBeNull();
    destroy();
  });
});

describe('delivery history of another cluster', () => {
  const EU = '9f1c4e2a-3b7d-4e19-a2c5-6d8f0b1e7a34';
  it('names the cluster in the title and links no change to this cluster', async () => {
    const h = stageHistory(true, [promote()]);
    fns.history.mockResolvedValue({ ...h, clusters: [{ id: 'hub-uid', name: 'hub', completeness: 'complete' }, { id: EU, name: 'eu-prod', completeness: 'complete' }],
      histories: h.histories.map((x) => ({ ...x, cluster: EU })) });
    const { target, destroy } = render({ namespace: 'prod', stage: 'pay', cluster: EU });
    await settle();
    expect(target.querySelector('[data-testid="page-title"]')!.textContent).toContain('History of eu-prod / prod/pay');
    expect(target.querySelector('a[href^="#/delivery/changes"]')).toBeNull();
    expect(target.textContent).toContain('Promote');
    destroy();
  });
});
