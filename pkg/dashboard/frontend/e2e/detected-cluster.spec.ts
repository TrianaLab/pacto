import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { appReady, settle } from './ready';

// What the demo cluster's operator detected (examples/demo/detected.json, the advisory
// source cluster-detection in examples/demo/source_fleet.go) against the built WASM
// demo: seven inferred rows inside the Detected providers block of Data sources, each
// with one provenance mark, the registry's proposal behind More details, and one toggle
// that hides them and says so in the URL, plus the operator's declared Installation row
// (510-S6), which the toggle keeps. No total row count is asserted: other stories add
// rows of their own.

const T = 20_000;
const DETECTED = ['Registry', 'Cloud', 'Sign-in', 'Exposure', 'Metrics', 'Nodes', 'Pull Secret'];
const DECLARED = "Declared: by the operator's configuration";

test.describe('WASM demo: what the cluster provides', () => {
  test('the detected rows are inferred, explain themselves and hide behind one toggle', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, '/#/fleet/sources');
    const block = page.getByTestId('detected-providers');
    await expect(block.getByRole('heading', { level: 2, name: 'Detected providers' })).toBeVisible({ timeout: T });
    // Rows and their Show all render together, so once one row shows the button's
    // presence is settled.
    await expect(block.getByTestId('provider-row').first()).toBeVisible({ timeout: T });
    const showAll = block.getByRole('button', { name: /^Show all/ });
    if (await showAll.isVisible()) await showAll.click();

    const detected = block.getByTestId('provider-row').filter({ has: page.getByRole('button', { name: 'Inferred: from cluster-detection' }) });
    await expect(detected).toHaveCount(DETECTED.length, { timeout: T });
    for (const name of DETECTED) {
      await expect(detected.locator('.dp-name').getByTitle(name, { exact: true })).toHaveCount(1);
    }

    const registry = detected.filter({ has: page.locator('.dp-name').getByTitle('Registry', { exact: true }) });
    const evidence = registry.locator('.dp-evidence');
    await expect(evidence).toContainText('ghcr.io/acme in 9 of 11 namespaces, 87% of workloads');
    await expect(evidence).toContainText('from running pods');
    // Muted through the text token, never a fade.
    expect(await registry.evaluate((el) => getComputedStyle(el).opacity)).toBe('1');

    const mark = registry.getByRole('button', { name: 'Inferred: from cluster-detection' });
    await mark.click();
    const tip = registry.getByRole('note');
    await expect(tip).toBeVisible();
    await expect(tip).toContainText('Inferred from cluster-detection');
    await expect(tip).toContainText('Nothing was turned on.');
    await page.keyboard.press('Escape');

    const more = registry.locator('details');
    await expect(more).not.toHaveAttribute('open');
    await more.locator('summary').click();
    await expect(more).toContainText('oci://ghcr.io/acme/<service>:<version>');
    await expect(more).toContainText('docker.io/library: 3 of 11 namespaces');

    await settle(page);
    const axe = await new AxeBuilder({ page }).include('[data-testid="detected-providers"]')
      .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
    expect(axe.violations).toEqual([]);

    await block.getByTestId('providers-inferred-toggle').getByRole('checkbox').uncheck();
    await expect(page).toHaveURL(/inferred=0/);
    await expect(detected).toHaveCount(0);
    await expect(block.getByTestId('providers-inferred-hidden')).toBeVisible();
  });

  test('the installation is declared and the pull Secret is a muted proposal the toggle hides', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, '/#/fleet/sources');
    const block = page.getByTestId('detected-providers');
    await expect(block.getByTestId('provider-row').first()).toBeVisible({ timeout: T });
    const showAll = block.getByRole('button', { name: /^Show all/ });
    if (await showAll.isVisible()) await showAll.click();

    const installation = block.getByTestId('provider-row').filter({ has: page.locator('.dp-name').getByTitle('Installation', { exact: true }) });
    await expect(installation).toHaveCount(1, { timeout: T });
    await expect(installation.locator('.dp-evidence')).toContainText('ghcr.io/acme');
    await expect(installation).not.toHaveClass(/dp-inferred/);
    await installation.getByRole('button', { name: DECLARED }).click();
    await expect(installation.getByRole('note')).toContainText("Declared by the operator's configuration");
    // Provenance, asOf and completeness, as the inferred tip carries them.
    await expect(installation.getByRole('note')).toContainText(/as of .+, complete\./);
    await page.keyboard.press('Escape');

    const pull = block.getByTestId('provider-row').filter({ has: page.locator('.dp-name').getByTitle('Pull Secret', { exact: true }) });
    await expect(pull).toHaveClass(/dp-inferred/);
    await pull.getByRole('button', { name: 'Inferred: from cluster-detection' }).click();
    await expect(pull.getByRole('note')).toContainText('named in the pod specs of');
    await page.keyboard.press('Escape');

    await block.getByTestId('providers-inferred-toggle').getByRole('checkbox').uncheck();
    await expect(page).toHaveURL(/inferred=0/);
    await expect(pull).toHaveCount(0);
    await expect(installation).toHaveCount(1);
  });

  test('the provenance tip stays inside the viewport at every width', async ({ page }) => {
    // The tip is the only place an inferred row names its source, asOf and completeness.
    // In the wide layout the mark sits mid-row, so a tip grown leftward from it once left
    // the viewport (900px). The layout gate never opens a tip, so the widths are swept here.
    await appReady(page, '/#/fleet/sources');
    const block = page.getByTestId('detected-providers');
    // Other stories' rows sort first, so the detected rows may sit behind Show all.
    await expect(block.getByTestId('provider-row').first()).toBeVisible({ timeout: T });
    const showAll = block.getByRole('button', { name: /^Show all/ });
    if (await showAll.isVisible()) await showAll.click();
    const mark = block.getByRole('button', { name: 'Inferred: from cluster-detection' }).first();
    await expect(mark).toBeVisible({ timeout: T });
    const widths = [...Array.from({ length: 29 }, (_, i) => 320 + i * 40), 1920, 2560, 3440];
    const outside: string[] = [];
    for (const width of widths) {
      await page.setViewportSize({ width, height: 900 });
      await mark.click();
      const tip = block.locator('.prov-tip:not([hidden])');
      await expect(tip).toBeVisible();
      const box = await tip.evaluate((el) => { const r = el.getBoundingClientRect(); return { left: r.left, right: r.right, max: document.documentElement.clientWidth }; });
      if (box.left < 0 || box.right > box.max) outside.push(`${width}px: ${Math.round(box.left)}..${Math.round(box.right)} of ${box.max}`);
      await page.keyboard.press('Escape');
    }
    expect(outside).toEqual([]);
  });
});
