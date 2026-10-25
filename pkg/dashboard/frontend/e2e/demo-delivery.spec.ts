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

  // The three gated moments, skipped: Skip performs the action, then the step
  // holds on its result until Continue.
  for (const says of ['asks only the affected teams', 'promote checkout 1.9.0', 'Only a person on the asked team decides', 'Every step is a signed record']) {
    await expect(text).toContainText(says);
    await page.getByTestId('demo-tour-skip').click();
    await expect(next).toBeVisible({ timeout: T });
    await next.click();
  }
  await expect(text).toContainText('History reads every step back');
  await next.click();
  await expect(page.getByTestId('demo-tour-explore').getByRole('link')).toHaveCount(3);
  await expect(page.getByTestId('demo-tour-more')).toHaveAttribute('href', '../examples/kubernetes-demo/');
  await next.click();
  await expect(bubble).toHaveCount(0);

  // Where it ended: 3.0.0 rolled back by carol, automation on hold.
  await page.goto('/#/delivery/environments');
  await expect(page.getByTestId('environments-board')).toContainText('Kept by a person', { timeout: T });
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

    // carol signed in half an hour ago: the decision asks her to sign in again,
    // through the simulated sign-in rather than a page load that would lose the tab.
    await switchTo(page, 'carol');
    // 3.0.0 is breaking: it ships only with a note saying why.
    await expect(panel.getByRole('button', { name: 'Approve' })).toBeDisabled({ timeout: T });
    await panel.getByLabel('Note (kept with the decision)').fill('orders 1.4 is ready for payments 3');
    await panel.getByRole('button', { name: 'Approve' }).click({ timeout: T });
    await page.getByTestId('fresh-login').getByRole('button', { name: 'Sign in again' }).click();
    const dlg = page.getByTestId('demo-signin');
    await expect(dlg).toContainText('Simulated sign-in');
    await dlg.getByTestId('demo-signin-submit').click();
    await expect(page.getByTestId('decision-kept')).toBeVisible({ timeout: T });
    await panel.getByRole('button', { name: 'Approve' }).click();
    await expect(page.getByTestId('decision-recorded')).toBeVisible({ timeout: T });

    // 3.0.0 ships and its record reads back verified.
    await page.goto('/#/delivery/history?namespace=prod&stage=payments');
    const prod = page.getByRole('region', { name: 'History of prod/payments' });
    await expect(prod).toContainText('Runs 3.0.0', { timeout: T });
    await expect(prod.getByTestId('history-source')).toHaveText('From signed records, complete');
  });

  test('history shows the deliberate gap, the untrusted record and a status-only Stage', async ({ page }) => {
    await ready(page, '#/delivery/history');
    const test_ = page.getByRole('region', { name: 'History of test/payments' });
    await expect(test_.getByTestId('history-source')).toHaveText('From signed records, partial', { timeout: T });
    await expect(test_.getByTestId('history-problems')).toContainText('is a gap');
    await expect(test_.getByTestId('history-unreadable')).toContainText('not signed by the key this tab trusts');
    await expect(page.getByRole('region', { name: 'History of test/fraud' }).getByTestId('history-source')).toHaveText('From status, partial');
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
      for (const hash of ['#/delivery/inbox', '#/delivery/environments', '#/delivery/history', '#/delivery/publish', '#/delivery/changes/prod/payments-promote-3']) {
        await page.goto(`/${hash}`);
        await expect(page.getByTestId('delivery-identity')).toContainText('Signed in as', { timeout: T });
        await audit(page, `${hash} on ${label}`);
      }
    });
  }
});
