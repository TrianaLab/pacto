/**
 * Delivery read from several clusters (514-S7): each row names its cluster by its
 * display name, a remote operation only reads here and shows the command that acts
 * in its own cluster, and same-named objects of two clusters stay two rows.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync, tick } from 'svelte';

const fns = vi.hoisted(() => ({ session: vi.fn(), inbox: vi.fn(), environments: vi.fn(), history: vi.fn(), blame: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  const { sessionApi } = await import('../testkit.svelte.ts');
  return {
    ...actual,
    api: {
      ...sessionApi(() => fns.session(), actual.ApiError), deliveryInbox: (...a: unknown[]) => fns.inbox(...a),
      deliveryEnvironments: (...a: unknown[]) => fns.environments(...a), deliveryHistory: (...a: unknown[]) => fns.history(...a),
      deliveryBlame: (...a: unknown[]) => fns.blame(...a), deliveryRefusals: () => Promise.resolve({ asOf: '', recorded: false, completeness: 'partial', refusals: [] }),
    },
  };
});

import { loadSession, session as auth } from '../lib/session.svelte.ts';
import { parseHash } from '../lib/router.ts';
// @ts-expect-error — Svelte component has no declaration file
import DeliveryInboxView from './DeliveryInboxView.svelte';
// @ts-expect-error — Svelte component has no declaration file
import DeliveryEnvironmentsView from './DeliveryEnvironmentsView.svelte';
// @ts-expect-error — Svelte component has no declaration file
import DeliveryHistoryView from './DeliveryHistoryView.svelte';

const D1 = 'sha256:' + '1'.repeat(64);
const D2 = 'sha256:' + '2'.repeat(64);
const EU = '1b2c3d4e-0000-4000-8000-000000000001';
const US = '1b2c3d4e-0000-4000-8000-000000000002';
const clusters = [{ id: EU, name: 'eu-prod', completeness: 'complete' }, { id: US, name: 'us-prod', completeness: 'complete' }];
const listing = { asOf: '2026-11-24T10:00:00Z', completeness: 'complete', clusters };
const UID = /[0-9a-f]{8}-[0-9a-f]{4}-/;

async function settle() {
  for (let i = 0; i < 6; i++) { await tick(); await Promise.resolve(); }
  flushSync();
}
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- component handle
function render(C: any, props: Record<string, unknown> = {}) {
  loadSession();
  const target = document.createElement('div');
  document.body.appendChild(target);
  const c = mount(C, { target, props });
  return { target, destroy: () => { unmount(c); target.remove(); } };
}
const text = (t: Element) => t.textContent?.replace(/\s+/g, ' ') ?? '';

beforeEach(() => {
  for (const f of Object.values(fns)) f.mockReset();
  auth.data = null; auth.error = null; auth.loaded = false;
  fns.session.mockResolvedValue({ mode: 'shared', login: true, signedIn: true, csrf: 'c', caller: { user: 'https://idp#carol', class: 'human', groups: ['team-checkout'], authTime: '1792560000' }, teamNarrowing: true });
});

describe('inbox across clusters', () => {
  const held = { namespace: 'prod', name: 'op1', stage: 'pay', type: 'delivery.pacto.trianalab.io/promote', to: D2, phase: 'Held', decision: 'approve', owners: ['team-checkout'], consumers: ['checkout'] };
  it('keeps same-named operations of two clusters apart and shows a remote one read-only with its command', async () => {
    fns.inbox.mockResolvedValue({ ...listing, frozen: [], errored: [], held: [held, { ...held, cluster: US, readOnly: true, next: 'pacto delivery approve op1 -n prod --context us-prod' }] });
    const { target, destroy } = render(DeliveryInboxView);
    await settle();
    const rows = [...target.querySelectorAll('[data-testid="inbox-held"] > li')];
    expect(rows.length).toBe(2);
    expect(text(rows[0])).toContain('eu-prod / Promote on prod/pay');
    expect(text(rows[1])).toContain('us-prod / Promote on prod/pay');
    expect(rows[0].querySelector('a.btn-primary')?.textContent).toBe('Decide');
    // The remote item: no Decide, no link to this cluster's change page, the command in the action slot.
    expect(rows[1].querySelector('a.btn-primary')).toBeNull();
    expect(rows[1].querySelector('a[href="#/delivery/changes/prod/op1"]')).toBeNull();
    expect(text(rows[1].querySelector('[data-testid="inbox-next"]')!)).toBe('Decide there: pacto delivery approve op1 -n prod --context us-prod');
    expect(rows[1].querySelector('.dl-title')?.getAttribute('title')).toBe(`Cluster us-prod (${US})`);
    for (const l of target.querySelectorAll('.dl-title')) expect(l.textContent).not.toMatch(UID);
    destroy();
  });
  it('links a remote frozen Stage to its history in its own cluster', async () => {
    fns.inbox.mockResolvedValue({ ...listing, held: [], errored: [], frozen: [{ namespace: 'prod', stage: 'pay', cluster: US, service: 'payments', environment: 'prod', frozen: true, held: 0, actions: [] }] });
    const { target, destroy } = render(DeliveryInboxView);
    await settle();
    const a = target.querySelector('[data-testid="inbox-frozen"] a') as HTMLAnchorElement;
    expect(text(a)).toBe('us-prod / payments in prod');
    expect(a.getAttribute('href')).toBe(`#/delivery/history?namespace=prod&stage=pay&cluster=${US}`);
    destroy();
  });
  it('adds no prefix with one cluster', async () => {
    fns.inbox.mockResolvedValue({ asOf: listing.asOf, completeness: 'complete', frozen: [], errored: [], held: [held] });
    const { target, destroy } = render(DeliveryInboxView);
    await settle();
    expect(text(target.querySelector('[data-testid="inbox-held"] .dl-title')!).trim()).toMatch(/^Promote on prod\/pay: /);
    expect(target.querySelector('[data-testid="inbox-held"] .dl-title')?.getAttribute('title')).toBeNull();
    expect(text(target.querySelector('[data-testid="inbox-held"]')!)).not.toContain(' / ');
    destroy();
  });
});

describe('Stages and history across clusters', () => {
  const row = { namespace: 'prod', stage: 'pay', service: 'payments', environment: 'prod', pointer: D1, version: '1.0.0', verified: true, failed: false, frozen: false, autoHold: false, held: 0, ready: 'True', actions: [] };
  it('shows both clusters\' Stages in one cell, each named by its cluster, with history links in that cluster', async () => {
    fns.environments.mockResolvedValue({ ...listing, environments: [row, { ...row, cluster: US }] });
    const { target, destroy } = render(DeliveryEnvironmentsView);
    await settle();
    const cells = [...target.querySelectorAll('[data-testid="env-cell"]')];
    expect(cells.length).toBe(2);
    expect(cells.map((c) => text(c.querySelector('.ev-stage')!))).toEqual(['eu-prod / prod/pay', 'us-prod / prod/pay']);
    expect(cells[1].querySelector('.ev-stage')?.getAttribute('title')).toBe(`Cluster us-prod (${US})`);
    expect([...cells[1].querySelectorAll('a')].some((a) => a.getAttribute('href') === `#/delivery/history?namespace=prod&stage=pay&cluster=${US}`)).toBe(true);
    destroy();
  });
  it('names each history Stage by its cluster and asks one remote Stage in its own cluster', async () => {
    const h = { namespace: 'prod', asOf: listing.asOf, completeness: 'complete', stage: { name: 'pay', pointer: D1 }, entries: [] };
    fns.history.mockResolvedValue({ ...listing, histories: [h, { ...h, cluster: US }] });
    let r = render(DeliveryHistoryView);
    await settle();
    const links = [...r.target.querySelectorAll('[data-testid="history-stage-row"] th a')];
    expect(links.map((a) => text(a))).toEqual(['eu-prod / pay', 'us-prod / pay']);
    expect(links[1].getAttribute('href')).toBe(`#/delivery/history?namespace=prod&stage=pay&cluster=${US}`);
    r.destroy();

    fns.history.mockResolvedValue({ asOf: listing.asOf, completeness: 'complete', histories: [{ ...h, cluster: US }] });
    fns.blame.mockResolvedValue({ asOf: listing.asOf, completeness: 'complete' });
    r = render(DeliveryHistoryView, parseHash(`#/delivery/history?namespace=prod&stage=pay&cluster=${US}`).params);
    await settle();
    expect(fns.history).toHaveBeenCalledWith('prod', 'pay', expect.objectContaining({ cluster: US }));
    ([...r.target.querySelectorAll('button')].find((b) => b.textContent?.startsWith('Who put')) as HTMLButtonElement).click();
    await settle();
    expect(fns.blame).toHaveBeenCalledWith('prod', 'pay', '', US);
    r.destroy();
  });
});
