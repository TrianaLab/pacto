/**
 * The path to autonomy, the would-have table on an observe Stage and the rung
 * on a change. `api` is mocked; the session store is the real one.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync, tick } from 'svelte';

const fns = vi.hoisted(() => ({ session: vi.fn(), autonomy: vi.fn(), environments: vi.fn(), operation: vi.fn(), plan: vi.fn(), propose: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return {
    ...actual,
    api: {
      session: () => fns.session(), deliveryAutonomy: () => fns.autonomy(), deliveryEnvironments: () => fns.environments(),
      deliveryOperation: (...a: unknown[]) => fns.operation(...a), deliveryPlan: (...a: unknown[]) => fns.plan(...a),
      deliveryPropose: (...a: unknown[]) => fns.propose(...a),
    },
  };
});

import { ApiError } from '../lib/api.ts';
import { deliverySession } from '../lib/deliverySession.svelte.ts';
import { shadowLabel, heldButRan, rungLabel, rungHelp, proposalText, blockerLabel, changeSummary, typeLabel } from '../lib/delivery.ts';
// @ts-expect-error — Svelte component has no declaration file
import DeliveryAutonomyView from './DeliveryAutonomyView.svelte';
// @ts-expect-error — Svelte component has no declaration file
import DeliveryEnvironmentsView from './DeliveryEnvironmentsView.svelte';
// @ts-expect-error — Svelte component has no declaration file
import DeliveryChangeView from './DeliveryChangeView.svelte';

const D1 = 'sha256:' + '1'.repeat(64);
const D2 = 'sha256:' + '2'.repeat(64);
const listing = { asOf: '2026-10-25T10:00:00Z', completeness: 'complete' };

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
const text = (t: Element) => t.textContent?.replace(/\s+/g, ' ') ?? '';

const page = {
  ...listing,
  contracted: { services: 12, uncontractedCallers: 3 },
  promotionsWithoutPerson: { count: 17, of: 20, untyped: 0 },
  stages: [
    { namespace: 'prod', stage: 'pay', service: 'payments', environment: 'prod', rung: 'advise',
      proposal: { rung: 'auto-nonbreaking', evidence: Array.from({ length: 10 }, (_, i) => `u${i}`) } },
    { namespace: 'test', stage: 'ledger', service: 'ledger', environment: 'test', rung: 'observe',
      proposal: { blockers: [{ kind: 'unlabelled-apply-ref', subject: 'ledger', message: 'label the applyRef' }] },
      lastShadow: { candidate: D2, predicted: 'Held', owners: ['team-checkout'] } },
    { namespace: 'old', stage: 'x', service: 'x', environment: 'old' },
  ],
  blockers: [
    { kind: 'missing-contract', subject: 'k8s:pay/Deployment/legacy', message: 'k8s:pay/Deployment/legacy calls payments at runtime but has no contract',
      fix: { command: 'pacto init --cluster --namespace pay --out ./pacto-drafts' } },
    { kind: 'undeclared-range', subject: 'checkout', stages: ['prod/pay'], fix: { snippet: 'dependencies:\n  - name: payments\n' } },
    { kind: 'unlabelled-apply-ref', subject: 'ledger', message: 'label the applyRef', stages: ['test/ledger'] },
  ],
};

beforeEach(() => {
  for (const f of Object.values(fns)) f.mockReset();
  deliverySession.data = null; deliverySession.error = null; deliverySession.loaded = false;
  fns.session.mockResolvedValue({ mode: 'shared', login: true, signedIn: true, fresh: true, csrf: 'c', caller: { user: 'https://idp#carol', class: 'human', groups: [] } });
  location.hash = '#/delivery/autonomy';
});

describe('path to autonomy', () => {
  it('shows the share of services seen, promotions without a person, each rung and its proposal', async () => {
    fns.autonomy.mockResolvedValue(page);
    const { target, destroy } = render(DeliveryAutonomyView);
    await settle();
    const share = text(target.querySelector('[data-testid="autonomy-contracted"]')!);
    expect(share).toContain('80%');
    expect(share).toContain('12 of the 15 services Pacto has seen: 3 workloads call a contracted service with no contract of their own.');
    expect(share).not.toMatch(/fleet/);
    const promos = text(target.querySelector('[data-testid="autonomy-promotions"]')!);
    expect(promos).toContain('17 of 20');
    expect(promos).toContain('last 20 operations per environment');
    const rows = [...target.querySelectorAll('[data-testid="autonomy-stages"] tbody tr')];
    expect(text(rows[0])).toContain('Advise');
    expect(text(rows[0])).toContain('Pacto proposes Auto non-breaking: 10 verified promotions, no incident.');
    expect(text(rows[1])).toContain('Nothing to propose yet. 1 blocker below.');
    expect(text(rows[1])).toContain('on Autonomous, Pacto would have chosen to hold for team-checkout');
    expect(text(rows[2])).toContain('Not reported');
    expect(rows[1].querySelector('button')).toBeNull();
    destroy();
  });

  it('proposes the next rung through a plan, as a delivery.pacto.trianalab.io/autonomy operation', async () => {
    fns.autonomy.mockResolvedValue(page);
    fns.plan.mockResolvedValue({ intentDigest: 'sha256:i', type: 'delivery.pacto.trianalab.io/autonomy', predicted: 'Held', question: 'raise the rung?', floor: { owners: ['team-payments'] }, verdicts: [] });
    fns.propose.mockResolvedValue({ operation: { metadata: { name: 'raise-1', namespace: 'prod' } } });
    const { target, destroy } = render(DeliveryAutonomyView);
    await settle();
    const btn = target.querySelector('button[aria-label="Propose Auto non-breaking for payments in prod"]') as HTMLButtonElement;
    btn.click();
    await settle();
    expect(fns.plan).toHaveBeenCalledWith({ namespace: 'prod', stage: 'pay', type: 'delivery.pacto.trianalab.io/autonomy', from: 'advise', to: 'auto-nonbreaking' });
    const panel = target.querySelector('[data-testid="propose-panel"]')!;
    expect(text(panel)).toContain('Rung change prod/pay: Auto non-breaking');
    expect(text(panel.querySelector('button[type="submit"]')!)).toBe('Propose rung change');
    (panel.querySelector('form') as HTMLFormElement).requestSubmit();
    await settle();
    expect(fns.propose).toHaveBeenCalledWith(expect.objectContaining({ type: 'delivery.pacto.trianalab.io/autonomy', to: 'auto-nonbreaking', intentDigest: 'sha256:i' }));
    expect(location.hash).toBe('#/delivery/changes/prod/raise-1');
    destroy();
  });

  it('lists blockers with a fix to copy, and copying changes nothing', async () => {
    fns.autonomy.mockResolvedValue(page);
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    const { target, destroy } = render(DeliveryAutonomyView);
    await settle();
    const list = target.querySelector('[data-testid="autonomy-blockers"]')!;
    const items = [...list.querySelectorAll('li')];
    expect(text(items[0])).toContain('No contract: k8s:pay/Deployment/legacy');
    expect(text(items[0])).toContain('pacto init --cluster --namespace pay --out ./pacto-drafts');
    expect(text(items[1])).toContain('No version range declared: checkout (prod/pay)');
    expect(items[2].querySelector('button')).toBeNull();
    expect(text(target)).toContain('Copying a fix changes nothing in the cluster');
    expect(text(target)).not.toMatch(/one-click/i);
    (items[1].querySelector('button') as HTMLButtonElement).click();
    await settle();
    expect(writeText).toHaveBeenCalledWith('dependencies:\n  - name: payments\n');
    expect(text(items[1])).toContain('Copied.');
    writeText.mockRejectedValueOnce(new Error('denied'));
    (items[0].querySelector('button') as HTMLButtonElement).click();
    await settle();
    expect(text(list)).not.toContain('Copied.');
    expect(fns.propose).not.toHaveBeenCalled();
    destroy();
  });

  it('says what it cannot know: no snapshot, no promotion, no Stage, no blocker, and a refusal', async () => {
    fns.autonomy.mockResolvedValue({ ...listing, completeness: 'partial', unreadable: ['books'], promotionsWithoutPerson: { count: 0, of: 0, untyped: 0 }, stages: [], blockers: [] });
    let r = render(DeliveryAutonomyView);
    await settle();
    expect(text(r.target)).toContain('Pacto has seen no service here yet, so it cannot say.');
    expect(text(r.target)).toContain('No promotion succeeded in the last 20 operations per environment.');
    r.destroy();
    // Operations with no type: Pacto cannot tell, so it never claims none succeeded.
    fns.autonomy.mockResolvedValue({ ...listing, promotionsWithoutPerson: { count: 0, of: 0, untyped: 4 }, stages: [], blockers: [] });
    r = render(DeliveryAutonomyView);
    await settle();
    expect(text(r.target)).toContain('Pacto cannot tell yet which operations were promotions.');
    expect(text(r.target)).not.toContain('No promotion succeeded');
    r.destroy();
    fns.autonomy.mockResolvedValue({ ...listing, promotionsWithoutPerson: { count: 1, of: 2, untyped: 1 }, stages: [], blockers: [] });
    r = render(DeliveryAutonomyView);
    await settle();
    expect(text(r.target)).toContain('Pacto cannot tell what 1 more operation was.');
    r.destroy();
    fns.autonomy.mockResolvedValue({ ...listing, completeness: 'partial', unreadable: ['books'], promotionsWithoutPerson: { count: 0, of: 0, untyped: 0 }, stages: [], blockers: [] });
    r = render(DeliveryAutonomyView);
    await settle();
    expect(text(r.target.querySelector('[data-testid="autonomy-empty"]')!)).toContain('that you can see');
    expect(r.target.querySelector('[data-testid="autonomy-no-blockers"]')).not.toBeNull();
    r.destroy();
    fns.autonomy.mockResolvedValue({ ...page, contracted: { services: 1, uncontractedCallers: 1 }, stages: [], blockers: [] });
    r = render(DeliveryAutonomyView);
    await settle();
    expect(text(r.target)).toContain('1 workload calls a contracted service with no contract of its own.');
    expect(text(r.target.querySelector('[data-testid="autonomy-empty"]')!)).toContain('in this cluster');
    r.destroy();
    fns.autonomy.mockRejectedValue(new ApiError(404, 'Install pacto-operator with delivery.enabled=true', 'DeliveryNotInstalled'));
    r = render(DeliveryAutonomyView);
    await settle();
    expect(text(r.target)).toContain('delivery.enabled=true');
    r.destroy();
  });
});

describe('would have versus what happened', () => {
  const row = { namespace: 'test', stage: 'ledger', service: 'ledger', environment: 'test', pointer: D1, version: '1.0.0', verified: true, failed: false,
    frozen: false, autoHold: false, held: 0, ready: 'True: Observing', actions: [], rung: 'observe',
    shadow: [
      { candidate: D2, predicted: 'Held', owners: ['team-checkout', 'team-ledger'], observedAt: '2026-10-25T09:00:00Z', verification: 'Verified' },
      { candidate: 'sha256:' + '3'.repeat(64), predicted: 'Proceeds', observedAt: '2026-10-24T09:00:00Z' },
      { candidate: 'sha256:' + '4'.repeat(64), predicted: 'Held', question: 'nobody owns it: accept?' },
    ] };

  it('shows the rung on every Stage and the advisory would-have table on observe only', async () => {
    fns.environments.mockResolvedValue({ ...listing, environments: [row, { ...row, environment: 'prod', stage: 'ledger-prod', rung: 'advise', shadow: undefined }] });
    const { target, destroy } = render(DeliveryEnvironmentsView);
    await settle();
    const rungs = [...target.querySelectorAll('[data-testid="rung"]')].map((b) => b.textContent);
    expect(rungs).toEqual(['Advise', 'Observe']);
    const tables = target.querySelectorAll('[data-testid="shadow"]');
    expect(tables.length).toBe(1);
    expect(text(tables[0])).toContain('advisory and unsigned');
    const rows = [...tables[0].querySelectorAll('[data-testid="shadow-row"]')];
    expect(text(rows[0])).toContain('hold for team-checkout and team-ledger');
    expect(text(rows[0])).toContain('2026-10-25 09:00 UTC');
    expect(rows[0].querySelector('[data-testid="held-but-ran"]')).not.toBeNull();
    expect(text(rows[1])).toContain('ship');
    expect(text(rows[1])).toContain('Not checked yet');
    expect(rows[1].querySelector('[data-testid="held-but-ran"]')).toBeNull();
    expect(text(rows[2])).toContain('ask: nobody owns it: accept?');
    expect(text(rows[2])).toContain('Not reported yet');
    expect(rows[2].querySelector('[data-testid="held-but-ran"]')).toBeNull();
    destroy();
  });
});

describe('the rung on a change', () => {
  it('names the Stage rung, and rungs, not digests, on an autonomy operation', async () => {
    const spec = { type: 'delivery.pacto.trianalab.io/autonomy', stage: 'pay', facet: 'rung', actor: { user: 'https://idp#carol' }, params: { from: 'advise', to: 'auto-nonbreaking' } };
    fns.operation.mockResolvedValue({
      explanation: { operation: 'prod/r1', spec, status: { phase: 'Held', actorClass: 'human' }, verdicts: [], stage: { name: 'pay' } },
      operation: { metadata: { name: 'r1', namespace: 'prod' }, spec, status: { phase: 'Held' } },
      actions: [], rung: 'advise',
    });
    const { target, destroy } = render(DeliveryChangeView, { namespace: 'prod', name: 'r1' });
    await settle();
    expect(text(target.querySelector('[data-testid="change-rung"]')!)).toContain('Advise');
    expect(target.querySelector('.page-hd-sub')?.textContent).toBe('The rung of pay in prod from Advise to Auto non-breaking waits for a person.');
    expect(text(target)).not.toContain('version unknown');
    destroy();
  });
});

describe('ladder words', () => {
  it('says each rung, prediction and blocker in plain words', () => {
    expect(rungLabel('autonomous')).toBe('Autonomous');
    expect(rungLabel('mystery')).toBe('mystery');
    expect(rungHelp('observe')).toContain('moves nothing');
    expect(rungHelp(undefined)).toContain('does not report a rung');
    expect(shadowLabel({ predicted: 'Rejected' })).toBe('refuse');
    expect(shadowLabel({ predicted: 'Waits' })).toBe('wait until a person unfreezes it');
    expect(shadowLabel({ predicted: 'Held' })).toBe('ask: a person decides');
    expect(shadowLabel({})).toBe('not planned yet');
    expect(shadowLabel({ predicted: 'Odd' })).toBe('Odd');
    expect(heldButRan({ predicted: 'Rejected', observedAt: 'x' })).toBe(true);
    expect(blockerLabel('incident')).toBe('A release failed here');
    expect(blockerLabel('new-kind')).toBe('new-kind');
    expect(blockerLabel(undefined)).toBe('Blocker');
    expect(proposalText('observe', { rung: 'advise', evidence: ['d'] })).toBe('Pacto proposes Advise: 1 version GitOps ran that verified, no incident.');
    expect(proposalText('advise', { rung: 'auto-nonbreaking' })).toBe('Pacto proposes Auto non-breaking: 0 verified promotions, no incident.');
    expect(typeLabel('delivery.pacto.trianalab.io/autonomy')).toBe('Rung change');
    expect(changeSummary({ spec: { type: 'delivery.pacto.trianalab.io/autonomy', stage: 's', params: { to: 'advise' } }, status: { phase: 'Held' } }, 'ns')).toBe('The rung of s in ns to Advise waits for a person.');
  });
});
