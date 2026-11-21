import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { settle } from './ready';

// External systems against the built WASM demo, at 1280px with the wide fallback font CI
// renders with: the Overview services line, the list and its class filter, an external
// system's page with its consumers and provenance, and the operational graph focused on
// one. The demo organization catalog (examples/demo/external-catalog.yaml) joins
// support-assistant and pay-agent, which both declare "model", on llm-provider, and
// carrier-gateway's parcel-carrier on its own entry. The demo embeds that catalog, so
// its signature reads unknown.

const T = 20_000;
const WIDE_FONT = '*{font-family: Verdana, "DejaVu Sans", sans-serif !important}';
const noSideScroll = (page: Page) => page.evaluate(() => [document.documentElement, document.body].every((e) => e.scrollWidth <= e.clientWidth + 1));

async function audit(page: Page, what: string) {
  await settle(page);
  const r = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
  expect(r.violations, `${what}: ${JSON.stringify(r.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) })))}`).toEqual([]);
}

test.describe('WASM demo — external systems', () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  test('the Overview splits the services line and links the external systems', async ({ page }) => {
    await page.goto('/#/fleet');
    await page.addStyleTag({ content: WIDE_FONT });
    const line = page.getByTestId('overview-services-line');
    await expect(line).toBeVisible({ timeout: T });
    await expect(line).toContainText(/\d+ declared(, \d+ inferred[^,]*)?, \d+ external/);
    await line.getByTestId('overview-externals').click();
    await expect(page).toHaveURL(/#\/fleet\/externals$/);
    await expect(page.getByRole('heading', { level: 1, name: 'External systems' })).toBeVisible({ timeout: T });
    // Services hosts the list: it is marked beneath that item, and no item is added.
    const nav = page.getByRole('navigation', { name: 'Primary' });
    await expect(nav.getByRole('link', { name: 'Services', exact: true })).toHaveAttribute('aria-current', 'true');
    await expect(nav.getByRole('link', { name: /External/ })).toHaveCount(0);
  });

  test('the list names parcel-carrier, joined by its catalog entry, and the class filter narrows it', async ({ page }) => {
    await page.goto('/#/fleet/externals');
    await page.addStyleTag({ content: WIDE_FONT });
    const list = page.getByTestId('external-list');
    await expect(list).toBeVisible({ timeout: T });
    const row = list.locator('tbody tr').filter({ has: page.locator('a.entity-link[href*="/fleet/externals/parcel-carrier"]') });
    await expect(row.locator('[data-label="Class"]')).toHaveText('Vendor API');
    await expect(row.locator('[data-label="Joined by"]')).toHaveText('catalog entry parcel-carrier');
    await expect(row.locator('[data-label="Consumers"]')).toHaveText('1');
    const llm = list.locator('tbody tr').filter({ has: page.locator('a.entity-link[href*="/fleet/externals/llm-provider"]') });
    await expect(llm.locator('[data-label="Consumers"]')).toHaveText('2');
    expect(await noSideScroll(page)).toBe(true);
    await audit(page, 'external list');
    await page.getByTestId('external-class-filter').getByRole('link', { name: 'Managed cloud' }).click();
    await expect(page).toHaveURL(/class=managed-cloud/);
    await expect(page.locator('a.entity-link[href*="/fleet/externals/parcel-carrier"]')).toHaveCount(0, { timeout: T });
  });

  test('an external system page lists its consumers and the organization catalog that joined them', async ({ page }) => {
    await page.goto('/#/fleet/externals/llm-provider');
    await page.addStyleTag({ content: WIDE_FONT });
    const consumers = page.getByTestId('external-consumers');
    await expect(consumers).toBeVisible({ timeout: T });
    await expect(consumers.locator('tbody tr')).toHaveCount(2);
    await expect(consumers).toContainText('support-assistant');
    await expect(consumers).toContainText('pay-agent');
    await expect(consumers).toContainText('hosted-llm/2026-06');
    const catalog = page.getByTestId('external-catalog');
    await expect(catalog).toContainText('Organization catalog');
    await expect(catalog).toContainText('entry llm-provider');
    await expect(catalog).toContainText('embedded copy, signature unknown (the demo catalog is embedded in the demo; it is not signed).');
    await expect(page.getByTestId('external-name-only')).toHaveCount(0);
    const declared = page.locator('[data-testid="external-source"][data-provenance="declared"]');
    // support-assistant and pay-agent both declare "model"; the catalog joins them here.
    await expect(declared.locator('[data-cell="where"]').filter({ hasText: '(name: model)' })).toHaveCount(2);
    await page.getByTestId('external-more-details').click();
    await expect(declared.locator('[data-cell="details"]').filter({ hasText: 'version hosted-llm/2026-06' })).toHaveCount(1);
    // Services hosts the list, so the trail runs through it as the nav does.
    await expect(page.getByRole('navigation', { name: 'Breadcrumb' })).toContainText(/Services.*External systems/);
    expect(await noSideScroll(page)).toBe(true);
    await audit(page, 'external entity');
  });

  test('an external only the node agent saw lists the cluster source as its one observed source', async ({ page }) => {
    // examples/demo/source_externals.go demoKernelEgressEdges: carrier-gateway's SFTP
    // partner, named by no contract, trace or catalog entry, grouped by its domain.
    await page.goto('/#/fleet/externals/host/northwind-partners.com');
    await page.addStyleTag({ content: WIDE_FONT });
    const rows = page.getByTestId('external-source');
    const observed = rows.and(page.locator('[data-provenance="observed"]'));
    await expect(observed).toHaveCount(1, { timeout: T });
    await expect(observed.locator('[data-cell="where"]')).toContainText('k8s-production');
    await expect(observed.locator('[data-cell="where"]')).not.toContainText('otel-traces');
    await expect(observed.locator('[data-cell="service"]')).toContainText('carrier-gateway');
    await expect(page.locator('[data-testid="external-source"][data-provenance="declared"]')).toHaveCount(0);
    expect(await noSideScroll(page)).toBe(true);
  });

  test('the graph focused on an external system draws it with its consumers', async ({ page }) => {
    await page.goto('/#/fleet/graph/external/llm-provider');
    await page.addStyleTag({ content: WIDE_FONT });
    await expect(page.getByTestId('graph-legend')).toContainText('External system', { timeout: T });
    const textalt = page.getByTestId('graph-textalt');
    await textalt.locator('summary').click();
    await expect(textalt.getByRole('button', { name: /support-assistant/ }).first()).toBeVisible();
    await expect(textalt.getByRole('button', { name: /pay-agent/ }).first()).toBeVisible();
    await expect(textalt.getByRole('button', { name: /llm-provider|Hosted LLM provider/ }).first()).toBeVisible();
    await expect(page.locator('main')).toContainText(/(llm-provider|Hosted LLM provider) is an external system/);
    expect(await noSideScroll(page)).toBe(true);
  });
});
