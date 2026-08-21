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

/** Reads a rendered HorizontalBars ranking by its caption, as a reader sees it. */
async function ranking(page: Page, title: string): Promise<Figure> {
  const f = await page.evaluate((t: string) => {
    const cap = [...document.querySelectorAll('.hb-title')].find((h) => h.textContent!.trim() === t);
    if (!cap) return null;
    const fig = cap.closest('figure')!;
    const text = (sel: string) => fig.querySelector(sel)?.textContent?.replace(/\s+/g, ' ').trim() ?? '';
    return {
      scope: text('.hb-scope'),
      description: text('.hb-desc'),
      buckets: [...fig.querySelectorAll('.hb-row')].map((li) => ({
        label: li.querySelector('.hb-label')!.textContent!.trim(),
        value: Number(li.querySelector('.hb-value')!.textContent!.trim().split(/\s+/)[0]),
        href: li.querySelector('a')?.getAttribute('href') ?? '',
      })),
    };
  }, title);
  expect(f, `no ranking titled "${title}" on this page`).not.toBeNull();
  return f!;
}

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
  expect(summary).toMatch(/Per-owner breakdown \d+ named owners?/);

  // And the page offers the owners themselves as entities, not merely as a chart axis.
  await page.getByRole('link', { name: 'Browse owners' }).click();
  await expect(page).toHaveURL(/#\/fleet\/owners/, { timeout: 30_000 });
  await expect(page.getByRole('heading', { level: 1, name: 'Owners' })).toBeVisible({ timeout: 30_000 });
});

/**
 * OWNERS. The page's inventory answers "who exists"; the summary above it answers "is
 * this fleet owned", which is a question about SERVICES and cannot be read off a roster
 * of owners at all -- a service nobody claims has no row there, and a service two teams
 * claim has two.
 *
 * So the summary is deliberately not the page's own population, and the tests below are
 * about exactly that seam: it must stay whole while the roster underneath it is searched
 * and paged, and every bucket it draws must open the population it counted.
 */
test('Owners: the ownership picture is the whole fleet, whatever the roster underneath is showing', async ({ page }) => {
  test.setTimeout(240_000);
  await boot(page, '#/fleet/owners');
  await expect(page.getByTestId('owner-list')).toBeVisible({ timeout: 30_000 });

  const roster = await pagerTotal(page, '.lv-range');
  const f = await figure(page, 'Declared ownership');
  const services = Number(f.scope.match(/^All (\d+) services/)?.[1]);
  expect(services, `the summary does not say what it counted: "${f.scope}"`).toBeGreaterThan(0);
  expect(f.scope).toContain('whatever this page is filtered or paged to');
  // Two populations, two numbers. If these were ever equal by construction the summary
  // would be a second rendering of the roster rather than a second question.
  expect(services).not.toBe(roster);
  expect(sum(f.buckets), 'the coverage buckets do not add up to the service population').toBe(services);

  // Search the roster down. The picture above it must not move: paging an inventory
  // cannot be allowed to redraw what "this fleet's ownership" means.
  // One owner's own name -- taken from the row's href, because the visible label carries
  // the entity kind in front of it. A term guaranteed to select fewer rows than the roster.
  const href = (await page.getByTestId('owner-list').locator('li a').first().getAttribute('href'))!;
  const term = decodeURIComponent(href.split('/').pop()!);
  await page.evaluate((t: string) => { location.hash = `/fleet/owners?text=${encodeURIComponent(t)}`; }, term);
  await expect.poll(() => pagerTotal(page, '.lv-range'), { timeout: 30_000 }).toBeLessThan(roster);
  expect(await figure(page, 'Declared ownership')).toEqual(f);

  // And page it, to the second owner onward -- a different set of rows, the same fleet.
  await page.evaluate(() => { location.hash = '/fleet/owners?offset=1'; });
  await expect(page.locator('.lv-range')).toHaveText(/^Showing 2/, { timeout: 30_000 });
  expect(await pagerTotal(page, '.lv-range')).toBe(roster);
  expect(await figure(page, 'Declared ownership')).toEqual(f);
});

test('Owners: a coverage bucket opens exactly the services it counted', async ({ page }) => {
  test.setTimeout(240_000);
  await boot(page, '#/fleet/owners');
  await expect(page.getByTestId('owner-list')).toBeVisible({ timeout: 30_000 });

  const f = await figure(page, 'Declared ownership');
  const populated = f.buckets.filter((b) => b.value > 0);
  // More than one, or the drill-down would be trivially the whole fleet. The demo fleet
  // has services whose revisions disagree about who owns them, on purpose.
  expect(populated.length, `only one populated bucket: ${JSON.stringify(f.buckets)}`).toBeGreaterThan(1);

  for (const b of populated) {
    await follow(page, b);
    await expect(page.locator('.sv-range')).toContainText(`of ${b.value}`, { timeout: 30_000 });
    expect(await page.getByTestId('service-list').locator('> li').count(),
      `"${b.label}" selected ${b.value} services and rendered none of them`).toBeGreaterThan(0);
    // The destination's own coverage figure is that one bucket and nothing else, which
    // is only true if both sides mean the same thing by it.
    const dest = await figure(page, 'Declared ownership');
    expect(dest.buckets.map((x) => x.label), `"${b.label}" landed on a list that is not that bucket`).toEqual([b.label]);
    expect(dest.buckets[0].value).toBe(b.value);
    await page.goBack();
    await expect(page.getByTestId('owner-list')).toBeVisible({ timeout: 30_000 });
  }
});

test('Owners: a ranked owner opens exactly the services it counted, and says what it left out', async ({ page }) => {
  test.setTimeout(240_000);
  await boot(page, '#/fleet/owners');
  await expect(page.getByTestId('owner-list')).toBeVisible({ timeout: 30_000 });

  const consistent = (await figure(page, 'Declared ownership')).buckets
    .find((b) => b.label === 'One declared owner')!;
  await page.locator('.ow-sum-more summary').click();
  const rank = await ranking(page, 'Services per owner');
  expect(rank.buckets.length).toBeGreaterThan(0);

  // The rows are a BOUND, not the roster, and the page reconciles the difference in
  // full: the ranked owners, the ones past the cut, and the services whose ownership
  // names nobody to rank account for every consistently owned service between them.
  // Three populations, three separate sentences — adding them together silently would
  // invent a fourth that is none of them.
  const other = Number(rank.scope.match(/account for (\d+) more services?/)?.[1] ?? 0);
  const note = (await page.getByTestId('owners-unidentified').textContent().catch(() => null)) || '';
  const nameless = Number(note.match(/^\s*(\d+) services?\b/)?.[1] ?? 0);
  expect(sum(rank.buckets) + other + nameless,
    `${rank.buckets.length} ranked rows + ${other} beyond the cut + ${nameless} naming nobody do not add up to the ${consistent.value} consistently owned services`)
    .toBe(consistent.value);
  expect(rank.scope).toContain('appear in no row here');

  // A row counts the services CONSISTENTLY owned by that team, so its destination has to
  // say both -- owner alone would open a longer list than the row the reader clicked.
  const top = rank.buckets[0];
  expect(top.href).toMatch(/[?&]ownership=consistent(&|$)/);
  await follow(page, top);
  await expect(page.locator('.sv-range')).toContainText(`of ${top.value}`, { timeout: 30_000 });
  expect(await pagerTotal(page, '.sv-range'), `"${top.label}" ranked ${top.value} services and opened a different number`)
    .toBe(top.value);
});

/**
 * The counterexample the ownership model is built around: ownership is authored on each
 * contract REVISION, so a service whose revisions name different teams is claimed by
 * both and cleanly owned by neither. Every surface has to answer that the same way, or a
 * bar and its own drill-down disagree.
 */
test('Owners: both teams disputing a service can find it, and neither owns it cleanly', async ({ page }) => {
  test.setTimeout(240_000);
  await boot(page, '#/fleet/services?ownership=conflicting');
  await expect(page.getByTestId('service-list')).toBeVisible({ timeout: 30_000 });

  // Discovered from the product API, so this follows the fixture rather than pinning a
  // service name: a disputed service, and every owner who can reach it by name.
  const disputed = await page.evaluate(async () => {
    const get = async (q: string) => (await (await fetch(`/api/fleet/entities?${q}`)).json()).entities || [];
    const svc = (await get('kinds=service&ownership=conflicting&limit=1'))[0];
    if (!svc) return null;
    const claimants: string[] = [];
    for (const o of await get('kinds=owner&limit=200')) {
      const mine = await get(`kinds=service&ownerKey=${encodeURIComponent(o.key)}&limit=200`);
      if (mine.some((s: { key: string }) => s.key === svc.key)) claimants.push(o.key);
    }
    return { key: svc.key, label: svc.label || svc.key, claimants };
  });
  expect(disputed, 'the demo fleet has no service whose revisions disagree about its owner').not.toBeNull();
  // Both of them, not just whichever revision the summary happened to pick.
  expect(disputed!.claimants.length, `only ${JSON.stringify(disputed!.claimants)} can see ${disputed!.key}`)
    .toBeGreaterThanOrEqual(2);

  const rows = () => page.getByTestId('service-list').locator('> li').allTextContents();
  for (const owner of disputed!.claimants) {
    // ownerKey=x means "at least one revision of this service names exactly x" -- the
    // canonical identity, not a name that might belong to a second owner. Both find it.
    await page.evaluate((o: string) => { location.hash = `/fleet/services?ownerKey=${encodeURIComponent(o)}`; }, owner);
    await expect.poll(rows, { timeout: 30_000 }).toEqual(expect.arrayContaining([expect.stringContaining(disputed!.label)]));

    // Adding "consistently owned" is what narrows it, and it must exclude the disputed
    // service for BOTH claimants -- neither team owns it outright.
    await page.evaluate((o: string) => { location.hash = `/fleet/services?ownerKey=${encodeURIComponent(o)}&ownership=consistent`; }, owner);
    await expect.poll(rows, { timeout: 30_000 }).not.toEqual(expect.arrayContaining([expect.stringContaining(disputed!.label)]));
  }
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
  // left for the reader to conflate.
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
