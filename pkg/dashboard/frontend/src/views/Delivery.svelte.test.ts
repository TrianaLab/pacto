/**
 * Component tests for the delivery views: the inbox, the environments board,
 * a change with its decision (fresh sign-in, refusals, local mode), history and
 * published versions. `api` is mocked; the session store is the real one.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync, tick } from 'svelte';

const fns = vi.hoisted(() => ({
  session: vi.fn(), inbox: vi.fn(), environments: vi.fn(), published: vi.fn(), history: vi.fn(),
  operation: vi.fn(), plan: vi.fn(), propose: vi.fn(), decide: vi.fn(), cancel: vi.fn(), signOut: vi.fn(),
  blame: vi.fn(), undo: vi.fn(), refusals: vi.fn(),
}));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return {
    ...actual,
    api: {
      session: () => fns.session(), deliveryInbox: () => fns.inbox(), deliveryEnvironments: () => fns.environments(),
      deliveryPublished: () => fns.published(), deliveryHistory: (...a: unknown[]) => fns.history(...a),
      deliveryOperation: (...a: unknown[]) => fns.operation(...a), deliveryPlan: (...a: unknown[]) => fns.plan(...a),
      deliveryPropose: (...a: unknown[]) => fns.propose(...a), deliveryDecide: (...a: unknown[]) => fns.decide(...a),
      deliveryCancel: (...a: unknown[]) => fns.cancel(...a), signOut: () => fns.signOut(),
      deliveryBlame: (...a: unknown[]) => fns.blame(...a), deliveryUndo: (...a: unknown[]) => fns.undo(...a),
      deliveryRefusals: () => fns.refusals(),
    },
  };
});

import { ApiError } from '../lib/api.ts';
import { deliverySession } from '../lib/deliverySession.svelte.ts';
import { keepPending } from '../lib/delivery.ts';
// @ts-expect-error — Svelte component has no declaration file
import DeliveryInboxView from './DeliveryInboxView.svelte';
// @ts-expect-error — Svelte component has no declaration file
import DeliveryEnvironmentsView from './DeliveryEnvironmentsView.svelte';
// @ts-expect-error — Svelte component has no declaration file
import DeliveryChangeView from './DeliveryChangeView.svelte';
// @ts-expect-error — Svelte component has no declaration file
import DeliveryHistoryView from './DeliveryHistoryView.svelte';
// @ts-expect-error — Svelte component has no declaration file
import DeliveryPublishView from './DeliveryPublishView.svelte';

const D1 = 'sha256:' + '1'.repeat(64);
const D2 = 'sha256:' + '2'.repeat(64);
const listing = { asOf: '2026-10-21T10:00:00Z', completeness: 'complete' };
const human = (groups: string[]) => ({ user: 'https://idp#carol', class: 'human', groups, authTime: '1792560000' });
const shared = (over = {}) => ({ mode: 'shared', login: true, signedIn: true, fresh: false, csrf: 'c', caller: human(['team-checkout']), ...over });

async function settle() {
  for (let i = 0; i < 6; i++) { await tick(); await Promise.resolve(); }
  flushSync();
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- component handle
function render(C: any, props: Record<string, unknown> = {}) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const c = mount(C, { target, props });
  return { target, destroy: () => { unmount(c); target.remove(); } };
}

// jsdom's selector engine misreads an `&` inside an attribute value.
const linkTo = (t: HTMLElement, href: string) => [...t.querySelectorAll('a')].find((a) => a.getAttribute('href') === href);
const text = (t: HTMLElement) => t.textContent?.replace(/\s+/g, ' ') ?? '';

beforeEach(() => {
  for (const f of Object.values(fns)) f.mockReset();
  deliverySession.data = null; deliverySession.error = null; deliverySession.loaded = false;
  fns.session.mockResolvedValue(shared());
  fns.refusals.mockResolvedValue({ asOf: listing.asOf, recorded: false, completeness: "partial", refusals: [] });
  sessionStorage.clear();
  location.hash = '#/delivery';
});

describe('inbox', () => {
  it('shows held changes with the question, the decision asked and the affected services', async () => {
    fns.inbox.mockResolvedValue({ ...listing, frozen: [{ namespace: 'prod', stage: 'pay', service: 'payments', environment: 'prod', frozenReason: 'Errored', verified: false, failed: false, frozen: true, autoHold: false, held: 0, actions: [] }],
      errored: [{ namespace: 'prod', name: 'e1', stage: 'pay', type: 'pacto.dev/promote', phase: 'Concluded', outcome: 'Errored' }],
      held: [{ namespace: 'prod', name: 'op1', stage: 'pay', type: 'pacto.dev/promote', to: D2, phase: 'Held', question: 'checkout accepts only 1.x: ship?', decision: 'approve', owners: ['team-checkout'], consumers: ['checkout'] }] });
    const { target, destroy } = render(DeliveryInboxView);
    await settle();
    const held = target.querySelector('[data-testid="inbox-held"]')!;
    expect(text(held as HTMLElement)).toContain('checkout accepts only 1.x: ship?');
    expect(text(held as HTMLElement)).toContain('Approve');
    expect(text(held as HTMLElement)).toContain('team-checkout');
    expect(held.querySelector('a[href="#/delivery/changes/prod/op1"]')).not.toBeNull();
    expect(held.querySelector('a[href="#/services/checkout"]')).not.toBeNull();
    expect(text(target)).toContain('Paused: Errored');
    expect(text(target.querySelector('[data-testid="inbox-errored"]') as HTMLElement)).toContain('Pacto could not tell how it ended');
    // The person's subject and, printed next to it, the username the cluster uses. Never only in a tooltip.
    expect(text(target)).toContain('Signed in as carol (https://idp#carol), in team-checkout');
    expect(target.querySelector('[data-testid="delivery-identity"] [title]')).toBeNull();
    destroy();
  });

  it('says which service to update first, with links to its Stage and its verified version', async () => {
    fns.inbox.mockResolvedValue({ ...listing, frozen: [], errored: [],
      held: [{ namespace: 'prod', name: 'op1', stage: 'payments-prod', type: 'pacto.dev/promote', to: D2, phase: 'Held', question: 'ship?', decision: 'approve', owners: ['team-checkout'], consumers: ['orders', 'billing'], order: ORDER }] });
    const { target, destroy } = render(DeliveryInboxView);
    await settle();
    const mf = target.querySelector('[data-testid="inbox-move-first"]') as HTMLElement;
    expect(text(mf)).toContain('Update orders first: orders 1.8.0 runs here and accepts only ^2.0.0');
    expect(linkTo(mf, '#/delivery/history?namespace=prod&stage=orders-prod')?.textContent).toBe('Stage prod/orders-prod');
    expect(linkTo(mf, '#/delivery/history?namespace=test&stage=orders-test')?.textContent).toBe('orders 1.9.0, verified on test/orders-test');
    expect(text(mf)).toContain('Update billing first: it accepts only ^2.0.0');
    expect(text(mf)).toContain('Pacto cannot tell what would move it first');
    expect(text(mf.querySelector('[data-testid="inbox-move-first-unknown"]') as HTMLElement)).toContain('This list may be incomplete: the operational graph is partial');
    destroy();
  });

  it('says nothing waits, and says when it may be incomplete', async () => {
    fns.inbox.mockResolvedValue({ asOf: listing.asOf, completeness: 'partial', unreadable: ['billing'], held: [], frozen: [], errored: [] });
    const { target, destroy } = render(DeliveryInboxView);
    await settle();
    expect(text(target.querySelector('[data-testid="inbox-empty"]') as HTMLElement)).toContain('Nothing waits on you that you can see');
    expect(text(target.querySelector('[data-testid="listing-partial"]') as HTMLElement)).toContain('the namespace billing');
    destroy();
  });

  it('says a list read only from guessed namespaces may be incomplete', async () => {
    fns.inbox.mockResolvedValue({ asOf: listing.asOf, completeness: 'partial', unreadable: ['*', 'a', 'b'], held: [], frozen: [], errored: [] });
    const { target, destroy } = render(DeliveryInboxView);
    await settle();
    const note = text(target.querySelector('[data-testid="listing-partial"]') as HTMLElement);
    expect(note).toContain('only the namespaces Pacto knows of');
    expect(note).toContain('the namespaces a, b');
    expect(note).not.toContain('*');
    destroy();
  });

  it('does not name a person from the ID token alone when the cluster did not say who they are', async () => {
    fns.session.mockResolvedValue(shared({ name: 'Release Bot', caller: undefined, callerError: 'whoami: forbidden' }));
    fns.inbox.mockResolvedValue({ ...listing, frozen: [], errored: [], held: [] });
    const { target, destroy } = render(DeliveryInboxView);
    await settle();
    const bar = text(target.querySelector('[data-testid="delivery-identity"]') as HTMLElement);
    expect(bar).not.toContain('Release Bot');
    expect(bar).toContain('The cluster did not say who you are: whoami: forbidden');
    destroy();
  });

  it('asks to sign in on a 401, and tells a failure apart from an empty inbox', async () => {
    fns.session.mockResolvedValue({ mode: 'shared', login: true, signedIn: false, fresh: false });
    fns.inbox.mockRejectedValue(new ApiError(401, 'sign in to see delivery', 'LoginRequired'));
    let r = render(DeliveryInboxView);
    await settle();
    expect(r.target.querySelector('[data-testid="delivery-signin"] a')?.getAttribute('href')).toContain('/auth/login?return=');
    expect(text(r.target)).toContain('You are not signed in');
    r.destroy();

    fns.inbox.mockRejectedValue(new ApiError(500, 'boom'));
    r = render(DeliveryInboxView);
    await settle();
    expect(text(r.target)).toContain('Can’t read delivery');
    expect(r.target.querySelector('[data-testid="inbox-empty"]')).toBeNull();
    r.destroy();

    fns.inbox.mockRejectedValue(new ApiError(404, 'install the chart', 'DeliveryNotInstalled'));
    r = render(DeliveryInboxView);
    await settle();
    expect(text(r.target)).toContain('Delivery is not installed');
    r.destroy();
  });
});

describe('environments', () => {
  const row = { namespace: 'prod', stage: 'pay', service: 'payments', environment: 'prod', pointer: D1, version: '1.0.0', verified: true, failed: false, frozen: true, frozenReason: 'x', autoHold: true, held: 1, candidate: D2, ready: 'False: CandidateUnverified', actions: ['promote', 'unfreeze'] };

  it('shows each service in each environment and plans before proposing', async () => {
    fns.environments.mockResolvedValue({ ...listing, environments: [row, { ...row, environment: 'test', stage: 'pay-test', version: '', pointer: '', verified: false, frozen: false, autoHold: false, held: 0, ready: 'True', actions: [] }] });
    fns.plan.mockResolvedValue({ intentDigest: 'sha256:i', type: 'pacto.dev/promote', predicted: 'Held', question: 'ok?', floor: { owners: ['team-checkout'] }, conflicts: ['other'], verdicts: [{ gate: 'pacto.diff', phase: 'pre', result: 'Unknown', citation: { classification: 'POTENTIAL_BREAKING' } }] });
    fns.propose.mockRejectedValueOnce(new ApiError(409, 'moved', 'PlanStale', { intentDigest: 'sha256:j', type: 'pacto.dev/promote', predicted: 'Proceeds' }));
    fns.propose.mockResolvedValueOnce({ operation: { metadata: { name: 'new-op', namespace: 'prod' } } });
    const { target, destroy } = render(DeliveryEnvironmentsView);
    await settle();
    const board = target.querySelector('[data-testid="environments-board"]')!;
    expect(text(board as HTMLElement)).toContain('Verified');
    expect(text(board as HTMLElement)).toContain('Paused');
    expect(text(board as HTMLElement)).toContain('1 waiting');
    expect(text(board as HTMLElement)).toContain('Cannot promote: CandidateUnverified');
    expect(text(board as HTMLElement)).toContain('Nothing yet');
    (board.querySelector('button[aria-label^="Promote payments in prod"]') as HTMLButtonElement).click();
    await settle();
    expect(fns.plan).toHaveBeenCalledWith({ namespace: 'prod', stage: 'pay', type: 'pacto.dev/promote', to: D2, from: D1 });
    const panel = target.querySelector('[data-testid="propose-panel"]')!;
    expect(text(panel as HTMLElement)).toContain('It would wait for a person to decide.');
    expect(text(panel as HTMLElement)).toContain('Only team-checkout may answer it.');
    (panel.querySelector('form') as HTMLFormElement).requestSubmit();
    await settle();
    expect(fns.propose).toHaveBeenLastCalledWith(expect.objectContaining({ intentDigest: 'sha256:i' }));
    expect(text(target)).toContain('Something changed since the plan');
    expect(text(target)).toContain('It would go ahead with no person involved.');
    (target.querySelector('[data-testid="propose-panel"] form') as HTMLFormElement).requestSubmit();
    await settle();
    expect(fns.propose).toHaveBeenLastCalledWith(expect.objectContaining({ intentDigest: 'sha256:j' }));
    expect(location.hash).toBe('#/delivery/changes/prod/new-op');
    destroy();
  });

  it('unpauses through a plan and shows refusals verbatim', async () => {
    fns.environments.mockResolvedValue({ ...listing, environments: [row] });
    fns.plan.mockRejectedValueOnce(new ApiError(403, 'operations is forbidden: User cannot create'));
    const { target, destroy } = render(DeliveryEnvironmentsView);
    await settle();
    (target.querySelector('button[aria-label="Unpause payments in prod"]') as HTMLButtonElement).click();
    await settle();
    expect(fns.plan).toHaveBeenCalledWith({ namespace: 'prod', stage: 'pay', type: 'pacto.dev/unfreeze' });
    const refusal = target.querySelector('[data-testid="refusal"]')!;
    expect(text(refusal as HTMLElement)).toContain('The cluster refused.');
    expect(text(refusal as HTMLElement)).toContain('operations is forbidden: User cannot create');
    (target.querySelector('[data-testid="propose-panel"] button') as HTMLButtonElement).click();
    await settle();
    expect(target.querySelector('[data-testid="propose-panel"]')).toBeNull();
    destroy();
  });

  it('plans again when another row is clicked, and gives focus back when the plan closes', async () => {
    fns.environments.mockResolvedValue({ ...listing, environments: [row, { ...row, service: 'ledger', stage: 'led', candidate: D1, pointer: D2 }] });
    fns.plan.mockImplementation(async (req: { stage: string }) => ({ intentDigest: 'sha256:' + req.stage, type: 'pacto.dev/promote', predicted: 'Proceeds' }));
    fns.propose.mockResolvedValue({ operation: { metadata: { name: 'n', namespace: 'prod' } } });
    const { target, destroy } = render(DeliveryEnvironmentsView);
    await settle();
    (target.querySelector('button[aria-label^="Promote payments in prod"]') as HTMLButtonElement).click();
    await settle();
    const unpauseLedger = target.querySelector('button[aria-label="Unpause ledger in prod"]') as HTMLButtonElement;
    unpauseLedger.click();
    await settle();
    expect(fns.plan).toHaveBeenLastCalledWith({ namespace: 'prod', stage: 'led', type: 'pacto.dev/unfreeze' });
    const panel = target.querySelector('[data-testid="propose-panel"]') as HTMLElement;
    expect(text(panel)).toContain('prod/led');
    expect(document.activeElement?.id).toBe('pp-title');
    (panel.querySelector('form') as HTMLFormElement).requestSubmit();
    await settle();
    expect(fns.propose).toHaveBeenLastCalledWith(expect.objectContaining({ stage: 'led', intentDigest: 'sha256:led' }));
    location.hash = '#/delivery';
    unpauseLedger.click();
    await settle();
    (target.querySelector('[data-testid="propose-panel"]') as HTMLElement).dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await settle();
    expect(target.querySelector('[data-testid="propose-panel"]')).toBeNull();
    expect(document.activeElement).toBe(unpauseLedger);
    destroy();
  });

  it('says when there is no Stage', async () => {
    fns.environments.mockResolvedValue({ ...listing, environments: [] });
    const { target, destroy } = render(DeliveryEnvironmentsView);
    await settle();
    expect(text(target.querySelector('[data-testid="environments-empty"]') as HTMLElement)).toContain('No Stage exists in this cluster');
    destroy();
  });
});

// What must move first, as the promoter writes it into status.plan.order.
const ORDER = { moveFirst: [
  { service: 'orders', version: '1.8.0', accepts: '^2.0.0', stage: { namespace: 'prod', name: 'orders-prod' },
    candidate: { digest: 'sha256:' + '9'.repeat(64), version: '1.9.0', accepts: '>=2.0.0 <4.0.0', verifiedOn: { namespace: 'test', name: 'orders-test' } },
    // What moveFirstMessage writes, full digest included.
    message: 'orders 1.8.0 runs in prod and accepts payments ^2.0.0, not 3.0.0. orders 1.9.0 (sha256:999999999999) accepts it (>=2.0.0 <4.0.0) and was verified on test/orders-test. Ship orders first: promote sha256:' + '9'.repeat(64) + ' on Stage prod/orders-prod.' },
  { service: 'billing', accepts: '^2.0.0', unknown: 'no Stage delivers billing in prod',
    message: 'billing runs in prod and accepts payments ^2.0.0, not 3.0.0. Pacto cannot tell what would move it first: no Stage delivers billing in prod.' },
], unknown: ['the operational graph is partial (unresolved-reference)'] };

function heldOperation(over: Record<string, unknown> = {}) {
  const status = { phase: 'Held', question: 'checkout accepts only 1.x: ship?', reason: 'ApprovalRequired',
    plan: { intentDigest: 'sha256:i', type: 'pacto.dev/promote', predicted: 'Held', floor: { decision: 'approve', owners: ['team-checkout'], consumers: ['checkout'] },
      verdicts: [{ gate: 'pacto.impact', phase: 'pre', result: 'Unknown' }] },
    approvals: [{ name: 'a1', by: 'https://idp#dave', decision: 'reject', authTime: '1792560000', note: 'not yet' }],
    times: { admitted: '2026-10-21T10:00:00Z', approvalDeadline: '2026-10-22T10:00:00Z' } };
  return {
    explanation: { operation: 'prod/op1', summary: 'promote payments to 2.0.0', spec: { type: 'pacto.dev/promote', stage: 'pay', facet: 'pointer', actor: { user: 'agent-bot' }, params: { from: D1, to: D2 }, reason: 'ship', onBehalfOf: { user: 'https://idp#erin' } },
      status: { phase: 'Held', reason: 'ApprovalRequired', actorClass: 'agent' },
      verdicts: [{ gate: 'mira.health', phase: 'verify', result: 'Pass', rerun: 'pacto delivery explain op1', citation: { query: JSON.stringify({ signal: 'traces', where: [{ attr: 'service.name', eq: 'payments' }] }), from: 'a', to: 'b', spans: '40' } }],
      stage: { name: 'pay', frozen: true, frozenReason: 'Errored' } },
    operation: { metadata: { name: 'op1', namespace: 'prod' }, spec: { type: 'pacto.dev/promote', stage: 'pay', facet: 'pointer', actor: { user: 'agent-bot' } }, status },
    actions: ['approve', 'reject', 'cancel'],
    ...over,
  };
}

describe('change detail', () => {
  it('shows the plan, predicted next to actual verdicts, decisions, the timeline and a Mira link', async () => {
    fns.operation.mockResolvedValue(heldOperation());
    const { target, destroy } = render(DeliveryChangeView, { namespace: 'prod', name: 'op1', miraURL: 'https://mira.example' });
    await settle();
    expect(text(target)).toContain('Promote on prod/pay');
    expect(text(target)).toContain('agent-bot (agent), for erin');
    expect(text(target.querySelector('[data-testid="predicted-verdicts"]') as HTMLElement)).toContain('pacto.impact');
    const actual = target.querySelector('[data-testid="actual-verdicts"]')!;
    expect(actual.querySelector('[data-testid="mira-link"]')?.getAttribute('href')).toContain('https://mira.example/#/traces?q=');
    expect(text(actual as HTMLElement)).toContain('pacto delivery explain op1');
    expect(text(target)).toContain('Reject by dave');
    expect(text(target)).toContain('Decision due by');
    expect(target.querySelector('[data-testid="affected-services"] a[href="#/services/checkout"]')).not.toBeNull();
    destroy();
  });

  it('says what to update first while held, and stops saying it once the change moved on', async () => {
    // A candidate whose contract names no version is named by its digest.
    const order = structuredClone(ORDER);
    delete (order.moveFirst[0].candidate as { version?: string }).version;
    const held = heldOperation();
    held.operation.status.plan = { ...held.operation.status.plan, order } as typeof held.operation.status.plan;
    fns.operation.mockResolvedValue(held);
    let { target, destroy } = render(DeliveryChangeView, { namespace: 'prod', name: 'op1' });
    await settle();
    const safer = target.querySelector('[data-testid="decision-safer"]') as HTMLElement;
    expect(safer.querySelector('h3')?.textContent).toBe('Safer: update these first');
    // The safer way out is read before either button.
    const approveBtn = target.querySelector('[data-testid="decide-approve"]')!;
    expect(safer.compareDocumentPosition(approveBtn) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    // The promoter re-plans a held change while it waits, but it still needs a person once nothing holds it.
    const intro = safer.querySelector('h3')?.nextElementSibling?.textContent ?? '';
    expect(intro).toContain('Pacto keeps this list current while the change waits');
    expect(intro).not.toContain('propose this change again');
    expect(intro).not.toContain('no longer holds');
    const mf = target.querySelector('[data-testid="move-first"]') as HTMLElement;
    expect(linkTo(mf, '#/delivery/history?namespace=test&stage=orders-test')?.textContent).toBe('orders 999999999999, verified on test/orders-test');
    destroy();

    held.explanation.status = { phase: 'Running' } as typeof held.explanation.status;
    fns.operation.mockResolvedValue(held);
    ({ target, destroy } = render(DeliveryChangeView, { namespace: 'prod', name: 'op1' }));
    await settle();
    expect(target.querySelector('[data-testid="move-first"]')).toBeNull();
    destroy();

    // Held again after it shipped (the verify window stayed Unknown): the
    // provider already runs, so what had to move first is history.
    held.explanation.status = { phase: 'Held' } as typeof held.explanation.status;
    held.operation.status = { ...held.operation.status, applied: true } as typeof held.operation.status;
    fns.operation.mockResolvedValue(held);
    ({ target, destroy } = render(DeliveryChangeView, { namespace: 'prod', name: 'op1' }));
    await settle();
    expect(target.querySelector('[data-testid="decision-panel"]')).not.toBeNull();
    expect(target.querySelector('[data-testid="move-first"]')).toBeNull();
    destroy();
  });

  it('says what each answer does, weighs both buttons alike and asks why before a breaking change ships', async () => {
    const held = heldOperation({ versions: { [D1]: '2.3.0', [D2]: '3.0.0' }, people: { 'agent-bot': 'pay-agent' } });
    held.operation.spec = { ...held.operation.spec, params: { from: D1, to: D2 } } as typeof held.operation.spec;
    held.operation.status.plan = { ...held.operation.status.plan, floor: { decision: 'approve', owners: ['team-checkout'], consumers: ['checkout', 'orders', 'billing'] },
      verdicts: [
        { gate: 'pacto.published', phase: 'pre', result: 'Pass' },
        { gate: 'pacto.diff', phase: 'pre', result: 'Pass', message: 'BREAKING: 3 contract changes', citation: { classification: 'BREAKING' } },
        { gate: 'pacto.impact', phase: 'pre', result: 'Pass', message: 'breaks checkout, orders', citation: { classification: 'BREAKING' } },
      ] } as typeof held.operation.status.plan;
    fns.operation.mockResolvedValue(held);
    fns.decide.mockResolvedValue({ approval: 'op1-abc' });
    const { target, destroy } = render(DeliveryChangeView, { namespace: 'prod', name: 'op1' });
    await settle();
    const panel = target.querySelector('[data-testid="decision-panel"]') as HTMLElement;
    const outcomes = text(panel.querySelector('[data-testid="decision-consequences"]') as HTMLElement);
    expect(outcomes).toContain('If you approve');
    expect(outcomes).toContain('3.0.0 goes to prod/pay now.');
    expect(outcomes).toContain('checkout, orders and billing do not accept it and may break until they are updated.');
    expect(outcomes).toContain('It is kept as your decision, with your name, your note and when you signed in.');
    expect(outcomes).toContain('You can undo it from the history of the environment.');
    expect(outcomes).toContain('If you reject');
    expect(outcomes).toContain('Nothing moves: prod/pay keeps running 2.3.0.');
    expect(outcomes).toContain('pay-agent sees your name and note as the reason.');
    expect(panel.querySelector('[data-testid="decision-breaking"]')?.textContent).toBe('Breaking');
    const approve = panel.querySelector('[data-testid="decide-approve"]') as HTMLButtonElement;
    const reject = panel.querySelector('[data-testid="decide-reject"]') as HTMLButtonElement;
    expect(approve.textContent).toBe('Approve shipping 3.0.0 to prod/pay');
    expect(reject.textContent).toBe('Reject: nothing moves');
    // Same weight: neither answer is the primary one.
    expect(approve.className).toBe(reject.className);
    expect(approve.classList.contains('btn-primary')).toBe(false);
    // A breaking change ships only with a note saying why; rejecting needs none.
    expect(approve.disabled).toBe(true);
    expect(reject.disabled).toBe(false);
    expect(text(panel)).toContain('required to approve a breaking change');
    expect(panel.querySelector('textarea')?.getAttribute('aria-describedby')).toBe('dp-note-why');
    const area = panel.querySelector('textarea') as HTMLTextAreaElement;
    area.value = '   ';
    area.dispatchEvent(new Event('input'));
    flushSync();
    expect(approve.disabled).toBe(true);
    area.value = 'orders 1.4 is ready for payments 3';
    area.dispatchEvent(new Event('input'));
    flushSync();
    expect(approve.disabled).toBe(false);
    expect(panel.querySelector('[data-testid="decision-note-required"]')).toBeNull();
    // The plan's evidence checks say what they found, never a green Pass on a breaking change.
    const predicted = target.querySelector('[data-testid="predicted-verdicts"]') as HTMLElement;
    const badges = [...predicted.querySelectorAll('[data-testid="verdict-badge"]')].map((b) => b.textContent);
    expect(badges).toEqual(['Pass', 'Checked', 'Checked']);
    expect([...predicted.querySelectorAll('[data-testid="verdict-chip"]')].map((b) => b.textContent)).toEqual(['Breaking', 'Breaking']);
    expect(predicted.querySelectorAll('.badge-ok').length).toBe(1);
    approve.click();
    await settle();
    expect(fns.decide).toHaveBeenCalledWith('prod', 'op1', 'approve', 'orders 1.4 is ready for payments 3');
    destroy();
  });

  it('names the change itself when the operation names no version, and asks no note when nothing breaks', async () => {
    const held = heldOperation({ actions: ['accept-unknown', 'reject'] });
    held.operation.status.plan = { ...held.operation.status.plan, floor: { decision: 'accept-unknown', consumers: ['orders'] } } as typeof held.operation.status.plan;
    held.operation.spec = { ...held.operation.spec, actor: undefined } as unknown as typeof held.operation.spec;
    fns.operation.mockResolvedValue(held);
    const { target, destroy } = render(DeliveryChangeView, { namespace: 'prod', name: 'op1' });
    await settle();
    const panel = target.querySelector('[data-testid="decision-panel"]') as HTMLElement;
    expect(panel.querySelector('[data-testid="decide-approve"]')?.textContent).toBe('Accept the unknown and ship this change');
    expect((panel.querySelector('[data-testid="decide-approve"]') as HTMLButtonElement).disabled).toBe(false);
    const outcomes = text(panel.querySelector('[data-testid="decision-consequences"]') as HTMLElement);
    expect(outcomes).toContain('this change goes to prod/pay now.');
    expect(outcomes).toContain('Pacto could not tell whether orders accept it.');
    expect(outcomes).toContain('Nothing moves: prod/pay keeps running what it runs now.');
    expect(outcomes).toContain('whoever asked sees your name and note as the reason.');
    expect(panel.querySelector('[data-testid="decision-breaking"]')).toBeNull();
    destroy();
  });

  it('sends a decision, and after a refused stale login keeps it for a fresh sign-in', async () => {
    fns.operation.mockResolvedValue(heldOperation());
    fns.decide.mockRejectedValueOnce(new ApiError(401, 'your login is older than ten minutes: sign in again', 'FreshLoginRequired'));
    const assign = vi.fn();
    const orig = window.location;
    // jsdom cannot navigate; capture the fresh-login redirect instead.
    Object.defineProperty(window, 'location', { configurable: true, value: { ...orig, hash: '#/delivery/changes/prod/op1', assign } });
    try {
      const { target, destroy } = render(DeliveryChangeView, { namespace: 'prod', name: 'op1' });
      await settle();
      const panel = target.querySelector('[data-testid="decision-panel"]')!;
      (panel.querySelector('textarea') as HTMLTextAreaElement).value = 'checkout is ready';
      panel.querySelector('textarea')!.dispatchEvent(new Event('input'));
      (panel.querySelector('[data-testid="decide-approve"]') as HTMLButtonElement).click();
      await settle();
      expect(fns.decide).toHaveBeenCalledWith('prod', 'op1', 'approve', 'checkout is ready');
      const fresh = target.querySelector('[data-testid="fresh-login"]')!;
      expect(text(fresh as HTMLElement)).toContain('older than ten minutes');
      expect(assign).not.toHaveBeenCalled(); // the person decides when to leave
      (fresh.querySelector('button') as HTMLButtonElement).click();
      expect(assign).toHaveBeenCalledWith('/auth/login?return=%2F%23%2Fdelivery%2Fchanges%2Fprod%2Fop1&fresh=1');
      expect(JSON.parse(sessionStorage.getItem('pacto.delivery.pending')!)).toEqual({ namespace: 'prod', name: 'op1', decision: 'approve', note: 'checkout is ready' });
      destroy();
    } finally {
      Object.defineProperty(window, 'location', { configurable: true, value: orig });
    }
  });

  it('shows the kept decision after the fresh sign-in and sends it only on a click', async () => {
    fns.session.mockResolvedValue(shared({ fresh: true }));
    fns.operation.mockResolvedValue(heldOperation());
    fns.decide.mockResolvedValue({ approval: 'op1-abc' });
    keepPending({ namespace: 'prod', name: 'op1', decision: 'approve', note: 'kept note' });
    const { target, destroy } = render(DeliveryChangeView, { namespace: 'prod', name: 'op1' });
    await settle();
    expect(text(target.querySelector('[data-testid="decision-kept"]') as HTMLElement)).toContain('Your choice was Approve');
    expect(fns.decide).not.toHaveBeenCalled();
    expect((target.querySelector('[data-testid="decision-panel"] textarea') as HTMLTextAreaElement).value).toBe('kept note');
    target.querySelector<HTMLButtonElement>('[data-testid="decide-approve"]')!.click();
    await settle();
    expect(fns.decide).toHaveBeenCalledWith('prod', 'op1', 'approve', 'kept note');
    expect(text(target.querySelector('[data-testid="decision-recorded"]') as HTMLElement)).toContain('recorded as op1-abc');
    // The pressed button is gone: focus lands on what happened.
    expect(document.activeElement).toBe(target.querySelector('[data-testid="decision-recorded"]'));
    expect(sessionStorage.getItem('pacto.delivery.pending')).toBeNull();
    destroy();
  });

  it('accepts an unknown on a change held after it shipped', async () => {
    const op = heldOperation({ actions: ['accept-unknown', 'reject'], versions: { [D1]: '1.0.0', [D2]: '2.0.0' } });
    op.operation.status = { ...op.operation.status, applied: true } as typeof op.operation.status;
    op.operation.spec = { ...op.operation.spec, params: { from: D1, to: D2 } } as typeof op.operation.spec;
    fns.operation.mockResolvedValue(op);
    fns.decide.mockResolvedValue({ approval: 'op1-acc' });
    const { target, destroy } = render(DeliveryChangeView, { namespace: 'prod', name: 'op1' });
    await settle();
    const accept = target.querySelector<HTMLButtonElement>('[data-testid="decide-approve"]')!;
    expect(accept.textContent).toBe('Accept the unknown and keep 2.0.0');
    // Rejecting a promote that already shipped rolls it back.
    expect(target.querySelector('[data-testid="decide-reject"]')?.textContent).toBe('Reject and roll back to 1.0.0');
    expect(text(target.querySelector('[data-testid="decision-consequences"]') as HTMLElement)).toContain('2.0.0 stays on prod/pay, and the checks that ended Unknown count as accepted.');
    expect(text(target.querySelector('[data-testid="decision-consequences"]') as HTMLElement)).toContain('prod/pay rolls back to 1.0.0.');
    accept.click();
    await settle();
    expect(fns.decide).toHaveBeenCalledWith('prod', 'op1', 'accept-unknown', '');
    destroy();
  });

  it('shows refusals: not your team, an agent, a local dashboard, a signed-out person and a server refusal', async () => {
    fns.operation.mockResolvedValue(heldOperation({ actions: ['cancel'] }));
    fns.session.mockResolvedValue(shared({ caller: human(['team-payments']) }));
    let r = render(DeliveryChangeView, { namespace: 'prod', name: 'op1' });
    await settle();
    expect(text(r.target.querySelector('[data-testid="decide-not-team"]') as HTMLElement)).toContain('Only team-checkout may answer this question; you are in team-payments');
    r.destroy();

    deliverySession.loaded = false;
    fns.session.mockResolvedValue(shared({ caller: { user: 'robot', class: 'agent', groups: ['pacto:agents'] } }));
    r = render(DeliveryChangeView, { namespace: 'prod', name: 'op1' });
    await settle();
    expect(text(r.target.querySelector('[data-testid="decide-agent"]') as HTMLElement)).toContain('agents may not decide');
    r.destroy();

    const own = heldOperation({ actions: ['cancel'] });
    own.operation.spec = { ...own.operation.spec, actor: { user: 'agent-bot' }, onBehalfOf: { user: 'https://idp#carol' } } as typeof own.operation.spec;
    fns.operation.mockResolvedValueOnce(own);
    deliverySession.loaded = false;
    fns.session.mockResolvedValue(shared({ caller: human(['team-checkout']) }));
    r = render(DeliveryChangeView, { namespace: 'prod', name: 'op1' });
    await settle();
    expect(text(r.target.querySelector('[data-testid="decide-own"]') as HTMLElement)).toContain('someone else must decide it');
    r.destroy();

    fns.session.mockResolvedValue({ mode: 'local', login: false, signedIn: true, fresh: false, caller: human([]) });
    r = render(DeliveryChangeView, { namespace: 'prod', name: 'op1' });
    await settle();
    expect(text(r.target.querySelector('[data-testid="decision-panel"]') as HTMLElement)).toContain('pacto delivery approve -n prod op1 --decision approve');
    expect(text(r.target)).toContain('Acting as carol (https://idp#carol) from this machine');
    r.destroy();

    fns.session.mockResolvedValue({ mode: 'shared', login: true, signedIn: false, fresh: false });
    r = render(DeliveryChangeView, { namespace: 'prod', name: 'op1' });
    await settle();
    expect(text(r.target.querySelector('[data-testid="decision-panel"]') as HTMLElement)).toContain('Sign in to decide');
    r.destroy();

    fns.session.mockResolvedValue(shared());
    fns.operation.mockResolvedValue(heldOperation());
    fns.decide.mockRejectedValue(new ApiError(403, 'only [team-checkout] may answer this question', 'NotYourTeam'));
    r = render(DeliveryChangeView, { namespace: 'prod', name: 'op1' });
    await settle();
    r.target.querySelector<HTMLButtonElement>('[data-testid="decide-reject"]')!.click();
    await settle();
    const refusal = r.target.querySelector('[data-testid="refusal"]')!;
    expect(refusal.getAttribute('data-code')).toBe('NotYourTeam');
    expect(text(refusal as HTMLElement)).toContain('Only the team the question names may answer it.');
    r.destroy();
  });

  it('cancels after a confirmation and rolls back through a plan', async () => {
    fns.operation.mockResolvedValue(heldOperation({ actions: ['cancel', 'rollback'] }));
    fns.cancel.mockRejectedValueOnce(new ApiError(409, 'the pointer already moved')).mockResolvedValueOnce({ cancelled: 'prod/op1' });
    fns.plan.mockResolvedValue({ intentDigest: 'sha256:r', type: 'pacto.dev/rollback', predicted: 'Proceeds' });
    const { target, destroy } = render(DeliveryChangeView, { namespace: 'prod', name: 'op1' });
    await settle();
    const btn = (label: string) => [...target.querySelectorAll('button')].find((b) => b.textContent === label)!;
    btn('Cancel this change').click(); await settle();
    btn('Keep it').click(); await settle();
    btn('Cancel this change').click(); await settle();
    btn('Yes, cancel it').click(); await settle();
    expect(text(target.querySelector('[data-testid="refusal"]') as HTMLElement)).toContain('the pointer already moved');
    btn('Cancel this change').click(); await settle();
    btn('Yes, cancel it').click(); await settle();
    expect(text(target)).toContain('Cancelled. Nothing changed.');
    expect(document.activeElement?.textContent).toBe('Cancelled. Nothing changed.');
    // Gone: nothing left to do on it.
    expect(btn('Cancel this change')).toBeUndefined();
    expect(btn('Roll back this change')).toBeUndefined();
    expect(target.querySelector('[data-testid="decision-panel"]')).toBeNull();
    destroy();
  });

  it('moves focus into the confirmation and back, and rolls back through a plan', async () => {
    fns.operation.mockResolvedValue(heldOperation({ actions: ['cancel', 'rollback'] }));
    fns.plan.mockResolvedValue({ intentDigest: 'sha256:r', type: 'pacto.dev/rollback', predicted: 'Proceeds' });
    const { target, destroy } = render(DeliveryChangeView, { namespace: 'prod', name: 'op1' });
    await settle();
    const btn = (label: string) => [...target.querySelectorAll('button')].find((b) => b.textContent === label)!;
    btn('Cancel this change').click(); await settle();
    expect(document.activeElement).toBe(btn('Keep it'));
    btn('Keep it').click(); await settle();
    expect(document.activeElement).toBe(btn('Cancel this change'));
    btn('Roll back this change').click(); await settle();
    expect(fns.plan).toHaveBeenCalledWith({ namespace: 'prod', stage: 'pay', type: 'pacto.dev/rollback', undoes: 'op1' });
    [...target.querySelectorAll<HTMLButtonElement>('[data-testid="propose-panel"] button')].find((b) => b.textContent === 'Cancel')!.click();
    await settle();
    expect(target.querySelector('[data-testid="propose-panel"]')).toBeNull();
    expect(document.activeElement).toBe(btn('Roll back this change'));
    destroy();
  });

  it('says when a change it shows is gone or could not be refreshed', async () => {
    fns.operation.mockResolvedValueOnce(heldOperation()).mockRejectedValueOnce(new ApiError(500, 'boom'))
      .mockRejectedValueOnce(new ApiError(404, 'operations "op1" not found'));
    const props = $state({ namespace: 'prod', name: 'op1', refreshTick: 0 });
    const { target, destroy } = render(DeliveryChangeView, props);
    await settle();
    expect(target.querySelector('[data-testid="decision-panel"]')).not.toBeNull();
    props.refreshTick = 1;
    await settle();
    expect(text(target.querySelector('[data-testid="change-stale"]') as HTMLElement)).toContain('may be out of date');
    expect(target.querySelector('[data-testid="decision-panel"]')).not.toBeNull();
    (target.querySelector('[data-testid="change-stale"] button') as HTMLButtonElement).click();
    await settle();
    expect(text(target.querySelector('[data-testid="change-gone"]') as HTMLElement)).toContain('This change is gone');
    expect(text(target)).toContain('Promote on prod/pay');
    expect(target.querySelector('[data-testid="decision-panel"]')).toBeNull();
    expect([...target.querySelectorAll('button')].find((b) => b.textContent === 'Cancel this change')).toBeUndefined();
    destroy();
  });

  it('tells a missing change apart from a failure', async () => {
    fns.operation.mockRejectedValue(new ApiError(404, 'operations "gone" not found'));
    let r = render(DeliveryChangeView, { namespace: 'prod', name: 'gone' });
    await settle();
    expect(text(r.target)).toContain('No such change');
    r.destroy();
    fns.operation.mockRejectedValue(new ApiError(500, 'boom'));
    r = render(DeliveryChangeView, { namespace: 'prod', name: 'gone' });
    await settle();
    expect(text(r.target)).toContain('Can’t read delivery');
    r.destroy();
  });
});

describe('history and published', () => {
  it('lists what happened per environment, with its namespace', async () => {
    fns.history.mockResolvedValue({ ...listing, histories: [
      { namespace: 'prod', asOf: listing.asOf, completeness: 'partial', note: 'Partial until records exist.', stage: { name: 'pay', pointer: D1 },
        entries: [{ seq: 3, operation: 'op1', type: 'pacto.dev/rollback', from: D2, to: D1, actor: 'carol', actorClass: 'human', outcome: 'Succeeded', verification: 'Verified', finished: '2026-10-21T11:00:00Z' }] },
      { namespace: 'test', asOf: listing.asOf, completeness: 'partial', note: '', stage: { name: 'pay', frozen: true }, entries: [] },
    ] });
    const { target, destroy } = render(DeliveryHistoryView, { namespace: 'prod', stage: 'pay' });
    await settle();
    expect(fns.history).toHaveBeenCalledWith('prod', 'pay');
    expect(text(target)).toContain('History of prod/pay');
    expect(target.querySelector('a[href="#/delivery/changes/prod/op1"]')?.textContent).toBe('Roll back');
    expect(text(target)).toContain('Succeeded, Verified');
    expect(text(target)).toContain('No change kept for this environment.');
    destroy();
  });

  it('says a history read from signed records, what is wrong with them and what was not recorded yet', async () => {
    fns.history.mockResolvedValue({ ...listing, histories: [
      { namespace: 'prod', asOf: listing.asOf, completeness: 'partial', source: 'records', verified: true, note: 'Read from the signed records.', stage: { name: 'pay', pointer: D1 },
        problems: ['seq 2 is a gap: the operation with local seq 2 was gone before it was recorded',
          'the record at seq 3 does not chain from sha256:bbbbbbbbbbbb at seq 2'],
        unreadable: ['sha256:bad: signature: signed by a key that is not trusted here', `sha256:${'e'.repeat(64)}: not trusted here`],
        entries: [
          { seq: 1, operation: 'op1', type: 'pacto.dev/promote', to: D1, actor: 'bot', actorClass: 'agent', outcome: 'Succeeded', kind: 'operation', record: 'sha256:' + 'a'.repeat(64) },
          { seq: 2, operation: '', type: '', actor: '', kind: 'gap', reason: 'Gap: gone', record: 'sha256:' + 'b'.repeat(64) },
          { seq: 3, operation: 'op3', type: '', actor: 'bot', kind: 'unrecordable', reason: 'Unrecordable: sensitive-value', record: 'sha256:' + 'c'.repeat(64) },
          { operation: 'op4', type: 'pacto.dev/promote', actor: 'bot', phase: 'Verifying' },
          { seq: 4, operation: 'op5', type: 'pacto.dev/promote', actor: 'bot', outcome: 'Succeeded', unverifiedRecord: 'sha256:' + 'd'.repeat(64) },
        ] },
      { namespace: 'test', asOf: listing.asOf, completeness: 'complete', source: 'records', verified: true, note: '', stage: { name: 'pay' }, entries: [] },
      { namespace: 'stage', asOf: listing.asOf, completeness: 'partial', source: 'records', note: '', stage: { name: 'pay' }, entries: [] },
      { namespace: 'dev', asOf: listing.asOf, completeness: 'partial', source: 'status', note: '', stage: { name: 'pay' },
        entries: [{ seq: 1, operation: 'op9', type: 'pacto.dev/promote', actor: 'bot', outcome: 'Succeeded' }] },
    ] });
    const { target, destroy } = render(DeliveryHistoryView);
    await settle();
    const sources = [...target.querySelectorAll('[data-testid="history-source"]')].map((e) => e.textContent);
    expect(sources).toEqual(['From signed records, partial', 'From signed records, complete', 'From records, not verified, partial', 'From status, partial']);
    expect(target.querySelector('[data-testid="history-problems"]')?.textContent).toContain('seq 2 is a gap');
    expect(target.querySelector('[data-testid="history-unreadable"]')?.textContent).toContain('not trusted here');
    // Ids are short inline; the full one is on hover and copied on a click.
    const ids = [...target.querySelectorAll('[data-testid^="history-"] button.dt-id')];
    expect(ids.map((b) => b.textContent)).toEqual(['bbbbbbbbbbbb', 'eeeeeeeeeeee']);
    expect(ids[0].getAttribute('title')).toBe(`sha256:${'b'.repeat(64)} (click to copy)`);
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    (ids[1] as HTMLButtonElement).click();
    expect(writeText).toHaveBeenCalledWith('sha256:' + 'e'.repeat(64));
    writeText.mockRejectedValueOnce(new Error('denied'));
    (ids[0] as HTMLButtonElement).click();
    await settle();
    expect(target.querySelector('[data-testid="history-unreadable"]')?.textContent).not.toContain('e'.repeat(20));
    expect(text(target)).toContain('aaaaaaaaaaaa');
    expect(text(target)).toContain('Gap');
    expect(target.querySelector('a[href="#/delivery/changes/prod/op3"]')?.textContent).toBe('Unrecordable');
    expect(text(target)).toContain('Not recorded yet');
    // A record that exists but did not verify is never "not recorded yet".
    expect(text(target)).toContain('Recorded, not verified');
    destroy();
  });

  it('says who decided each change, or that no person did, and whether its signature verified', async () => {
    fns.history.mockResolvedValue({ ...listing, people: { 'https://idp#carol': 'Carol' }, histories: [
      { namespace: 'prod', asOf: listing.asOf, completeness: 'complete', source: 'records', verified: true, note: '', stage: { name: 'pay', pointer: D2 },
        entries: [
          { seq: 1, operation: 'op1', type: 'pacto.dev/promote', to: D1, actor: 'bot', actorClass: 'agent', outcome: 'Succeeded', kind: 'operation', record: 'sha256:' + 'a'.repeat(64) },
          { seq: 2, operation: 'op2', type: 'pacto.dev/promote', from: D1, to: D2, actor: 'bot', actorClass: 'agent', outcome: 'Succeeded', kind: 'operation', record: 'sha256:' + 'b'.repeat(64),
            approvals: [{ by: 'https://idp#carol', decision: 'approve', operation: 'u' }, { decision: 'approve' }] },
          { seq: 3, operation: '', type: '', actor: '', kind: 'gap', record: 'sha256:' + 'c'.repeat(64) },
          { operation: 'op4', type: 'pacto.dev/promote', actor: 'bot', phase: 'Held' },
        ] },
      { namespace: 'stage', asOf: listing.asOf, completeness: 'partial', source: 'records', note: '', stage: { name: 'pay' },
        entries: [{ seq: 1, operation: 'op5', type: 'pacto.dev/promote', actor: 'bot', outcome: 'Rejected', kind: 'operation', record: 'sha256:' + 'd'.repeat(64),
          approvals: [{ by: 'dave', decision: 'reject', operation: 'u' }] }] },
    ] });
    const { target, destroy } = render(DeliveryHistoryView);
    await settle();
    expect([...target.querySelectorAll('th')].map((t) => t.textContent)).toContain('Decided by');
    const decided = [...target.querySelectorAll('[data-testid="history-decided"]')].map((e) => text(e as HTMLElement));
    expect(decided).toEqual(['No person', 'Carol (https://idp#carol) (Approve)', '–', 'Waiting for a person', 'dave (Reject)']);
    const sigs = [...target.querySelectorAll('[data-testid="history-signature"]')].map((e) => e.textContent);
    expect(sigs).toEqual(['Signature verified', 'Signature verified', 'Signature verified', 'Signature not verified']);
    destroy();
  });

  it('says when there is no history', async () => {
    fns.history.mockResolvedValue({ ...listing, histories: [] });
    const { target, destroy } = render(DeliveryHistoryView);
    await settle();
    expect(text(target)).toContain('No history yet');
    destroy();
  });

  it('lists published versions with their signature state and where they run', async () => {
    fns.published.mockResolvedValue({ ...listing, services: [{ service: 'payments', versions: [
      { digest: D2, version: '2.0.0', contract: 'oci://r/p@sha256:c', signed: true, stages: [{ namespace: 'prod', stage: 'pay', environment: 'prod', running: true, verified: true, failed: false }] },
      { digest: D1, signed: false, stages: [{ namespace: 'prod', stage: 'pay', environment: 'prod', running: false, verified: false, failed: true }] },
    ] }] });
    let r = render(DeliveryPublishView);
    await settle();
    expect(text(r.target)).toContain('2.0.0');
    expect(text(r.target)).toContain('Signature checked');
    expect(text(r.target)).toContain('prod: running, verified');
    expect(text(r.target)).toContain('Signature not checked here');
    expect(text(r.target)).toContain('prod: known, failed');
    r.destroy();
    fns.published.mockResolvedValue({ ...listing, services: [] });
    r = render(DeliveryPublishView);
    await settle();
    expect(text(r.target)).toContain('Nothing published yet');
    r.destroy();
  });
});

describe('a list whose refresh fails', () => {
  it.each([
    ['inbox', DeliveryInboxView, fns.inbox, { held: [], frozen: [], errored: [] }],
    ['environments', DeliveryEnvironmentsView, fns.environments, { environments: [] }],
    ['history', DeliveryHistoryView, fns.history, { histories: [] }],
    ['published', DeliveryPublishView, fns.published, { services: [] }],
  ])('%s keeps what it showed and says it may be out of date', async (_name, View, fn, body) => {
    fn.mockResolvedValueOnce({ ...listing, ...body }).mockRejectedValueOnce(new ApiError(503, 'cluster unreachable'))
      .mockResolvedValue({ ...listing, ...body });
    const props = $state({ refreshTick: 0 });
    const { target, destroy } = render(View, props);
    await settle();
    expect(target.querySelector('[data-testid="listing-stale"]')).toBeNull();
    props.refreshTick = 1;
    await settle();
    const stale = target.querySelector('[data-testid="listing-stale"]') as HTMLElement;
    expect(text(stale)).toContain('may be out of date');
    expect(stale.getAttribute('role')).toBe('alert');
    (stale.querySelector('button') as HTMLButtonElement).click();
    await settle();
    expect(fn).toHaveBeenCalledTimes(3);
    expect(target.querySelector('[data-testid="listing-stale"]')).toBeNull();
    destroy();
  });
});

describe('names, versions and one clear sentence', () => {
  it('says the person, the versions and a question without full digests in the inbox', async () => {
    fns.session.mockResolvedValue(shared({ name: 'Carol Diaz' }));
    fns.inbox.mockResolvedValue({ ...listing, frozen: [], errored: [], held: [
      { namespace: 'prod', name: 'op1', stage: 'pay', type: 'pacto.dev/promote', from: D1, to: D2, fromVersion: '2.4.0', toVersion: '3.0.0', phase: 'Held',
        question: `Promote to ${D2}?`, decision: 'approve', owners: ['team-checkout'] },
      { namespace: 'prod', name: 'op2', stage: 'pay', type: 'pacto.dev/promote', to: D2, toVersion: '3.0.0', phase: 'Held', decision: 'approve' },
    ] });
    const { target, destroy } = render(DeliveryInboxView);
    await settle();
    // The name is a claim Carol set herself, so the cluster's username sits next to it.
    expect(text(target)).toContain('Signed in as Carol Diaz (https://idp#carol)');
    expect(target.querySelector('[data-testid="delivery-identity"] [title]')).toBeNull();
    expect(target.querySelector('a[href="#/delivery/changes/prod/op1"]')?.textContent?.trim()).toBe('Promote on prod/pay: 2.4.0 to 3.0.0');
    expect(target.querySelector('a[href="#/delivery/changes/prod/op2"]')?.textContent?.trim()).toBe('Promote on prod/pay: 3.0.0');
    expect(text(target)).toContain('Promote to sha256:222222222222?');
    expect(text(target)).not.toContain('2'.repeat(64));
    destroy();
  });

  it('says what moves first in one sentence, never with a full digest', async () => {
    const order = { moveFirst: [
      ORDER.moveFirst[0],
      { service: 'ledger', version: '1.0.0', accepts: '^2.0.0', stage: { namespace: 'prod', name: 'ledger-prod' },
        candidate: { digest: D1, version: '2.0.0', verifiedOn: { namespace: 'test', name: 'ledger-test' } }, message: 'm' },
      { service: 'fraud', accepts: '^2.0.0', candidate: { digest: D2, accepts: '^3.0.0', verifiedOn: { namespace: 'test', name: 'fraud-test' } }, message: 'm' },
      { service: 'audit', version: '1.0.0', accepts: '^2.0.0', stage: { namespace: 'prod', name: 'audit-prod' }, message: 'm' },
      { service: 'mail', message: 'm' },
    ] };
    fns.inbox.mockResolvedValue({ ...listing, frozen: [], errored: [],
      held: [{ namespace: 'prod', name: 'op1', stage: 'pay', type: 'pacto.dev/promote', to: D2, phase: 'Held', decision: 'approve', order }] });
    const { target, destroy } = render(DeliveryInboxView);
    await settle();
    const items = [...target.querySelectorAll('[data-testid="inbox-move-first"] li.mf-item')].map((li) => text(li as HTMLElement).trim());
    expect(items[0]).toBe('Update orders first: orders 1.8.0 runs here and accepts only ^2.0.0; orders 1.9.0, verified on test/orders-test accepts the new version, so promote it on Stage prod/orders-prod.');
    expect(items[1]).toContain('ledger 2.0.0, verified on test/ledger-test no longer depends on it, so promote it on Stage prod/ledger-prod.');
    expect(items[2]).toBe('Update fraud first: it accepts only ^2.0.0; fraud 222222222222, verified on test/fraud-test accepts the new version, so ship it first.');
    expect(items[3]).toBe('Update audit first: audit 1.0.0 runs here and accepts only ^2.0.0, and no version verified upstream can move first yet (it runs on Stage prod/audit-prod).');
    expect(items[4]).toBe('Update mail first, and no version verified upstream can move first yet.');
    expect(text(target)).not.toContain('9'.repeat(64));
    destroy();
  });

  it('promotes to a version, not a digest', async () => {
    const row = { namespace: 'prod', stage: 'pay', service: 'payments', environment: 'prod', pointer: D1, version: '1.0.0', candidate: D2, candidateVersion: '2.0.0',
      verified: true, failed: false, frozen: false, autoHold: false, held: 0, ready: 'True', actions: ['promote'] };
    fns.environments.mockResolvedValue({ ...listing, environments: [row] });
    fns.plan.mockResolvedValue({ intentDigest: 'sha256:i', type: 'pacto.dev/promote', predicted: 'Proceeds' });
    const { target, destroy } = render(DeliveryEnvironmentsView);
    await settle();
    const btn = target.querySelector('button[aria-label="Promote payments in prod to 2.0.0"]') as HTMLButtonElement;
    expect(btn.textContent).toBe('Promote 2.0.0');
    btn.click();
    await settle();
    expect(target.querySelector('#pp-title')?.textContent?.trim()).toBe('Promote prod/pay: 2.0.0');
    destroy();
  });

  it('names versions and people on a change', async () => {
    fns.session.mockResolvedValue(shared({ name: 'Carol Diaz' }));
    const op = heldOperation({ versions: { [D1]: '2.4.0', [D2]: '3.0.0' }, people: { 'https://idp#dave': 'Dave Lee' } });
    op.explanation.summary = `promote payments to ${D2}`;
    op.operation.status.question = `Ship ${D2}?`;
    fns.operation.mockResolvedValue(op);
    const { target, destroy } = render(DeliveryChangeView, { namespace: 'prod', name: 'op1' });
    await settle();
    expect(text(target)).toContain('From2.4.0 111111111111To3.0.0 222222222222');
    expect(text(target)).toContain('Reject by Dave Lee');
    expect(text(target)).toContain('promote payments to 3.0.0 (222222222222)');
    expect(text(target)).toContain('Ship sha256:222222222222?');
    expect(text(target)).not.toContain('2'.repeat(64));
    destroy();
  });

  it('says when a version is unknown, with the short digest', async () => {
    fns.operation.mockResolvedValue(heldOperation({ versions: { [D2]: '3.0.0' } }));
    const { target, destroy } = render(DeliveryChangeView, { namespace: 'prod', name: 'op1' });
    await settle();
    expect(text(target)).toContain('From111111111111 (version unknown)To3.0.0 222222222222');
    destroy();
  });
});

describe('history blame and undo', () => {
  const REC = 'sha256:' + 'a'.repeat(64);
  const history = () => ({ ...listing, versions: { [D1]: '2.4.0', [D2]: '3.0.0' }, people: { 'https://idp#carol': 'Carol Diaz' }, histories: [
    { namespace: 'prod', asOf: listing.asOf, completeness: 'complete', source: 'records', verified: true, note: 'read from records', stage: { name: 'pay', pointer: D2 },
      entries: [
        { seq: 1, operation: 'op1', type: 'pacto.dev/promote', from: D1, to: D2, actor: 'https://idp#carol', actorClass: 'human', outcome: 'Succeeded',
          finished: '2026-10-21T11:00:05Z', kind: 'operation', record: REC, pointer: { before: D1, after: D2 } },
        { seq: 2, operation: 'op2', type: 'pacto.dev/promote', to: D1, actor: 'system:serviceaccount:agents:pay-agent', actorClass: 'agent', outcome: 'Failed', finished: 'soon' },
      ] },
    { namespace: 'test', asOf: listing.asOf, completeness: 'partial', source: 'status', note: '', stage: { name: 'pay', pointer: D1 }, entries: [] },
  ] });

  it('shows versions, readable actors and times, and who put the running version there', async () => {
    fns.history.mockResolvedValue(history());
    fns.blame.mockResolvedValue({ stage: { name: 'pay', pointer: D2 }, digest: D2, asOf: listing.asOf, completeness: 'complete', source: 'records',
      note: `the newest change that moved the Stage to ${D2}`, onBehalfOf: 'https://idp#erin', intentReason: 'ship the fix',
      change: { operation: 'op1', type: 'pacto.dev/promote', from: D1, to: D2, actor: 'system:serviceaccount:agents:pay-agent', actorClass: 'agent', finished: '2026-10-21T11:00:05Z' },
      approvals: [{ by: 'https://idp#carol', decision: 'approve', operation: 'u', authTime: '1792560000', note: 'ok' }],
      verdicts: [{ gate: 'pacto.diff', phase: 'pre', result: 'Pass' }], versions: { [D1]: '2.4.0', [D2]: '3.0.0' }, people: { 'https://idp#carol': 'Carol Diaz' } });
    const { target, destroy } = render(DeliveryHistoryView);
    await settle();
    const prod = target.querySelector('section[aria-label="History of prod/pay"]') as HTMLElement;
    const rows = [...prod.querySelectorAll('tbody tr')].map((r) => [...r.querySelectorAll('td')].map((td) => text(td as HTMLElement).trim()));
    expect(rows[0].slice(2, 7)).toEqual(['2.4.0 → 3.0.0', 'Carol Diaz (https://idp#carol) (human)', 'The person who asked', 'Succeeded', '2026-10-21 11:00 UTC']);
    expect(rows[1].slice(2, 7)).toEqual(['– → 2.4.0', 'pay-agent (system:serviceaccount:agents:pay-agent) (agent)', 'No person', 'Failed', 'soon']);
    expect(text(prod)).toContain('Runs 3.0.0 222222222222');
    // Only a change that moved the environment can be undone.
    expect(prod.querySelectorAll('button[aria-label^="Undo"]').length).toBe(1);

    const blameBtn = [...prod.querySelectorAll('button')].find((b) => b.textContent === 'Who put 3.0.0 here?') as HTMLButtonElement;
    blameBtn.focus();
    blameBtn.click();
    await settle();
    expect(fns.blame).toHaveBeenCalledWith('prod', 'pay');
    const panel = target.querySelector('[data-testid="blame-panel"]') as HTMLElement;
    expect(text(panel)).toContain('Who put 3.0.0 here');
    expect(text(panel.querySelector('[data-testid="blame-change"]') as HTMLElement)).toContain('Promote by pay-agent (system:serviceaccount:agents:pay-agent) (agent), for erin (https://idp#erin), from 2.4.0, finished 2026-10-21 11:00 UTC. Why: “ship the fix”.');
    expect(text(panel)).toContain('Approve by Carol Diaz (https://idp#carol)');
    expect(text(panel.querySelector('[data-testid="blame-verdicts"]') as HTMLElement)).toContain('pacto.diff');
    expect(text(panel.querySelector('[data-testid="blame-source"]') as HTMLElement).trim()).toBe('Read from its signed record. The newest change that moved the Stage to sha256:222222222222.');
    panel.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await settle();
    expect(target.querySelector('[data-testid="blame-panel"]')).toBeNull();
    expect(document.activeElement).toBe(blameBtn);
    destroy();
  });

  it('says when blame is unsigned, unknown or refused', async () => {
    fns.history.mockResolvedValue(history());
    const { target, destroy } = render(DeliveryHistoryView);
    await settle();
    const open = async (ns: string) => {
      const sec = target.querySelector(`section[aria-label="History of ${ns}/pay"]`) as HTMLElement;
      (sec.querySelector('button') as HTMLButtonElement).click();
      await settle();
      return target.querySelector('[data-testid="blame-panel"]') as HTMLElement;
    };
    fns.blame.mockResolvedValueOnce({ stage: { name: 'pay' }, digest: D2, completeness: 'partial', source: 'status', note: 'read from its status',
      change: { operation: 'op1', type: 'pacto.dev/promote', to: D2, actor: 'bob' }, unsigned: ['change', 'approvals'] });
    let panel = await open('prod');
    expect(text(panel)).toContain('Who put 222222222222 here');
    expect(text(panel)).toContain('No person approved it');
    expect(text(panel)).toContain('No signed record vouches for this: change, approvals come from the change\'s own status.');
    expect(text(panel)).toContain('The history is partial, so this may not be the whole story.');
    (panel.querySelector('button') as HTMLButtonElement).click();
    await settle();
    fns.blame.mockResolvedValueOnce({ stage: { name: 'pay' }, digest: D1, completeness: 'partial', source: 'status', note: 'unknown, not nobody', versions: { [D1]: '2.4.0' } });
    panel = await open('test');
    expect(text(panel)).toContain('Who put 2.4.0 here');
    expect(panel.querySelector('[data-testid="blame-change"]')).toBeNull();
    expect(text(panel.querySelector('[data-testid="blame-source"]') as HTMLElement).trim()).toBe('Unknown, not nobody. The history is partial, so this may not be the whole story.');
    (panel.querySelector('button') as HTMLButtonElement).click();
    await settle();
    fns.blame.mockRejectedValueOnce(new ApiError(403, 'no', 'Forbidden'));
    panel = await open('test');
    expect(text(panel)).toContain('no');
    destroy();
  });

  it('plans an undo, shows the command and proposes nothing on its own', async () => {
    fns.history.mockResolvedValue(history());
    fns.undo.mockResolvedValueOnce({ operation: 'prod/op1', kind: 'rollback', note: 'it is the change the Stage runs now, so a rollback bound to it undoes it',
      command: 'pacto delivery propose -n prod --stage pay --type rollback --undoes op1', plan: { intentDigest: 'sha256:i', type: 'pacto.dev/rollback', predicted: 'Proceeds' } });
    fns.plan.mockResolvedValue({ intentDigest: 'sha256:i', type: 'pacto.dev/rollback', predicted: 'Proceeds' });
    const { target, destroy } = render(DeliveryHistoryView);
    await settle();
    const undoBtn = target.querySelector('button[aria-label="Undo op1"]') as HTMLButtonElement;
    undoBtn.click();
    await settle();
    expect(fns.undo).toHaveBeenCalledWith('prod', 'op1', 'pay');
    let panel = target.querySelector('[data-testid="undo-panel"]') as HTMLElement;
    expect(text(panel.querySelector('[data-testid="undo-note"]') as HTMLElement).trim()).toBe('Roll back. It is the change the Stage runs now, so a rollback bound to it undoes it.');
    expect(text(panel)).toContain('pacto delivery propose -n prod --stage pay --type rollback --undoes op1');
    expect(fns.propose).not.toHaveBeenCalled();
    ([...panel.querySelectorAll('button')].find((b) => b.textContent === 'Plan this roll back') as HTMLButtonElement).click();
    await settle();
    expect(fns.plan).toHaveBeenCalledWith({ namespace: 'prod', stage: 'pay', type: 'pacto.dev/rollback', undoes: 'op1' });
    // Escape inside the plan closes the plan, not the undo.
    (panel.querySelector('[data-testid="propose-panel"]') as HTMLElement).dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
    await settle();
    expect(panel.querySelector('[data-testid="propose-panel"]')).toBeNull();
    expect(target.querySelector('[data-testid="undo-panel"]')).not.toBeNull();
    panel.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await settle();
    expect(target.querySelector('[data-testid="undo-panel"]')).toBeNull();

    fns.undo.mockResolvedValueOnce({ operation: 'prod/op1', kind: 'promote', note: `the Stage changed since, so going back to ${D1} is a promote`,
      command: 'pacto delivery propose --to ' + D1, plan: { intentDigest: 'sha256:i', type: 'pacto.dev/promote', to: D1, predicted: 'Held' }, versions: { [D1]: '2.4.0' } });
    undoBtn.click();
    await settle();
    panel = target.querySelector('[data-testid="undo-panel"]') as HTMLElement;
    expect(text(panel.querySelector('[data-testid="undo-note"]') as HTMLElement).trim()).toBe('Promote to 2.4.0. The Stage changed since, so going back to sha256:111111111111 is a promote.');
    ([...panel.querySelectorAll('button')].find((b) => b.textContent === 'Plan this promote') as HTMLButtonElement).click();
    await settle();
    expect(fns.plan).toHaveBeenLastCalledWith({ namespace: 'prod', stage: 'pay', type: 'pacto.dev/promote', to: D1 });
    expect(panel.querySelector('#pp-title')?.textContent?.trim()).toBe('Promote prod/pay: 2.4.0');
    ([...panel.querySelectorAll('button')].find((b) => b.textContent === 'Close') as HTMLButtonElement).click();
    await settle();

    fns.undo.mockResolvedValueOnce({ operation: 'prod/op1', kind: 'none', note: 'it moved nothing, so there is nothing to undo' });
    undoBtn.click();
    await settle();
    panel = target.querySelector('[data-testid="undo-panel"]') as HTMLElement;
    expect(text(panel.querySelector('[data-testid="undo-note"]') as HTMLElement).trim()).toBe('Nothing to undo. It moved nothing, so there is nothing to undo.');
    expect([...panel.querySelectorAll('button')].map((b) => b.textContent)).toEqual(['Close']);
    (panel.querySelector('button') as HTMLButtonElement).click();
    await settle();

    fns.undo.mockRejectedValueOnce(new ApiError(404, 'operation prod/op1 is not in the cluster', 'Refused'));
    undoBtn.click();
    await settle();
    expect(text(target.querySelector('[data-testid="undo-panel"]') as HTMLElement)).toContain('operation prod/op1 is not in the cluster');
    destroy();
  });
});

describe('refused publishes', () => {
  const pub = { ...listing, services: [] };

  it('says when the cluster keeps no record of them', async () => {
    fns.published.mockResolvedValue(pub);
    const { target, destroy } = render(DeliveryPublishView);
    await settle();
    expect(text(target.querySelector('[data-testid="refused-unrecorded"]') as HTMLElement)).toContain('This cluster keeps no record of refused publishes');
    destroy();
  });

  it('lists recent refusals with the bump they need, and says when the list may be incomplete', async () => {
    fns.published.mockResolvedValue(pub);
    fns.refusals.mockResolvedValue({ asOf: listing.asOf, recorded: true, completeness: 'partial', note: 'one registry could not be read', unreadable: ['registry.example: timeout'],
      refusals: [
        { service: 'payments', version: '2.4.3', check: 'version', requiredBump: 'major', baseline: '2.4.2', reason: 'removing a field is a breaking change',
          actor: 'system:serviceaccount:agents:pay-agent', at: '2026-10-22T09:00:00Z', verified: true },
        { service: 'orders', version: '1.5.0', check: 'sign', reason: 'publish.sign is not configured.', at: '2026-10-22T08:00:00Z', verified: false },
        { service: 'fraud', version: '1.0.1', check: 'version', requiredBump: 'odd', reason: 'x', at: 'later', verified: false },
      ] });
    const { target, destroy } = render(DeliveryPublishView);
    await settle();
    const sec = target.querySelector('[data-testid="refused-publishes"]') as HTMLElement;
    expect(text(sec.querySelector('[data-testid="refused-partial"]') as HTMLElement).trim()).toBe('This list may be incomplete: one registry could not be read. Could not read: registry.example: timeout.');
    const rows = [...sec.querySelectorAll('li')].map((li) => text(li as HTMLElement).trim());
    expect(rows[0]).toBe('payments 2.4.3 Needs a major version Signed record Removing a field is a breaking change. Compared with 2.4.2. By pay-agent (system:serviceaccount:agents:pay-agent), 2026-10-22 09:00 UTC');
    expect(rows[1]).toBe('orders 1.5.0 Refused: sign Publish.sign is not configured. 2026-10-22 08:00 UTC');
    expect(rows[2]).toContain('Needs odd');
    destroy();
  });

  it('says when none was refused, and when the list could not be read', async () => {
    fns.published.mockResolvedValue(pub);
    fns.refusals.mockResolvedValue({ asOf: listing.asOf, recorded: true, completeness: 'complete', refusals: [] });
    let r = render(DeliveryPublishView);
    await settle();
    expect(text(r.target)).toContain('No publish was refused.');
    r.destroy();
    fns.refusals.mockResolvedValue({ asOf: listing.asOf, recorded: true, completeness: 'partial' });
    r = render(DeliveryPublishView);
    await settle();
    expect(text(r.target)).toContain('No publish was refused that Pacto could read.');
    expect(text(r.target)).toContain('This list may be incomplete.');
    r.destroy();
    fns.refusals.mockRejectedValueOnce(new ApiError(500, 'boom'));
    fns.refusals.mockResolvedValueOnce({ asOf: listing.asOf, recorded: true, completeness: 'complete', refusals: [] });
    r = render(DeliveryPublishView);
    await settle();
    const sec = r.target.querySelector('[data-testid="refused-publishes"]') as HTMLElement;
    expect(text(sec)).toContain('Could not read refused publishes: boom');
    (sec.querySelector('button') as HTMLButtonElement).click();
    await settle();
    expect(text(sec)).toContain('No publish was refused.');
    r.destroy();
  });
});
