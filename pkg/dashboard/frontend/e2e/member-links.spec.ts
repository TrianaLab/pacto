import { test, expect } from '@playwright/test';
import { appReady } from './ready';

// 514-S4: the demo hub's members carry their outbound channel. eu-prod is live and
// accepts the hub's tokens (shared issuer); us-west's channel dropped and it reads
// under the group mapping alice approved, which Needs attention lists.

const ISSUER = 'https://login.northwind.example/realms/production-engineering/protocol/openid-connect';
const T = 30_000;

test('Data sources shows each member channel muted, with the issuer in the tip', async ({ page }) => {
  await appReady(page, '/#/fleet/sources');
  const eu = page.getByTestId('source-cluster').filter({ hasText: 'eu-prod' });
  await expect(eu.locator('.sc-link')).toHaveText('live · shared issuer', { timeout: T });
  await expect(eu.locator('.sc-link')).not.toContainText(ISSUER);
  const us = page.getByTestId('source-cluster').filter({ hasText: 'us-west' });
  await expect(us.locator('.sc-link')).toHaveText('down');
  await expect(page.getByTestId('source-cluster').filter({ has: page.locator('bdi', { hasText: /^production$/ }) }).locator('.sc-link')).toHaveCount(0);

  await eu.getByRole('button', { name: 'About cluster eu-prod' }).click();
  const tip = eu.locator('[role="note"]');
  await expect(tip).toBeVisible();
  await expect(tip).toContainText(`${ISSUER}: its tokens are accepted.`);
  await expect(us.locator('[role="note"]')).toContainText('group mapping platform-engineers=pacto-readers approved by oidc:alice@northwind.example');
});

test('Needs attention lists the issuer us-west reads under a group mapping', async ({ page }) => {
  await appReady(page, '/#/fleet/attention?category=cluster');
  // The list beside the Overview's map words CLUSTER_ISSUER_DIFFERS by its summary.
  const item = page.getByTestId('fleet-map-rail-item').filter({ hasText: "Member clusters do not accept this hub's sign-in tokens" });
  await expect(item).toHaveCount(1, { timeout: T });
  await expect(item).toContainText('Reads in us-west use the group mapping approved by oidc:alice@northwind.example');
});
