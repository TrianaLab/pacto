import { test, expect } from '@playwright/test';
import { appReady, openMermaidDoc } from './ready';

// Rich bundle documentation in the PRODUCT information architecture: a revision page
// must not merely list its docs, it must open them, render the Markdown as formatted
// content and render a Mermaid fence as a real diagram. jsdom cannot render Mermaid
// (it needs a browser), so this can only be proven here.
//
// The body is read lazily through the product API, keyed by the canonical revision key
// plus the exact published path, so what runs here is the mechanism a real deployment
// uses -- not a frontend fixture. The rejection cases (traversal, unlisted path,
// oversized body, two same-named services in different domains) are proven against the
// engine and the HTTP transport in Go, where they can be constructed exactly.

const T = 20_000;

test('a revision doc opens as rendered Markdown with its mermaid diagram as SVG', async ({ page }) => {
  await appReady(page);
  const doc = await openMermaidDoc(page);

  // Formatted content, not raw source: the heading is an element, and the Markdown
  // syntax that produced the second heading is gone from the text.
  await expect(doc.locator('.markdown-body h1')).toHaveText(/Payments Service/, { timeout: T });
  await expect(doc.locator('.markdown-body')).not.toContainText('## Request flow');

  // Mermaid replaced the code block with a rendered SVG diagram.
  await expect(doc.locator('.mermaid-diagram svg').first()).toBeVisible({ timeout: T });
  // The rendered SVG carries our node labels as native <text> (htmlLabels:false).
  await expect(doc.locator('.mermaid-diagram').first()).toContainText('payments-service');
  // The raw mermaid fence must NOT be left visible as source.
  await expect(page.locator('code.language-mermaid')).toHaveCount(0);
});

test('a revision doc opens in a keyboard-usable reading view', async ({ page }) => {
  await appReady(page);
  const doc = await openMermaidDoc(page);
  await expect(doc.locator('.markdown-body h1')).toBeVisible({ timeout: T });

  await doc.getByRole('button', { name: 'Read full screen' }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toBeVisible({ timeout: T });
  await expect(dialog.locator('.markdown-body h1')).toHaveText(/Payments Service/);
  // The diagram renders in the reading view too, not only inline.
  await expect(dialog.locator('.mermaid-diagram svg').first()).toBeVisible({ timeout: T });

  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();
});
