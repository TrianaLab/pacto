import { test, expect, type Page } from '@playwright/test';
import { boot } from './typographyChecks';

/**
 * The population claims a list page makes, measured against the list underneath them.
 *
 * Every figure on Services and on the two entity inventories is computed by the BACKEND
 * over the complete filtered population, with paging excluded. The unit tests prove the
 * component draws whatever aggregate it is handed; only a browser can prove the aggregate
 * and the rows describe the SAME query -- which is the claim that actually matters, and
 * the one that fails silently. An aggregate left over from the previous filter, or one
 * derived from the twenty-five rows on screen, renders identically to a correct one.
 *
 * The sharpest available form of that claim, and the one used below: filter the list DOWN
 * TO one of the figure's own buckets. If both sides describe the same population, that
 * bucket must then account for all of it -- its value equals the new total, and it is the
 * only bucket left. A page-derived aggregate fails it, a stale one fails it, and an
 * aggregate computed over a different population than the rows fails it.
 */

interface Bucket { label: string; value: number; href: string }
interface Figure { scope: string; description: string; buckets: Bucket[] }

/** Reads a rendered DistributionBar by its caption, as a reader sees it. */
async function figure(page: Page, title: string): Promise<Figure> {
  const f = await page.evaluate((t: string) => {
    const cap = [...document.querySelectorAll('.dist-title')].find((h) => h.textContent!.trim() === t);
    if (!cap) return null;
    const fig = cap.closest('figure')!;
    const text = (sel: string) => fig.querySelector(sel)?.textContent?.replace(/\s+/g, ' ').trim() ?? '';
    return {
      scope: text('.dist-scope'),
      description: text('.dist-desc'),
      buckets: [...fig.querySelectorAll('.dist-item')].map((li) => ({
        label: li.querySelector('.dist-label')!.textContent!.trim(),
        value: Number(li.querySelector('.dist-value')!.textContent!.trim()),
        href: li.querySelector('a')?.getAttribute('href') ?? '',
      })),
    };
  }, title);
  expect(f, `no figure titled "${title}" on this page`).not.toBeNull();
  return f!;
}

const sum = (b: Bucket[]) => b.reduce((n, x) => n + x.value, 0);

/** The pager's own count of the population, which is the rows' side of the claim. */
async function pagerTotal(page: Page, sel: string): Promise<number> {
  const text = (await page.locator(sel).first().textContent()) ?? '';
  const m = text.match(/of\s+(\d+)/);
  expect(m, `no "Showing x-y of N" range in "${text}"`).not.toBeNull();
  return Number(m![1]);
}

/** The largest bucket that is a drill-down and does not already hold the whole population. */
function drillable(f: Figure, total: number): Bucket {
  const b = f.buckets
    .filter((x) => x.href && x.value > 0 && x.value < total)
    .sort((x, y) => y.value - x.value)[0];
  expect(b, `no proper drillable bucket in ${JSON.stringify(f.buckets)} (total ${total})`).toBeTruthy();
  return b;
}

/**
 * Follows a bucket's own href. Clicking the legend link would be closer to the reader's
 * gesture, but the legend can be scrolled under the sticky header at some viewport
 * heights and this test is about the population, not about hit targets -- those are
 * measured in viz-acceptance.spec.ts and responsive.spec.ts.
 */
async function follow(page: Page, b: Bucket): Promise<void> {
  await page.evaluate((href: string) => { location.hash = href.replace(/^#/, ''); }, b.href);
}

test('Services: a bucket of the figure and the rows under it are the same population', async ({ page }) => {
  test.setTimeout(240_000);
  await boot(page, '#/fleet/services');
  await expect(page.getByTestId('service-list')).toBeVisible({ timeout: 30_000 });

  const total = await pagerTotal(page, '.sv-range');
  const before = await figure(page, 'Compliance');
  // Unfiltered, the figure says so in words and reconciles to the whole snapshot.
  expect(before.scope).toBe(`All ${total} services in the snapshot.`);
  expect(sum(before.buckets), 'the compliance buckets do not add up to the population').toBe(total);

  const bucket = drillable(before, total);
  await follow(page, bucket);
  await expect(page.locator('.sv-range')).toContainText(`of ${bucket.value}`, { timeout: 30_000 });

  const after = await figure(page, 'Compliance');
  // The rows narrowed; so did the figure, to exactly the bucket that was chosen. A
  // figure still describing 18 services beside a list of 5 is the failure this catches.
  expect(await pagerTotal(page, '.sv-range')).toBe(bucket.value);
  expect(after.scope).toBe(`All ${bucket.value} matching services, not just this page.`);
  expect(after.buckets.map((b) => b.label), `filtering to "${bucket.label}" left other buckets populated`)
    .toEqual([bucket.label]);
  expect(after.buckets[0].value).toBe(bucket.value);
  // And the second figure on the page followed the same filter rather than staying
  // behind on the unfiltered population.
  expect(sum((await figure(page, 'Declared ownership')).buckets)).toBe(bucket.value);
});

test('Services: ownership coverage is a distribution of services, and it drills to the owners', async ({ page }) => {
  test.setTimeout(240_000);
  await boot(page, '#/fleet/services');
  await expect(page.getByTestId('service-list')).toBeVisible({ timeout: 30_000 });

  const total = await pagerTotal(page, '.sv-range');
  const f = await figure(page, 'Declared ownership');
  // Coverage is a partition of the services, not a count of owners: the buckets say how
  // many services are cleanly owned, how many disagree with themselves, and how many
  // nobody claims -- and those three account for every service.
  expect(sum(f.buckets)).toBe(total);
  expect(f.description).toContain('authored on each contract revision');

  const bucket = drillable(f, total);
  await follow(page, bucket);
  await expect(page.locator('.sv-range')).toContainText(`of ${bucket.value}`, { timeout: 30_000 });
  const rows = await page.getByTestId('service-list').locator('li').count();
  expect(rows, 'the ownership bucket selected a population it then rendered none of').toBeGreaterThan(0);

  // The per-owner ranking answers the follow-up question ("who carries it") and says how
  // many distinct owners it is ranking, so the ten rows never read as the whole roster.
  const summary = (await page.locator('.sv-inv-more summary').textContent())!.replace(/\s+/g, ' ').trim();
  expect(summary).toMatch(/Per-owner breakdown \d+ declared owners?/);

  // And the page offers the owners themselves as entities, not merely as a chart axis.
  await page.getByRole('link', { name: 'Browse owners' }).click();
  await expect(page).toHaveURL(/#\/fleet\/owners/, { timeout: 30_000 });
  await expect(page.getByRole('heading', { level: 1, name: 'Owners' })).toBeVisible({ timeout: 30_000 });
});

test('Revisions: the readiness figure covers every revision, and a bucket opens the revisions in it', async ({ page }) => {
  test.setTimeout(240_000);
  await boot(page, '#/fleet/revisions');
  await expect(page.getByTestId('entity-list')).toBeVisible({ timeout: 30_000 });

  const total = await pagerTotal(page, '.lv-range');
  const f = await figure(page, 'Contract revision readiness');
  expect(f.scope).toBe(`All ${total} contract revisions in the snapshot.`);
  expect(sum(f.buckets), 'the readiness buckets do not add up to the revision population').toBe(total);
  // The unit is stated in the caption, and the word compliance is disclaimed rather than
  // left for the reader to conflate. This is requirement 4 in its own words.
  expect(f.description).toContain('This is not compliance');

  const bucket = drillable(f, total);
  await follow(page, bucket);
  await expect(page.locator('.lv-range')).toContainText(`of ${bucket.value}`, { timeout: 30_000 });
  expect((await figure(page, 'Contract revision readiness')).scope)
    .toBe(`All ${bucket.value} matching contract revisions, not just this page.`);

  // A bucket is a queue of revisions, so a row in it opens a revision.
  await page.getByTestId('entity-list').locator('li a').first().click();
  await expect(page).toHaveURL(/#\/fleet\/revisions\/.+/, { timeout: 30_000 });
  await expect(page.getByRole('heading', { level: 2, name: 'Readiness' })).toBeVisible({ timeout: 30_000 });
});

test('a revision passing its own readiness gate can still run on a target that does not comply', async ({ page }) => {
  test.setTimeout(240_000);
  await boot(page, '#/fleet');

  // Discovered from the product API rather than hard-coded, so this follows the fixture.
  // The pair is the whole point: readiness is DECLARED by a revision about itself, and
  // compliance is OBSERVED about a running target. A product that collapsed them would
  // have to either hide this revision's passing gate or launder its target's violation.
  const pair = await page.evaluate(async () => {
    const list = await (await fetch('/api/fleet/entities?kinds=revision&limit=200')).json();
    for (const ref of list.entities || []) {
      const d = await (await fetch(`/api/fleet/entities/revision?key=${encodeURIComponent(ref.key)}`)).json();
      const r = d.revision;
      if (!r?.readiness?.passing) continue;
      const targets = [...(r.exactTargets?.items || []), ...(r.inferredTargets?.items || [])];
      const bad = targets.find((t: { status?: string }) => t.status === 'NonCompliant');
      if (bad) return { revision: ref.key, target: bad.key as string };
    }
    return null;
  });
  expect(pair, 'the demo fleet has no passing revision running on a non-compliant target').not.toBeNull();

  await boot(page, `#/fleet/revisions/${encodeURIComponent(pair!.revision)}`);
  const readiness = page.locator('#sec-readiness');
  await expect(readiness).toBeVisible({ timeout: 30_000 });

  // Both verdicts on one page, each attached to its own subject and neither restated as
  // the other: the revision's gate passes, and the target it runs on is non-compliant.
  await expect(readiness).toContainText('Passing');
  await expect(readiness).toContainText('not a measurement of the running system');
  const targetRow = page.locator('.ref-row').filter({ hasText: pair!.target.split('/').pop()! }).first();
  await expect(targetRow).toContainText('Not compliant', { timeout: 30_000 });

  // The page header carries the REVISION's status. Whatever it says, it does not
  // advertise the revision as compliant on the strength of a readiness gate.
  const header = (await page.locator('[data-testid="page-title"]').locator('..').textContent()) ?? '';
  expect(header).not.toMatch(/\bCompliant\b/);
});
