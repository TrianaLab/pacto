import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

// The delivery journeys against the LIVE Kubernetes demo
// (tests/acceptance/kind/delivery-demo.sh browser): a real dashboard, real Dex
// sign-in and the payments story's held change, payments 3.0.0 for prod, which
// after B6 asks only team orders. Each test is a new browser, so a new person.
//
// Each title starts with its story beat: the demo script runs them one beat at
// a time (--grep) and checks the cluster between beats, so B7 is the agent and
// alice refused and carol approving, B8 carol accepting the unknown, B9 her
// rollback, and History reads the signed records last.

const DEMO = !!process.env.PW_DELIVERY_DEMO;
// The held change's name: payments advances on its own in prod, so the promoter
// names the operation and the demo script passes it in.
const OP = process.env.PW_CHANGE ?? 'payments-3-0-0';
const CHANGE = `/#/delivery/changes/prod/${OP}`;
const T = 30_000;
// The team examples/story says is asked about payments 3.0.0 after B6.
const ASKED = process.env.PW_ASKED ?? 'orders';

test.describe.configure({ mode: 'serial' });
// Dex serves the demo's self-signed certificate.
test.use({ ignoreHTTPSErrors: true });
test.skip(!DEMO, 'runs only against the Kubernetes delivery demo: tests/acceptance/kind/delivery-demo.sh browser');

// dexLogin fills Dex's password form for user, wherever the browser is sent to it.
async function dexLogin(page: Page, user: string): Promise<void> {
  await page.locator('input[name="login"]').fill(`${user}@example.com`, { timeout: T });
  await page.locator('input[name="password"]').fill('password');
  await page.locator('button[type="submit"]').click();
}

// decide presses choice on the held change. The demo asks for no recent
// sign-in (delivery.promoter.authFreshness is off), so the decision is
// recorded at once; with it on and the sign-in too old, the dashboard asks in
// a dialog to sign in again, keeps the choice and the note, and sends it on
// one more press.
async function decide(page: Page, choice: string, note: string, user = 'carol'): Promise<void> {
  const panel = page.getByTestId('decision-panel');
  await expect(panel.getByRole('button', { name: choice })).toBeVisible({ timeout: T });
  await panel.getByLabel('Note (kept with the decision)').fill(note);
  await panel.getByRole('button', { name: choice }).click();
  const recorded = page.getByTestId('decision-recorded');
  const fresh = page.getByTestId('fresh-login');
  await expect(recorded.or(fresh)).toBeVisible({ timeout: T });
  if (await fresh.isVisible()) {
    await fresh.getByRole('button', { name: /^Sign in again to / }).click();
    await dexLogin(page, user);
    await expect(page.getByTestId('decision-kept')).toContainText(`Your choice was ${choice}`, { timeout: T });
    await panel.getByRole('button', { name: choice }).click();
  }
  await expect(recorded).toContainText('recorded as', { timeout: T });
}

// signIn signs user in through the dashboard's own Sign in with OIDC link and comes back to hash.
async function signIn(page: Page, user: string, hash: string): Promise<void> {
  await page.goto(hash);
  await page.getByRole('link', { name: 'Sign in with OIDC', exact: true }).first().click({ timeout: T });
  await dexLogin(page, user);
  await expect(page.getByTestId('identity')).toContainText('Signed in as', { timeout: T });
}

async function axe(page: Page, what: string): Promise<void> {
  const r = await new AxeBuilder({ page }).analyze();
  expect(r.violations, `${what}: ${JSON.stringify(r.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) })))}`).toEqual([]);
}

test('B7: the agent sees the held change and the refused publish, and may not decide', async ({ page }) => {
  await signIn(page, 'pay-agent', CHANGE);
  await expect(page.getByTestId('decision-panel')).toBeVisible({ timeout: T });
  await expect(page.getByTestId('decide-agent')).toContainText('agents cannot approve');
  await expect(page.getByTestId('decision-panel').getByRole('button', { name: 'Approve' })).toHaveCount(0);
  // From and To name versions, read from the artifacts, with the digest second.
  await expect(page.locator('dt:text-is("To") + dd')).toContainText('3.0.0');
  await expect(page.locator('dt:text-is("From") + dd')).toContainText(/\d+\.\d+\.\d+/);
  await expect(page.getByText('(version unknown)')).toHaveCount(0);
  await axe(page, 'change, as the agent');

  await page.goto('/#/delivery/publish');
  const refused = page.getByTestId('refused-publishes');
  await expect(refused).toContainText('payments 2.4.3', { timeout: T });
  await expect(refused).toContainText('Needs a major version');
  await axe(page, 'published');
});

test('B7: alice is not on the asked team and gets no buttons', async ({ page }) => {
  await signIn(page, 'alice', CHANGE);
  const notTeam = page.getByTestId('decide-not-team');
  await expect(notTeam).toContainText('may answer this question', { timeout: T });
  await expect(notTeam).toContainText(ASKED);
  await expect(page.getByTestId('decision-panel').getByRole('button')).toHaveCount(0);
});

test('B7: carol finds the change in her inbox and approves it', async ({ page }) => {
  await signIn(page, 'carol', '/#/delivery/inbox');
  await expect(page.getByRole('heading', { level: 1, name: 'Inbox' })).toBeVisible({ timeout: T });
  const held = page.getByTestId('inbox-held');
  await expect(held).toContainText('payments', { timeout: T });
  await axe(page, 'inbox');
  await held.locator(`a[href="#/delivery/changes/prod/${OP}"]`).first().click();
  await expect(page).toHaveURL(new RegExp(`#/delivery/changes/prod/${OP}$`));

  await axe(page, 'change, as carol');
  await decide(page, 'Approve', 'orders 1.4 is ready for payments 3');
});

test('B8: carol accepts the unknown check on the shipped change', async ({ page }) => {
  await signIn(page, 'carol', CHANGE);
  await decide(page, 'Accept the unknown', 'too little traffic yet; accepting');
});

test('B9: carol rolls the change back from the dashboard', async ({ page }) => {
  await signIn(page, 'carol', CHANGE);
  await page.getByRole('button', { name: 'Roll back this change' }).click({ timeout: T });
  const panel = page.getByTestId('propose-panel');
  await expect(panel.getByTestId('plan-prediction')).toBeVisible({ timeout: T });
  await panel.getByRole('button', { name: 'Propose roll back' }).click();
  await expect(page).not.toHaveURL(new RegExp(`${OP}$`), { timeout: T });
});

// pay-agent's model is an external dependency Pacto cannot see, so its contract check
// says Unknown and a person on its team accepts that before it counts as shipped.
test('agent: alice accepts the unknown on pay-agent 1.0.0 in test', async ({ page }) => {
  await signIn(page, 'alice', `/#/delivery/changes/test/${process.env.PW_AGENT_CHANGE ?? ''}`);
  await decide(page, 'Accept the unknown', 'the model is run by its vendor; the evals passed', 'alice');
});

test('History: the payments prod steps read back from signed records that verify', async ({ page }) => {
  await signIn(page, 'carol', '/#/delivery/history?namespace=prod&stage=payments');
  const prod = page.getByRole('region', { name: 'History of prod/payments' });
  await expect(prod.getByTestId('history-source')).toContainText('From signed records', { timeout: T });
  await expect(prod.getByRole('row').filter({ has: page.locator('td[data-label="Record"] button') }).first()).toBeVisible();
  await axe(page, 'history');
});

// The service the payments prod history names, from its revision link (#459): a
// revision key is the service key, an @ and the digest.
let paymentsService = '';
let carolChanges = 0;

test('History: carol reads the impact verdict prod/payments recorded and the revision of each version', async ({ page }) => {
  await signIn(page, 'carol', '/#/delivery/history?namespace=prod&stage=payments');
  const prod = page.getByRole('region', { name: 'History of prod/payments' });
  await expect(prod.getByTestId('history-impact').first()).toContainText('Impact recorded', { timeout: T });
  const rev = prod.locator('td[data-label="Version"] a[href^="#/fleet/revisions/"]').first();
  await expect(rev).toBeVisible();
  const key = decodeURIComponent((await rev.getAttribute('href'))!.slice('#/fleet/revisions/'.length));
  paymentsService = key.slice(0, key.lastIndexOf('@'));
  expect(paymentsService).not.toBe('');
});

test('History: the payments service page says when its changes reached a Stage, from signed records', async ({ page }) => {
  expect(paymentsService, 'the history journey names the payments service').not.toBe('');
  await signIn(page, 'carol', `/#/fleet/services/${encodeURIComponent(paymentsService)}?section=changes`);
  const changes = page.getByTestId('service-changes');
  await expect(changes.getByTestId('service-changes-caption')).not.toContainText('reading times', { timeout: T });
  await expect(changes.locator('[data-testid="service-change-reached"] a').first()).toBeVisible({ timeout: T });
  carolChanges = await changes.getByTestId('service-change-row').count();
  expect(carolChanges).toBeGreaterThan(0);
  await axe(page, 'service changes, as carol');
});

test('History: dave, who may read no Stage, sees the same changes with their times hidden', async ({ page }) => {
  expect(carolChanges, 'the journey before counts carol\'s changes').toBeGreaterThan(0);
  await signIn(page, 'dave', `/#/fleet/services/${encodeURIComponent(paymentsService)}?section=changes`);
  const changes = page.getByTestId('service-changes');
  await expect(changes.getByTestId('service-changes-caption')).not.toContainText('reading times', { timeout: T });
  await expect(changes.getByTestId('service-change-reached').first()).toContainText('time hidden', { timeout: T });
  await expect(changes.getByTestId('service-change-row')).toHaveCount(carolChanges);
  await expect(changes.locator('[data-testid="service-change-reached"] a')).toHaveCount(0);
});

// The Metrics page totals the run records (unsigned statistics) the demo wrote:
// the story's publishes, the promoter's operations and the history seeded for
// the services the cluster runs. carol may list Pactos, Stages and Operations
// in every namespace (the demo's approver ClusterRole, like alice and
// pay-agent), so nothing she reads is counted as outside her view.
test('Metrics: a signed-in reader sees the story\'s records', async ({ page }) => {
  await signIn(page, 'carol', '/#/metrics');
  const content = page.getByTestId('metrics-content');
  await expect(content).toBeVisible({ timeout: T });
  await expect(content.getByTestId('stats-strip').first()).toContainText(/[1-9][\d,]* records? read/);
  await expect(page.getByTestId('metrics-hidden')).toHaveCount(0);
  await expect(page.getByTestId('metrics-service-row').filter({ hasText: 'payments' }).first()).toBeVisible();
  // Environments follow the promotion path: test feeds prod.
  const rows = page.getByTestId('metrics-env-row');
  await expect(rows.first()).toBeVisible();
  const envs = (await rows.locator('th a bdi').allInnerTexts()).map((s) => s.trim());
  expect(envs.indexOf('test'), `environments ${envs.join(', ')}`).toBeGreaterThanOrEqual(0);
  expect(envs.indexOf('test'), `environments ${envs.join(', ')}`).toBeLessThan(envs.indexOf('prod'));
  await axe(page, 'metrics');
});
