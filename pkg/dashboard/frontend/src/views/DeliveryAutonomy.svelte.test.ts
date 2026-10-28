/**
 * The path to autonomy, the would-have table on an observe Stage and the level
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
import { shadowLabel, heldButRan, levelLabel, levelHelp, proposalText, blockerLabel, blockerService, changeSummary, typeLabel } from '../lib/delivery.ts';
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
// The text a reader sees: a closed help tip stays in the DOM, hidden, so leave it out.
const text = (t: Element) => {
  const c = t.cloneNode(true) as Element;
  c.querySelectorAll('[hidden]').forEach((h) => h.remove());
  return c.textContent?.replace(/\s+/g, ' ') ?? '';
};

const page = {
  ...listing,
  contracted: { services: 12, uncontractedCallers: 3 },
  promotionsWithoutPerson: { count: 17, of: 20, untyped: 0 },
  stages: [
    { namespace: 'prod', stage: 'pay', service: 'payments', environment: 'prod', level: 'advise',
      proposal: { level: 'auto-nonbreaking', evidence: Array.from({ length: 10 }, (_, i) => `u${i}`) } },
    { namespace: 'test', stage: 'ledger', service: 'ledger', environment: 'test', level: 'observe',
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
  fns.session.mockResolvedValue({ mode: 'shared', login: true, signedIn: true, csrf: 'c', caller: { user: 'https://idp#carol', class: 'human', groups: [] } });
  location.hash = '#/delivery/autonomy';
});

describe('path to autonomy', () => {
  it('shows the share of services seen, promotions without a person, each level and its proposal', async () => {
    fns.autonomy.mockResolvedValue(page);
    const { target, destroy } = render(DeliveryAutonomyView);
    await settle();
    const share = text(target.querySelector('[data-testid="autonomy-contracted"]')!);
    expect(share).toContain('80%');
    expect(share).toContain('12 of the 15 services Pacto has seen');
    expect(share).toContain('3 uncontracted callers');
    expect(share).not.toMatch(/fleet/);
    const promos = text(target.querySelector('[data-testid="autonomy-promotions"]')!);
    expect(promos).toContain('17 of 20');
    // How it is counted is one (?) away.
    expect(promos).not.toContain('last 20 operations per environment');
    (target.querySelector('[data-testid="autonomy-promotions"] button[aria-label="About this count"]') as HTMLButtonElement).click();
    flushSync();
    expect(text(target.querySelector('[data-testid="autonomy-promotions"]')!)).toContain('last 20 operations per environment');
    // The page answers first: what stands in the way and what can move up now.
    expect(text(target.querySelector('[data-testid="page-answer"]')!)).toContain('3 blockers stand in the way. 1 Stage can move up now.');
    const rows = [...target.querySelectorAll('[data-testid="autonomy-stages"] tbody tr')];
    expect(text(rows[0])).toContain('Advise');
    expect(text(rows[0])).toContain('Pacto proposes Auto non-breaking: 10 verified promotions, no incident.');
    expect(text(rows[1])).toContain('1 blocker below.');
    expect(text(rows[1])).not.toContain('Nothing to propose yet');
    expect(text(rows[1].querySelector('[data-testid="would-have"]')!)).toBe('Pacto would have chosen to hold for team-checkout.');
    // A level says what it means behind its (?), not under every row.
    expect(text(rows[0])).not.toContain('A person proposes or approves every promote');
    expect(text(rows[2])).toContain('Not reported');
    expect(rows[1].querySelector('button.btn')).toBeNull();
    destroy();
  });

  it('proposes the next level through a plan, as a delivery.pacto.trianalab.io/autonomy operation', async () => {
    fns.autonomy.mockResolvedValue(page);
    fns.plan.mockResolvedValue({ intentDigest: 'sha256:i', type: 'delivery.pacto.trianalab.io/autonomy', predicted: 'Held', question: 'raise the level?', floor: { owners: ['team-payments'] }, verdicts: [] });
    fns.propose.mockResolvedValue({ operation: { metadata: { name: 'raise-1', namespace: 'prod' } } });
    const { target, destroy } = render(DeliveryAutonomyView);
    await settle();
    const btn = target.querySelector('button[aria-label="Propose Auto non-breaking for payments in prod"]') as HTMLButtonElement;
    btn.click();
    await settle();
    expect(fns.plan).toHaveBeenCalledWith({ namespace: 'prod', stage: 'pay', type: 'delivery.pacto.trianalab.io/autonomy', from: 'advise', to: 'auto-nonbreaking' });
    const panel = target.querySelector('[data-testid="propose-panel"]')!;
    expect(text(panel)).toContain('Level change prod/pay: Auto non-breaking');
    expect(text(panel.querySelector('button[type="submit"]')!)).toBe('Propose level change');
    (panel.querySelector('form') as HTMLFormElement).requestSubmit();
    await settle();
    expect(fns.propose).toHaveBeenCalledWith(expect.objectContaining({ type: 'delivery.pacto.trianalab.io/autonomy', to: 'auto-nonbreaking', intentDigest: 'sha256:i' }));
    expect(location.hash).toBe('#/delivery/changes/prod/raise-1');
    destroy();
  });

  it('pages Stages and blockers apart, caps the Stages a blocker names and never rounds a share to 0%', async () => {
    fns.autonomy.mockResolvedValue({ ...page, contracted: { services: 1, uncontractedCallers: 300 },
      page: { total: 400, offset: 0, limit: 25 }, blockersPage: { total: 60, offset: 25, limit: 25 },
      blockers: [{ kind: 'needs-verified', subject: 'payments', stages: Array.from({ length: 300 }, (_, i) => `ns/s${i}`) }],
      blockerKinds: [{ kind: 'needs-verified', count: 60 }, { kind: 'missing-contract', count: 1303 }], canMoveUp: 7 });
    fns.plan.mockReturnValue(new Promise(() => {}));
    const { target, destroy } = render(DeliveryAutonomyView, { blockers: '25', q: 'pay' });
    await settle();
    expect(text(target.querySelector('[data-testid="autonomy-contracted"] .au-value')!)).toBe('<1%');
    expect(target.querySelector('[data-testid="autonomy-pager-next"]')?.getAttribute('href')).toBe('#/delivery/autonomy?offset=25&q=pay&blockers=25');
    expect(target.querySelector('[data-testid="autonomy-blockers-pager-next"]')?.getAttribute('href')).toBe('#/delivery/autonomy?q=pay&blockers=50');
    const blocker = target.querySelector('[data-testid="autonomy-blockers"] li.au-blocker')!;
    expect(blocker.querySelectorAll('.au-stages li').length).toBe(3);
    // The row clips a long name; its detail says it whole.
    expect(blocker.querySelector('.au-detail .au-full code')?.textContent).toBe('payments');
    expect(text(blocker)).toContain('On 300 Stages');
    expect(text(blocker)).toContain('Show all 300');
    // A kind counts over every page, not only the blockers this page shows.
    expect(text(target.querySelector('[data-testid="blocker-kind-count"]')!)).toBe('60 in all, 1 on this page');
    // Every kind and its count, not only the kind this page shows.
    const kinds = [...target.querySelectorAll('[data-testid="autonomy-blocker-kinds"] li')].map((li) => text(li as HTMLElement).trim());
    expect(kinds).toEqual([expect.stringMatching(/^60 /), expect.stringMatching(/^1,303 /)]);
    // What can move up counts every page.
    expect(text(target.querySelector('[data-testid="page-answer"]')!)).toContain('7 Stages can move up now.');
    expect(text(target.querySelector('[data-testid="page-answer"]')!)).not.toContain('on this page');
    // The blockers are paged, so a Stage's count links to them rather than pointing below.
    expect(text(target)).not.toContain('below.');
    expect(target.querySelector('[data-testid="stage-blockers"]')?.getAttribute('href')).toMatch(/^#\/delivery\/autonomy\?q=/);
    // Propose opens its plan in a row under the Stage it is for.
    (target.querySelector('button[aria-label^="Propose"]') as HTMLButtonElement).click();
    await settle();
    expect(target.querySelector('[data-testid="propose-panel"]')?.closest('tr.au-plan-row')).not.toBeNull();
    destroy();
  });

  it('lists blockers with a fix to copy, and copying changes nothing', async () => {
    fns.autonomy.mockResolvedValue(page);
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    const { target, destroy } = render(DeliveryAutonomyView);
    await settle();
    const list = target.querySelector('[data-testid="autonomy-blockers"]')!;
    const items = [...list.querySelectorAll('li.au-blocker')];
    // Grouped by kind, one line per blocker that opens to its detail.
    const groups = [...list.querySelectorAll('section.au-group')];
    expect(groups.map((g) => text(g.querySelector('h3')!))).toEqual(['No contract 1', 'No version range declared 1', 'GitOps can still write the version 1']);
    expect(text(items[0].querySelector('summary')!)).toContain('k8s:pay/Deployment/legacy No Stage');
    expect(items[0].querySelector('details')!.open).toBe(false);
    expect(text(items[0])).toContain('pacto init --cluster --namespace pay --out ./pacto-drafts');
    expect(text(items[1])).toContain('checkout 1 Stage');
    expect(text(items[1])).toContain('On 1 Stage: prod/pay');
    expect(items[2].querySelector('button')).toBeNull();
    // A service subject opens its service from the detail, never from inside the summary;
    // a caller with no contract or a GitOps object has no page.
    expect(items[1].querySelector('summary a')).toBeNull();
    expect(items[1].querySelector('.au-full a')!.getAttribute('href')).toContain('checkout');
    expect(items[0].querySelector('.au-full a')).toBeNull();
    expect(items[2].querySelector('.au-full a')).toBeNull();
    (target.querySelector('button[aria-label="About fixes"]') as HTMLButtonElement).click();
    flushSync();
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
    expect(text(r.target)).toContain('1 uncontracted caller');
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
    frozen: false, autoHold: false, held: 0, ready: 'True: Observing', actions: [], level: 'observe',
    shadow: [
      { candidate: D2, predicted: 'Held', owners: ['team-checkout', 'team-ledger'], observedAt: '2026-10-25T09:00:00Z', verification: 'Verified' },
      { candidate: 'sha256:' + '3'.repeat(64), predicted: 'Proceeds', observedAt: '2026-10-24T09:00:00Z' },
      { candidate: 'sha256:' + '4'.repeat(64), predicted: 'Held', question: 'nobody owns it: accept?' },
    ] };

  it('shows the level on every Stage and the advisory would-have table on observe only', async () => {
    fns.environments.mockResolvedValue({ ...listing, environments: [row, { ...row, environment: 'prod', stage: 'ledger-prod', level: 'advise', shadow: undefined }] });
    const { target, destroy } = render(DeliveryEnvironmentsView);
    await settle();
    const levels = [...target.querySelectorAll('[data-testid="level"]')].map((b) => b.textContent);
    // Columns run the way changes travel: test before prod.
    expect(levels).toEqual(['Observe', 'Advise']);
    const tables = target.querySelectorAll('[data-testid="shadow"]');
    expect(tables.length).toBe(1);
    // One explanation for every observing Stage, not one per Stage.
    expect(text(target.querySelector('[data-testid="shadow-summary"]') as HTMLElement)).toContain('advisory and unsigned');
    expect(text(tables[0])).not.toContain('advisory and unsigned');
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

describe('the level on a change', () => {
  it('names the Stage level, and levels, not digests, on an autonomy operation', async () => {
    const spec = { type: 'delivery.pacto.trianalab.io/autonomy', stage: 'pay', facet: 'level', actor: { user: 'https://idp#carol' }, params: { from: 'advise', to: 'auto-nonbreaking' } };
    fns.operation.mockResolvedValue({
      explanation: { operation: 'prod/r1', spec, status: { phase: 'Held', actorClass: 'human' }, verdicts: [], stage: { name: 'pay' } },
      operation: { metadata: { name: 'r1', namespace: 'prod' }, spec, status: { phase: 'Held' } },
      actions: [], level: 'advise',
    });
    const { target, destroy } = render(DeliveryChangeView, { namespace: 'prod', name: 'r1' });
    await settle();
    expect(text(target.querySelector('[data-testid="change-level"]')!)).toContain('Advise');
    expect(target.querySelector('.page-hd-sub')?.textContent).toBe('The level of pay in prod from Advise to Auto non-breaking waits for a person.');
    expect(text(target)).not.toContain('version unknown');
    destroy();
  });
});

describe('ladder words', () => {
  it('says each level, prediction and blocker in plain words', () => {
    expect(levelLabel('autonomous')).toBe('Autonomous');
    expect(levelLabel('mystery')).toBe('mystery');
    expect(levelHelp('observe')).toContain('moves nothing');
    expect(levelHelp(undefined)).toContain('does not report a level');
    expect(shadowLabel({ predicted: 'Rejected' })).toBe('refuse');
    expect(shadowLabel({ predicted: 'Waits' })).toBe('wait until a person unfreezes it');
    expect(shadowLabel({ predicted: 'Held' })).toBe('ask: a person decides');
    expect(shadowLabel({})).toBe('not planned yet');
    expect(shadowLabel({ predicted: 'Odd' })).toBe('Odd');
    expect(heldButRan({ predicted: 'Rejected', observedAt: 'x' })).toBe(true);
    expect(blockerLabel('incident')).toBe('A release failed here');
    expect(blockerLabel('new-kind')).toBe('new-kind');
    expect(blockerLabel(undefined)).toBe('Blocker');
    expect(blockerService({ kind: 'incident', subject: 'payments' })).toBe('payments');
    expect(blockerService({ kind: 'incident', subject: 'sha256:abc' })).toBe('');
    expect(blockerService({ kind: 'incident', subject: '0f8e2a1c-1234-4abc-9def-0123456789ab' })).toBe('');
    expect(blockerService({ kind: 'missing-contract', subject: 'k8s:pay/Deployment/legacy' })).toBe('');
    expect(blockerService({ kind: 'no-observed-digest', subject: 'payments-ref' })).toBe('');
    expect(blockerService({ kind: 'needs-verified', subject: '' })).toBe('');
    expect(proposalText('observe', { level: 'advise', evidence: ['d'] })).toBe('Pacto proposes Advise: 1 version GitOps ran that verified, no incident.');
    expect(proposalText('advise', { level: 'auto-nonbreaking' })).toBe('Pacto proposes Auto non-breaking: 0 verified promotions, no incident.');
    expect(typeLabel('delivery.pacto.trianalab.io/autonomy')).toBe('Level change');
    expect(changeSummary({ spec: { type: 'delivery.pacto.trianalab.io/autonomy', stage: 's', params: { to: 'advise' } }, status: { phase: 'Held' } }, 'ns')).toBe('The level of s in ns to Advise waits for a person.');
  });
});
