import { readFileSync } from 'node:fs';
import { test, expect, type Page } from '@playwright/test';
import { appReady } from './ready';
import { installScaleFixture } from './layoutGate';

// 514-S5: Data sources' Clusters block holds one collapsed Candidate clusters disclosure:
// the clusters the hub's Argo CD, Flux and Cluster API know, grouped by source, each
// matched to a member only by a UID it states. Argo CD and Flux groups hand out the
// generated install as a download. The candidates come from the scale fixture behind
// __pactoClusterJoin = 'candidates'.

async function candidates(page: Page, mode: 'candidates' | 'candidates-zero' = 'candidates'): Promise<void> {
  await page.addInitScript(installScaleFixture);
  await page.addInitScript((m) => { (window as unknown as { __pactoClusterJoin?: unknown }).__pactoClusterJoin = m; }, mode);
  await appReady(page);
  await page.goto('/#/fleet/sources');
}

test('the disclosure lists the candidates by source, collapsed until opened', async ({ page }) => {
  await candidates(page);
  const det = page.getByTestId('cluster-candidates');
  await expect(det.locator('summary')).toHaveText('5 candidate clusters', { timeout: 30_000 });
  await expect(det.getByTestId('cluster-candidate').first()).toBeHidden();
  await det.locator('summary').click();
  const groups = det.getByTestId('cluster-candidate-group');
  await expect(groups).toHaveCount(3);
  await expect(groups.nth(0)).toHaveAttribute('data-source', 'argocd');
  await expect(groups.nth(2)).toHaveAttribute('data-source', 'capi');
  await expect(groups.nth(2).getByTestId('cluster-candidate-install')).toHaveCount(0);
  const cards = det.getByTestId('cluster-candidate');
  await expect(cards.locator('.scc-word')).toHaveText(['matched to eu-prod', 'states the same UID', 'not matched', 'not matched', 'not matched']);
  await expect(det).not.toContainText('not joined');
  await expect(cards.first().locator('.scc-labels')).toHaveText('env=prod region=eu-north-1 tier=gold +1');
});

test('a matched card opens its member chip', async ({ page }) => {
  await candidates(page);
  const det = page.getByTestId('cluster-candidates');
  await det.locator('summary').click({ timeout: 30_000 });
  await det.getByTestId('cluster-candidate-member').click();
  await expect(page.locator('#sc-member-9f1c4e2a-3b7d-4e19-a2c5-6d8f0b1e7a34')).toBeFocused();
  await expect(det.getByTestId('cluster-candidate').nth(1).locator('button')).toHaveCount(1); // only its source mark
});

test('Get ApplicationSet downloads the install, with registry and no hub URL', async ({ page }) => {
  await candidates(page, 'candidates-zero');
  const det = page.getByTestId('cluster-candidates');
  await det.locator('summary').click({ timeout: 30_000 });
  const [download] = await Promise.all([page.waitForEvent('download'), det.getByRole('button', { name: 'Get ApplicationSet' }).click()]);
  expect(download.suggestedFilename()).toBe('pacto-members-argocd.yaml');
  const text = readFileSync(await download.path(), 'utf8');
  expect(text).toContain('registry: ghcr.io/acme');
  expect(text).not.toContain('hub:');
  await expect(det.getByTestId('cluster-candidate-error')).toHaveCount(0);
});

test('the demo hub lists its three candidates and hands out its ApplicationSet', async ({ page }) => {
  await appReady(page);
  await page.goto('/#/fleet/sources');
  const det = page.getByTestId('cluster-candidates');
  await expect(det.locator('summary')).toHaveText('3 candidate clusters', { timeout: 30_000 });
  await det.locator('summary').click();
  await expect(det.getByTestId('cluster-candidate-group')).toHaveCount(2);
  await expect(det.getByTestId('cluster-candidate').locator('.scc-word')).toHaveText(['matched to eu-prod', 'not matched', 'not matched']);
  await expect(det.getByRole('button', { name: 'Get Flux manifests' })).toHaveCount(0);
  await det.getByTestId('cluster-candidate-member').click();
  await expect(page.locator('#sc-member-9f1c4e2a-3b7d-4e19-a2c5-6d8f0b1e7a34')).toBeFocused();
  const [download] = await Promise.all([page.waitForEvent('download'), det.getByRole('button', { name: 'Get ApplicationSet' }).click()]);
  expect(download.suggestedFilename()).toBe('pacto-members-argocd.yaml');
  const text = readFileSync(await download.path(), 'utf8');
  expect(text).toContain('kind: ApplicationSet');
  expect(text).toContain('registry: demo.pacto.local');
  expect(text).toContain('# warning: Argo CD cluster Secret argocd/eu-prod');
  expect(text).not.toContain('hub:');
  await expect(det.getByTestId('cluster-candidate-error')).toHaveCount(0);
});
