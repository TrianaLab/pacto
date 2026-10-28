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

// decide presses choice on the held change as user. A decision counts only on a fresh
// login: a plain sign-in is not one, so the dashboard keeps the choice and
// sends the person straight to sign in again (or, when the server says so
// only on the press, offers it).
async function decide(page: Page, user: string, choice: string, note: string): Promise<void> {
  const panel = page.getByTestId('decision-panel');
  await expect(panel.getByRole('button', { name: choice })).toBeVisible({ timeout: T });
  await panel.getByLabel('Note (kept with the decision)').fill(note);
  await panel.getByRole('button', { name: choice }).click();
  const recorded = page.getByTestId('decision-recorded');
  const fresh = page.getByTestId('fresh-login');
  const login = page.locator('input[name="login"]');
  await expect(recorded.or(fresh).or(login)).toBeVisible({ timeout: T });
  if (!(await recorded.isVisible())) {
    if (await fresh.isVisible()) await fresh.getByRole('button', { name: 'Sign in again' }).click();
    await dexLogin(page, user);
    await expect(page.getByTestId('decision-kept')).toContainText(`Your choice was ${choice}`, { timeout: T });
    await panel.getByRole('button', { name: choice }).click();
  }
  await expect(recorded).toContainText('recorded as', { timeout: T });
}

// signIn signs user in through the dashboard's own Sign in link and comes back to hash.
async function signIn(page: Page, user: string, hash: string): Promise<void> {
  await page.goto(hash);
  await page.getByRole('link', { name: 'Sign in' }).first().click({ timeout: T });
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
  await expect(page.getByTestId('decide-agent')).toContainText('agents may not decide');
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

test('B7: carol finds the change in her inbox and approves it after a fresh sign-in', async ({ page }) => {
  await signIn(page, 'carol', '/#/delivery/inbox');
  await expect(page.getByRole('heading', { level: 1, name: 'Inbox' })).toBeVisible({ timeout: T });
  const held = page.getByTestId('inbox-held');
  await expect(held).toContainText('payments', { timeout: T });
  await axe(page, 'inbox');
  await held.locator(`a[href="#/delivery/changes/prod/${OP}"]`).first().click();
  await expect(page).toHaveURL(new RegExp(`#/delivery/changes/prod/${OP}$`));

  await axe(page, 'change, as carol');
  await decide(page, 'carol', 'Approve', 'orders 1.4 is ready for payments 3');
});

test('B8: carol accepts the unknown check on the shipped change', async ({ page }) => {
  await signIn(page, 'carol', CHANGE);
  await decide(page, 'carol', 'Accept the unknown', 'too little traffic yet; accepting');
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
  await decide(page, 'alice', 'Accept the unknown', 'the model is run by its vendor; the evals passed');
});

test('History: the payments prod steps read back from signed records that verify', async ({ page }) => {
  await signIn(page, 'carol', '/#/delivery/history?namespace=prod&stage=payments');
  const prod = page.getByRole('region', { name: 'History of prod/payments' });
  await expect(prod.getByTestId('history-source')).toContainText('From signed records', { timeout: T });
  await expect(prod.getByRole('row').filter({ has: page.locator('td[data-label="Record"] button') }).first()).toBeVisible();
  await axe(page, 'history');
});
