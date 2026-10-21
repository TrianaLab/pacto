import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

// Delivery views in a real browser, over the built WASM demo.
//
// The demo has no cluster, so it serves no delivery: its capabilities say so and the
// first test proves the views stay out of sight there. Every other test wraps the
// engine's serve function (the one seam every API call passes, see swr.spec.ts) with a
// small delivery backend that behaves like the dashboard's: who the caller is comes from
// a persona, a decision needs a fresh sign-in and only the named team may decide. A
// mutating call without the session's X-Pacto-CSRF token is refused, as the real guard
// does. The sign-in itself is a stand-in identity provider at /auth/login that marks the
// next session fresh and sends the browser back, exactly like the real redirect does.

const T = 30_000;

// One worker for the file: each test boots its own wasm engine, and nine at once on one
// machine starve each other into blank pages that prove nothing about delivery.
test.describe.configure({ mode: 'default' });

type Persona = 'carol' | 'alice' | 'robot' | 'local' | 'signed-out';

function installDelivery(persona: Persona): void {
  interface ServeResult { status: number; body: string; contentType: string }
  type Serve = (method: string, path: string, body: string | null, headers?: Record<string, string>) => ServeResult;
  let real: Serve | null = null;
  const json = (status: number, v: unknown): ServeResult => ({ status, body: JSON.stringify(v), contentType: 'application/json' });
  const S = sessionStorage;
  const fresh = () => S.getItem('mock.fresh') === '1';
  const decided = () => S.getItem('mock.decided');
  const D1 = 'sha256:' + '1'.repeat(64);
  const D2 = 'sha256:' + '2'.repeat(64);
  const callers: Record<string, unknown> = {
    carol: { user: 'https://idp#carol', class: 'human', groups: ['team-checkout'], authTime: '1792560000' },
    alice: { user: 'https://idp#alice', class: 'human', groups: ['team-payments'], authTime: '1792560000' },
    robot: { user: 'https://idp#robot', class: 'agent', groups: ['pacto:agents'] },
    local: { user: 'kind-admin', class: 'human', groups: ['system:masters'] },
  };
  const caller = callers[persona] as { groups: string[]; class: string } | undefined;
  const mayDecide = persona === 'carol';
  const listing = { asOf: '2026-10-21T10:00:00Z', completeness: 'complete' };
  const plan = { intentDigest: 'sha256:intent', type: 'pacto.dev/promote', from: D1, to: D2, predicted: 'Held',
    question: 'payments 2.0.0 removes a field checkout reads. Ship it?',
    floor: { human: true, decision: 'approve', owners: ['team-checkout'], consumers: ['checkout'] },
    verdicts: [{ gate: 'pacto.diff', phase: 'pre', result: 'Unknown', citation: { classification: 'BREAKING' } }],
    order: { moveFirst: [{ service: 'checkout', version: '3.0.0', accepts: '^1.4.0', stage: { namespace: 'prod', name: 'checkout-prod' },
      candidate: { digest: 'sha256:' + '3'.repeat(64), version: '3.1.0', accepts: '>=1.4.0 <3.0.0', verifiedOn: { namespace: 'test', name: 'checkout-test' } },
      // What moveFirstMessage writes, full digest included, so the narrow check below covers it.
      message: 'checkout 3.0.0 runs in prod and accepts payments ^1.4.0, not 2.0.0. checkout 3.1.0 (sha256:333333333333) accepts it (>=1.4.0 <3.0.0) ' +
        'and verified on test/checkout-test. Ship checkout first: promote sha256:' + '3'.repeat(64) + ' on Stage prod/checkout-prod.' }],
    unknown: ['the operational graph is partial (unresolved-reference)'] } };
  const op = () => ({
    metadata: { name: 'op1', namespace: 'prod' },
    spec: { type: 'pacto.dev/promote', stage: 'payments-prod', facet: 'pointer', actor: { user: 'system:serviceaccount:agents:release-bot' }, params: { from: D1, to: D2 } },
    status: decided()
      ? { phase: 'Running', plan, approvals: [{ name: decided(), by: 'https://idp#carol', decision: 'approve', authTime: '1792560000' }] }
      : { phase: 'Held', reason: 'ApprovalRequired', question: plan.question, plan, times: { admitted: '2026-10-21T10:00:00Z' } },
  });

  const delivery = (method: string, route: string): ServeResult => {
    if (route === '/api/session') {
      if (persona === 'signed-out') return json(200, { mode: 'shared', login: true, signedIn: false, fresh: false });
      return json(200, { mode: persona === 'local' ? 'local' : 'shared', login: persona !== 'local', signedIn: true,
        fresh: fresh(), csrf: 't', caller });
    }
    if (persona === 'signed-out') return json(401, { status: 401, code: 'LoginRequired', detail: 'sign in to see delivery' });
    if (route === '/api/delivery/inbox') {
      return json(200, { ...listing, held: decided() || !caller?.groups.includes('team-checkout') ? [] : [{
        namespace: 'prod', name: 'op1', stage: 'payments-prod', type: 'pacto.dev/promote', from: D1, to: D2, phase: 'Held',
        question: plan.question, decision: 'approve', owners: ['team-checkout'], consumers: ['checkout'], order: plan.order }], frozen: [], errored: [] });
    }
    if (route === '/api/delivery/environments') {
      return json(200, { ...listing, environments: [{ namespace: 'prod', stage: 'payments-prod', service: 'payments', environment: 'prod',
        pointer: D1, version: '1.4.0', verified: true, failed: false, frozen: false, autoHold: false, held: decided() ? 0 : 1, candidate: D2,
        ready: 'True', actions: ['promote'] }] });
    }
    if (route === '/api/delivery/operations/prod/op1' && method === 'GET') {
      const o = op();
      const actions = o.status.phase === 'Held' && mayDecide ? ['approve', 'reject', 'cancel'] : ['cancel'];
      return json(200, { explanation: { operation: 'prod/op1', summary: 'promote payments-prod from 1.4.0 to 2.0.0', spec: o.spec,
        status: { phase: o.status.phase, reason: o.status.reason, actorClass: 'agent' }, verdicts: plan.verdicts.map((v) => ({ ...v, rerun: 'pacto delivery explain -n prod op1' })),
        stage: { name: 'payments-prod', pointer: D1 } }, operation: o, actions });
    }
    if (route === '/api/delivery/plan' && method === 'POST') return json(200, plan);
    if (route === '/api/delivery/operations/prod/op1/decision' && method === 'POST') {
      if (persona === 'local') return json(403, { status: 403, code: 'ApproveFromCLI', detail: 'a local dashboard never decides' });
      if (!fresh()) return json(401, { status: 401, code: 'FreshLoginRequired', detail: 'deciding needs a fresh login: sign in again' });
      if (!mayDecide) return json(403, { status: 403, code: 'NotYourTeam', detail: 'only [team-checkout] may answer this question' });
      S.setItem('mock.decided', 'op1-approve-x7k2');
      return json(201, { approval: 'op1-approve-x7k2' });
    }
    return json(404, { status: 404, code: 'NotFound', detail: `no mock for ${method} ${route}` });
  };

  const wrapped: Serve = (method, path, body, headers = {}) => {
    const route = path.split('?')[0];
    // Like the dashboard's guard: a signed-in session's mutating call carries
    // the session's CSRF token, or it is refused.
    if (route.startsWith('/api/delivery/') && method !== 'GET' && persona !== 'signed-out' && headers['x-pacto-csrf'] !== 't') {
      return json(403, { status: 403, code: 'Forbidden', detail: 'missing or wrong X-Pacto-CSRF header' });
    }
    if (route === '/api/capabilities') {
      const doc = JSON.parse((real as Serve)(method, path, body).body);
      return json(200, { ...doc, delivery: true, miraURL: 'https://mira.example',
        auth: { mode: persona === 'local' ? 'local' : 'shared', login: persona !== 'local', signedIn: persona !== 'signed-out' } });
    }
    if (route === '/api/session' || route.startsWith('/api/delivery/')) return delivery(method, route);
    return (real as Serve)(method, path, body);
  };
  Object.defineProperty(window, '__pactoServe', {
    configurable: true,
    get() { return real ? wrapped : undefined; },
    set(v: Serve) { real = v; },
  });
}

/** A stand-in identity provider: a fresh login marks the next session fresh. */
async function fakeIdP(page: Page) {
  await page.route('**/auth/login?**', (route) => {
    const u = new URL(route.request().url());
    const ret = u.searchParams.get('return') || '/';
    const fresh = u.searchParams.get('fresh') === '1';
    return route.fulfill({ status: 200, contentType: 'text/html', body:
      `<script>${fresh ? "sessionStorage.setItem('mock.fresh','1');" : ''}location.replace(${JSON.stringify(ret)});</script>` });
  });
}

async function as(page: Page, persona: Persona) {
  await page.addInitScript(installDelivery, persona);
  await fakeIdP(page);
}

async function audit(page: Page, what: string) {
  await page.waitForTimeout(400); // let entrances settle, as axe.spec.ts does
  const r = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
  expect(r.violations, `${what}: ${JSON.stringify(r.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) })))}`).toEqual([]);
}

test.describe('Delivery views', () => {
  test('stay out of sight where delivery is not installed', async ({ page }) => {
    await page.goto('/#/fleet');
    await expect(page.getByRole('link', { name: 'Overview', exact: true }).first()).toBeVisible({ timeout: T });
    await expect(page.locator('.navbar-nav-desktop').getByRole('link', { name: 'Delivery' })).toHaveCount(0);
    await page.goto('/#/delivery/inbox');
    await expect(page.getByTestId('delivery-unavailable')).toBeVisible({ timeout: T });
  });

  test('the inbox shows what waits on my team, and the keyboard reaches the change', async ({ page }) => {
    await as(page, 'carol');
    await page.goto('/#/fleet');
    await page.locator('.navbar-nav-desktop').getByRole('link', { name: 'Delivery' }).click();
    await expect(page.getByRole('heading', { level: 1, name: 'Inbox' })).toBeVisible({ timeout: T });
    const held = page.getByTestId('inbox-held');
    await expect(held).toContainText('removes a field checkout reads');
    await expect(held).toContainText('team-checkout');
    await expect(held.getByRole('link', { name: 'checkout', exact: true })).toBeVisible();
    await expect(page.getByTestId('delivery-identity')).toContainText('Signed in as https://idp#carol');
    await audit(page, 'inbox');

    const link = held.getByRole('link', { name: /Promote on prod\/payments-prod/ });
    await link.focus();
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(/#\/delivery\/changes\/prod\/op1$/);
    await expect(page.getByTestId('decision-panel')).toContainText('Ship it?', { timeout: T });
    await expect(page.getByTestId('actual-verdicts')).toContainText('pacto delivery explain -n prod op1');
    await audit(page, 'change detail');
  });

  test('a held change says which service to update first and links to it', async ({ page }) => {
    await as(page, 'carol');
    await page.goto('/#/delivery/inbox');
    const inbox = page.getByTestId('inbox-move-first');
    await expect(inbox).toContainText('Update checkout first: checkout 3.0.0 runs here and accepts only ^1.4.0', { timeout: T });
    await expect(inbox.getByRole('link', { name: 'Stage prod/checkout-prod' })).toHaveAttribute('href', '#/delivery/history?namespace=prod&stage=checkout-prod');
    await expect(inbox.getByRole('link', { name: 'checkout 3.1.0, verified on test/checkout-test' })).toHaveAttribute('href', '#/delivery/history?namespace=test&stage=checkout-test');
    await expect(page.getByTestId('inbox-move-first-unknown')).toContainText('This list may be incomplete');

    await page.goto('/#/delivery/changes/prod/op1');
    const section = page.getByRole('region', { name: 'Update these first' });
    await expect(section).toContainText('Pacto never moves them for you.', { timeout: T });
    const candidate = section.getByRole('link', { name: 'checkout 3.1.0, verified on test/checkout-test' });
    await candidate.focus();
    await expect(candidate).toBeFocused();
    await audit(page, 'change detail with what moves first');

    // The message carries a full digest: on a phone it wraps instead of scrolling the page sideways.
    await page.setViewportSize({ width: 320, height: 800 });
    for (const hash of ['#/delivery/inbox', '#/delivery/changes/prod/op1']) {
      await page.goto('/' + hash);
      await expect(page.getByText('Ship checkout first').first()).toBeVisible({ timeout: T });
      expect(await page.evaluate(() => [document.documentElement, document.body].every((e) => e.scrollWidth <= e.clientWidth + 1)), hash).toBe(true);
    }
  });

  test('a decision asks for a fresh sign-in, keeps the choice and sends it on a click', async ({ page }) => {
    await as(page, 'carol');
    await page.goto('/#/delivery/changes/prod/op1');
    const panel = page.getByTestId('decision-panel');
    await expect(panel).toBeVisible({ timeout: T });
    await panel.getByLabel('Note (kept with the decision)').fill('checkout 3.1 reads the new field');
    await panel.getByRole('button', { name: 'Approve' }).click();
    const fresh = page.getByTestId('fresh-login');
    await expect(fresh).toContainText('Deciding needs a fresh sign-in.');
    // Detected, not acted on: the page waits for the person to choose to leave.
    await page.waitForTimeout(500);
    await expect(page).toHaveURL(/#\/delivery\/changes\/prod\/op1$/);

    await fresh.getByRole('button', { name: 'Sign in again' }).click();
    await expect(page).toHaveURL(/#\/delivery\/changes\/prod\/op1$/, { timeout: T });
    await expect(page.getByTestId('decision-kept')).toContainText('Your choice was Approve', { timeout: T });
    await expect(panel.getByLabel('Note (kept with the decision)')).toHaveValue('checkout 3.1 reads the new field');
    await page.waitForTimeout(500);
    await expect(page.getByTestId('decision-recorded')).toHaveCount(0); // never sent on its own

    await panel.getByRole('button', { name: 'Approve' }).click();
    await expect(page.getByTestId('decision-recorded')).toContainText('recorded as op1-approve-x7k2');
    await page.goto('/#/delivery/inbox');
    await expect(page.getByTestId('inbox-empty')).toBeVisible({ timeout: T });
  });

  test('another team is told who may answer and gets no buttons', async ({ page }) => {
    await as(page, 'alice');
    await page.goto('/#/delivery/changes/prod/op1');
    await expect(page.getByTestId('decide-not-team')).toContainText('Only team-checkout may answer this question; you are in team-payments', { timeout: T });
    await expect(page.getByTestId('decision-panel').getByRole('button')).toHaveCount(0);
  });

  test('an agent is told agents may not decide', async ({ page }) => {
    await as(page, 'robot');
    await page.goto('/#/delivery/changes/prod/op1');
    await expect(page.getByTestId('decide-agent')).toContainText('agents may not decide', { timeout: T });
  });

  test('a local dashboard shows the command that decides', async ({ page }) => {
    await as(page, 'local');
    await page.goto('/#/delivery/changes/prod/op1');
    await expect(page.getByTestId('decision-panel')).toContainText('pacto delivery approve -n prod op1 --decision approve', { timeout: T });
    await expect(page.getByTestId('delivery-identity')).toContainText('Acting as kind-admin from this machine');
  });

  test('a signed-out person is asked to sign in and sees no delivery data', async ({ page }) => {
    await as(page, 'signed-out');
    await page.goto('/#/delivery/environments');
    await expect(page.getByTestId('delivery-signin')).toBeVisible({ timeout: T });
    await expect(page.getByTestId('environments-board')).toHaveCount(0);
    await audit(page, 'signed out');
  });

  test('the environments board plans a promote before proposing it', async ({ page }) => {
    await as(page, 'carol');
    await page.goto('/#/delivery/environments');
    const board = page.getByTestId('environments-board');
    await expect(board).toContainText('1.4.0', { timeout: T });
    await expect(board).toContainText('Verified');
    await audit(page, 'environments');
    await board.getByRole('button', { name: /Promote payments in prod/ }).click();
    const panel = page.getByTestId('propose-panel');
    await expect(panel).toContainText('It would wait for a person to decide.', { timeout: T });
    await expect(panel).toContainText('Only team-checkout may answer it.');
    await page.keyboard.press('Escape');
    await expect(panel).toHaveCount(0);
  });
});


test.describe('Delivery views in the dark theme', () => {
  test.use({ colorScheme: 'dark' });
  test('pass the accessibility audit', async ({ page }) => {
    await as(page, 'carol');
    await page.goto('/#/delivery/changes/prod/op1');
    await expect(page.getByTestId('decision-panel')).toBeVisible({ timeout: T });
    await audit(page, 'change detail, dark');
    await page.goto('/#/delivery/environments');
    await expect(page.getByTestId('environments-board')).toBeVisible({ timeout: T });
    await audit(page, 'environments, dark');
  });
});
