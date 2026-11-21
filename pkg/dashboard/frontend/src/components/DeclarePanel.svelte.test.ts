/**
 * The Declare panel (513-S6): asks for the plan only once open; signs through a fresh
 * login whose URL holds only the ticket; back from it shows the whole plan and
 * publishes on a click; without a signing login saves a draft; a draft is accepted
 * only after its own plan is loaded; refusals and the command always show.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';

const { planFn, acceptFn } = vi.hoisted(() => ({ planFn: vi.fn(), acceptFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { fleetDeclare: (...a: unknown[]) => planFn(...a), fleetDeclareAccept: (...a: unknown[]) => acceptFn(...a) } };
});
// @ts-expect-error — Svelte component has no declaration file
import DeclarePanel from './DeclarePanel.svelte';
import { ApiError } from '../lib/api.ts';

const KEY = 'default/k8s:shop/Deployment/cart';
const LONG_ID = 'ana.maria.long-identity-name@a-very-long-identity-provider-domain.example.com';
const COMMAND = 'pacto declare cart --namespace shop --registry ghcr.io/acme --intent sha256:i1';

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
function answer(patch: Record<string, any> = {}, planPatch: Record<string, any> = {}): Record<string, any> {
  return {
    plan: {
      service: 'cart', version: '0.1.0', bundleRepo: 'ghcr.io/acme/cart', state: 'draft', inferredFrom: KEY, deploy: 'none',
      ranges: [{ dependency: 'ledger', compatibility: 'unknown', proposed: '^2.3.0', observed: '2.3.1' }, { dependency: 'audit', compatibility: 'unknown' }],
      signer: { class: 'agent', source: 'none' }, intentDigest: 'sha256:i1',
      contract: { text: 'pactoVersion: "2.5"\nservice:\n  name: cart\n' }, record: { text: 'inferredFrom: cart\n' }, ...planPatch,
    },
    signing: { mode: 'none', reason: 'run the command in your terminal to sign' }, write: { available: true }, command: COMMAND, ...patch,
  };
}
const DRAFTS = [
  { tag: 'draft-2', digest: 'sha256:' + 'd'.repeat(64), state: 'draft', asker: { class: 'agent' }, onBehalfOf: 'ana', requested: ['audit ^1.4.0'], signed: false },
  { tag: 'draft-1', state: 'unreadable', asker: {}, signed: false, reason: 'not a bundle' },
];
const FRESH = { signing: { mode: 'fresh-login', identity: LONG_ID }, signTicket: 'tk1' };

let target: HTMLElement;
let comp: ReturnType<typeof mount> | null = null;
beforeEach(() => { planFn.mockReset(); acceptFn.mockReset(); sessionStorage.clear(); target = document.createElement('div'); document.body.appendChild(target); });
afterEach(() => { if (comp) { unmount(comp); comp = null; } target.remove(); });

const text = (el: Element | null) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim();
const q = (s: string) => target.querySelector(s);
async function render(props: Record<string, unknown> = {}) {
  comp = mount(DeclarePanel, { target, props: { serviceKey: KEY, open: true, ...props } });
  flushSync();
  await vi.waitFor(() => expect(q('[data-testid="declare"] dl, [role="alert"]')).toBeTruthy());
  flushSync();
}
const button = (label: string) => Array.from(target.querySelectorAll('button')).find((b) => text(b).startsWith(label)) as HTMLButtonElement;

describe('DeclarePanel', () => {
  it('asks for nothing while closed and plans once opened', async () => {
    planFn.mockResolvedValue(answer());
    const props = $state({ serviceKey: KEY, open: false });
    comp = mount(DeclarePanel, { target, props });
    flushSync();
    expect(planFn).not.toHaveBeenCalled();
    props.open = true;
    flushSync();
    await vi.waitFor(() => expect(planFn).toHaveBeenCalledTimes(1));
    expect(planFn).toHaveBeenCalledWith({ serviceKey: KEY, intentDigest: undefined, fromDraft: undefined });
  });

  it('with a signing login shows Sign, whose URL holds the ticket and never an accept list', async () => {
    planFn.mockResolvedValue(answer(FRESH));
    await render();
    const sign = q('[data-testid="declare-sign"]') as HTMLAnchorElement;
    expect(text(sign)).toBe(`Sign as ${LONG_ID}`);
    expect(sign.querySelector('bdi')?.textContent).toBe(LONG_ID);
    const href = sign.getAttribute('href') ?? '';
    expect(href).toContain('ticket=tk1');
    expect(href).not.toContain('accept');
    expect(decodeURIComponent(decodeURIComponent(href))).toContain('signed=sha256:i1');
    expect(text(q('[data-testid="declare-signer"]'))).toContain(`You, ${LONG_ID}, after you sign in again`);
  });

  it('back from the signing login re-renders the whole plan from the answer and publishes on a click', async () => {
    planFn.mockResolvedValue(answer(FRESH, { ranges: [{ dependency: 'ledger', compatibility: '^2.3.0', proposed: '^2.3.0', accepted: true }], state: 'declared' }));
    acceptFn.mockResolvedValue({ published: 'ghcr.io/acme/cart@sha256:p', tag: '0.1.0', signed: true });
    await render({ signed: 'sha256:i1' });
    expect(planFn).toHaveBeenCalledWith({ serviceKey: KEY, intentDigest: 'sha256:i1', fromDraft: undefined });
    expect(text(q('[data-testid="declare-signed-return"]'))).toBe(`Signed in again as ${LONG_ID}.`);
    expect((q('input[aria-label="Accept ledger range"]') as HTMLInputElement).checked).toBe(true);
    expect(q('[data-testid="declare-sign"]')).toBeNull();
    expect(acceptFn).not.toHaveBeenCalled();
    button('Publish 0.1.0').click();
    await vi.waitFor(() => expect(q('[data-testid="declare-result"]')).toBeTruthy());
    expect(acceptFn).toHaveBeenCalledWith({ serviceKey: KEY, accept: ['ledger'], intentDigest: 'sha256:i1', mode: 'sign', fromDraft: undefined });
    expect(text(q('[data-testid="declare-result"]'))).toBe(`Published 0.1.0, signed by you as ${LONG_ID}.`);
  });

  it('says the plan moved when the signing login came back for another intent', async () => {
    planFn.mockResolvedValue(answer(FRESH));
    await render({ signed: 'sha256:other' });
    expect(text(q('[data-testid="declare-moved"]'))).toBe('The plan changed while you signed in, so sign again');
    expect(q('[data-testid="declare-sign"]')).toBeTruthy();
  });

  it('without a signing login shows Save draft and the command, no Sign, and saving lists the draft', async () => {
    planFn.mockResolvedValueOnce(answer()).mockResolvedValueOnce(answer({ drafts: [{ ...DRAFTS[0], tag: 'draft-3' }], draftsTotal: 1 }));
    acceptFn.mockResolvedValue({ tag: 'draft-3', draft: true, published: 'ghcr.io/acme/cart@sha256:d', command: 'pacto declare cart --registry ghcr.io/acme --from-draft draft-3' });
    await render();
    expect(q('[data-testid="declare-sign"]')).toBeNull();
    expect(text(q('[data-testid="declare-signer"]'))).toBe('Nobody signs here: run the command in your terminal to sign');
    expect(text(q('[data-testid="declare-command"]'))).toContain(COMMAND);
    button('Save draft').click();
    await vi.waitFor(() => expect(q('[data-testid="declare-draft"]')).toBeTruthy());
    expect(acceptFn).toHaveBeenCalledWith({ serviceKey: KEY, accept: [], intentDigest: 'sha256:i1', mode: 'draft', fromDraft: undefined });
    expect(text(q('[data-testid="declare-result"]'))).toContain('Saved draft-3.');
    expect(text(q('[data-testid="declare-accept-command"]'))).toContain('pacto declare cart --registry ghcr.io/acme --from-draft draft-3');
  });

  it('with no write shows the reason and no Save draft', async () => {
    planFn.mockResolvedValue(answer({ ...FRESH, write: { available: false, reason: 'the installation gave the dashboard no push credential: run the command' } }));
    await render();
    expect(text(q('[data-testid="declare-no-write"]'))).toBe('the installation gave the dashboard no push credential: run the command');
    expect(q('[data-testid="declare-save-draft"]')).toBeNull();
    expect(q('[data-testid="declare-sign"]')).toBeNull();
    expect(text(q('[data-testid="declare-command"]'))).toContain(COMMAND);
  });

  it('replans when a range is checked, one mark per range row and none on the command', async () => {
    planFn.mockResolvedValueOnce(answer()).mockResolvedValueOnce(answer({}, {
      ranges: [{ dependency: 'ledger', compatibility: '^2.3.0', proposed: '^2.3.0', accepted: true }, { dependency: 'audit', compatibility: 'unknown' }], intentDigest: 'sha256:i2' }));
    await render();
    const rows = Array.from(target.querySelectorAll('[data-testid="declare-ranges"] .fpf-row'));
    expect(rows).toHaveLength(2);
    expect(rows.map((r) => r.querySelectorAll('.prov-mark').length)).toEqual([1, 1]);
    expect(q('[data-testid="declare-command"] .prov-mark')).toBeNull();
    expect(rows[0].classList.contains('fpf-inferred')).toBe(true);
    expect(text(rows[0])).toContain('keep unknown (proposed ^2.3.0)');
    const audit = q('input[aria-label="Accept audit range"]') as HTMLInputElement;
    expect(audit.disabled).toBe(true);
    const ledger = q('input[aria-label="Accept ledger range"]') as HTMLInputElement;
    ledger.click();
    await vi.waitFor(() => expect(planFn).toHaveBeenCalledTimes(2));
    expect(planFn).toHaveBeenLastCalledWith({ serviceKey: KEY, accept: ['ledger'] });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="declare-ranges"] .fpf-row')?.classList.contains('fpf-inferred')).toBe(false));
  });

  it('lists the drafts, an unsigned proposer as asserted, and accepts one only after loading its plan', async () => {
    const draftPlan = answer(FRESH, { intentDigest: 'sha256:dp', unbacked: ['owner set to ana'], state: 'declared' });
    planFn.mockResolvedValueOnce(answer({ ...FRESH, drafts: DRAFTS, draftsTotal: 2 })).mockResolvedValueOnce(draftPlan);
    await render();
    const rows = Array.from(target.querySelectorAll('[data-testid="declare-draft"]'));
    expect(rows.map((r) => text(r.querySelector('.dp-draft')))).toEqual(['draft-2 by an agent for ana, requests audit ^1.4.0', 'draft-1 is unreadable: not a bundle']);
    expect(rows[1].querySelector('.prov-mark')).toBeNull();
    expect(rows[0].querySelector('.prov-mark .prov-tip')?.textContent).toBe('Asserted by the proposer, unsigned: nobody proved ana asked for it');
    expect(rows[1].querySelector('button')).toBeNull();
    (rows[0].querySelector('button[aria-label="Accept draft-2"]') as HTMLButtonElement).click();
    await vi.waitFor(() => expect(q('[data-testid="declare-unbacked"]')).toBeTruthy());
    expect(planFn).toHaveBeenLastCalledWith({ serviceKey: KEY, fromDraft: { tag: 'draft-2', digest: DRAFTS[0].digest } });
    expect(acceptFn).not.toHaveBeenCalled();
    expect(text(q('[data-testid="declare-draft-plan"]'))).toContain('Accepting draft-2.');
    // What nothing backs comes before Sign, and Sign carries the draft over the login.
    const unbacked = q('[data-testid="declare-unbacked"]') as HTMLElement;
    const sign = q('[data-testid="declare-sign"]') as HTMLAnchorElement;
    expect(unbacked.compareDocumentPosition(sign) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(text(unbacked)).toContain('owner set to ana');
    expect(q('[data-testid="declare-save-draft"]')).toBeNull();
    sign.addEventListener('click', (e) => e.preventDefault());
    sign.click();
    expect(JSON.parse(sessionStorage.getItem('pacto-declare-draft:sha256:dp') ?? '')).toEqual({ tag: 'draft-2', digest: DRAFTS[0].digest });
  });

  it('back from signing a draft posts fromDraft with that plan digest', async () => {
    const from = { tag: 'draft-2', digest: DRAFTS[0].digest };
    sessionStorage.setItem('pacto-declare-draft:sha256:dp', JSON.stringify(from));
    planFn.mockResolvedValue(answer(FRESH, { intentDigest: 'sha256:dp', state: 'declared' }));
    acceptFn.mockResolvedValue({ tag: '0.1.0', signed: true, published: 'r@sha256:x' });
    await render({ signed: 'sha256:dp' });
    expect(planFn).toHaveBeenCalledWith({ serviceKey: KEY, intentDigest: 'sha256:dp', fromDraft: from });
    button('Publish 0.1.0').click();
    await vi.waitFor(() => expect(acceptFn).toHaveBeenCalled());
    expect(acceptFn).toHaveBeenCalledWith({ serviceKey: KEY, accept: undefined, intentDigest: 'sha256:dp', mode: 'sign', fromDraft: from });
    await vi.waitFor(() => expect(sessionStorage.getItem('pacto-declare-draft:sha256:dp')).toBeNull());
  });

  it('shows a refusal with its code and command, and a stale plan says what moved', async () => {
    planFn.mockResolvedValue(answer());
    acceptFn.mockResolvedValueOnce({ refusal: { code: 'NotAllowed', message: 'ghcr.io/acme/cart belongs to other/cart', command: 'pacto declare cart --namespace shop' } })
      .mockResolvedValueOnce({ refusal: { code: 'PlanStale', message: 'moved' }, plan: answer({}, { version: '0.2.0', intentDigest: 'sha256:i3' }) });
    await render();
    button('Save draft').click();
    await vi.waitFor(() => expect(q('[data-testid="refusal"]')).toBeTruthy());
    expect(q('[data-testid="refusal"]')?.getAttribute('data-code')).toBe('NotAllowed');
    expect(text(q('[data-testid="refusal"]'))).toContain('ghcr.io/acme/cart belongs to other/cart');
    expect(text(target)).toContain('pacto declare cart --namespace shop');
    button('Save draft').click();
    await vi.waitFor(() => expect(q('[data-testid="declare-moved"]')).toBeTruthy());
    expect(text(q('[data-testid="declare-moved"]'))).toBe('The plan changed since you read it: the version (0.1.0 to 0.2.0) moved. Read it again');
    expect(q('[data-testid="refusal"]')).toBeNull();
  });

  it('shows a refused plan and a failed request', async () => {
    planFn.mockResolvedValueOnce(answer({ plan: undefined, refusal: { code: 'WorkloadNotFound', message: 'no workload cart in shop' } }));
    await render();
    expect(text(q('[data-testid="refusal"]'))).toContain('no workload cart in shop');
    expect(text(q('[data-testid="declare-command"]'))).toContain(COMMAND);
    unmount(comp!); comp = null; target.innerHTML = '';
    planFn.mockRejectedValueOnce(new ApiError(403, 'sign in to declare', 'IdentityRequired'));
    await render();
    expect(q('[data-testid="refusal"]')?.getAttribute('data-code')).toBe('IdentityRequired');
    unmount(comp!); comp = null; target.innerHTML = '';
    acceptFn.mockRejectedValueOnce(new ApiError(403, 'you may not update cart', 'ReviewFailed'));
    planFn.mockResolvedValueOnce(answer());
    await render();
    button('Save draft').click();
    await vi.waitFor(() => expect(q('[data-testid="refusal"]')?.getAttribute('data-code')).toBe('ReviewFailed'));
  });

  it('keeps the files collapsed in focusable, labelled boxes and notes a truncated one', async () => {
    planFn.mockResolvedValue(answer({}, { contract: { text: 'x'.repeat(10), truncated: true }, notes: ['a note'], policyNote: 'owner policy', owner: 'team-a', unchanged: '' }));
    await render();
    for (const [id, label] of [['declare-contract', 'pacto.yaml'], ['declare-record', 'pacto.inferred.yaml']]) {
      const fold = q(`[data-testid="${id}"]`) as HTMLDetailsElement;
      expect(fold.open).toBe(false);
      const pre = fold.querySelector('pre') as HTMLElement;
      expect(pre.getAttribute('tabindex')).toBe('0');
      expect(pre.getAttribute('aria-label')).toBe(label);
      expect(pre.closest('bdi')).toBeNull();
    }
    expect(text(q('[data-testid="declare-contract"]'))).toContain('Showing the first 64 KiB.');
    expect(text(target)).toContain('a note');
    expect(text(target)).toContain('owner policy');
    expect(text(target)).toContain('team-a');
  });

  it('writes nothing for an unchanged plan and goes back from a draft plan', async () => {
    planFn.mockResolvedValueOnce(answer({ drafts: DRAFTS, draftsTotal: 25 }, { unchanged: '0.1.0' }))
      .mockResolvedValueOnce(answer({}, { unbackedUnchecked: 'the baseline is unreadable' }))
      .mockResolvedValueOnce(answer());
    await render();
    expect(text(target)).toContain('nothing changed since 0.1.0, so nothing is written');
    expect(q('[data-testid="declare-save-draft"]')).toBeNull();
    expect(text(q('[data-testid="declare-drafts"]'))).toContain('23 more');
    (q('[data-testid="declare-draft"] button[aria-label="Accept draft-2"]') as HTMLButtonElement).click();
    await vi.waitFor(() => expect(planFn).toHaveBeenCalledTimes(2));
    await vi.waitFor(() => expect(text(q('[data-testid="declare-unbacked"]'))).toContain('the baseline is unreadable'));
    button('Back to a new declaration').click();
    await vi.waitFor(() => expect(q('[data-testid="declare-draft-plan"]')).toBeNull());
    expect(planFn).toHaveBeenLastCalledWith({ serviceKey: KEY, accept: [] });
  });
});
