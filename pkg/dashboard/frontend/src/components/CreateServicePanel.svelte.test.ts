/**
 * Create service (515-S6): no form on a host that cannot create; the portal first when
 * one is configured; essentials only until Review; refusals at their field with code,
 * closest value and both commands; Sign only with a fresh login and write, Save draft
 * otherwise; after a publish the reference, the signer, the commands and the zip.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync, createRawSnippet } from 'svelte';

const { planFn, acceptFn } = vi.hoisted(() => ({ planFn: vi.fn(), acceptFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { fleetCreateService: (...a: unknown[]) => planFn(...a), fleetCreateServiceAccept: (...a: unknown[]) => acceptFn(...a) } };
});
// @ts-expect-error — Svelte component has no declaration file
import CreateServicePanel from './CreateServicePanel.svelte';
import { ApiError } from '../lib/api.ts';

const KEY = 'ghcr.io/acme/paths/go-http-postgres';
const REF = `oci://${KEY}@sha256:${'4c1e'.repeat(16)}`;
const ID = 'alice@acme.io';
const CLI = `pacto init invoices --from ${REF} --owner billing --set 'configurations[0].values.storageGi=500'`;
const MCP = '{"offerings":[{"offering":"postgresql","values":{"storageGi":500}}]}';
const PG = {
  offering: 'postgresql', version: '16.1.0', requested: true, classes: ['small', 'large'],
  parameters: [
    { path: 'storageGi', type: 'integer', tier: 'tunable', minimum: 10, maximum: 500, required: false, sensitive: false },
    { path: 'size', type: 'string', tier: 'contract', enum: ['small', 'large'], default: 'small', required: false, sensitive: false },
    { path: 'ha', type: 'boolean', tier: 'contract', required: false, sensitive: false },
    { path: 'password', type: 'string', tier: 'contract', required: false, sensitive: true },
  ],
  guarantees: [{ interface: 'sql', guarantees: { timeout: '5s' }, text: 'timeout 5s' }],
};
const REDIS = { offering: 'redis', version: '7.0.0', requested: false, classes: [], parameters: [{ path: 'memoryMi', type: 'integer', tier: 'contract', required: false, sensitive: false }] };

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
function answer(patch: Record<string, any> = {}, planPatch: Record<string, any> = {}): Record<string, any> {
  return {
    plan: {
      service: 'invoices', version: '0.1.0', bundleRepo: 'ghcr.io/acme/invoices', state: 'declared', owner: 'billing',
      signer: { class: 'human', source: 'login' }, intentDigest: 'sha256:i1', path: REF, pathName: 'go-http-postgres', pathVersion: '1.2.0',
      signature: { step: 'signature', status: 'unknown', detail: 'no template key is configured' }, namespace: 'team-billing',
      revision: `pacto-create:${ID}:2026-11-23T10:02:11Z`, initCli: 'pacto init invoices --from x', consumableCompleteness: 'complete',
      contract: { text: 'pactoVersion: "2.5"\nservice:\n  name: invoices\n' }, record: { text: 'format: x\n' }, ...planPatch,
    },
    offerings: [PG, REDIS],
    signing: { mode: 'none', reason: 'this dashboard has no signing login' }, write: { available: true }, ...patch,
  };
}
const FRESH = { signing: { mode: 'fresh-login', identity: ID }, signTicket: 'tk1' };
const COMMANDS = createRawSnippet(() => ({ render: () => '<div data-testid="golden-path-create">pacto init &lt;folder&gt; --from oci://x</div>' }));

let target: HTMLElement;
let comp: ReturnType<typeof mount> | null = null;
beforeEach(() => { planFn.mockReset(); acceptFn.mockReset(); sessionStorage.clear(); target = document.createElement('div'); document.body.appendChild(target); });
afterEach(() => { if (comp) { unmount(comp); comp = null; } target.remove(); });

const text = (el: Element | null) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim();
const q = (s: string) => target.querySelector(s);
const tid = (id: string) => q(`[data-testid="${id}"]`);
function render(props: Record<string, unknown> = {}) {
  comp = mount(CreateServicePanel, { target, props: { pathKey: KEY, pathName: 'go-http-postgres', pathVersion: '1.2.0', requested: ['postgresql'], canCreate: true, commands: COMMANDS, ...props } });
  flushSync();
}
function type(id: string, v: string) {
  const el = tid(id) as HTMLInputElement;
  el.value = v;
  el.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
}
async function fillAndReview() {
  type('create-name', 'invoices');
  type('create-owner', 'billing');
  type('create-namespace', 'team-billing');
  (tid('create-review') as HTMLButtonElement).click();
  await vi.waitFor(() => expect(tid('create-plan-heading')).toBeTruthy());
  flushSync();
}
const button = (label: string) => Array.from(target.querySelectorAll('button')).find((b) => text(b).startsWith(label)) as HTMLButtonElement;

describe('CreateServicePanel', () => {
  it('renders no form and the commands expanded on a host that cannot create', () => {
    render({ canCreate: false });
    expect(tid('create-form')).toBeNull();
    const cmds = tid('golden-path-create')!;
    expect(cmds.closest('details')).toBeNull();
    expect(text(cmds)).toContain('pacto init');
    expect(tid('create-open-portal')).toBeNull();
  });

  it('leads with Open in Backstage only with a portal, and creates here second', () => {
    render({ portalCreateURL: 'https://backstage.acme.io/create' });
    const links = Array.from(target.querySelectorAll('a.btn, button.btn'));
    expect(text(links[0])).toBe('Open in Backstage');
    expect(links[0].getAttribute('href')).toBe('https://backstage.acme.io/create');
    expect(tid('create-form')).toBeNull();
    (tid('create-here') as HTMLButtonElement).click();
    flushSync();
    expect(tid('create-form')).toBeTruthy();
    expect(tid('create-here')).toBeNull();
    comp && unmount(comp);
    comp = null;
    target.innerHTML = '';
    render();
    expect(tid('create-open-portal')).toBeNull();
    expect(tid('create-form')).toBeTruthy();
  });

  it('shows the essentials only, asks nothing until Review and checks the name first', () => {
    render();
    expect(planFn).not.toHaveBeenCalled();
    expect(text(tid('create-form'))).toContain('Golden path go-http-postgres 1.2.0');
    expect(text(tid('create-requested'))).toContain('postgresql');
    expect((tid('create-more') as HTMLDetailsElement).open).toBe(false);
    expect((tid('create-yourself') as HTMLDetailsElement).open).toBe(false);
    expect(tid('create-plan')).toBeNull();
    (tid('create-review') as HTMLButtonElement).click();
    flushSync();
    expect(planFn).not.toHaveBeenCalled();
    expect(q('[role="alert"]')?.textContent).toBe('Name the new service');
    expect(tid('create-name')?.getAttribute('aria-invalid')).toBe('true');
    // No raw key in a visible label (#600): the path shows its name and version.
    expect(text(target)).not.toContain(KEY);
  });

  it('plans on Review, moves focus to the plan and reads an unchecked path as signature Unknown', async () => {
    planFn.mockResolvedValue(answer());
    render();
    type('create-repository', 'ghcr.io/acme/invoices');
    type('create-revision', 'abc123');
    await fillAndReview();
    expect(planFn).toHaveBeenCalledWith(expect.objectContaining({ path: KEY, name: 'invoices', owner: 'billing', namespace: 'team-billing', repository: 'ghcr.io/acme/invoices', revision: 'abc123', offerings: [] }));
    expect(document.activeElement).toBe(tid('create-plan-heading'));
    expect(text(tid('create-plan-heading'))).toBe('Plan: invoices 0.1.0');
    expect(text(tid('create-signature'))).toBe('signature Unknown');
    const plan = text(tid('create-plan'));
    expect(plan).toContain('0.1.0, contract only, nothing delivered');
    expect(plan).toContain('ghcr.io/acme/invoices');
    expect(plan).toContain(`pacto-create:${ID}`);
    // The pinned reference lives in the mark's tip, never in a visible label.
    expect(plan).toContain(`Pinned at ${REF}`);
    expect(plan.replace(`Pinned at ${REF}`, '')).not.toContain('sha256:');
    expect(text(tid('create-requested'))).toBe('');
    // The requested offering is drawn with its classes and bounds; the others are folded.
    const pg = q('[data-testid="create-offering"][data-offering="postgresql"]')!;
    expect(text(pg)).toContain('class small | large');
    expect(text(pg)).toContain('10-500, varies per environment');
    expect(text(pg)).toContain('Secret reference');
    // Its guarantees are collapsed under the offering; an offering with none draws no fold.
    const g = pg.querySelector('[data-testid="create-guarantees"]') as HTMLDetailsElement;
    expect(g.open).toBe(false);
    expect(text(g)).toContain('sql: timeout 5s');
    expect(q('[data-testid="create-offering"][data-offering="redis"] [data-testid="create-guarantees"]')).toBeNull();
    expect((tid('create-other-offerings') as HTMLDetailsElement).open).toBe(false);
    expect(text(tid('create-yourself'))).toContain('pacto init invoices --from x');
    // Without a signing login: Save draft, no Sign.
    expect(tid('create-sign')).toBeNull();
    expect(tid('create-save-draft')).toBeTruthy();
    expect(text(tid('create-signer'))).toContain('You do not sign here: this dashboard has no signing login');
  });

  it('shows every refusal at its field with code, closest value and both commands, and offers no Sign', async () => {
    const refusals = [
      { code: 'ParameterOutOfRange', message: 'storageGi 900 is above 500', field: 'configurations[database].values.storageGi', closest: 500, cli: CLI, mcp: MCP },
      { code: 'ClassNotOffered', message: 'size xl is not offered', field: 'configurations[database].values.size', closest: 'small', allowed: ['small', 'large'], cli: CLI },
      { code: 'EnvironmentNotDeclared', message: 'the golden path declares no environment qa', field: 'environments[qa]' },
      { code: 'OfferingNotConsumable', message: 'mysql is not an offering you may consume here', field: 'offerings[mysql]' },
    ];
    planFn.mockResolvedValue(answer({ ...FRESH, refusals }, { intentDigest: '' }));
    render();
    await fillAndReview();
    const pg = q('[data-testid="create-offering"][data-offering="postgresql"]')!;
    const storage = pg.querySelector('[data-field="storageGi"]')!;
    const r = storage.querySelector('[data-testid="create-refusal"]')!;
    expect(r.getAttribute('data-code')).toBe('ParameterOutOfRange');
    expect(text(r)).toContain('storageGi 900 is above 500, closest 500');
    expect(text(r)).toContain(CLI);
    expect(text(r)).toContain(`pacto_create_service ${MCP}`);
    expect(storage.querySelector('input')?.getAttribute('aria-invalid')).toBe('true');
    expect(text(pg.querySelector('[data-field="size"]'))).toContain('closest small (also large)');
    expect((tid('create-per-env') as HTMLDetailsElement).open).toBe(true);
    expect(text(tid('create-per-env'))).toContain('EnvironmentNotDeclared');
    expect(text(target)).toContain('OfferingNotConsumable');
    expect(tid('create-sign')).toBeNull();
    expect(tid('create-save-draft')).toBeNull();
  });

  it('sends the values typed, once and per environment, on Review again', async () => {
    planFn.mockResolvedValue(answer());
    render();
    await fillAndReview();
    const pg = q('[data-testid="create-offering"][data-offering="postgresql"]')!;
    const storage = pg.querySelector('[data-field="storageGi"] input') as HTMLInputElement;
    storage.value = '900';
    storage.dispatchEvent(new Event('input', { bubbles: true }));
    const size = pg.querySelector('[data-field="size"] select') as HTMLSelectElement;
    size.value = 'large';
    size.dispatchEvent(new Event('change', { bubbles: true }));
    type('create-env', 'prod');
    const envStorage = tid('create-per-env')!.querySelector('[data-field="storageGi"] input') as HTMLInputElement;
    envStorage.value = '200';
    envStorage.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    expect(text(tid('create-review'))).toBe('Review again');
    (tid('create-review') as HTMLButtonElement).click();
    await vi.waitFor(() => expect(planFn).toHaveBeenCalledTimes(2));
    expect(planFn.mock.calls[1][0].offerings).toEqual([{ offering: 'postgresql', values: { storageGi: 900, size: 'large' }, environments: { prod: { storageGi: 200 } } }]);
  });

  it('shows a refused plan verbatim with its command', async () => {
    planFn.mockResolvedValue({ ...answer(), plan: undefined, refusal: { code: 'ServiceExists', message: 'invoices is already taken in this registry: pick another name', command: 'pacto init invoices --from x' } });
    render();
    await fillAndReview();
    expect(text(tid('create-plan-heading'))).toBe('Nothing can be published as it stands');
    expect(text(q('[data-testid="refusal"]'))).toContain('invoices is already taken in this registry: pick another name');
    expect(text(tid('create-refusal-command'))).toContain('pacto init invoices --from x');
    expect(tid('create-save-draft')).toBeNull();
  });

  it('says a 403 as the server words it and offers nothing to sign or save', async () => {
    planFn.mockRejectedValue(new ApiError(403, 'alice may not create services in team-billing', 'NotAllowed'));
    render();
    type('create-name', 'invoices');
    type('create-namespace', 'team-billing');
    (tid('create-review') as HTMLButtonElement).click();
    await vi.waitFor(() => expect(q('[data-testid="refusal"]')).toBeTruthy());
    expect(text(q('[data-testid="refusal"]'))).toContain('alice may not create services in team-billing');
    expect(tid('create-sign')).toBeNull();
    expect(tid('create-save-draft')).toBeNull();
  });

  it('signs through a fresh login that keeps the inputs, then publishes and hands over', async () => {
    planFn.mockResolvedValue(answer(FRESH));
    render();
    await fillAndReview();
    const sign = tid('create-sign') as HTMLAnchorElement;
    expect(tid('create-save-draft')).toBeNull();
    const url = new URL(sign.getAttribute('href')!, 'https://x');
    expect(url.searchParams.get('ticket')).toBe('tk1');
    expect(url.searchParams.get('return')).toContain('create=1&signed=sha256%3Ai1');
    sign.addEventListener('click', (e) => e.preventDefault());
    sign.click();
    expect(JSON.parse(sessionStorage.getItem('pacto-create:sha256:i1')!)).toMatchObject({ name: 'invoices', namespace: 'team-billing' });
    unmount(comp!);
    target.innerHTML = '';

    planFn.mockReset();
    planFn.mockResolvedValue(answer({ ...FRESH, signedIn: true }));
    const files = [{ path: 'pacto.yaml', text: 'service: invoices\n', truncated: false }];
    acceptFn.mockResolvedValue({ published: 'ghcr.io/acme/invoices@sha256:7d2a', tag: '0.1.0', signed: true, initCli: 'pacto init invoices --from y', gitCli: 'gh repo create <org>/invoices --private --clone', pullCli: 'pacto pull oci://z', files, appearsWithin: 'It shows in the fleet within a minute' });
    render({ signed: 'sha256:i1' });
    await vi.waitFor(() => expect(tid('create-publish')).toBeTruthy());
    expect(planFn).toHaveBeenCalledWith(expect.objectContaining({ name: 'invoices', namespace: 'team-billing', intentDigest: 'sha256:i1' }));
    (tid('create-publish') as HTMLButtonElement).click();
    await vi.waitFor(() => expect(tid('create-result')).toBeTruthy());
    expect(acceptFn).toHaveBeenCalledWith(expect.objectContaining({ mode: 'sign', intentDigest: 'sha256:i1', name: 'invoices' }));
    expect(document.activeElement).toBe(tid('create-result'));
    const res = text(tid('create-result'));
    expect(res).toContain(`Published ghcr.io/acme/invoices@sha256:7d2a by ${ID}`);
    expect(res).toContain('It shows in the fleet within a minute, declared at 0.1.0 with nothing delivered');
    expect(res).toContain('pacto init invoices --from y');
    expect(res).toContain('gh repo create <org>/invoices');
    expect(tid('create-publish')).toBeNull();
    expect(sessionStorage.getItem('pacto-create:sha256:i1')).toBeNull();

    const created = vi.fn(() => 'blob:x');
    const revoked = vi.fn();
    Object.assign(URL, { createObjectURL: created, revokeObjectURL: revoked });
    const clicked = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) { expect(this.download).toBe('invoices.zip'); });
    (tid('create-zip') as HTMLButtonElement).click();
    expect(created).toHaveBeenCalled();
    expect(clicked).toHaveBeenCalled();
    clicked.mockRestore();
    expect(tid('create-pull')).toBeNull();
  });

  it('offers the pull command in place of a zip when files are truncated', async () => {
    sessionStorage.setItem('pacto-create:sha256:i1', JSON.stringify({ name: 'invoices', owner: '', namespace: 'team-billing', repository: '', revision: '', once: {}, perEnv: {}, env: '', draft: null }));
    planFn.mockResolvedValue(answer({ ...FRESH, signedIn: true }));
    acceptFn.mockResolvedValue({ published: 'r@sha256:1', signed: true, pullCli: 'pacto pull oci://z', files: [{ path: 'a', text: 'x' }], filesTotal: 70 });
    render({ signed: 'sha256:i1' });
    await vi.waitFor(() => expect(tid('create-publish')).toBeTruthy());
    (tid('create-publish') as HTMLButtonElement).click();
    await vi.waitFor(() => expect(tid('create-result')).toBeTruthy());
    expect(tid('create-zip')).toBeNull();
    expect(text(tid('create-pull'))).toContain('pacto pull oci://z');
  });

  it('says a used signing login or a moved plan when it comes back', async () => {
    const kept = JSON.stringify({ name: 'invoices', owner: '', namespace: 'team-billing', repository: '', revision: '', once: {}, perEnv: {}, env: '', draft: null });
    sessionStorage.setItem('pacto-create:sha256:i1', kept);
    planFn.mockResolvedValue(answer(FRESH));
    render({ signed: 'sha256:i1' });
    await vi.waitFor(() => expect(text(tid('create-moved'))).toContain('expired or was used'));
    unmount(comp!);
    target.innerHTML = '';
    planFn.mockResolvedValue(answer({ ...FRESH, signedIn: true }, { intentDigest: 'sha256:i2' }));
    render({ signed: 'sha256:i1' });
    await vi.waitFor(() => expect(text(tid('create-moved'))).toContain('changed while you signed in'));
    unmount(comp!);
    target.innerHTML = '';
    planFn.mockClear();
    render({ signed: 'sha256:none' });
    expect(planFn).not.toHaveBeenCalled();
  });

  it('saves a draft, reviews a draft and reads a stale plan again', async () => {
    const drafts = [{ tag: 'draft-1', digest: 'sha256:' + 'd'.repeat(64), state: 'draft', asker: { class: 'agent' }, onBehalfOf: 'alice', signed: false }, { tag: 'draft-0', state: 'unreadable', asker: {}, signed: false, reason: 'not a bundle' }];
    planFn.mockResolvedValue(answer({ drafts, draftsTotal: 2 }));
    acceptFn.mockResolvedValueOnce({ draft: true, tag: 'draft-2', acceptUrl: '#/fleet/services/x?create=1' });
    render();
    await fillAndReview();
    button('Save draft').click();
    await vi.waitFor(() => expect(tid('create-result')).toBeTruthy());
    expect(text(tid('create-result'))).toContain('Saved draft-2');
    expect(tid('create-result')!.querySelector('a')?.getAttribute('href')).toBe('#/fleet/services/x?create=1');
    expect(acceptFn).toHaveBeenCalledWith(expect.objectContaining({ mode: 'draft' }));

    expect(target.querySelectorAll('[data-testid="create-draft"]').length).toBe(2);
    (q('[aria-label="Review draft-1"]') as HTMLButtonElement).click();
    await vi.waitFor(() => expect(planFn).toHaveBeenLastCalledWith(expect.objectContaining({ fromDraft: { tag: 'draft-1', digest: drafts[0].digest } })));
    await vi.waitFor(() => expect(text(target)).toContain('Accepting draft-1'));
    // Accepting a draft is signed, never saved again.
    expect(tid('create-save-draft')).toBeNull();
  });

  it('reads a stale plan again and says a refused write', async () => {
    planFn.mockResolvedValue(answer());
    acceptFn.mockResolvedValueOnce({ refusal: { code: 'PlanStale', message: 'moved' }, plan: answer({}, { intentDigest: 'sha256:i9' }) });
    acceptFn.mockResolvedValueOnce({ refusals: [{ code: 'ServiceExists', message: 'invoices is already taken', field: '' }] });
    acceptFn.mockRejectedValueOnce(new ApiError(403, 'alice may not create services in team-billing', 'NotAllowed'));
    acceptFn.mockResolvedValueOnce({ refusal: { code: 'NoRegistry', message: 'no registry', command: 'pacto init x' } });
    render();
    await fillAndReview();
    button('Save draft').click();
    await vi.waitFor(() => expect(text(tid('create-moved'))).toContain('The plan changed since you read it'));
    button('Save draft').click();
    await vi.waitFor(() => expect(text(q('[data-testid="refusal"]'))).toContain('invoices is already taken'));
    button('Save draft').click();
    await vi.waitFor(() => expect(text(q('[data-testid="refusal"]'))).toContain('may not create services'));
    button('Save draft').click();
    await vi.waitFor(() => expect(text(target)).toContain('pacto init x'));
    expect(acceptFn.mock.calls[1][0].intentDigest).toBe('sha256:i9');
  });

  it('says why this host writes nothing and notes a partial offerings list', async () => {
    planFn.mockResolvedValue(answer({ write: { available: false, reason: 'this dashboard has no push credential' } }, { consumableCompleteness: 'partial', notes: ['a note'], policyNote: 'policy' }));
    render();
    await fillAndReview();
    expect(text(tid('create-no-write'))).toBe('this dashboard has no push credential');
    expect(text(target)).toContain('The offerings list is partial: an offering not listed may exist.');
    expect(text(target)).toContain('a note');
    expect(text(target)).toContain('policy');
  });
});
