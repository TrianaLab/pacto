import { test, expect, type Page } from '@playwright/test';
import { appReady } from './ready';
import { installScaleFixture } from './layoutGate';

// A revision declared from a running service: the Inferred block opens with one muted
// line naming the declarer, the workload and how complete the cluster read was, with one
// mark whose note holds the rest. Against the built WASM demo, with newsletter-service's
// revision answered as a declaration by the scale fixture.

const T = 30_000;

async function openDeclared(page: Page, variant: true | 'dropped' | 'notAllowed') {
  await page.addInitScript((v) => { (window as unknown as { __pactoClusterFacts?: boolean | 'dropped' | 'notAllowed' }).__pactoClusterFacts = v; }, variant);
  await page.addInitScript(installScaleFixture);
  await appReady(page);
  await page.goto('/#/fleet/revisions?service=newsletter-service');
  const link = page.locator('main a[href*="/fleet/revisions/"]').first();
  await expect(link).toBeVisible({ timeout: T });
  await page.goto(`/${await link.getAttribute('href')}`);
  const line = page.getByTestId('revision-inferred').getByTestId('inferred-declaration');
  await expect(line).toBeVisible({ timeout: T });
  return line;
}

test.describe('WASM demo — a declared revision', () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  test('names the declarer, the workload and the cluster read, the rest in the mark', async ({ page }) => {
    const line = await openDeclared(page, true);
    await expect(line).toContainText(/Declared by (ana)+@acme\.example\.com from k8s:newsletter-x+\/Deployment\/newsletter-service · cluster read 1 of 2 sources complete/);
    const mark = line.getByRole('button', { name: 'About this declaration' });
    await expect(mark).toHaveCount(1);
    await mark.click();
    const note = line.getByRole('note');
    await expect(note).toContainText('signature checked');
    await expect(note).toContainText('https://accounts.google.com');
    await expect(note).toContainText('Baseline 1.4.0.');
    await expect(note).toContainText('could not see 2 services and 5 callers');
    // A cluster row's own mark says when it was read and what it proposes.
    const row = page.getByTestId('inferred-field').first();
    await row.getByRole('button').click();
    await expect(row.getByRole('note')).toContainText(/Read from the cluster (.+ ago|on .+)\./);
    await expect(row.getByRole('note')).not.toContainText('2026-11-17T10:00:00Z');
    await expect(row.getByRole('note')).toContainText('Proposed ^2.0.0, not declared.');
  });

  test('fits a phone with no horizontal scroll', async ({ page }) => {
    await openDeclared(page, true);
    await page.setViewportSize({ width: 390, height: 844 });
    await page.getByTestId('revision-inferred').locator('.ifl-more > summary').click();
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
  });

  test('an unverified signer is named by no subject, with no rows and no namespace', async ({ page }) => {
    const line = await openDeclared(page, 'dropped');
    const block = page.getByTestId('revision-inferred');
    await expect(line).toContainText('Declaration by an unverified signer');
    await expect(block.getByTestId('inferred-field')).toHaveCount(0);
    await expect(block).not.toContainText('newsletter-x');
    await expect(line.locator('bdi')).not.toContainText('mallory');
    await line.getByRole('button', { name: 'About this declaration' }).click();
    await expect(line.getByRole('note')).toContainText('signature not checked or not by that signer');
    await expect(line.getByRole('note')).toContainText('From a workload you may not read.');
    await expect(line.getByRole('note')).not.toContainText('cluster fields left out');
  });

  test('a signer the installation does not allow is named by no subject, with why in the mark', async ({ page }) => {
    const line = await openDeclared(page, 'notAllowed');
    await expect(line).toContainText('Declaration by a signer this installation does not allow');
    await expect(line.locator('bdi')).not.toContainText('mallory');
    await line.getByRole('button', { name: 'About this declaration' }).click();
    const note = line.getByRole('note');
    await expect(note).toContainText('Not accepted: the installation signer policy lists no issuer https://accounts.google.com.');
    await expect(note).toContainText('The record claims: Baseline 1.4.0.');
  });
});
