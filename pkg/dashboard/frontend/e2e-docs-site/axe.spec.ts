import { devices, expect, test, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

// The accessibility gate for the DOCUMENTATION SITE, the counterpart to
// e2e/axe.spec.ts (which audits the dashboard bundle). The two are separate
// products with separate stylesheets, and the docs site had four real defects
// no Markdown check could see: prose links distinguished from body text by
// colour alone (2.56:1 light / 1.83:1 dark, WCAG 1.4.1), comment and variable
// tokens in code blocks at 4.47:1 on the light surface (1.4.3), and Material's
// search dialog and progress bar shipping with no accessible name (1.1.1).
// Fixed in docs/stylesheets/extra.css and docs/javascripts/aria-labels.js.
//
// NO rule is disabled. Everything axe knows is asserted, best-practice rules
// included; the one theme-owned node that cannot be satisfied is subtracted by
// target below, so the rule stays armed for every other element on the page.
const PAGES = [
  // The hero template from overrides/, the only page not rendered by stock Material.
  '/',
  // Long-form prose with admonitions, tabbed blocks and many inline links.
  '/quickstart/',
  '/installation/',
  // The densest table in the site, and the most code-block-heavy page.
  '/contract-reference/validation/',
  // Assembled into the site by the integration hook, not authored under docs/.
  '/integrations/kubernetes/installation/',
  // Generated from the CRD schema, and the only page wide enough to overflow its
  // table wrapper at a desktop width — which is how a scrollable region with no
  // keyboard stop stayed invisible to a five-page audit.
  '/integrations/kubernetes/crd-reference/',
];

// Material renders its instant-navigation progress bar as a bare div at the top
// of <body>, outside every landmark, and a progressbar cannot itself be one.
// It is theme-owned markup we do not author; overriding base.html to move one
// empty div is a worse trade than naming the exception here. Everything else on
// the page — including our own hero, which had to be given a name to become a
// landmark (overrides/home.html) — is still held to the rule.
const THEME_EXCEPTIONS = [{ rule: 'region', target: '.md-progress' }];

async function audit(page: Page, label: string) {
  // Audit the RESTING page. `.md-content__inner` runs the `pacto-page-in` fade
  // (extra.css), and axe blends a foreground against its background using the
  // opacity in effect when it samples: caught at opacity 0.705 this page
  // reported 40 contrast failures that all cleared AA once the fade finished.
  // Racing a timeout so a never-ending animation cannot hang the audit.
  await Promise.race([
    page.evaluate(() =>
      Promise.all((document.getAnimations?.() ?? []).map((a) => a.finished.catch(() => {}))),
    ),
    page.waitForTimeout(1_000),
  ]);
  const results = await new AxeBuilder({ page }).analyze();
  const violations = results.violations
    .map((v) => ({
      ...v,
      nodes: v.nodes.filter(
        (n) =>
          !THEME_EXCEPTIONS.some(
            (e) => e.rule === v.id && n.target.join(' ') === e.target,
          ),
      ),
    }))
    // A rule whose every node was a named exception is not a finding; one with
    // any node left is, and it reports only the nodes that are genuinely ours.
    .filter((v) => v.nodes.length > 0);

  expect(
    violations.map((v) => ({
      id: v.id,
      impact: v.impact,
      nodes: v.nodes.map((n) => ({
        target: n.target,
        why: (n.any[0]?.message ?? n.all[0]?.message ?? '').slice(0, 160),
      })),
    })),
    label,
  ).toEqual([]);
}

for (const path of PAGES) {
  // Both schemes, because they are different palettes and a ratio that clears
  // AA on white can fail on the slate surface — which is exactly how the link
  // contrast defect above read differently in each.
  test(`light scheme is accessible (${path})`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: 'light' });
    await page.goto(path);
    await expect(page.locator('[data-md-color-scheme]').first()).toHaveAttribute(
      'data-md-color-scheme',
      'default',
    );
    await audit(page, `axe (light) ${path}`);
  });

  test(`dark scheme is accessible (${path})`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: 'dark' });
    await page.goto(path);
    // Assert the palette actually flipped. Without this the dark run silently
    // re-audits the light page and reports a pass for a scheme it never loaded.
    await expect(page.locator('[data-md-color-scheme]').first()).toHaveAttribute(
      'data-md-color-scheme',
      'slate',
    );
    await audit(page, `axe (dark) ${path}`);
  });
}

// A phone is a different document, not a narrower one: the drawer renders every
// navigation level at once, and a code block that fits at 1440px overflows at
// 390px. Both of those produced real defects a desktop-only audit could not see
// — three navigation landmarks with an empty accessible name, and 112 scrollable
// regions with no way to reach them from the keyboard. Light scheme only: the
// palette is what the two scheme runs above are for, and repeating it here would
// double the audit to re-check contrast that does not depend on the viewport.
// The device descriptor names a browser as well as a screen, and `use()` inside
// a describe cannot switch browsers. The screen is the whole point here; the
// engine stays the project's.
const IPHONE = { ...devices['iPhone 13'] } as Record<string, unknown>;
delete IPHONE.defaultBrowserType;

test.describe('mobile', () => {
  test.use(IPHONE);

  for (const path of PAGES) {
    test(`the phone layout is accessible (${path})`, async ({ page }) => {
      await page.goto(path);
      await audit(page, `axe (mobile) ${path}`);
    });
  }
});
