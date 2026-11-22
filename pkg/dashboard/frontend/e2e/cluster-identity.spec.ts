import { test, expect } from '@playwright/test';
import { appReady } from './ready';
import { installScaleFixture } from './layoutGate';

// 514-S1: a target page names its cluster, and a 3-part key that names targets in more
// than one cluster offers a chooser instead of a guess. 514-S3: the demo is a hub with
// two members, so its canonical target keys carry the cluster, and a 3-part key that
// matches one target still offers it in the chooser while a demo source is incomplete.

const CANONICAL = '#/fleet/targets/5b0c6c3e-7f1a-4c55-9d1e-2b8a4e6f0a11%2Fprod%2FDeployment%2Fpayments-service';
const LEGACY = '#/fleet/targets/prod%2FDeployment%2Fpayments-service';

test('a target page says which cluster it runs in and how the name was found', async ({ page }) => {
  await appReady(page);
  await page.goto(`/${CANONICAL}`);
  const fact = page.getByTestId('target-cluster');
  await expect(fact).toContainText('Runs in cluster', { timeout: 30_000 });
  await expect(fact).toContainText('production (from context name)');
  await expect(fact.locator('[title="5b0c6c3e-7f1a-4c55-9d1e-2b8a4e6f0a11"]')).toHaveText('5b0c6c3e');
  await expect(page).toHaveURL((u) => u.hash === CANONICAL);
});

test('a 3-part key of a hub target offers its one match while a source is incomplete', async ({ page }) => {
  await appReady(page);
  await page.goto(`/${LEGACY}`);
  const chooser = page.getByTestId('target-key-chooser');
  await expect(chooser).toContainText('a source is incomplete', { timeout: 30_000 });
  const links = chooser.locator('a');
  await expect(links).toHaveCount(1);
  await links.first().click();
  await expect(page).toHaveURL((u) => u.hash === CANONICAL, { timeout: 30_000 });
  await expect(page.getByTestId('target-cluster')).toBeVisible({ timeout: 30_000 });
});

test('a key that names targets in more than one cluster offers a chooser', async ({ page }) => {
  await page.addInitScript(installScaleFixture);
  await appReady(page);
  await page.evaluate(() => { (window as unknown as { __pactoAmbiguousTarget?: boolean }).__pactoAmbiguousTarget = true; });
  await page.goto('/#/fleet/targets/scale-ambiguous%2Fkubernetes%2Fpayments?correlationID=INC-42');
  const chooser = page.getByTestId('target-key-chooser');
  await expect(chooser).toContainText('This target key names targets in more than one cluster; choose one.', { timeout: 30_000 });
  await expect(page.locator('h1').first()).toBeVisible();
  const links = chooser.locator('a');
  await expect(links).toHaveCount(7);
  for (const a of await links.all()) {
    const key = await a.locator('bdi').textContent();
    expect(key).toMatch(/^([0-9a-f-]+|_)\/scale-ambiguous\/kubernetes\/payments$/);
    expect(await a.getAttribute('href')).toBe(`#/fleet/targets/${encodeURIComponent(key ?? '')}?correlationID=INC-42`);
  }
  // The target with no known cluster has a key of its own, so its link leaves the chooser.
  await expect(chooser.getByText('scale-ambiguous/kubernetes/payments (cluster unknown)')).toBeVisible();
});

test('a graph focus on a key that names more than one cluster offers the candidates to focus', async ({ page }) => {
  await page.addInitScript(installScaleFixture);
  await appReady(page);
  await page.evaluate(() => { (window as unknown as { __pactoAmbiguousTarget?: boolean }).__pactoAmbiguousTarget = true; });
  await page.goto('/#/fleet/graph/target/scale-ambiguous%2Fkubernetes%2Fpayments');
  const links = page.getByTestId('target-key-chooser').locator('a');
  await expect(links).toHaveCount(7, { timeout: 30_000 });
  for (const a of await links.all()) {
    const key = await a.locator('bdi').textContent();
    expect(await a.getAttribute('href')).toBe(`#/fleet/graph/target/${encodeURIComponent(key ?? '')}`);
  }
});
