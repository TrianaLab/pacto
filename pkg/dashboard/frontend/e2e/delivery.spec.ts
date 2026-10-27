import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { settle } from './ready';
import { DOC_ORIGIN } from './ports';

// Delivery views in a real browser, over the built WASM demo.
//
// The offline `pacto doc` export serves no delivery, and the first test proves the
// views stay out of sight there. The demo itself simulates delivery (the payments
// story, demo-delivery.spec.ts); every other test here wraps the
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
  type Serve = (method: string, path: string, body: string | null, headers?: Record<string, string>) => Promise<ServeResult>;
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
  const plan = { intentDigest: 'sha256:intent', type: 'delivery.pacto.trianalab.io/promote', from: D1, to: D2, predicted: 'Held',
    question: 'payments 2.0.0 removes a field checkout reads. Ship it?',
    floor: { human: true, decision: 'approve', owners: ['team-checkout'], consumers: ['checkout'] },
    verdicts: [{ gate: 'pacto.diff', phase: 'pre', result: 'Unknown', citation: { classification: 'BREAKING' } }],
    order: { moveFirst: [{ service: 'checkout', version: '3.0.0', accepts: '^1.4.0', stage: { namespace: 'prod', name: 'checkout-prod' },
      candidate: { digest: 'sha256:' + '3'.repeat(64), version: '3.1.0', accepts: '>=1.4.0 <3.0.0', verifiedOn: { namespace: 'test', name: 'checkout-test' } },
      // What moveFirstMessage writes, full digest included, so the narrow check below covers it.
      message: 'checkout 3.0.0 runs in prod and accepts payments ^1.4.0, not 2.0.0. checkout 3.1.0 (sha256:333333333333) accepts it (>=1.4.0 <3.0.0) ' +
        'and was verified on test/checkout-test. Ship checkout first: promote sha256:' + '3'.repeat(64) + ' on Stage prod/checkout-prod.' }],
    unknown: ['the operational graph is partial (unresolved-reference)'] } };
  const op = () => ({
    metadata: { name: 'op1', namespace: 'prod' },
    spec: { type: 'delivery.pacto.trianalab.io/promote', stage: 'payments-prod', facet: 'pointer', actor: { user: 'system:serviceaccount:agents:release-bot' }, params: { from: D1, to: D2 } },
    status: decided()
      ? { phase: 'Running', plan, approvals: [{ name: decided(), by: 'https://idp#carol', decision: 'approve', authTime: '1792560000' }] }
      : { phase: 'Held', reason: 'ApprovalRequired', question: plan.question, plan, times: { admitted: '2026-10-21T10:00:00Z' } },
  });

  const delivery = (method: string, route: string, body: string | null = null): ServeResult => {
    if (route === '/api/session') {
      if (persona === 'signed-out') return json(200, { mode: 'shared', login: true, signedIn: false, fresh: false });
      return json(200, { mode: persona === 'local' ? 'local' : 'shared', login: persona !== 'local', signedIn: true,
        fresh: fresh(), csrf: 't', caller });
    }
    if (persona === 'signed-out') return json(401, { status: 401, code: 'LoginRequired', detail: 'sign in to see delivery' });
    if (route === '/api/delivery/inbox') {
      return json(200, { ...listing, held: decided() || !caller?.groups.includes('team-checkout') ? [] : [{
        namespace: 'prod', name: 'op1', stage: 'payments-prod', type: 'delivery.pacto.trianalab.io/promote', from: D1, to: D2, phase: 'Held',
        question: plan.question, decision: 'approve', owners: ['team-checkout'], consumers: ['checkout'], order: plan.order }], frozen: [], errored: [] });
    }
    if (route === '/api/delivery/environments') {
      return json(200, { ...listing, environments: [{ namespace: 'prod', stage: 'payments-prod', service: 'payments', environment: 'prod',
        pointer: D1, version: '1.4.0', verified: true, failed: false, frozen: false, autoHold: false, held: decided() ? 0 : 1, candidate: D2,
        ready: 'True', actions: ['promote'], level: 'advise' }, { namespace: 'test', stage: 'payments-test', service: 'payments', environment: 'test',
        pointer: D1, version: '1.4.0', verified: true, failed: false, frozen: false, autoHold: false, held: 0, ready: 'True: Observing', actions: [], level: 'observe',
        shadow: [{ candidate: D2, predicted: 'Held', owners: ['team-checkout'], observedAt: '2026-10-21T09:00:00Z', verification: 'Verified' },
          { candidate: D1, predicted: 'Proceeds', observedAt: '2026-10-20T09:00:00Z', verification: 'Verified' }] }],
        // The server orders the columns the way changes travel, as environmentColumns does.
        environmentNames: ['test', 'prod'], environmentTiers: { test: 'test', prod: 'prod' } });
    }
    if (route === '/api/delivery/autonomy') {
      return json(200, { ...listing, contracted: { services: 4, uncontractedCallers: 1 }, promotionsWithoutPerson: { count: 3, of: 5, untyped: 0 },
        stages: [{ namespace: 'prod', stage: 'payments-prod', service: 'payments', environment: 'prod', level: 'advise',
          proposal: { level: 'auto-nonbreaking', evidence: ['u1', 'u2', 'u3'] } },
        { namespace: 'test', stage: 'payments-test', service: 'payments', environment: 'test', level: 'observe',
          proposal: { blockers: [{ kind: 'unlabelled-apply-ref', subject: 'payments', message: 'label the applyRef' }] },
          lastShadow: { candidate: D2, predicted: 'Held', owners: ['team-checkout'] } }],
        blockers: [{ kind: 'missing-contract', subject: 'k8s:pay/Deployment/legacy', message: 'k8s:pay/Deployment/legacy calls payments at runtime but has no contract',
          fix: { command: 'pacto init --cluster --namespace pay --out ./pacto-drafts' } },
        { kind: 'unlabelled-apply-ref', subject: 'payments', message: 'label the applyRef', stages: ['test/payments-test'] }] });
    }
    if (route === '/api/delivery/operations/prod/op1' && method === 'GET') {
      const o = op();
      const actions = o.status.phase === 'Held' && mayDecide ? ['approve', 'reject', 'cancel'] : ['cancel'];
      return json(200, { explanation: { operation: 'prod/op1', summary: 'promote payments-prod from 1.4.0 to 2.0.0', spec: o.spec,
        status: { phase: o.status.phase, reason: o.status.reason, actorClass: 'agent' }, verdicts: plan.verdicts.map((v) => ({ ...v, rerun: 'pacto delivery explain -n prod op1' })),
        stage: { name: 'payments-prod', pointer: D1 } }, operation: o, actions });
    }
    if (route === '/api/delivery/history') {
      const rec = (c: string) => 'sha256:' + c.repeat(64);
      return json(200, { ...listing, versions: { [D1]: '1.4.0', [D2]: '2.0.0' }, histories: [
        { namespace: 'prod', stage: { name: 'payments-prod', pointer: D2 }, asOf: listing.asOf, completeness: 'partial', source: 'records', verified: true,
          note: 'read from the signed records in the registry, which show problems', head: rec('c'), problemCount: 1, unreadableCount: 1,
          problems: ['seq 3 moved the Stage from ' + D1 + ', but the change before it left ' + D2 + ': something moved it with no record'],
          unreadable: [rec('d') + ': signature: signed by a key that is not trusted here'],
          entries: [
            { seq: 1, operation: 'op0', type: 'delivery.pacto.trianalab.io/promote', from: '', to: D1, actor: 'system:serviceaccount:pacto-delivery:pacto-operator-delivery', actorClass: 'controller',
              phase: 'Concluded', outcome: 'Succeeded', verification: 'Verified', finished: '2026-10-20T09:00:00Z', kind: 'operation', record: rec('a') },
            { seq: 2, operation: '', type: '', actor: '', phase: 'Concluded', kind: 'gap', reason: 'Gap: the operation with local seq 2 was gone before it was recorded',
              finished: '2026-10-20T10:00:00Z', record: rec('b') },
            { seq: 3, operation: 'op1', type: 'delivery.pacto.trianalab.io/promote', from: D1, to: D2, actor: 'https://idp#carol', actorClass: 'human', phase: 'Concluded',
              outcome: 'Succeeded', verification: 'Verified', finished: '2026-10-21T10:00:00Z', kind: 'operation', record: rec('c'), pointer: { before: D1, after: D2 } },
            { operation: 'op2', type: 'delivery.pacto.trianalab.io/promote', from: D2, to: D1, actor: 'system:serviceaccount:agents:release-bot', phase: 'Verifying' },
          ] },
        { namespace: 'test', stage: { name: 'payments-test', pointer: D2 }, asOf: listing.asOf, completeness: 'partial', source: 'status',
          note: 'read from the Operation objects that still exist; an operation deleted from the cluster is missing',
          entries: [{ seq: 4, operation: 'op5', type: 'delivery.pacto.trianalab.io/rollback', from: D2, to: D1, actor: 'https://idp#alice', actorClass: 'human',
            phase: 'Concluded', outcome: 'Succeeded', verification: 'Verified', finished: '2026-10-21T09:00:00Z' }] },
      ] });
    }
    if (route === '/api/delivery/blame') {
      return json(200, { stage: { name: 'payments-prod', pointer: D2 }, digest: D2, asOf: listing.asOf, completeness: 'complete', source: 'records',
        note: 'the newest change that moved the Stage to ' + D2, intentReason: 'ship the fix',
        change: { operation: 'op1', type: 'delivery.pacto.trianalab.io/promote', from: D1, to: D2, actor: 'https://idp#carol', actorClass: 'human', finished: '2026-10-21T10:00:00Z' },
        approvals: [{ by: 'https://idp#dave', decision: 'approve', operation: 'u1', authTime: '1792560000' }],
        verdicts: [{ gate: 'pacto.diff', phase: 'pre', result: 'Pass' }], versions: { [D1]: '1.4.0', [D2]: '2.0.0' }, people: { 'https://idp#dave': 'Dave Lee' } });
    }
    if (route === '/api/delivery/undo') {
      return json(200, { operation: 'prod/op1', kind: 'promote', note: 'the Stage changed since, so going back to ' + D1 + ' is a promote, and every gate runs',
        plan: { ...plan, to: D1 }, command: 'pacto delivery propose -n prod --stage payments-prod --type promote --to ' + D1 + ' --reason "undo op1" --intent-digest sha256:intent',
        versions: { [D1]: '1.4.0' } });
    }
    if (route === '/api/delivery/published') return json(200, { ...listing, services: [] });
    if (route === '/api/delivery/refusals') {
      return json(200, { asOf: listing.asOf, recorded: true, completeness: 'complete',
        note: "read from each service's contract bundle repository; their signatures are not checked here", refusals: [
        { service: 'payments', version: '2.4.3', check: 'version', requiredBump: 'major', baseline: '2.4.2', reason: 'removing a field is a breaking change',
          actor: 'system:serviceaccount:agents:pay-agent', at: '2026-10-21T08:00:00Z', verified: true }] });
    }
    if (route === '/api/delivery/plan' && method === 'POST' && JSON.parse(body || '{}').type === 'delivery.pacto.trianalab.io/autonomy') {
      return json(200, { intentDigest: 'sha256:raise', type: 'delivery.pacto.trianalab.io/autonomy', from: 'advise', to: 'auto-nonbreaking', predicted: 'Held',
        question: 'raise payments-prod to auto-nonbreaking?', floor: { human: true, decision: 'approve', owners: ['team-payments'] }, verdicts: [] });
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

  const wrapped: Serve = async (method, path, body, headers = {}) => {
    const route = path.split('?')[0];
    // Like the dashboard's guard (internal/authn/guard.go): with a session,
    // every mutating call under /api/ and to /auth/logout carries the
    // session's CSRF token, or it is refused.
    if ((route.startsWith('/api/') || route === '/auth/logout') && method !== 'GET' && persona !== 'signed-out' && headers['x-pacto-csrf'] !== 't') {
      return json(403, { status: 403, code: 'Forbidden', detail: 'missing or wrong X-Pacto-CSRF header' });
    }
    if (route === '/api/capabilities') {
      const doc = JSON.parse((await (real as Serve)(method, path, body)).body);
      return json(200, { ...doc, delivery: true, miraURL: 'https://mira.example',
        auth: { mode: persona === 'local' ? 'local' : 'shared', login: persona !== 'local', signedIn: persona !== 'signed-out' } });
    }
    if (route === '/api/session' || route.startsWith('/api/delivery/')) return delivery(method, route, body);
    return (real as Serve)(method, path, body);
  };
  // The demo signs people in itself (window.__pactoSignIn); these tests cover the
  // real redirect to /auth/login instead, so the hook is kept away.
  Object.defineProperty(window, '__pactoSignIn', { configurable: true, get() { return undefined; }, set() {} });
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
  await settle(page);
  const r = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
  expect(r.violations, `${what}: ${JSON.stringify(r.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) })))}`).toEqual([]);
}

test.describe('Delivery views', () => {
  test('stay out of sight where delivery is not installed', async ({ page }) => {
    await page.goto(`${DOC_ORIGIN}/#/services`);
    const nav = page.getByRole('navigation', { name: 'Primary' }).first();
    await expect(nav.getByRole('link', { name: 'Services', exact: true })).toBeVisible({ timeout: T });
    await expect(nav.getByRole('link', { name: 'Delivery' })).toHaveCount(0);
    await page.goto(`${DOC_ORIGIN}/#/delivery/inbox`);
    await expect(page.getByTestId('delivery-unavailable')).toBeVisible({ timeout: T });
  });

  test('the inbox shows what waits on my team, and the keyboard reaches the change', async ({ page }) => {
    await as(page, 'carol');
    await page.goto('/#/fleet');
    await page.locator('.navbar-nav-desktop').getByRole('link', { name: 'Delivery' }).click();
    await expect(page.getByRole('heading', { level: 1, name: 'Inbox' })).toBeVisible({ timeout: T });
    const held = page.getByTestId('inbox-held');
    await expect(held).toContainText('Breaks checkout');
    await expect(held).toContainText('team-checkout');
    await expect(held.getByRole('link', { name: 'checkout', exact: true })).toBeVisible();
    await expect(page.getByTestId('delivery-identity')).toContainText('Signed in as carol https://idp#carol');
    await audit(page, 'inbox');

    const link = held.getByRole('link', { name: /^Promote on prod\/payments-prod/ });
    await link.focus();
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(/#\/delivery\/changes\/prod\/op1$/);
    await expect(page.getByTestId('change-answer')).toContainText('It breaks checkout', { timeout: T });
    // Held before it applies: what happened would only repeat the plan, so it waits.
    // The plan's check ended Unknown, a problem, so the folded checks start open.
    await expect(page.getByTestId('change-checks')).toHaveAttribute('open', '');
    await expect(page.getByTestId('predicted-verdicts')).toBeVisible();
    await expect(page.getByTestId('actual-verdicts')).toHaveCount(0);
    await audit(page, 'change detail');
  });

  test('a held change says which service to update first and links to it', async ({ page }) => {
    await as(page, 'carol');
    await page.goto('/#/delivery/changes/prod/op1');
    const section = page.getByTestId('decision-safer');
    await expect(section).toContainText('Safer: update checkout', { timeout: T });
    await expect(section.getByRole('link', { name: 'checkout, Stage prod/checkout-prod' })).toHaveAttribute('href', '#/delivery/history?namespace=prod&stage=checkout-prod');
    await expect(page.getByTestId('move-first-unknown')).toContainText('This list may be incomplete');
    const candidate = section.getByRole('link', { name: 'checkout 3.1.0, verified on test/checkout-test' });
    await candidate.focus();
    await expect(candidate).toBeFocused();
    await audit(page, 'change detail with what moves first');

    // One sentence, no full digest: on a phone it wraps instead of scrolling the page sideways.
    expect(await page.locator('body').innerText()).not.toContain('3'.repeat(64));
    await page.setViewportSize({ width: 320, height: 800 });
    for (const hash of ['#/delivery/changes/prod/op1']) {
      await page.goto('/' + hash);
      await expect(page.getByTestId('decision-safer')).toBeVisible({ timeout: T });
      expect(await page.evaluate(() => [document.documentElement, document.body].every((e) => e.scrollWidth <= e.clientWidth + 1)), hash).toBe(true);
    }
  });

  test('a decision asks for a fresh sign-in, keeps the choice and sends it on a click', async ({ page }) => {
    await as(page, 'carol');
    await page.goto('/#/delivery/changes/prod/op1');
    const panel = page.getByTestId('decision-panel');
    await expect(panel).toBeVisible({ timeout: T });
    // Said before anything is pressed: pressing a button is choosing to leave and sign in.
    await expect(page.getByTestId('decision-stale')).toContainText('takes you to sign in again');
    await panel.getByLabel('Note (kept with the decision)').fill('checkout 3.1 reads the new field');
    await panel.getByRole('button', { name: 'Approve' }).click();
    await expect(page).toHaveURL(/#\/delivery\/changes\/prod\/op1$/, { timeout: T });
    await expect(page.getByTestId('decision-kept')).toContainText('Your choice was Approve', { timeout: T });
    await expect(panel.getByLabel('Note (kept with the decision)')).toHaveValue('checkout 3.1 reads the new field');
    await page.waitForTimeout(500);
    await expect(page.getByTestId('decision-recorded')).toHaveCount(0); // never sent on its own

    const confirm = panel.getByRole('button', { name: /^Confirm: Approve .* \(signed in at / });
    await expect(confirm).toBeFocused();
    await confirm.click();
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

  test('history says when it comes from signed records and when it is only partial', async ({ page }) => {
    await as(page, 'carol');
    // Every environment is one line: its newest change, how many and its problems, linked to the rest.
    await page.goto('/#/delivery/history');
    const summary = page.getByTestId('history-summary');
    await expect(summary.getByRole('row', { name: /^payments-prod in prod/ })).toContainText('2 problems', { timeout: T });
    await expect(summary.getByRole('link', { name: 'All 4 changes on prod/payments-prod' })).toHaveAttribute('href', '#/delivery/history?namespace=prod&stage=payments-prod');
    await audit(page, 'history summary');
    await page.goto('/#/delivery/history?namespace=prod&stage=payments-prod');
    const prod = page.getByRole('region', { name: 'History of prod/payments-prod' });
    const test_ = page.getByRole('region', { name: 'History of test/payments-test' });
    await expect(prod.getByTestId('history-source')).toHaveText('From signed records, every signature verified, partial.', { timeout: T });
    await expect(test_.getByTestId('history-source')).toHaveText('From status, partial.');
    await expect(prod.getByTestId('history-problems')).toContainText('something moved it with no record');
    await expect(prod.getByTestId('history-unreadable')).toContainText('not trusted here');
    await expect(prod.getByRole('row', { name: /^#2 Gap/ })).toBeVisible();
    await expect(prod.getByRole('row', { name: /op2|Verifying/ }).first()).toContainText('Not recorded yet');
    await expect(prod.getByRole('cell', { name: 'cccccccccccc' })).toBeVisible();
    await expect(test_.getByRole('link', { name: 'Roll back' })).toHaveAttribute('href', '#/delivery/changes/test/op5');
    await audit(page, 'history');

    // Problems name full digests: on a phone they wrap instead of scrolling the page sideways.
    await page.setViewportSize({ width: 320, height: 800 });
    await page.goto('/#/delivery/history?namespace=prod&stage=payments-prod');
    await expect(prod.getByTestId('history-problems')).toBeVisible({ timeout: T });
    expect(await page.evaluate(() => [document.documentElement, document.body].every((e) => e.scrollWidth <= e.clientWidth + 1))).toBe(true);
  });

  test('history says who put a version there and how to undo a change, and nothing is proposed', async ({ page }) => {
    await as(page, 'carol');
    await page.goto('/#/delivery/history?namespace=prod&stage=payments-prod');
    const prod = page.getByRole('region', { name: 'History of prod/payments-prod' });
    await expect(prod.getByRole('row', { name: /^#3 Promote/ })).toContainText('1.4.0 → 2.0.0', { timeout: T });
    await expect(prod.getByRole('row', { name: /^#1 Promote/ })).toContainText('pacto-delivery/pacto-operator-delivery Controller');
    await prod.getByRole('button', { name: 'Who put 2.0.0 here?' }).click();
    const blame = page.getByTestId('blame-panel');
    await expect(blame).toContainText('Approve by Dave Lee', { timeout: T });
    await expect(blame.getByTestId('blame-source')).toContainText('Read from its signed record.');
    await audit(page, 'history with blame');
    await page.keyboard.press('Escape');
    await expect(blame).toHaveCount(0);
    await prod.getByRole('button', { name: 'Undo op1' }).click();
    const undo = page.getByTestId('undo-panel');
    await expect(undo.getByTestId('undo-note')).toContainText('Promote to 1.4.0.', { timeout: T });
    await expect(undo).toContainText('Nothing has been proposed.');
    await audit(page, 'history with undo');
    await undo.getByRole('button', { name: 'Plan this promote' }).click();
    await expect(undo.getByTestId('propose-panel')).toContainText('Promote prod/payments-prod: 1.4.0', { timeout: T });
    // The command carries full digests: on a phone it wraps instead of scrolling the page sideways.
    await page.setViewportSize({ width: 320, height: 800 });
    expect(await page.evaluate(() => [document.documentElement, document.body].every((e) => e.scrollWidth <= e.clientWidth + 1))).toBe(true);
  });

  test('the published view lists recently refused publishes with the bump they need', async ({ page }) => {
    await as(page, 'carol');
    await page.goto('/#/delivery/publish');
    const refused = page.getByTestId('refused-publishes');
    await expect(refused).toContainText('payments 2.4.3', { timeout: T });
    await expect(refused).toContainText('Needs a major version');
    await expect(refused).toContainText('By agents/pay-agent');
    await expect(refused.getByTestId('refused-note')).toContainText('Read from each service');
    await audit(page, 'published with refusals');
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


test.describe('The path to autonomy', () => {
  test('shows the levels, proposes the next one through a plan and offers fixes to copy', async ({ page }) => {
    await as(page, 'carol');
    await page.goto('/#/delivery/environments');
    await page.getByRole('navigation', { name: 'Delivery' }).getByRole('link', { name: 'Autonomy' }).click();
    await expect(page.getByRole('heading', { level: 1, name: 'Path to autonomy' })).toBeVisible({ timeout: T });
    await expect(page.getByTestId('autonomy-contracted')).toContainText('80%');
    await expect(page.getByTestId('autonomy-contracted')).toContainText('of the 5 services Pacto has seen');
    await expect(page.getByTestId('autonomy-promotions')).toContainText('3 of 5');
    await expect(page.getByTestId('autonomy-stages')).toContainText('Pacto proposes Auto non-breaking: 3 verified promotions, no incident.');
    await expect(page.getByTestId('autonomy-blockers')).toContainText('pacto init --cluster --namespace pay --out ./pacto-drafts');
    await audit(page, 'autonomy');
    await page.getByRole('button', { name: 'Propose Auto non-breaking for payments in prod' }).click();
    const panel = page.getByTestId('propose-panel');
    await expect(panel).toContainText('Level change prod/payments-prod: Auto non-breaking', { timeout: T });
    await expect(panel).toContainText('raise payments-prod to auto-nonbreaking?');
    await audit(page, 'autonomy proposal');
    await page.keyboard.press('Escape');
    await expect(panel).toHaveCount(0);
  });

  test('an observe Stage shows what Pacto would have done next to what GitOps ran', async ({ page }) => {
    await as(page, 'carol');
    await page.goto('/#/delivery/environments');
    const shadow = page.getByTestId('shadow');
    await expect(page.getByTestId('shadow-summary')).toContainText('advisory and unsigned', { timeout: T });
    await shadow.locator('summary').click();
    // The board orders environments test before prod (the server's environmentNames).
    await expect(page.getByTestId('level')).toHaveText(['Observe', 'Advise']);
    await expect(shadow.getByTestId('shadow-row').first()).toContainText('hold for team-checkout');
    await expect(shadow.getByTestId('held-but-ran')).toHaveCount(1);
    await audit(page, 'environments with an observe Stage');
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
    await page.goto('/#/delivery/history');
    await expect(page.getByTestId('history-summary')).toBeVisible({ timeout: T });
    await audit(page, 'history summary, dark');
    await page.goto('/#/delivery/history?namespace=prod&stage=payments-prod');
    await expect(page.getByTestId('history-problems')).toBeVisible({ timeout: T });
    await audit(page, 'history, dark');
    await page.goto('/#/delivery/autonomy');
    await expect(page.getByTestId('autonomy-stages')).toBeVisible({ timeout: T });
    await audit(page, 'autonomy, dark');
  });
});
