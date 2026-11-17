import { test, expect, type Page } from '@playwright/test';
import { appReady } from './ready';

// The demo's newsletter-service declares nothing; its pacto.inferred.yaml holds what
// Pacto read from the folder: the port its Dockerfile exposes, the owner its
// catalog-info.yaml names over CODEOWNERS and the team label, the environments its
// values files name and a chart dependency with no Chart.lock. Against the built WASM
// demo: the revision page lists those rows, each with one mark whose note names the
// source, and the files Pacto did not read sit in one closed disclosure.

const T = 30_000;

function watchConsole(page: Page): string[] {
  const errors: string[] = [];
  page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });
  page.on('pageerror', (e) => errors.push(e.message));
  return errors;
}

test.describe('WASM demo — a service whose folder Pacto reads', () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  test('the revision page lists the port, the owner and the environments the folder states', async ({ page }) => {
    const errors = watchConsole(page);
    await appReady(page);
    await page.goto('/#/fleet/revisions?service=newsletter-service');
    const link = page.locator('main a[href*="/fleet/revisions/"]').first();
    await expect(link).toBeVisible({ timeout: T });
    await page.goto(`/${await link.getAttribute('href')}`);

    const inferred = page.getByTestId('revision-inferred');
    await expect(inferred).toBeVisible({ timeout: T });
    await inferred.locator('.ifl-more > summary').click();
    const row = (field: string) => inferred.getByTestId('inferred-field').filter({ has: page.locator('code', { hasText: new RegExp(`^${field.replace(/[[\].]/g, '\\$&')}$`) }) });
    const note = async (field: string) => {
      await row(field).getByRole('button', { name: `About inferred ${field}` }).click();
      const n = row(field).getByRole('note');
      await expect(n).toBeVisible();
      return n;
    };

    await expect(row('ports[8080]')).toContainText('8080/tcp');
    await expect(await note('ports[8080]')).toContainText('Source: Dockerfile:12.');
    await page.keyboard.press('Escape');

    await expect(row('service.owner')).toContainText('team-growth');
    const owner = await note('service.owner');
    await expect(owner).toContainText('Source: catalog-info.yaml:7.');
    await expect(owner).toContainText('not verified; never clears a gate.');
    await expect(owner).toContainText('Outranks CODEOWNERS:4, deploy/deployment.yaml:6.');
    await page.keyboard.press('Escape');

    await expect(row('environments[staging].file')).toContainText('chart/values-staging.yaml');
    await expect(row('environments[production].file')).toContainText('chart/values-production.yaml');
    await expect(inferred).not.toContainText('demo-placeholder-password');

    const findings = page.getByTestId('revision-discovery-findings');
    await expect(findings).not.toHaveAttribute('open', /.*/);
    await expect(findings.locator('summary')).toContainText('1 file not read');
    await findings.locator('summary').click();
    await expect(findings).toContainText("chart dependency 'redis' has no Chart.lock digest, not read");
    await expect(findings).toContainText('chart/Chart.yaml:5');
    expect(errors).toEqual([]);
  });
});
