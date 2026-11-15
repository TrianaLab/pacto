import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { appReady, settle } from './ready';

// Acting from an investigation, in the WASM demo: the target, revision and Change
// analysis pages offer the plans the person may already take, the plan carries the
// ticket from the page address (?correlationID=), and proposing is a second press
// with the person's own identity. The demo answers every API call in the tab through
// window.__pactoServe, so a POST is counted there: the network never sees one.

const T = 30_000;
const WIDE_FONT = '*{font-family: Verdana, "DejaVu Sans", sans-serif !important}';
const STORY_PAYMENTS = 'registry.example%252Fstory%2Fpayments';
// payments 3.0.0: prod offers the promote of its artifact (examples/story).
const PAYMENTS_300 = 'sha256:a16380137bcba704971cd4258237e24fb8822d93f66bd3f280bd829005665323';
const CHECKOUT_TEST = '#/fleet/targets/test%2FDeployment%2Fcheckout';
const PAYMENTS_PROD = '#/fleet/targets/prod%2FDeployment%2Fpayments';
const PLAN = /^Plan /;

test.describe.configure({ mode: 'default' });

/** Count the operations proposed in the tab: POST /api/delivery/operations. */
function countProposals() {
  type Serve = (method: string, path: string, body: string | null, headers?: Record<string, string>) => Promise<unknown>;
  const w = window as unknown as { __proposals: number };
  w.__proposals = 0;
  let real: Serve | undefined;
  const wrapped: Serve = (method, path, body, headers) => {
    if (method === 'POST' && path.split('?')[0] === '/api/delivery/operations') w.__proposals++;
    return (real as Serve)(method, path, body, headers);
  };
  Object.defineProperty(window, '__pactoServe', { configurable: true, get() { return real ? wrapped : undefined; }, set(v: Serve) { real = v; } });
}
const proposals = (page: Page) => page.evaluate(() => (window as unknown as { __proposals: number }).__proposals);

async function open(page: Page, hash: string) {
  await appReady(page, `/${hash}`);
}

async function switchTo(page: Page, who: 'alice' | 'carol') {
  await page.getByTestId('identity-trigger').click();
  await page.getByRole('button', { name: 'Switch person' }).click();
  await page.getByTestId(`demo-persona-${who}`).click();
  await expect(page.getByTestId('demo-signin')).toHaveCount(0);
}

/** Press the page's first Plan offer and wait for the plan it opens. */
async function planFirst(page: Page) {
  await page.getByRole('button', { name: PLAN }).first().click({ timeout: T });
  const undo = page.getByTestId('undo-panel');
  if (await undo.isVisible()) await undo.getByRole('button', { name: /^Plan this / }).click({ timeout: T });
  const panel = page.getByTestId('propose-panel');
  await expect(panel.getByTestId('plan-prediction')).toBeVisible({ timeout: T });
  return panel;
}

async function audit(page: Page, what: string) {
  await settle(page);
  const r = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
  expect(r.violations, `${what}: ${JSON.stringify(r.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) })))}`).toEqual([]);
}

const noSideScroll = (page: Page) => page.evaluate(() => [document.documentElement, document.body].every((e) => e.scrollWidth <= e.clientWidth + 1));

test.describe('WASM demo — act from an investigation', () => {
  test('the target, revision and Change analysis pages plan with the ticket from the address', async ({ page }) => {
    test.setTimeout(120_000);
    await open(page, `${CHECKOUT_TEST}?correlationID=CHG-1`);
    await expect(page.getByTestId('target-delivery').getByRole('button', { name: /^Plan undo in test/ })).toBeVisible({ timeout: T });
    let panel = await planFirst(page);
    await expect(panel.getByTestId('pp-correlation')).toHaveValue('CHG-1');

    await page.goto(`/#/fleet/revisions/${STORY_PAYMENTS}@${PAYMENTS_300}?correlationID=CHG-2`);
    await expect(page.getByRole('button', { name: /^Plan promote of 3\.0\.0 in prod/ })).toBeVisible({ timeout: T });
    panel = await planFirst(page);
    await expect(panel.getByTestId('pp-correlation')).toHaveValue('CHG-2');

    await page.goto(`/#/fleet/changes/${STORY_PAYMENTS}?correlationID=CHG-3`);
    await page.getByRole('button', { name: 'Compare revisions' }).click({ timeout: T });
    panel = await planFirst(page);
    await expect(panel.getByTestId('pp-correlation')).toHaveValue('CHG-3');
  });

  test('with no ticket in the address the field starts empty', async ({ page }) => {
    await open(page, PAYMENTS_PROD);
    // The target runs a story revision the fleet holds, so its page can say which.
    await expect(page.getByRole('button', { name: PLAN }).first()).toBeVisible({ timeout: T });
    await expect(page.locator('main')).not.toContainText('Pacto cannot tell which revision runs here');
    const panel = await planFirst(page);
    await expect(panel.getByTestId('pp-correlation')).toHaveValue('');
  });

  test('as Alice, proposing from the target page lands on the change with no staleness warning', async ({ page }) => {
    test.setTimeout(120_000);
    await page.addInitScript(countProposals);
    await open(page, `${PAYMENTS_PROD}?correlationID=CHG-2041`);
    await switchTo(page, 'alice');
    await page.goto(`/${PAYMENTS_PROD}?correlationID=CHG-2041`);
    const panel = await planFirst(page);
    await expect(panel.getByTestId('pp-on-behalf-of')).toHaveCount(0);
    await panel.getByLabel('Why (kept with the change)').fill('payments 3.0.0 under the incident ticket');
    await panel.getByRole('button', { name: /^Propose/ }).click();
    await expect(page).toHaveURL(/#\/delivery\/changes\/prod\//, { timeout: T });
    await expect(page.getByText('Something changed since the plan')).toHaveCount(0);
    // A typed reason is outside the plan read, so the panel proposes again once.
    expect(await proposals(page)).toBeGreaterThan(0);
  });

  test('as pay-agent, nothing is proposed until it names the person it acts for', async ({ page }) => {
    test.setTimeout(120_000);
    await page.addInitScript(countProposals);
    await open(page, `${PAYMENTS_PROD}?correlationID=CHG-2041`);
    const panel = await planFirst(page);
    const behalf = panel.getByTestId('pp-on-behalf-of');
    await expect(behalf).toHaveValue('');
    await panel.getByRole('button', { name: /^Propose/ }).click();
    await expect(behalf).toHaveJSProperty('validity.valueMissing', true);
    expect(await behalf.evaluate((e) => e.matches(':invalid'))).toBe(true);
    await expect(panel).toBeVisible();
    expect(await proposals(page)).toBe(0);

    await behalf.fill('dana@example.com');
    await panel.getByRole('button', { name: /^Propose/ }).click();
    await expect(page).toHaveURL(/#\/delivery\/changes\/prod\//, { timeout: T });
    await expect(page.getByText('Something changed since the plan')).toHaveCount(0);
    expect(await proposals(page)).toBeGreaterThan(0);
  });

  test('one ticket lists the incident: the promote, the rollback and the step down', async ({ page }) => {
    await open(page, '#/delivery/history?correlationID=CHG-2041');
    await expect(page.getByRole('link', { name: '3 changes on prod/payments' })).toBeVisible({ timeout: T });
    await audit(page, 'history of one ticket');
    await page.getByRole('link', { name: '3 changes on prod/payments' }).click();
    const history = page.locator('[aria-label="History of prod/payments"]');
    await expect(history).toBeVisible({ timeout: T });
    for (const name of ['payments-promote-2', 'payments-rollback-3', 'payments-autonomy-4']) {
      await expect(history.locator(`a[href$="/delivery/changes/prod/${name}"]`).first()).toBeVisible();
    }
  });

  test('the target page and its open plan are accessible and fit at 1280 with the wide font', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    await open(page, `${PAYMENTS_PROD}?correlationID=CHG-2041`);
    await page.addStyleTag({ content: WIDE_FONT });
    await expect(page.getByRole('button', { name: PLAN }).first()).toBeVisible({ timeout: T });
    await audit(page, 'target page with Plan offers');
    expect(await noSideScroll(page)).toBe(true);
    await planFirst(page);
    await audit(page, 'target page with the plan open');
    expect(await noSideScroll(page)).toBe(true);
  });
});
