import { test, expect, type Page } from '@playwright/test';
import { appReady, openMermaidDoc } from './ready';

// The demo carries the dashboard's Content-Security-Policy as a <meta> tag (see
// DEMO_CSP in examples/demo/Makefile). Walking the pages that run the most code --
// the graph, delivery and a Mermaid diagram -- with no violation proves the policy
// that backs the Markdown sanitiser does not break the product.

async function collectViolations(page: Page): Promise<string[]> {
  const seen: string[] = [];
  await page.exposeFunction('__cspViolation', (v: string) => seen.push(v));
  await page.addInitScript(() => {
    document.addEventListener('securitypolicyviolation', (e) => {
      (window as unknown as { __cspViolation: (v: string) => void }).__cspViolation(
        `${e.effectiveDirective} blocked ${e.blockedURI || 'inline'} at ${e.sourceFile}:${e.lineNumber}`,
      );
    });
  });
  return seen;
}

test('the dashboard runs under its Content-Security-Policy with no violation', async ({ page }) => {
  const violations = await collectViolations(page);
  await appReady(page);
  await expect(page.locator('meta[http-equiv="Content-Security-Policy"]')).toHaveAttribute('content', /script-src 'self' 'wasm-unsafe-eval';/);

  await page.goto('/#/fleet/graph');
  await expect(page.getByTestId('graph-discovery')).toBeVisible({ timeout: 20_000 });
  await page.goto('/#/delivery/inbox');
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible({ timeout: 20_000 });

  const doc = await openMermaidDoc(page);
  await expect(doc.locator('.mermaid-diagram svg').first()).toBeVisible({ timeout: 20_000 });

  expect(violations).toEqual([]);

  // The policy is live: an inline script is refused and reported.
  await page.evaluate(() => {
    const s = document.createElement('script');
    s.textContent = 'window.__inlineRan = true';
    document.head.appendChild(s);
  });
  await expect.poll(() => violations.some((v) => v.startsWith('script-src'))).toBe(true);
  expect(await page.evaluate(() => (window as unknown as { __inlineRan?: boolean }).__inlineRan)).toBeUndefined();
});
