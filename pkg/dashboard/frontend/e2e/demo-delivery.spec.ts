import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { settle } from './ready';

// The browser demo's delivery: the payments story simulated in the tab
// (examples/demo/source_delivery.go), with no mock. Plans, checks and records come
// from Pacto's own code; the simulated promoter moves every 1.5 s, so waits are on
// what the page says, never on time.

const T = 30_000;

// One worker for the file: each test boots its own engine and plays a story that
// takes a few seconds of simulated promoter time.
test.describe.configure({ mode: 'default' });

async function ready(page: Page, hash: string) {
  await page.goto(`/${hash}`);
  await expect(page.getByTestId('demo-delivery-start')).toBeVisible({ timeout: T });
}

/** Propose a promote from the Environments board, as the current person. */
async function promote(page: Page, service: string, version: string) {
  await page.goto('/#/delivery/environments');
  await page.getByRole('button', { name: `Promote ${service} in prod to ${version}` }).click({ timeout: T });
  const panel = page.getByTestId('propose-panel');
  await expect(panel.getByTestId('plan-prediction')).toBeVisible({ timeout: T });
  await panel.getByRole('button', { name: /^Propose/ }).click();
  await expect(page).toHaveURL(/#\/delivery\/changes\/prod\//, { timeout: T });
}

async function switchTo(page: Page, who: 'pay-agent' | 'alice' | 'carol') {
  await page.getByRole('button', { name: 'Switch person' }).click();
  await page.getByTestId(`demo-persona-${who}`).click();
  await expect(page.getByTestId('demo-signin')).toHaveCount(0);
}

/** Does the tour's cut-out enclose sel? A light on the wrong control is the defect. */
async function lit(page: Page, sel: string): Promise<boolean> {
  return page.evaluate((selector) => {
    const spot = document.querySelector('[data-testid="demo-tour-spotlight"]') as HTMLElement | null;
    const t = document.querySelector(selector);
    if (!spot || spot.hidden || !t) return false;
    const a = spot.getBoundingClientRect();
    const b = t.getBoundingClientRect();
    return a.left <= b.left + 1 && a.right >= b.right - 1 && a.top <= b.top + 1 && a.bottom >= b.bottom - 1;
  }, sel);
}

async function audit(page: Page, what: string) {
  await settle(page);
  const r = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
  expect(r.violations, `${what}: ${JSON.stringify(r.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) })))}`).toEqual([]);
}

/**
 * Play the delivery tour to its end, skipping the gated moments, and check where it
 * ends. `reload` says whether starting it must reload the page first: only when the
 * tab's records changed since load.
 */
async function playTour(page: Page, reload: boolean) {
  await page.evaluate(() => { (window as unknown as { __noReload?: number }).__noReload = 1; });
  await page.getByTestId('demo-delivery-start').click();
  const bubble = page.getByTestId('demo-tour-bubble');
  const next = page.getByTestId('demo-tour-next');
  const text = page.getByTestId('demo-tour-text');

  await expect(text).toContainText('simulated', { timeout: T });
  expect(await page.evaluate(() => (window as unknown as { __noReload?: number }).__noReload)).toBe(reload ? undefined : 1);
  await next.click();
  await expect(text).toContainText('Act 1, pactify a service');
  await expect(page.getByTestId('demo-tour-pre')).toContainText('pacto init --plan --owner risk');
  await expect(page.getByTestId('demo-tour-pre')).toContainText('team: risk');
  await next.click();
  await expect(text).toContainText('Act 2, a safe change ships on its own');
  await next.click();
  await expect(text).toContainText('A bad release is caught');
  await expect(page.getByTestId('actual-verdicts')).toContainText('recorded for this demo', { timeout: T });
  await next.click();

  // The gated moments, one action each, skipped: Skip performs the action, and the
  // step either moves on by itself or holds on its result until Continue.
  await expect(text).toContainText('press Promote on payments 3.0.0');
  for (let i = 0; i < 30 && !((await text.textContent()) ?? '').includes('History reads every step back'); i++) {
    const before = await text.textContent();
    await page.getByTestId('demo-tour-skip').click();
    await expect
      .poll(async () => (await text.textContent()) !== before || (await next.isVisible()), { timeout: T })
      .toBe(true);
    if ((await text.textContent()) === before) await next.click();
  }
  await expect(text).toContainText('History reads every step back');
  await next.click();
  await expect(page.getByTestId('demo-tour-explore').getByRole('link')).toHaveCount(3);
  await expect(page.getByTestId('demo-tour-more')).toHaveAttribute('href', '../examples/kubernetes-demo/');
  await next.click();
  await expect(bubble).toHaveCount(0);

  // Where it ended: 3.0.0 rolled back by carol, automation on hold.
  await page.goto('/#/delivery/environments');
  await expect(page.getByTestId('environments-board')).toContainText('Auto-hold', { timeout: T });
}

test.describe('Browser demo: the delivery story', () => {
  test('the strip says delivery is simulated and names the signing key, without covering the page', async ({ page }) => {
    await ready(page, '#/delivery/environments');
    const strip = page.getByTestId('demo-strip');
    await expect(strip).toContainText('Simulated delivery: no cluster and no promoter');
    await expect(strip).toContainText(/Signing key: [0-9a-f]{8}\./);
    // In the page's flow: the board starts below the strip, never under it.
    const s = await strip.boundingBox();
    const board = await page.getByTestId('environments-board').boundingBox();
    expect(board!.y).toBeGreaterThanOrEqual(s!.y + s!.height);
    await page.goto('/#/fleet');
    await expect(strip).not.toContainText('Simulated delivery');
  });

  test('a breaking change asks only the affected teams, and only a freshly signed-in person on them decides', async ({ page }) => {
    test.setTimeout(120_000);
    await ready(page, '#/delivery/environments');
    await promote(page, 'payments', '3.0.0');
    const panel = page.getByTestId('decision-panel');
    await expect(panel).toContainText('Asked of: checkout, orders', { timeout: T });
    // pay-agent asked for it, and is an agent: either way it gets no buttons.
    await expect(page.getByTestId('decide-own')).toBeVisible();
    await expect(panel.getByRole('button', { name: 'Approve' })).toHaveCount(0);
    const change = page.url();

    // checkout moves first, with nobody involved; the question shrinks to orders.
    await promote(page, 'checkout', '1.9.0');
    await expect(page.locator('.cd-status')).toContainText(/Concluded|Succeeded/, { timeout: T });
    await page.goto(change);
    await expect(panel).toContainText('Asked of: orders', { timeout: T });
    await expect(panel).not.toContainText('checkout, orders');

    // alice is on team payments, not orders.
    await switchTo(page, 'alice');
    await expect(page.getByTestId('decide-not-team')).toBeVisible({ timeout: T });

    // carol signed in half an hour ago: the panel says so before she presses
    // anything, and Approve takes her straight to the simulated sign-in rather
    // than a page load that would lose the tab.
    await switchTo(page, 'carol');
    await expect(page.getByTestId('decision-stale')).toBeVisible({ timeout: T });
    // 3.0.0 is breaking: it ships only with a note saying why.
    await expect(panel.getByRole('button', { name: 'Approve' })).toBeDisabled({ timeout: T });
    await panel.getByLabel('Note (kept with the decision)').fill('orders 1.4 is ready for payments 3');
    await panel.getByRole('button', { name: 'Approve' }).click({ timeout: T });
    const dlg = page.getByTestId('demo-signin');
    await expect(dlg).toContainText('Simulated sign-in');
    await dlg.getByTestId('demo-signin-submit').click();
    await expect(page.getByTestId('decision-kept')).toBeVisible({ timeout: T });
    // Back from signing in, one button says it confirms, and it has focus.
    const confirm = panel.getByRole('button', { name: /^Confirm: Approve/ });
    await expect(confirm).toBeFocused();
    await confirm.click();
    await expect(page.getByTestId('decision-recorded')).toBeVisible({ timeout: T });

    // B8: too little telemetry leaves 3.0.0 Unknown after it ships. The page
    // says what Pacto could not see and how to fix it, and carol accepts it.
    await page.goto(change);
    await expect(page.getByTestId('verdict-unknown')).toContainText('Mira', { timeout: T });
    await expect(page.getByTestId('decision-unknown')).toContainText('Unknown is never a yes');
    await panel.getByRole('button', { name: /^Accept the unknown/ }).click();
    await expect(page.getByTestId('decision-recorded')).toBeVisible({ timeout: T });

    // 3.0.0 ships and its record reads back verified.
    await page.goto('/#/delivery/history?namespace=prod&stage=payments');
    const prod = page.getByRole('region', { name: 'History of prod/payments' });
    await expect(prod).toContainText('Runs 3.0.0', { timeout: T });
    await expect(prod.getByTestId('history-source')).toHaveText('From signed records, every signature verified, complete.');
  });

  test('history shows the deliberate gap, the untrusted record and a status-only Stage', async ({ page }) => {
    await ready(page, '#/delivery/history');
    await expect(page.getByTestId('history-summary').getByRole('row', { name: /^payments in test/ })).toContainText('problem', { timeout: T });
    await ready(page, '#/delivery/history?namespace=test&stage=payments');
    const test_ = page.getByRole('region', { name: 'History of test/payments' });
    await expect(test_.getByTestId('history-source')).toHaveText('From signed records, every signature verified, partial.', { timeout: T });
    await expect(test_.getByTestId('history-problems')).toContainText('is a gap');
    await expect(test_.getByTestId('history-unreadable')).toContainText('not signed by the key this tab trusts');
    await ready(page, '#/delivery/history?namespace=test&stage=fraud');
    await expect(page.getByRole('region', { name: 'History of test/fraud' }).getByTestId('history-source')).toHaveText('From status, partial.', { timeout: T });
  });

  test('the published view lists the refused 2.4.3 with the bump it needs, said to be unchecked', async ({ page }) => {
    await ready(page, '#/delivery/publish');
    const refused = page.getByTestId('refused-publishes');
    await expect(refused).toContainText('2.4.3', { timeout: T });
    await expect(refused).toContainText('major');
  });

  test('the tour plays both acts as five moments and hands over the rest', async ({ page }) => {
    test.setTimeout(180_000);
    await ready(page, '#/fleet');
    // Past a second boundary on purpose: the records carry the clock, and a tour that
    // read it as a change would reload a page nothing had touched.
    await page.waitForTimeout(1_100);
    await playTour(page, false);
  });

  // The report this came from: the step said "press Switch person, pick Carol, write
  // why and approve, after she signs in again" while the light sat on the decision card
  // and Switch person stayed dimmed. Now each action is its own step, the light is on
  // the control it names, and doing it moves the tour on.
  test('the tour lights the control each step asks for, one action at a time', async ({ page }) => {
    test.setTimeout(240_000);
    await ready(page, '#/fleet');
    await page.waitForTimeout(1_100);
    await page.getByTestId('demo-delivery-start').click();
    const text = page.getByTestId('demo-tour-text');
    const next = page.getByTestId('demo-tour-next');
    await expect(text).toContainText('simulated', { timeout: T });
    for (let i = 0; i < 4; i++) await next.click();

    const step = async (says: string, sel: string) => {
      await expect(text).toContainText(says, { timeout: T });
      await expect.poll(() => lit(page, sel), { timeout: T, message: `the light is on ${sel} for "${says}"` }).toBe(true);
    };
    await step('press Promote on payments 3.0.0', '[aria-label="Promote payments in prod to 3.0.0"]');
    await page.getByRole('button', { name: 'Promote payments in prod to 3.0.0' }).click();
    await step('so press Propose promote', '[data-testid="propose-panel"] button[type=submit]');
    await page.getByTestId('propose-panel').getByRole('button', { name: /^Propose/ }).click();
    await expect(next).toBeVisible({ timeout: T });
    await next.click();
    await step('press Promote on checkout 1.9.0', '[aria-label="Promote checkout in prod to 1.9.0"]');
    await page.getByRole('button', { name: 'Promote checkout in prod to 1.9.0' }).click();
    await step('Press Propose promote: checkout ships', '[data-testid="propose-panel"] button[type=submit]');
    await page.getByTestId('propose-panel').getByRole('button', { name: /^Propose/ }).click();
    await expect(next).toBeVisible({ timeout: T });
    await next.click();

    await step('so press Switch person', '[data-testid="delivery-identity"] button');
    await page.getByRole('button', { name: 'Switch person' }).click();
    await step('Pick Carol', '[data-testid="demo-persona-carol"]');
    await page.getByTestId('demo-persona-carol').click();
    await step('Write in the note why 3.0.0 should ship', '[data-testid="decision-panel"] textarea');
    await page.getByTestId('decision-panel').locator('textarea').fill('orders 1.4 is ready for payments 3');
    await step('Press Approve shipping 3.0.0', '[data-testid="decide-approve"]');
    await page.getByTestId('decide-approve').click();
    await step('sign in again as Carol', '[data-testid="demo-signin-submit"]');
    await page.getByTestId('demo-signin-submit').click();
    await step('Press Confirm', '[data-testid="decide-approve"]');
    await page.getByTestId('decide-approve').click();
    // 3.0.0 ships, then the checks end Unknown: a screen worth reading, so it holds.
    await expect(text).toContainText('Then Pacto could not see', { timeout: T });
    await expect(next).toBeVisible({ timeout: T });
    await next.click();
    await step('why accepting the unknown is safe', '[data-testid="decision-panel"] textarea');
  });

  // The engine can resolve before the strip mounts. A tour owed by a reload must still
  // start: here mount() is held until the engine is ready, with the replay flag set.
  test('the tour owed by a reload starts when the engine is ready before the strip mounts', async ({ page }) => {
    await page.addInitScript(() => {
      window.sessionStorage.setItem('pacto-demo-delivery-tour', '1');
      const add = document.addEventListener.bind(document);
      document.addEventListener = ((type: string, fn: EventListener, opts?: unknown) => {
        if (type === 'DOMContentLoaded' && fn.name === 'mount') {
          const w = window as unknown as { __pactoReady: Promise<unknown> };
          return add(type, () => { void w.__pactoReady.then(() => setTimeout(fn, 0)); }, opts as boolean);
        }
        return add(type, fn, opts as boolean);
      }) as typeof document.addEventListener;
    });
    await page.goto('/#/fleet');
    await expect(page.getByTestId('demo-tour-text')).toContainText('simulated', { timeout: T });
  });

  // The steps say "as pay-agent" and describe the world the replay leaves at load.
  // A reader who switched to Carol first would propose as Carol, and Carol may not
  // approve her own change; a second play would start from a world the copy no
  // longer describes. Both must end where the first play does.
  test('the tour tells the same story after a switch of person and on a second play', async ({ page }) => {
    test.setTimeout(360_000);
    await ready(page, '#/delivery/environments');
    await switchTo(page, 'carol');
    // Switching person changes no record, so no reload: setPersona alone must make
    // the first play pay-agent's. The second starts after the first changed records.
    await playTour(page, false);
    await playTour(page, true);
  });

  for (const [label, viewport] of [['desktop', null], ['a phone', { width: 390, height: 844 }]] as const) {
    test(`every delivery view passes the accessibility audit on ${label}`, async ({ page }) => {
      if (viewport) await page.setViewportSize(viewport);
      await ready(page, '#/delivery/inbox');
      for (const hash of ['#/delivery/inbox', '#/delivery/environments', '#/delivery/history', '#/delivery/history?namespace=prod&stage=payments', '#/delivery/publish', '#/delivery/changes/prod/payments-promote-3']) {
        await page.goto(`/${hash}`);
        await expect(page.getByTestId('delivery-identity')).toContainText('Signed in as', { timeout: T });
        await audit(page, `${hash} on ${label}`);
      }
    });
  }
});
