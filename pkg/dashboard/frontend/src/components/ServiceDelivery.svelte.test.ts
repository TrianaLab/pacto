/**
 * The Delivery section of a service page: its explicit states and the
 * service's Stages, matched by exact service name.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync, tick } from 'svelte';

const { environments, plan, undo, progress, history } = vi.hoisted(() => ({ environments: vi.fn(), plan: vi.fn(), undo: vi.fn(), progress: vi.fn(), history: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { ...actual.api, deliveryEnvironments: (...a: unknown[]) => environments(...a),
    deliveryPlan: (...a: unknown[]) => plan(...a), deliveryUndo: (...a: unknown[]) => undo(...a),
    deliveryProgress: (...a: unknown[]) => progress(...a), deliveryHistory: (...a: unknown[]) => history(...a) } };
});

import { ApiError } from '../lib/api.ts';
import { deliveryPublishUrl, deliveryChangeUrl } from '../lib/router.ts';
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
  beforeEach(() => {
    environments.mockReset(); plan.mockReset(); undo.mockReset(); progress.mockReset(); history.mockReset();
    history.mockResolvedValue({ asOf: '2026-11-04T10:00:00Z', completeness: 'complete', histories: [] });
  });

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

  it('links held changes to the Inbox and the one held change to its page', async () => {
    environments.mockResolvedValue(answer([row({ stage: 'o2', environment: 'qa', held: 2 }),
      row({ stage: 'o3', environment: 'prod', namespace: 'prod', held: 1, heldOperation: 'op-held' })]));
    const v = render({ domain: 'ghcr.io/a' });
    await settle();
    const held = [...v.target.querySelectorAll('a')].find((a) => a.textContent?.includes('2 waiting'));
    expect(held?.getAttribute('href')).toBe('#/delivery/inbox?service=orders&domain=ghcr.io%2Fa');
    const one = [...v.target.querySelectorAll('a')].find((a) => a.textContent?.includes('1 waiting'));
    expect(one?.getAttribute('href')).toBe(deliveryChangeUrl('prod', 'op-held'));
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

  const why = (over = {}) => ({ namespace: 'prod', stage: 'orders', asOf: '2026-11-04T10:00:00Z', state: 'needs-person', completeness: 'complete',
    summary: 'A person must approve the promote of 2.5.0.', candidate: D2, autoPromote: 'the level is advise: a person proposes each promote',
    reasons: [{ code: 'Held', message: 'op-1 waits for a person', fix: 'pacto delivery approve -n prod op-1' }],
    plan: { type: 'delivery.pacto.trianalab.io/promote', to: D2, predicted: 'Held', intentDigest: 'sha256:i', next: ['A person of team-a approves it.'] }, ...over });
  const openWhy = async (v: { target: HTMLElement }, i = 0) => {
    const d = v.target.querySelectorAll<HTMLDetailsElement>('details.sd-why')[i];
    d.open = !d.open;
    d.dispatchEvent(new Event('toggle'));
    await settle();
    return d;
  };

  it('the Why disclosure asks for progress once and shows the answer with asOf', async () => {
    environments.mockResolvedValue(answer([row()]));
    progress.mockResolvedValue(why());
    const v = render({});
    await settle();
    expect(progress).not.toHaveBeenCalled();
    await openWhy(v);
    expect(progress).toHaveBeenCalledWith('prod', 'orders');
    const card = v.target.querySelector('[data-testid="service-delivery-why"]')!;
    const t = card.textContent ?? '';
    for (const x of ['Needs a person', 'A person must approve the promote of 2.5.0.', 'op-1 waits for a person', 'level is advise', '2026-11-04 10:00 UTC'])
      expect(t).toContain(x);
    expect(card.querySelector('[data-testid="why-command"]')?.textContent).toContain('pacto delivery approve -n prod op-1');
    expect(card.querySelector('[data-testid="plan-next"]')?.textContent).toContain('A person of team-a approves it.');
    expect(card.querySelector('form')).toBeNull(); // a preview: proposing stays behind the offer buttons
    await openWhy(v);
    await openWhy(v);
    expect(progress).toHaveBeenCalledTimes(1);
    v.destroy();
  });

  it('a blocked Why that could not read its operations is partial', async () => {
    environments.mockResolvedValue(answer([row()]));
    progress.mockResolvedValue(why({ state: 'blocked', completeness: 'partial', reasons: [{ code: 'Frozen', message: 'frozen' }, { code: 'OpsUnknown', message: 'operations unread' }] }));
    const v = render({});
    await settle();
    await openWhy(v);
    const card = v.target.querySelector('[data-testid="service-delivery-why"]')!;
    expect(card.textContent).toContain('Partial');
    expect(card.textContent).not.toContain('Complete');
    v.destroy();
  });

  it('an agent-shaped answer shows its fix in words and no command', async () => {
    environments.mockResolvedValue(answer([row()]));
    progress.mockResolvedValue(why({ reasons: [{ code: 'Held', message: 'op-1 waits for a person', fix: 'a person of team-a approves it themselves; you cannot answer it' }] }));
    const v = render({});
    await settle();
    await openWhy(v);
    const card = v.target.querySelector('[data-testid="service-delivery-why"]')!;
    expect(card.textContent).toContain('person of team-a approves it themselves');
    expect(card.querySelector('[data-testid="why-command"]')).toBeNull();
    v.destroy();
  });

  it('a Why that fails says so', async () => {
    environments.mockResolvedValue(answer([row()]));
    progress.mockRejectedValue(new ApiError(503, 'cluster unreachable', 'ClusterUnreachable'));
    const v = render({});
    await settle();
    await openWhy(v);
    expect(v.target.textContent).toContain('cluster unreachable');
    v.destroy();
  });

  const placement = (items: unknown[]) => ({ total: items.length, count: items.length, truncated: false, items });
  const env = (environment: string, over = {}) => ({ environment, targets: 2, declared: 1, notDeclared: 1, unknown: 0,
    revisions: [{ kind: 'revision', key: `r-${environment}`, label: 'orders 1.2.0', version: '1.2.0', href: `#/fleet/revisions/r-${environment}` }],
    revisionsTotal: 1, revisionsTruncated: false, ...over });
  const META = { asOf: '2026-11-04T09:00:00Z', completeness: 'complete' };

  it('without delivery, shows where the service runs as observed from targets', async () => {
    const v = render({ state: 'not-served', placement: placement([env('prod'), env('', { declared: 0, notDeclared: 0, unknown: 2 })]), meta: META });
    await settle();
    const strip = v.target.querySelector('[data-testid="service-placement"]')!;
    const t = strip.textContent ?? '';
    expect(t).toContain('Where it runs (observed)');
    expect(t).not.toContain('targets Pacto can see');
    expect(t).toContain('observed from targets, not a promotion step');
    expect(t).toContain('2026-11-04 09:00 UTC');
    expect(t).toContain('Complete');
    const rows = [...strip.querySelectorAll('[data-testid="service-placement-row"]')];
    expect(rows.length).toBe(2);
    expect(rows[0].textContent).toContain('prod');
    for (const x of ['2 targets', '1 declared', '1 not declared']) expect(rows[0].textContent).toContain(x);
    expect(rows[0].querySelector('a')?.getAttribute('href')).toBe('#/fleet/revisions/r-prod');
    expect(rows[1].textContent).toContain('no environment label');
    expect(rows[1].textContent).toContain('2 unknown');
    v.destroy();
  });

  it('a partial fleet answer reads targets Pacto can see and counts truncated revisions', async () => {
    environments.mockResolvedValue(answer([]));
    const v = render({ placement: placement([env('prod', { revisionsTotal: 11, revisionsTruncated: true })]), meta: { ...META, completeness: 'partial' } });
    await settle();
    const strip = v.target.querySelector('[data-testid="service-placement"]')!;
    expect(strip.textContent).toContain('Where it runs (observed, targets Pacto can see)');
    expect(strip.textContent).toContain('and 10 more');
    expect(strip.textContent).toContain('2026-11-04 09:00 UTC');
    v.destroy();
  });

  it('when delivery is not installed, shows the observed strip', async () => {
    environments.mockRejectedValue(new ApiError(404, 'no delivery', 'DeliveryNotInstalled'));
    const v = render({ placement: placement([env('prod')]), meta: META });
    await settle();
    expect(v.target.querySelector('[data-testid="service-placement"]')).not.toBeNull();
    v.destroy();
  });

  it('beside Stages, shows only environments no Stage covers, and only on a complete listing', async () => {
    environments.mockResolvedValue(answer([row({ environment: 'prod' })]));
    const v = render({ placement: placement([env('prod'), env('staging')]), meta: META });
    await settle();
    const rows = [...v.target.querySelectorAll('[data-testid="service-placement-row"]')];
    expect(rows.map((r) => r.querySelector('bdi')?.textContent)).toEqual(['staging']);
    v.destroy();

    environments.mockResolvedValue(answer([row({ environment: 'prod' })], { completeness: 'partial', unreadable: ['team-b'] }));
    const w = render({ placement: placement([env('prod'), env('staging')]), meta: META });
    await settle();
    expect(w.target.querySelector('[data-testid="service-placement"]')).toBeNull();
    w.destroy();
  });

  it('leaves out an environment the revision declares but no target reports', async () => {
    const v = render({ state: 'not-served', placement: placement([env('prod'), env('preview', { targets: 0, declared: 0, notDeclared: 0, revisions: [] })]), meta: META });
    await settle();
    const rows = [...v.target.querySelectorAll('[data-testid="service-placement-row"]')];
    expect(rows.map((r) => r.querySelector('bdi')?.textContent)).toEqual(['prod']);
    v.destroy();
  });

  it('no strip when every environment has a Stage', async () => {
    environments.mockResolvedValue(answer([row({ environment: 'prod' })]));
    const v = render({ placement: placement([env('prod')]), meta: META });
    await settle();
    expect(v.target.querySelector('[data-testid="service-placement"]')).toBeNull();
    v.destroy();
  });

  it('shows the newest change per Stage with applied, cause and correlation, marking unsigned cells', async () => {
    environments.mockResolvedValue(answer([row(), row({ stage: 'o2', environment: 'qa' })]));
    history.mockResolvedValue({ asOf: '2026-11-04T10:00:00Z', completeness: 'complete', histories: [
      { namespace: 'prod', stage: { name: 'orders' }, completeness: 'complete', entries: [
        { operation: 'op-2', type: 'delivery.pacto.trianalab.io/promote', outcome: 'Succeeded', applied: '2026-11-04T09:30:00Z', cause: { reason: 'auto-promoted' }, correlationID: 'corr-2' },
        { operation: 'op-1', type: 'delivery.pacto.trianalab.io/promote', outcome: 'Succeeded', applied: '2026-11-03T09:30:00Z', correlationID: 'corr-old' },
      ] },
      { namespace: 'prod', stage: { name: 'o2' }, completeness: 'partial', entries: [
        { operation: 'op-3', type: 'delivery.pacto.trianalab.io/freeze', outcome: 'Succeeded', applied: '2026-11-04T08:00:00Z', correlationID: 'corr-3', record: 'rec-3' },
      ] },
    ] });
    const v = render({});
    await settle();
    expect(history).toHaveBeenCalledWith('', '', { service: 'orders', domain: '', limit: 25 });
    const [a, b] = [...v.target.querySelectorAll('[data-testid="service-delivery-row"]')].map((r) => r.querySelector('[data-testid="service-delivery-last"]')!);
    for (const x of ['2026-11-04 09:30 UTC', 'auto-promoted', 'corr-2', 'from status, not signed']) expect(a.textContent).toContain(x);
    expect(a.textContent).not.toContain('corr-old');
    expect(a.textContent).toContain('Last change:');
    expect(b.textContent).toContain('corr-3');
    // A partial history's first line may not be the newest change.
    expect(b.textContent).toContain('Newest change Pacto could read:');
    expect(b.textContent).toContain('Partial');
    expect(b.textContent).not.toContain('not signed');
    expect(v.target.textContent).not.toMatch(/\bunsigned\b/);
    expect(v.target.querySelector(`a[href="${deliveryPublishUrl({ service: 'orders' })}"]`)?.textContent).toContain('Published versions');
    v.destroy();
  });
});
