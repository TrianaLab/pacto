import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { settle } from './ready';

// Publisher trust in the browser demo (examples/demo/source_delivery_trust.go), with
// no mock: audit-log's registry watch read a version signed from a hotfix branch the
// installation's rule does not cover, so a person confirms the signer from the inbox.

const T = 30_000;
const HOTFIX = 'https://github.com/trianalab/audit-log/.github/workflows/hotfix.yml@refs/heads/hotfix';

async function switchTo(page: Page, who: 'pay-agent' | 'alice' | 'carol') {
  await page.getByTestId('identity-trigger').click();
  await page.getByRole('button', { name: 'Switch person' }).click();
  await page.getByTestId(`demo-persona-${who}`).click();
  await expect(page.getByTestId('demo-signin')).toHaveCount(0);
}

async function audit(page: Page, what: string) {
  await settle(page);
  const r = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
  expect(r.violations, `${what}: ${JSON.stringify(r.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) })))}`).toEqual([]);
}

test('a person confirms a new signer from the inbox, and the Published view then says a pin accepts it', async ({ page }) => {
  test.setTimeout(120_000);
  await page.goto('/#/delivery/inbox');
  await expect(page.getByTestId('demo-delivery-start')).toBeVisible({ timeout: T });
  // An agent is never asked to confirm a signer.
  await expect(page.locator('a.dl-decide', { hasText: 'Confirm identity' })).toHaveCount(0);
  await switchTo(page, 'alice');

  const held = page.getByTestId('inbox-held');
  await expect(held).toContainText(`audit-log: new signer ${HOTFIX} (repository https://github.com/trianalab/audit-log, attested)`, { timeout: T });
  await audit(page, 'inbox, new signer');
  await held.getByRole('link', { name: `Confirm identity: ${HOTFIX}` }).click();

  await expect(page).toHaveURL(/#\/delivery\/changes\/prod\/audit-log-trust-/, { timeout: T });
  await expect(page.getByTestId('change-publisher')).toContainText(`Signer ${HOTFIX}, issuer https://token.actions.githubusercontent.com, repository https://github.com/trianalab/audit-log`, { timeout: T });
  await audit(page, 'change, new signer');
  const panel = page.getByTestId('decision-panel');
  // Confirming pins the signer and moves nothing, and the panel says so.
  await expect(panel).toContainText(`Pacto pins ${HOTFIX} for the service prod/audit-log delivers. Nothing moves`, { timeout: T });
  await expect(panel).not.toContainText('Approve shipping');
  await panel.getByRole('button', { name: 'Confirm this signer', exact: true }).click({ timeout: T });
  await expect(page.getByTestId('decision-recorded')).toBeVisible({ timeout: T });

  await page.goto('/#/delivery/inbox');
  await settle(page);
  await expect(page.locator('a.dl-decide', { hasText: 'Confirm identity' })).toHaveCount(0);

  await page.goto('/#/delivery/publish?service=audit-log');
  await expect(page.getByTestId('published-publisher')).toHaveText(/^Accepted by pin/, { timeout: T });
});

test('the newest payments-service revision says which check accepted its signer', async ({ page }) => {
  await page.goto('/#/fleet/revisions?service=payments-service');
  await page.locator('a[href*="/fleet/revisions/"]').first().click({ timeout: T });
  await expect(page.getByTestId('bundle-checked-against')).toContainText(/Checked against\s*rule/, { timeout: T });
});
