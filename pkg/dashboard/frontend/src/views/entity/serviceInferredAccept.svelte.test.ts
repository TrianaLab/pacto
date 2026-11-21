/**
 * "Accept all proposed ranges" carried out from the service page (513-S6, 508-S7's
 * handoff): Sign and Save draft by what the person can do here, the acceptance drafts
 * folded inside the plan, accepting a draft through its own plan, the return from the
 * signing login, and all of it inside the Inferred block alone.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import { session } from '../../lib/session.svelte.ts';

const { detailFn, acceptFn, executeFn } = vi.hoisted(() => ({ detailFn: vi.fn(), acceptFn: vi.fn(), executeFn: vi.fn() }));
vi.mock('../../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../lib/api.ts')>();
  return { ...actual, api: { fleetEntityDetail: (...a: unknown[]) => detailFn(...a), fleetEntities: vi.fn(),
    fleetAccept: (...a: unknown[]) => acceptFn(...a), fleetAcceptExecute: (...a: unknown[]) => executeFn(...a) } };
});

// @ts-expect-error — Svelte component has no declaration file
import FleetEntityView from '../FleetEntityView.svelte';

const KEY = 'ghcr.io/acme/reports';
const preview = (items: unknown[]) => ({ total: items.length, count: items.length, truncated: false, items });
const proposal = { field: 'dependencies[ledger].compatibility', value: 'compatibility unknown', proposed: '^2.3.0', source: 'snapshot:reg/s@sha256:1', provenance: 'inferred' };
const detail = {
  meta: { schemaVersion: 'pacto.dev/fleet-product/v1', snapshotId: 's', asOf: '2026-11-21T10:00:00Z', completeness: 'complete', sources: [] },
  entity: { kind: 'service', key: KEY, label: 'reports', href: `/fleet/services/${encodeURIComponent(KEY)}` },
  actions: [],
  service: {
    summary: { targets: 0, revisions: 0, declaredDependencies: 0 }, ownership: { declared: false },
    capabilities: preview([]), sources: preview([]), deployments: preview([]), activeRevisions: preview([]), revisions: preview([]),
    dependencies: preview([]), unresolvedDependencies: preview([]), dependents: preview([]), referencedBy: preview([]), limitations: preview([]),
    inferredFields: preview([proposal]),
  },
};

const LONG = 'ana.maria.de.los.angeles.fernandez-gutierrez@very-long-subdomain.acme.example.com';
const plan = (extra: Record<string, unknown> = {}) => ({ service: KEY, version: '1.6.0', subject: 'ghcr.io/acme/reports@sha256:1',
  ranges: [{ field: 'dependencies[ledger].compatibility', value: '^2.3.0' }], whoMayAccept: 'ana (the DRI)', intentDigest: 'sha256:i', ...extra });
const answer = (execute: unknown, planExtra: Record<string, unknown> = {}) => ({ plan: plan(planExtra), command: 'pacto accept oci://ghcr.io/acme/reports --proposed --intent sha256:i', execute });
const fresh = { signing: { mode: 'fresh-login', identity: LONG }, write: { available: true }, signTicket: 't1' };
const none = { signing: { mode: 'none', reason: 'a token sign-in never signs' }, write: { available: true } };
const readOnly = { signing: { mode: 'none', reason: 'no signing login' }, write: { available: false, reason: 'the installation gave the dashboard no push credential: run the command' } };
const DRAFTS = { draftsTotal: 2, drafts: [
  { tag: 'pacto-accept-draft-2', state: 'draft', onBehalfOf: 'ana@acme.com', ranges: [{ field: 'dependencies[ledger].compatibility', value: '^2.3.0' }], signed: false },
  { tag: 'pacto-accept-draft-1', state: 'unreadable', reason: 'holds no acceptance draft', signed: false },
] };

const fact = (t: HTMLElement, id: string) => t.querySelector(`[data-testid="${id}"]`) as HTMLElement | null;
const text = (el: Element | null) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim();

let target: HTMLElement;
let comp: ReturnType<typeof mount> | null = null;
beforeEach(() => {
  detailFn.mockReset(); acceptFn.mockReset(); executeFn.mockReset();
  detailFn.mockResolvedValue(detail);
  session.data = { mode: 'shared', signedIn: true } as never;
  sessionStorage.clear();
  target = document.createElement('div');
  document.body.appendChild(target);
});
afterEach(() => { if (comp) { unmount(comp); comp = null; } target.remove(); });

async function render(signed = '') {
  comp = mount(FleetEntityView, { target, props: { kind: 'service', entityKey: KEY, refreshTick: 0, declareQuery: { open: false, signed } } });
  await vi.waitFor(() => expect(target.querySelector('.svc-entity')).toBeTruthy());
  flushSync();
}

async function openPlan() {
  await render();
  const sec = fact(target, 'service-inferred')!.querySelector('details') as HTMLDetailsElement;
  sec.open = true;
  sec.dispatchEvent(new Event('toggle'));
  flushSync();
  (target.querySelector('button.apr-action') as HTMLButtonElement).click();
  await vi.waitFor(() => expect(fact(target, 'accept-plan')?.querySelector('p.apr-text')).toBeTruthy());
  flushSync();
}

describe('service page: accept all proposed ranges here', () => {
  it('offers Sign with a fresh login and Save draft where the person can do both', async () => {
    acceptFn.mockResolvedValue(answer(fresh));
    await openPlan();
    const sign = fact(target, 'accept-sign') as HTMLAnchorElement;
    expect(text(sign)).toBe(`Sign as ${LONG}`);
    const href = new URL(sign.getAttribute('href')!, 'http://x');
    expect(href.pathname).toBe('/auth/sign');
    expect(href.searchParams.get('ticket')).toBe('t1');
    expect(href.searchParams.get('return')).toContain('signed=sha256%3Ai');
    expect(fact(target, 'accept-save-draft')).toBeTruthy();
    expect(fact(target, 'accept-no-sign')).toBeNull();
  });

  it('offers only Save draft where nobody signs, and says why', async () => {
    acceptFn.mockResolvedValue(answer(none));
    await openPlan();
    expect(fact(target, 'accept-sign')).toBeNull();
    expect(text(fact(target, 'accept-no-sign'))).toContain('a token sign-in never signs');
    executeFn.mockResolvedValue({ draft: 'ghcr.io/acme/reports:pacto-accept-draft-3', command: 'pacto accept ghcr.io/acme/reports --draft ghcr.io/acme/reports:pacto-accept-draft-3' });
    (fact(target, 'accept-save-draft') as HTMLButtonElement).click();
    await vi.waitFor(() => expect(fact(target, 'accept-result')).toBeTruthy());
    expect(executeFn).toHaveBeenCalledWith({ serviceKey: KEY, intentDigest: 'sha256:i', mode: 'draft', fromDraft: undefined });
    expect(text(fact(target, 'accept-result'))).toContain('Saved ghcr.io/acme/reports:pacto-accept-draft-3');
    expect(fact(target, 'accept-plan')!.querySelector('[title="pacto accept ghcr.io/acme/reports --draft ghcr.io/acme/reports:pacto-accept-draft-3"]')).toBeTruthy();
  });

  it('offers no button on a dashboard that cannot write, only the reason and the command', async () => {
    acceptFn.mockResolvedValue(answer(readOnly));
    await openPlan();
    expect(fact(target, 'accept-sign')).toBeNull();
    expect(fact(target, 'accept-save-draft')).toBeNull();
    expect(text(fact(target, 'accept-no-write'))).toContain('no push credential');
    // A dashboard that only plans answers no execute block at all.
    unmount(comp!); comp = null;
    acceptFn.mockResolvedValue(answer(undefined));
    await openPlan();
    expect(fact(target, 'accept-execute')).toBeNull();
    expect(text(fact(target, 'accept-plan'))).toContain('Run this to sign it with your Sigstore login');
  });

  it('shows a refusal with its command', async () => {
    acceptFn.mockResolvedValue(answer(none));
    await openPlan();
    executeFn.mockResolvedValue({ refusal: { code: 'NotAllowed', message: 'accepting ranges for reports needs update on Deployment billing/reports', command: 'pacto accept x' } });
    (fact(target, 'accept-save-draft') as HTMLButtonElement).click();
    await vi.waitFor(() => expect(text(fact(target, 'accept-plan'))).toContain('needs update on Deployment'));
    expect(fact(target, 'accept-result')).toBeNull();
  });

  it('lists the drafts folded, and accepts one through its own plan', async () => {
    acceptFn.mockResolvedValueOnce(answer(fresh, DRAFTS));
    await openPlan();
    const drafts = fact(target, 'accept-drafts') as HTMLDetailsElement;
    expect(drafts.open).toBe(false);
    expect(text(drafts.querySelector('summary'))).toBe('Drafts (2)');
    const rows = [...drafts.querySelectorAll('li')];
    expect(text(rows[0])).toContain('pacto-accept-draft-2 for ana@acme.com: dependencies[ledger].compatibility ^2.3.0');
    expect(rows[0].querySelector('button[aria-label="About pacto-accept-draft-2"]')).toBeTruthy();
    expect(rows[1].className).toContain('apr-muted');
    expect(text(rows[1])).toContain('cannot be read: holds no acceptance draft');
    expect(rows[1].querySelector('button')).toBeNull();
    acceptFn.mockResolvedValueOnce(answer({ ...fresh, signTicket: 't2' }, { draft: 'pacto-accept-draft-2', onBehalfOf: 'ana@acme.com', intentDigest: 'sha256:d' }));
    (rows[0].querySelector('button.apr-link') as HTMLButtonElement).click();
    await vi.waitFor(() => expect(fact(target, 'accept-draft-plan')).toBeTruthy());
    expect(acceptFn).toHaveBeenLastCalledWith(KEY, 'pacto-accept-draft-2');
    expect(text(fact(target, 'accept-draft-plan'))).toContain('Accepting pacto-accept-draft-2, written for ana@acme.com');
    // A draft is accepted with a signature only: no Save draft.
    expect(fact(target, 'accept-save-draft')).toBeNull();
    const sign = fact(target, 'accept-sign') as HTMLAnchorElement;
    target.addEventListener('click', (e) => e.preventDefault()); // jsdom does not navigate
    sign.dispatchEvent(new MouseEvent('click', { cancelable: true, bubbles: true }));
    expect(sessionStorage.getItem('pacto-accept-draft:sha256:d')).toBe('pacto-accept-draft-2');
  });

  it('a person who cannot sign sees the drafts but cannot accept them', async () => {
    acceptFn.mockResolvedValue(answer(none, DRAFTS));
    await openPlan();
    expect(fact(target, 'accept-drafts')!.querySelector('button.apr-link')).toBeNull();
  });

  it('comes back from the signing login to the open plan and attaches on one click', async () => {
    sessionStorage.setItem('pacto-accept-draft:sha256:d', 'pacto-accept-draft-2');
    acceptFn.mockResolvedValue(answer(fresh, { draft: 'pacto-accept-draft-2', intentDigest: 'sha256:d' }));
    await render('sha256:d');
    await vi.waitFor(() => expect(fact(target, 'accept-signed-return')).toBeTruthy());
    expect((fact(target, 'service-inferred')!.querySelector('details') as HTMLDetailsElement).open).toBe(true);
    expect(acceptFn).toHaveBeenCalledWith(KEY, 'pacto-accept-draft-2');
    executeFn.mockResolvedValue({ record: 'ghcr.io/acme/reports@sha256:r', state: 'counted' });
    (fact(target, 'accept-execute')!.querySelector('button.btn-primary') as HTMLButtonElement).click();
    await vi.waitFor(() => expect(fact(target, 'accept-result')).toBeTruthy());
    expect(executeFn).toHaveBeenCalledWith({ serviceKey: KEY, intentDigest: 'sha256:d', mode: 'sign', fromDraft: 'pacto-accept-draft-2' });
    expect(text(fact(target, 'accept-result'))).toContain('Attached ghcr.io/acme/reports@sha256:r, signed by you: counted');
    expect(sessionStorage.getItem('pacto-accept-draft:sha256:d')).toBeNull();
    expect(fact(target, 'accept-signed-return')).toBeNull();
  });

  it('adds no block outside the Inferred block', async () => {
    acceptFn.mockResolvedValue(answer(fresh, DRAFTS));
    await openPlan();
    const inferred = fact(target, 'service-inferred')!;
    for (const id of ['accept-execute', 'accept-drafts', 'accept-sign', 'accept-save-draft']) {
      expect(target.querySelectorAll(`[data-testid="${id}"]`)).toHaveLength(1);
      expect(inferred.contains(fact(target, id))).toBe(true);
    }
  });
});
