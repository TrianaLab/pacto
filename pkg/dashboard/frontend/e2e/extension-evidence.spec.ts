import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { appReady, settle } from './ready';

// Two trusted producers report on payments-service's extension capabilities in the
// demo: the dispute auditor reports 2.0.1's payments.trianalab.com/dispute present and
// the refund checker reports 1.2.1's payments.trianalab.com/refund absent. Against the
// built WASM demo, each revision page names the extension by its ref, says who checks
// it and keeps the producer, key and time in one discreet mark.

const T = 30_000;

// Revision keys are service@digest, so a version never goes in a URL: pick the list link
// that names the version.
async function openRevision(page: Page, version: string) {
  await appReady(page);
  await page.goto('/#/fleet/revisions?service=payments-service');
  const escaped = version.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const exact = new RegExp(`\\b${escaped}(?![.\\d])`);
  const link = page.locator('main a[href*="/fleet/revisions/"]').filter({ hasText: exact }).first();
  await expect(link).toBeVisible({ timeout: T });
  await link.click();
  const sec = page.locator('details').filter({ has: page.locator('summary.ps-head', { hasText: 'Capabilities' }) }).first();
  await expect(sec).toBeAttached({ timeout: T });
  await sec.locator('summary').first().click();
  return sec;
}

test.describe('WASM demo — extension capabilities a producer checks', () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  const cases = [
    { version: '2.0.1', ref: 'payments.trianalab.com/dispute', label: 'Checked by dispute-auditor',
      tip: /dispute-auditor \(key dispute-auditor-2026\) reported it present .+ through a signed report\./ },
    { version: '1.2.1', ref: 'payments.trianalab.com/refund', label: 'Reported absent by refund-checker',
      tip: /refund-checker \(key refund-checker-2026\) reported it absent .+ through a signed report\./ },
  ];
  for (const c of cases) {
    test(`${c.version} names ${c.ref} and who checks it`, async ({ page }) => {
      const sec = await openRevision(page, c.version);
      const row = sec.locator('.re-caps li').filter({ has: page.locator('.ri-name', { hasText: c.ref }) });
      await expect(row).toHaveCount(1);
      await expect(row.getByTestId('ext-check')).toHaveText(c.label);
      await row.getByRole('button', { name: `About ${c.ref}` }).click();
      await expect(row.getByRole('note')).toContainText(c.tip);
      await page.mouse.move(0, 0);
      await settle(page);
      const r = await new AxeBuilder({ page }).include('[data-testid=ext-check]')
        .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
      expect(r.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) }))).toEqual([]);
    });
  }
});
