import { test, expect, type Page } from '@playwright/test';

// The demo's newsletter-service ships a zero-byte pacto.yaml beside chart/Chart.yaml
// (pactoVersion 2.5). Against the built WASM demo: the service is listed and counted,
// its name reads as inferred with the line it came from, and its version as not
// declared, never as a value Pacto made up.

const T = 20_000;

function watchConsole(page: Page): string[] {
  const errors: string[] = [];
  page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });
  page.on('pageerror', (e) => errors.push(e.message));
  return errors;
}

test.describe('WASM demo — a service with an empty contract', () => {
  test('the services list holds newsletter-service with the inferred name chip', async ({ page }) => {
    const errors = watchConsole(page);
    await page.goto('/#/fleet/services?text=newsletter');
    const row = page.getByTestId('service-list').locator('li').filter({ hasText: 'newsletter-service' });
    await expect(row).toHaveCount(1, { timeout: T });
    const chip = row.getByTestId('name-inferred');
    await expect(chip).toBeVisible();
    await expect(chip).toContainText('Name inferred from chart/Chart.yaml:2');
    expect(errors).toEqual([]);
  });

  test('its revision page shows the inferred name with its source and no declared version', async ({ page }) => {
    const errors = watchConsole(page);
    await page.goto('/#/fleet/revisions?service=newsletter-service');
    const link = page.locator('main a[href*="/fleet/revisions/"]').first();
    await expect(link).toBeVisible({ timeout: T });
    await page.goto(`/${await link.getAttribute('href')}`);
    const inferred = page.getByTestId('revision-inferred');
    await expect(inferred).toBeVisible({ timeout: T });
    await expect(inferred).toContainText('Inferred');
    await expect(inferred).toContainText('service.name');
    // The source sits in the mark's note, opened by its button.
    await inferred.getByRole('button', { name: 'About inferred service.name' }).click();
    await expect(inferred.getByRole('note')).toContainText('Source: chart/Chart.yaml:2');
    await expect(page.getByText('Version not declared', { exact: true })).toBeVisible();
    expect(errors).toEqual([]);
  });

  test('its service page title says the name is inferred and where from', async ({ page }) => {
    const errors = watchConsole(page);
    await page.goto('/#/fleet/services/newsletter-service');
    await expect(page.getByTestId('page-title')).toContainText('newsletter-service', { timeout: T });
    const chip = page.getByTestId('page-name-inferred');
    await expect(chip).toBeVisible();
    await expect(chip).toContainText('Name inferred from chart/Chart.yaml:2');
    expect(errors).toEqual([]);
  });

  test('the overview counts it', async ({ page }) => {
    const errors = watchConsole(page);
    await page.goto('/#/fleet');
    const count = page.locator('dt', { hasText: /^Services$/ }).locator('xpath=following-sibling::dd[1]');
    await expect(count).toBeVisible({ timeout: T });
    const all = await page.evaluate(async () => {
      const r = await (await fetch('/api/fleet/entities?kinds=service&declaration=declared&limit=1')).json();
      const n = await (await fetch('/api/fleet/entities?kinds=service&text=newsletter-service&limit=5')).json();
      const ov = await (await fetch('/api/fleet/overview')).json();
      return { total: r.total as number, goldenPaths: (ov.summary?.goldenPaths ?? 0) as number,
        newsletter: (n.entities ?? []).map((e: { label: string }) => e.label) };
    });
    expect(all.newsletter).toContain('newsletter-service');
    // The overview counts golden paths and inferred services apart; every other service, this one included, is in.
    // With external systems declared the line reads "N: D declared, E external"; the first link is D.
    await expect(count.locator('a').first()).toHaveText(new RegExp(`^${all.total - all.goldenPaths}( declared)?$`));
    expect(errors).toEqual([]);
  });
});
