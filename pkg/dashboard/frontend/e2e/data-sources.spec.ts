import { test, expect, type Page } from '@playwright/test';
import { boot } from './typographyChecks';

/**
 * Data sources as a product surface: findable from the overview, walkable to a source,
 * and honest about the two different things a source page counts.
 *
 * The demo fleet is the fixture and it is adversarial on purpose (examples/demo/
 * source_fleet.go): six sources in three health states, of which `local` is Available
 * and `edge-cluster` is Unavailable. That combination is the whole point -- it means
 * the snapshot is partial WHILE the source you are looking at is fine, which is the
 * state that used to print "Source unavailable" above a page badged Available.
 *
 * None of these can be proven off the components. The unit tests prove each piece draws
 * what it is handed; only the built browser proves the overview, the inventory and the
 * source page are describing the same six sources, that the links between them land,
 * and that a number computed over the complete population did not quietly become a
 * number computed over the page.
 */

const flat = (s: string | null) => (s ?? '').replace(/\s+/g, ' ').trim();

/** The demo fleet: 4 available, 1 partial (registry-mirror), 1 unavailable (edge-cluster). */
const DEMO_SOURCES = 6;

async function overviewSources(page: Page) {
  const band = page.locator('#sec-sources');
  await expect(band).toBeVisible();
  return band;
}

test.describe('the overview leads to the data sources', () => {
  test('data sources are a section of the overview, in its contents, with one way in', async ({ page }) => {
    await boot(page, '#/fleet');
    const band = await overviewSources(page);

    // A section, not a caption: its own heading, and the section is labelled by it.
    const heading = band.getByRole('heading', { level: 2, name: 'Data sources' });
    await expect(heading).toBeVisible();
    await expect(band).toHaveAttribute('aria-labelledby', (await heading.getAttribute('id'))!);

    // Findable from the page contents, which builds itself from the DOM. The entries
    // are BUTTONS, not links -- an href="#sec-..." would be a second meaning for the
    // fragment in a hash-routed app.
    await expect(page.locator('.toc').getByRole('button', { name: 'Data sources', exact: true })).toHaveCount(1);

    // One action into the complete inventory, and it is the only one in the section.
    const viewAll = band.getByRole('link', { name: 'View all data sources' });
    await expect(viewAll).toHaveAttribute('href', '#/fleet/sources');

    // ...and it did not become a fifth primary destination.
    const primary = page.locator('header nav').first();
    await expect(primary.getByRole('link', { name: /data source/i })).toHaveCount(0);
  });

  test('the overview states the whole source population, not the chips it drew', async ({ page }) => {
    await boot(page, '#/fleet');
    const band = await overviewSources(page);
    // The demo is small enough that the capped chip list happens to be complete. The
    // claim that matters is that the SENTENCE and the CHIPS agree with each other and
    // with the inventory -- three surfaces, one population.
    const tally = flat(await band.locator('.ov-tally').textContent());
    expect(tally).toBe(`${DEMO_SOURCES} data sources — 1 unavailable, 1 partial, 4 available.`);

    const chips = band.locator('a.sh-chip');
    await expect(chips).toHaveCount(DEMO_SOURCES);
    // Least healthy first, so the source that costs you data is the first one read.
    expect(flat(await chips.first().textContent())).toContain('Unavailable');
  });

  test('a source chip lands on that exact source, not on a filtered list of it', async ({ page }) => {
    await boot(page, '#/fleet');
    const band = await overviewSources(page);
    const chip = band.locator('a.sh-chip', { hasText: 'edge-cluster' });
    await expect(chip).toHaveAttribute('href', '#/fleet/sources/edge-cluster');
    await chip.click();
    await expect(page.locator('main h1')).toContainText('edge-cluster');
    await expect(page.locator('#sec-this-data-source')).toBeVisible();
  });
});

test.describe('overview to inventory to source and back', () => {
  test('the whole path walks, and every step knows where it is', async ({ page }) => {
    await boot(page, '#/fleet');

    // 1. Overview -> inventory.
    await (await overviewSources(page)).getByRole('link', { name: 'View all data sources' }).click();
    await expect(page).toHaveURL(/#\/fleet\/sources$/);
    await expect(page.locator('main h1')).toContainText('Data sources');
    // The inventory's own total agrees with the sentence the overview just showed.
    await expect(page.locator('.page-hd')).toContainText(`${DEMO_SOURCES} data sources`);

    // 2. Inventory -> a source.
    await page.locator('.lv-item a', { hasText: 'local' }).first().click();
    await expect(page.locator('main h1')).toContainText('local');

    // 3. The trail says how it got here, and the last crumb is not a link.
    const crumbs = page.getByRole('navigation', { name: 'Breadcrumb' });
    expect(flat(await crumbs.textContent()).replace(/\s*›\s*/g, ' > ')).toContain('Overview > Data sources > local');
    await expect(crumbs.getByRole('link', { name: 'Data sources' })).toHaveAttribute('href', '#/fleet/sources');

    // 4. ...and there is a route back to the inventory in the page itself, not only in
    //    the trail: a breadcrumb is a location, not an invitation.
    const allSources = page.locator('#sec-this-data-source').getByRole('link', { name: 'All data sources' });
    await expect(allSources).toHaveAttribute('href', '#/fleet/sources');

    // 5. Back and Forward restore real pages, not blank shells.
    await page.goBack();
    await expect(page.locator('main h1')).toContainText('Data sources');
    await expect(page.locator('.lv-item').first()).toBeVisible();
    await page.goBack();
    await expect(page.locator('main h1')).toContainText('Operational overview');
    await expect(page.locator('#sec-sources')).toBeVisible();
    await page.goForward();
    await expect(page.locator('main h1')).toContainText('Data sources');
    await page.goForward();
    await expect(page.locator('main h1')).toContainText('local');
    await expect(page.locator('#sec-this-data-source')).toBeVisible();
  });

  test('the inventory summary counts every source and filters to the bucket you pick', async ({ page }) => {
    await boot(page, '#/fleet/sources');
    const tally = page.getByTestId('source-tally');
    const links = tally.getByRole('link');
    await expect(links).toHaveText(['1 unavailable', '1 partial', '4 available']);

    // The bucket IS the filter. Following it must leave exactly that many rows.
    await links.first().click();
    await expect(page).toHaveURL(/sourceHealth=unavailable/);
    await expect(page.locator('.lv-item')).toHaveCount(1);
    await expect(page.locator('.lv-item').first()).toContainText('edge-cluster');

    // The summary is over the COMPLETE population, so a filter does not shrink it --
    // it only marks where you are.
    await expect(tally.getByRole('link')).toHaveText(['1 unavailable', '1 partial', '4 available']);
    await expect(tally.locator('a[aria-current="true"]')).toHaveText('1 unavailable');
  });
});

test.describe('a source page answers about itself', () => {
  test('an available source is never told it is unavailable by the fleet caveat', async ({ page }) => {
    // The demo snapshot is partial BECAUSE edge-cluster is down. `local` is not.
    await boot(page, '#/fleet/sources/local');

    // The header badges this source: Available.
    await expect(page.locator('.page-hd .tag')).toHaveText('Available');

    // The caveat, on the same screen, is about the SNAPSHOT and says so in its first
    // words -- then attributes the gap to a count of other sources.
    const caveat = flat(await page.locator('.knowledge').first().textContent());
    expect(caveat).toContain('The fleet snapshot is missing data.');
    expect(caveat).toContain('1 data source is unavailable');
    expect(caveat).toContain('this page may be incomplete');
    // The old wording, indistinguishable from a claim about this source.
    expect(caveat).not.toContain('Source unavailable');

    // And the page says in its own words that THIS source is fine.
    expect(flat(await page.locator('#sec-this-data-source .se-lead').textContent()))
      .toBe('This data source answered in full, so everything it holds is in the snapshot.');
  });

  test('records sent and product entities contributed are stated as different measurements', async ({ page }) => {
    await boot(page, '#/fleet/sources/local');

    // What it SENT: the raw records. `local` is the bundle registry, so revisions and
    // no targets -- the two are named separately and never summed on screen.
    const records = flat(await page.locator('.se-fact', { hasText: 'Source records' }).textContent());
    const sentRevisions = Number(records.match(/(\d+) contract revisions?/)![1]);
    const sentTargets = Number(records.match(/(\d+) operational targets?/)![1]);
    expect(sentRevisions).toBeGreaterThan(0);

    // What the PRODUCT owes it: more, and counted differently. Services are derived
    // from the revisions a source sends and are sent by nobody, so an honest
    // attribution is strictly larger here. Equal numbers would mean the page is
    // printing one measurement twice.
    const breakdown = flat(await page.locator('.se-breakdown').textContent());
    const services = Number(breakdown.match(/(\d+) services?/)![1]);
    expect(services).toBeGreaterThan(0);
    expect(breakdown).toContain(`from ${sentRevisions + sentTargets} records it sent`);

    // The preview is honest against the ENTITY total, never the record count.
    const total = Number(flat(await page.locator('[data-testid="preview-count"]').first().textContent()).match(/of (\d+)/)?.[1] ?? '0');
    expect(total).toBeGreaterThan(sentRevisions + sentTargets);

    // ...and the difference is explained on the page, not left for a reader to subtract.
    await expect(page.getByRole('heading', { name: 'Product entities contributed' })).toBeVisible();
  });

  test('a contributed entity is reachable from the source that supplied it', async ({ page }) => {
    await boot(page, '#/fleet/sources/local');
    const first = page.locator('.se-breakdown').locator('xpath=following-sibling::*').getByRole('link').first();
    const href = await first.getAttribute('href');
    expect(href).toMatch(/^#\/fleet\/(services|revisions|targets)\//);
    await first.click();
    await expect(page.locator('main h1')).toBeVisible();
    await expect(page.locator('main')).not.toContainText('Something went wrong');
  });

  test('an unavailable source says what failed, in words, under a heading of its own', async ({ page }) => {
    await boot(page, '#/fleet/sources/edge-cluster');
    await expect(page.locator('.page-hd .tag')).toHaveText('Unavailable');
    expect(flat(await page.locator('#sec-this-data-source .se-lead').textContent()))
      .toBe('This data source did not answer, so nothing it holds reached the snapshot.');

    // The failure is a section, so the contents navigator can offer it and it cannot be
    // mistaken for a stray banner.
    const failure = page.locator('#sec-reported-failure');
    await expect(failure.getByRole('heading', { level: 2, name: 'Reported failure' })).toBeVisible();
    // Human sentence first, machine code after -- never the enum alone.
    expect(flat(await failure.locator('.se-error-msg').textContent())).toBe('edge cluster unreachable');
    await expect(failure.locator('.se-error-code')).toHaveText('SOURCE_UNAVAILABLE');

    // Nothing reached the snapshot, and the page says that rather than drawing zeroes
    // in a breakdown.
    await expect(page.locator('.se-breakdown')).toHaveCount(0);
    await expect(page.getByText('Nothing in the product is attributable to this data source.')).toBeVisible();
  });

  test('the source page uses the width it is given, with or without a contents rail', async ({ page }) => {
    // The regression this exists for: PageToc renders nothing below three sections, and
    // under the old two-column GRID the body -- then the first child -- was placed into
    // the 200px rail track. A page of prose in a gutter, with the rest of the row empty.
    await page.setViewportSize({ width: 1440, height: 900 });

    // `edge-cluster` reports a failure and so has three sections -- enough for the
    // navigator. `local` has two and gets none. Both must fill the row they are in.
    for (const [source, expectRail] of [['edge-cluster', true], ['local', false]] as Array<[string, boolean]>) {
      await boot(page, `#/fleet/sources/${source}`);
      const layout = page.locator('.page-toc-layout');
      await expect(layout).toBeVisible();
      expect(await layout.locator('.toc').count() > 0, `${source} rail`).toBe(expectRail);

      const outer = (await layout.boundingBox())!;
      const main = (await layout.locator('.page-toc-main').boundingBox())!;
      // With a rail: the body takes what is left. Without one: the body takes the row.
      const floor = expectRail ? 0.7 : 0.99;
      expect(main.width / outer.width, `${source} body share of the row`).toBeGreaterThan(floor);
      // Never the gutter.
      expect(main.width, `${source} body width`).toBeGreaterThan(400);
    }
  });
});
