import { test, expect, type Page } from '@playwright/test';

// section K: validate that Mermaid diagrams in bundle docs actually RENDER in the browser
// — not merely that the ```mermaid source survives. The demo's payments-service
// v2.1.0 overview carries a flowchart; opening it must replace the code block with
// a real <svg>. jsdom cannot render Mermaid (it needs a browser), so this can only
// be proven here.

async function waitReady(page: Page) {
  await page.goto('/');
  await expect(page.getByRole('link', { name: 'Operational Graph' })).toBeVisible({ timeout: 20_000 });
}

// DEFERRED (Part 1.4 option C): this validated Mermaid RENDERING via the legacy
// ServiceDetailView. The Phase-4 dual-UI cleanup correctly makes the legacy service
// detail unreachable on a Fleet host (a Fleet demo canonicalizes #/services/:name to the
// product entity page), and the product entity pages currently expose bounded doc
// PREVIEWS (title/path), not rendered content -- the product API does not carry full doc
// content. Rich-doc/Mermaid rendering in the product IA (product API doc content + a
// product doc viewer) is a later-phase migration; the capability still works on the
// non-Fleet `pacto doc` export via ServiceDetailView. Re-enable this against the product
// doc viewer once that migration lands.
test.fixme('a bundle doc with a mermaid diagram renders to SVG', async ({ page }) => {
  await waitReady(page);
  await page.goto('/#/services/payments-service');

  // Open the Documentation collapsible section, then expand the overview doc.
  const docSection = page.locator('button.section-toggle', { hasText: 'Documentation' });
  await expect(docSection).toBeVisible({ timeout: 20_000 });
  await docSection.click();
  await page.getByRole('button', { name: /overview\.md/ }).first().click();

  // Mermaid replaced the code block with a rendered SVG diagram.
  const diagram = page.locator('.mermaid-diagram svg').first();
  await expect(diagram).toBeVisible({ timeout: 20_000 });
  // The rendered SVG carries our node labels as native <text> (htmlLabels:false).
  await expect(page.locator('.mermaid-diagram').first()).toContainText('payments-service');
  // The raw ```mermaid fence must NOT be left visible as source.
  await expect(page.locator('code.language-mermaid')).toHaveCount(0);
});
