/**
 * The Delivery section of a service page: its explicit states and the
 * service's Stages, matched by exact service name.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync, tick } from 'svelte';

const environments = vi.hoisted(() => vi.fn());
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { ...actual.api, deliveryEnvironments: (...a: unknown[]) => environments(...a) } };
});

import { ApiError } from '../lib/api.ts';
// @ts-expect-error — Svelte component has no declaration file
import ServiceDelivery from './ServiceDelivery.svelte';

const D1 = 'sha256:' + '1'.repeat(64);
const D2 = 'sha256:' + '2'.repeat(64);
const row = (over = {}) => ({ service: 'orders', namespace: 'prod', stage: 'orders', environment: 'prod', pointer: D1, version: '2.4.0',
  verified: false, failed: false, frozen: false, autoHold: false, held: 0, actions: [], ...over });
const answer = (rows: unknown[]) => ({ asOf: '2026-11-04T10:00:00Z', completeness: 'complete', environments: rows,
  page: { offset: 0, limit: 1, total: rows.length ? 1 : 0 }, environmentNames: [], environmentTiers: {}, counts: { failed: 0, frozen: 0, waiting: 0 } });

async function settle() {
  for (let i = 0; i < 6; i++) { await tick(); await Promise.resolve(); }
  flushSync();
}

function render(props: Record<string, unknown>) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const p = $state({ service: 'orders', state: 'enabled', refreshTick: 0, ...props });
  const c = mount(ServiceDelivery, { target, props: p });
  return { target, p, destroy: () => { unmount(c); target.remove(); } };
}

describe('ServiceDelivery', () => {
  beforeEach(() => { environments.mockReset(); });

  it.each([
    ['unknown', 'Checking whether this dashboard serves delivery.'],
    ['no-user', 'no signed-in user'],
    ['not-served', 'does not serve the delivery API'],
  ])('%s makes no call and says why', async (state, text) => {
    const v = render({ state });
    await settle();
    expect(environments).not.toHaveBeenCalled();
    expect(v.target.textContent).toContain(text);
    v.destroy();
  });

  it('exact service match: asks for one service and shows only its Stages', async () => {
    environments.mockResolvedValue(answer([row(), row({ service: 'orders-api', stage: 'orders-api', version: '9.9.9' })]));
    const v = render({});
    await settle();
    expect(environments).toHaveBeenCalledWith({ service: 'orders', limit: 1 });
    expect(v.target.textContent).toContain('2.4.0');
    expect(v.target.textContent).not.toContain('9.9.9');
    const link = v.target.querySelector('[data-testid="service-delivery-row"] a') as HTMLAnchorElement;
    expect(link.getAttribute('href')).toBe('#/delivery/history?namespace=prod&stage=orders');
    expect(v.target.querySelector('a[href="#/delivery/history?q=orders"]')?.textContent).toContain('Delivery history');
    expect(v.target.querySelector('a[href="#/delivery/environments?q=orders"]')?.textContent).toContain('Open in Delivery');
    v.destroy();
  });

  it('a 404 says delivery is not installed', async () => {
    environments.mockRejectedValue(new ApiError(404, 'no delivery', 'DeliveryNotInstalled'));
    const v = render({});
    await settle();
    expect(v.target.textContent).toContain('Delivery is not installed');
    v.destroy();
  });

  it('a 503 cannot read delivery and retries', async () => {
    environments.mockRejectedValue(new ApiError(503, 'cluster unreachable', 'ClusterUnreachable'));
    const v = render({});
    await settle();
    expect(v.target.textContent).toContain('Can’t read delivery');
    expect(v.target.textContent).toContain('cluster unreachable');
    const retry = [...v.target.querySelectorAll('button')].find((b) => /retry/i.test(b.textContent ?? '')) as HTMLButtonElement;
    retry.click();
    await settle();
    expect(environments).toHaveBeenCalledTimes(2);
    v.destroy();
  });

  it('no Stage says delivery is opt-in', async () => {
    environments.mockResolvedValue(answer([]));
    const v = render({});
    await settle();
    expect(v.target.textContent).toContain('No Stages for this service. Delivery is opt-in per service.');
    v.destroy();
  });

  it('shows the board badges, frozen reason and candidate', async () => {
    environments.mockResolvedValue(answer([
      row({ environment: 'prod', verified: true }),
      row({ environment: 'qa', stage: 'orders-qa', failed: true }),
      row({ environment: 'dev', stage: 'orders-dev', frozen: true, frozenReason: 'Errored', candidateVersion: '2.4.1', candidate: D2 }),
      row({ environment: 'stage', stage: 'orders-stage', held: 2, candidate: D2 }),
    ]));
    const v = render({});
    await settle();
    const text = v.target.textContent ?? '';
    for (const t of ['Verified', 'Failed', 'Frozen', 'Frozen: Errored', '2 waiting', 'Candidate 2.4.1', `Candidate ${D2.slice(7, 19)}`]) expect(text).toContain(t);
    expect(v.target.querySelectorAll('[data-testid="service-delivery-row"] a[href^="#/delivery/history?namespace=prod&stage=orders"]').length).toBe(4);
    v.destroy();
  });

  it('a refresh asks again without clearing the rows', async () => {
    environments.mockResolvedValue(answer([row()]));
    const v = render({});
    await settle();
    let resolve: (x: unknown) => void = () => {};
    environments.mockReturnValue(new Promise((r) => { resolve = r; }));
    v.p.refreshTick = 1;
    await settle();
    expect(environments).toHaveBeenCalledTimes(2);
    expect(v.target.textContent).toContain('2.4.0');
    resolve(answer([row({ version: '2.5.0' })]));
    await settle();
    expect(v.target.textContent).toContain('2.5.0');
    v.destroy();
  });

  it('asks once the dashboard turns out to serve delivery', async () => {
    environments.mockResolvedValue(answer([row()]));
    const v = render({ state: 'unknown' });
    await settle();
    expect(environments).not.toHaveBeenCalled();
    v.p.state = 'enabled';
    await settle();
    expect(environments).toHaveBeenCalledTimes(1);
    expect(v.target.textContent).toContain('2.4.0');
    v.destroy();
  });
});
