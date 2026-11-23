import { test, expect } from '@playwright/test';
import { boot } from './typographyChecks';

/**
 * A producer speaking through the open contribution seams (509-S11), in the demo:
 * acme-idp contributes ledger-sync as a signed record and reports the story's fraud
 * through the Evidence Server (examples/demo/source_contribution.go). Data sources
 * lists its one row, the contributed service reads inferred with one mark in its
 * header and offers no Declare, and the target names the producer that reported it.
 */
const T = 30_000;
const LEDGER_SYNC = `#/fleet/services/${encodeURIComponent('registry.example%2Fstory%2Fpacto-inferred%2Facme-idp/ledger-sync')}`;
const FRAUD_TARGET = `#/fleet/targets/${encodeURIComponent('acme-idp/external/fraud')}`;

test('Data sources lists the producer row with its status', async ({ page }) => {
  await boot(page, '#/fleet/sources');
  const block = page.getByTestId('detected-providers');
  await expect(block.getByTestId('provider-row').first()).toBeVisible({ timeout: T });
  const showAll = block.getByTestId('providers-show-all');
  if (await showAll.isVisible()) await showAll.click();
  const row = block.getByTestId('provider-row').filter({ hasText: 'acme-idp' });
  await expect(row).toHaveCount(1, { timeout: T });
  await expect(row).toContainText('contributes inferred services from registry.example/story/pacto-inferred/acme-idp/services');
  await expect(row).toContainText('reported targets, contributed records');
  // Available: the status word is read by assistive technology only.
  await expect(row.locator('.dp-status')).toHaveText('Available');
});

test('the contributed service shows one mark with its producer and no Declare action', async ({ page }) => {
  await boot(page, LEDGER_SYNC);
  const mark = page.getByTestId('service-contribution');
  await expect(mark).toHaveCount(1, { timeout: T });
  await expect(mark).toContainText('Contributed by acme-idp');
  await mark.getByRole('button', { name: 'Inferred: contributed by acme-idp' }).click();
  const tip = mark.locator('.prov-tip');
  await expect(tip).toBeVisible();
  await expect(tip).toContainText('registry.example/story/pacto-inferred/acme-idp/services@sha256:');
  await expect(tip).toContainText('signed by key acme-idp-2026');
  await expect(tip).toContainText('never clears a gate');
  await expect(page.getByRole('button', { name: /^Declare/ })).toHaveCount(0);
  await expect(page.getByRole('link', { name: /^Declare/ })).toHaveCount(0);
});

test('a target the producer reported names it', async ({ page }) => {
  await boot(page, FRAUD_TARGET);
  const fact = page.getByTestId('target-reported-by');
  await expect(fact).toHaveCount(1, { timeout: T });
  await expect(fact).toHaveText(/Reported by\s*acme-idp/);
});
