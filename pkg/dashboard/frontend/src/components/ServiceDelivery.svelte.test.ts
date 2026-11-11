/**
 * The Delivery section of a service page: its explicit states and the
 * service's Stages, matched by exact service name.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync, tick } from 'svelte';

const { environments, plan, undo } = vi.hoisted(() => ({ environments: vi.fn(), plan: vi.fn(), undo: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { ...actual.api, deliveryEnvironments: (...a: unknown[]) => environments(...a),
    deliveryPlan: (...a: unknown[]) => plan(...a), deliveryUndo: (...a: unknown[]) => undo(...a) } };
});

import { ApiError } from '../lib/api.ts';
// @ts-expect-error — Svelte component has no declaration file
import ServiceDelivery from './ServiceDelivery.svelte';

const D1 = 'sha256:' + '1'.repeat(64);
const D2 = 'sha256:' + '2'.repeat(64);
const row = (over = {}) => ({ service: 'orders', namespace: 'prod', stage: 'orders', environment: 'prod', pointer: D1, version: '2.4.0',
  verified: false, failed: false, frozen: false, autoHold: false, held: 0, actions: [], offers: [], state: 'unverified', ...over });
const answer = (rows: unknown[], over = {}) => ({ asOf: '2026-11-04T10:00:00Z', completeness: 'complete', environments: rows, ...over,
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
  beforeEach(() => { environments.mockReset(); plan.mockReset(); undo.mockReset(); });

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
    expect(environments).toHaveBeenCalledWith({ service: 'orders', domain: '', limit: 1 });
    expect(v.target.textContent).toContain('2.4.0');
    expect(v.target.textContent).not.toContain('9.9.9');
    const link = v.target.querySelector('[data-testid="service-delivery-row"] a') as HTMLAnchorElement;
    expect(link.getAttribute('href')).toBe('#/delivery/history?namespace=prod&stage=orders');
    expect(v.target.querySelector('a[href="#/delivery/history?service=orders"]')?.textContent).toContain('Delivery history');
    expect(v.target.querySelector('a[href="#/delivery/environments?service=orders"]')?.textContent).toContain('Open in Delivery');
    expect(v.target.querySelector('[data-testid="service-delivery-nodomain"]')).toBeNull(); // no domain, nothing to tell apart
    v.destroy();
  });

  it('same-named services in two domains never share Stages', async () => {
    environments.mockResolvedValue(answer([
      row({ domain: 'ghcr.io/a', version: '1.0.0' }),
      row({ domain: 'ghcr.io/b', stage: 'orders-b', version: '2.0.0' }),
      row({ stage: 'orders-any', version: '3.0.0' }),
    ]));
    const v = render({ domain: 'ghcr.io/a' });
    await settle();
    expect(environments).toHaveBeenCalledWith({ service: 'orders', domain: 'ghcr.io/a', limit: 1 });
    expect(v.target.textContent).toContain('1.0.0');
    expect(v.target.textContent).not.toContain('2.0.0');
    expect(v.target.textContent).toContain('3.0.0'); // names no domain, so it matches any
    // ...and only that row says the match is by name only.
    const notes = [...v.target.querySelectorAll('[data-testid="service-delivery-nodomain"]')];
    expect(notes.length).toBe(1);
    expect(notes[0].closest('[data-testid="service-delivery-row"]')?.textContent).toContain('3.0.0');
    expect(notes[0].textContent).toContain('Stage names no domain: matched by service name only');
    // The links carry the same exact service and domain.
    expect(v.target.querySelector('a[href="#/delivery/history?service=orders&domain=ghcr.io%2Fa"]')).not.toBeNull();
    expect(v.target.querySelector('a[href="#/delivery/environments?service=orders&domain=ghcr.io%2Fa"]')).not.toBeNull();
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
  it('a partial listing with no rows never says the service has no Stages', async () => {
    environments.mockResolvedValue(answer([], { completeness: 'partial', unreadable: ['team-a: forbidden'] }));
    const v = render({});
    await settle();
    const t = v.target.textContent ?? '';
    expect(t).toContain('No Stages of this service that you can see');
    expect(t).toContain('team-a: forbidden');
    expect(t).not.toContain('No Stages for this service');
    expect(t).toContain('2026-11-04 10:00 UTC');
    v.destroy();
  });

  it('a partial listing with rows says some may be missing', async () => {
    environments.mockResolvedValue(answer([row()], { completeness: 'partial', unreadable: ['team-b'] }));
    const v = render({});
    await settle();
    const note = v.target.querySelector('[data-testid="service-delivery-partial"]')!;
    expect(note.textContent).toContain('team-b');
    expect(v.target.textContent).toContain('2.4.0');
    v.destroy();
  });

  it('marks each Stage with its state; a Stage with no pointer reads running nothing yet', async () => {
    environments.mockResolvedValue(answer([row({ pointer: '', version: '', state: 'empty' }), row({ stage: 'o2', environment: 'qa', state: 'verified', verified: true })]));
    const v = render({});
    await settle();
    const marks = [...v.target.querySelectorAll('[data-testid="env-state-mark"]')];
    expect(marks.map((m) => m.getAttribute('data-state'))).toEqual(['empty', 'verified']);
    expect(v.target.textContent).toContain('running nothing yet');
    v.destroy();
  });

  it('offers available actions as buttons and unavailable ones disabled with the reason in text', async () => {
    environments.mockResolvedValue(answer([row({ candidate: D2, candidateVersion: '2.5.0', offers: [
      { action: 'promote', available: true, reason: 'a newer candidate is verified upstream', to: D2 },
      { action: 'freeze', available: false, reason: 'your Kubernetes RBAC does not let you create Operations in prod' },
    ] })]));
    plan.mockResolvedValue({ type: 'delivery.pacto.trianalab.io/promote', predicted: 'Held', intentDigest: 'sha256:i', next: ['A person approves it.'] });
    const v = render({});
    await settle();
    const buttons = [...v.target.querySelectorAll<HTMLButtonElement>('[data-testid="service-delivery-offer"]')];
    expect(buttons.map((b) => [b.textContent?.trim(), b.disabled])).toEqual([['Promote', false], ['Freeze', true]]);
    expect(v.target.textContent).toContain('Freeze: your Kubernetes RBAC does not let you create Operations in prod');
    buttons[0].click();
    await settle();
    expect(plan).toHaveBeenCalledWith({ namespace: 'prod', stage: 'orders', type: 'delivery.pacto.trianalab.io/promote', to: D2, from: D1 });
    expect(v.target.querySelector('[data-testid="plan-next"]')?.textContent).toContain('A person approves it.');
    v.destroy();
  });

  it('undo opens the undo panel for the operation it undoes', async () => {
    environments.mockResolvedValue(answer([row({ offers: [{ action: 'undo', available: true, reason: 'undo the change that shipped 2.4.0', operation: 'op-7', to: D2 }] })]));
    undo.mockResolvedValue({ kind: 'none', note: 'nothing to undo' });
    const v = render({});
    await settle();
    (v.target.querySelector('[data-testid="service-delivery-offer"]') as HTMLButtonElement).click();
    await settle();
    expect(undo).toHaveBeenCalledWith('prod', 'op-7', 'orders');
    expect(v.target.querySelector('[data-testid="undo-panel"]')).not.toBeNull();
    v.destroy();
  });

  it('links the version to its fleet revision and the contract it names', async () => {
    environments.mockResolvedValue(answer([row({ revision: 'registry.example/story/orders@sha256:abc', contractRef: 'registry.example/story/orders@sha256:abc', sourceRevision: '0f1e2d' })]));
    const v = render({});
    await settle();
    const r = v.target.querySelector('[data-testid="service-delivery-row"]')!;
    expect(r.querySelector('a')?.getAttribute('href')).toBe('#/fleet/revisions/' + encodeURIComponent('registry.example/story/orders@sha256:abc'));
    expect(r.textContent).toContain('0f1e2d');
    expect(r.textContent).toContain('an annotation, not signed');
    expect(r.textContent).not.toMatch(/\bunsigned\b/);
    v.destroy();
  });

  it('says ? waiting when the operations could not be read, and links held changes to the Inbox', async () => {
    environments.mockResolvedValue(answer([row({ heldUnknown: true }), row({ stage: 'o2', environment: 'qa', held: 2 })]));
    const v = render({ domain: 'ghcr.io/a' });
    await settle();
    expect(v.target.textContent).toContain('? waiting');
    const held = [...v.target.querySelectorAll('a')].find((a) => a.textContent?.includes('2 waiting'));
    expect(held?.getAttribute('href')).toBe('#/delivery/inbox?service=orders&domain=ghcr.io%2Fa');
    v.destroy();
  });

  it('shows readyFix or the source problem, never both', async () => {
    environments.mockResolvedValue(answer([
      row({ ready: 'False: CandidateUnverified', readyMessage: 'the OCIRepository verifies no publisher', readyFix: 'set spec.verify on the OCIRepository',
        upstream: { kind: 'artifact', problem: 'unverified', repository: 'orders-repo' } }),
      row({ stage: 'o2', environment: 'qa', ready: 'True', upstream: { kind: 'artifact', problem: 'missing', repository: 'gone-repo' } }),
    ]));
    const v = render({});
    await settle();
    const [a, b] = [...v.target.querySelectorAll('[data-testid="service-delivery-row"]')];
    expect(a.textContent).toContain('OCIRepository verifies no publisher');
    expect(a.textContent).toContain('Fix: Set spec.verify on the OCIRepository');
    expect(a.textContent).not.toContain('Unverified artifacts');
    expect(b.textContent).toContain('Missing artifacts');
    v.destroy();
  });

  it('shows the newest re-check and whether it is about the running version', async () => {
    environments.mockResolvedValue(answer([row({
      lastCheck: { at: '2026-11-04T09:58:00Z', current: false, digest: D2, result: 'Unknown', verdicts: [{ gate: 'pacto.runtime', phase: 'verify', result: 'Unknown', message: 'no metrics' }] },
      watched: { status: 'Unknown', reason: 'NotRechecked' },
    })]));
    const v = render({});
    await settle();
    const t = v.target.textContent ?? '';
    expect(t).toContain('Re-checked 2026-11-04 09:58 UTC, Unknown, stale');
    expect(t).toContain('Not re-checked since it shipped');
    // The verdicts mount once the check is opened.
    expect(t).not.toContain('no metrics');
    const d = v.target.querySelector('details.sd-check') as HTMLDetailsElement;
    d.open = true;
    d.dispatchEvent(new Event('toggle'));
    await settle();
    expect(v.target.textContent).toContain('no metrics');
    v.destroy();
  });

  it('links an observe Stage to what Pacto would have done', async () => {
    environments.mockResolvedValue(answer([row({ level: 'observe', shadow: [{ candidate: D2, predicted: 'Proceeds' }] })]));
    const v = render({});
    await settle();
    expect([...v.target.querySelectorAll('a')].some((a) => a.getAttribute('href') === '#/delivery/autonomy?service=orders')).toBe(true);
    v.destroy();
  });
});
