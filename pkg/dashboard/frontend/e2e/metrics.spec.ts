import { test, expect, type Page } from '@playwright/test';
import { appReady, settle } from './ready';
import { installScaleFixture, layoutProblems, loaded, openMetricsAt, toggleSidebar } from './layoutGate';

// Browser acceptance for the Metrics page (#493 S4). The fixture replaces the demo's own run
// records (#493 S5), so every test installs the scale fixture and picks its state through
// `sessionStorage['fixture.stats']` before the first load: `off` serves no repository,
// `restricted` a narrow signed-in view and unset a recorded window. Playwright shards by
// test, so each test sets its own state and depends on no other.

const WIDE_FONT = '*{font-family: Verdana, "DejaVu Sans", sans-serif !important}';

async function withStats(page: Page, mode: string) {
  await page.addInitScript(installScaleFixture);
  await page.addInitScript((m) => sessionStorage.setItem('fixture.stats', m), mode);
}

async function openMetrics(page: Page, mode = '', hash = '#/metrics') {
  await withStats(page, mode);
  await appReady(page, `/${hash}`);
  if (mode !== 'off') await expect(page.getByTestId('metrics-content')).toBeVisible({ timeout: 30_000 });
}

/** What the fixture answers for /api/stats, read through the same serve the page uses. */
const answer = (page: Page, query = '') => page.evaluate(async (q) => {
  const w = window as unknown as { __pactoServe: (method: string, path: string, body: string | null) => Promise<{ body: string }> };
  return JSON.parse((await w.__pactoServe('GET', `/api/stats${q}`, null)).body) as { environments: { environment: string }[] };
}, query);

test('the sidebar opens Metrics, folded and unfolded', async ({ page }) => {
  await withStats(page, '');
  await appReady(page, '/#/fleet');
  const link = page.locator('[data-testid="sidebar"]').getByRole('link', { name: 'Metrics' });
  await link.click();
  await expect(page).toHaveURL(/#\/metrics$/);
  await expect(page.getByRole('heading', { level: 1, name: 'Metrics' })).toBeVisible();
  await page.goto('/#/fleet');
  await toggleSidebar(page);
  await link.click();
  await expect(page).toHaveURL(/#\/metrics$/);
  await expect(page.getByTestId('metrics-content')).toBeVisible({ timeout: 30_000 });
});

test('the palette opens Metrics', async ({ page }) => {
  await withStats(page, '');
  await appReady(page, '/#/fleet');
  await page.keyboard.press('ControlOrMeta+k');
  await expect(page.locator('.cp-panel')).toBeVisible();
  await page.locator('.cp-panel input').fill('metrics');
  await expect(page.locator('.cp-item.selected .cp-item-label')).toHaveText('Metrics');
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/#\/metrics$/);
  await expect(page.getByRole('heading', { level: 1, name: 'Metrics' })).toBeVisible();
});

test('with no stats repository the page explains recording and shows no number', async ({ page }) => {
  await openMetrics(page, 'off');
  const off = page.getByTestId('metrics-off');
  await expect(off.getByRole('heading', { name: 'Metrics are off' })).toBeVisible();
  await expect(page.getByTestId('metrics-content')).toHaveCount(0);
  await expect(page.getByTestId('metrics-tiles')).toHaveCount(0);
  // The explainer's copy lines name a repository, never a count.
  const prose = await off.locator('h2, p').allInnerTexts();
  expect(prose.join(' ')).not.toMatch(/\d/);
});

test('the provenance line says how sure the page is', async ({ page }) => {
  await openMetrics(page);
  const strip = page.getByTestId('metrics-prov').getByTestId('stats-strip');
  await expect(strip).toContainText('unsigned statistics');
  await expect(strip).toContainText(/as of /);
  await expect(strip).toContainText('3 skipped');
  await expect(strip.locator('.badge')).toHaveText('Partial'); // the fixture skipped 3 records
});

test('never draws zero for unknown', async ({ page }) => {
  await openMetrics(page);
  // The fixture's next-to-newest bucket holds no record: every weekly chart hatches it.
  for (const id of ['metrics-stopped', 'metrics-gates']) {
    const card = page.getByTestId(id);
    await expect(card.locator('.cc-unknown').first()).toBeVisible();
    await expect(card.locator('.cc-plate').first()).toHaveText('no records');
    await expect(card.locator('.cc-val')).not.toHaveText(['0']);
  }
  // A rate with nothing to take it over is unknown, never 0%.
  const rolledBack = page.getByTestId('metrics-env-row').nth(1).locator('td[data-label="Rolled back"]');
  await expect(rolledBack).toHaveText('unknown');
  await expect(rolledBack.locator('.hatch')).toHaveCount(1);
});

test('environments follow the Stage order, not their names', async ({ page }) => {
  await openMetrics(page);
  const section = page.getByTestId('metrics-environments');
  await expect(section.getByRole('heading', { name: 'Environments, in the order their Stages promote' })).toBeVisible();
  const want = (await answer(page)).environments.map((e) => e.environment);
  const shown = await section.locator('[data-testid="metrics-env-row"] th a').evaluateAll((as) => as.map((a) => a.getAttribute('title')));
  expect(shown).toEqual(want);
  expect([...want].sort((a, b) => a.localeCompare(b))).not.toEqual(want);
});

test('the window picker changes the URL and the bucket count', async ({ page }) => {
  await openMetrics(page);
  const bars = page.getByTestId('metrics-stopped').locator('.mx-multiples > div').first().locator('.cc-hit');
  await expect(bars).toHaveCount(5); // 30 days: four weeks and a short 2-day bucket
  await page.getByLabel('Window').selectOption('7d');
  await expect(page).toHaveURL(/#\/metrics\?(.*&)?window=7d/);
  await expect(bars).toHaveCount(7);
  await page.getByLabel('Window').selectOption('90d');
  await expect(page).toHaveURL(/window=90d/);
  await expect(bars).toHaveCount(13);
});

test('a blocked gate opens its records, each with its change analysis', async ({ page }) => {
  await openMetrics(page);
  const tile = page.getByTestId('metrics-tiles').locator('.mx-tile').filter({ hasText: 'Release gates' }).getByRole('link');
  const lists = Number(await tile.getAttribute('data-lists'));
  await tile.click();
  await expect(page).toHaveURL(/metric=blocked/);
  const panel = page.getByTestId('metrics-records');
  await expect(panel.getByTestId('metrics-record')).toHaveCount(Math.min(25, lists));
  const changes = panel.getByRole('link', { name: 'Change analysis' });
  await expect(changes).toHaveCount(2);
  const href = (await changes.first().getAttribute('href')) ?? '';
  const q = new URLSearchParams(href.split('?')[1]);
  expect(q.get('old')).toMatch(/^sha256:/);
  expect(q.get('new')).toMatch(/^sha256:/);
  await changes.first().click();
  await expect(page).toHaveURL((u) => u.hash.startsWith('#/fleet/changes/payments-service?')
    && new URLSearchParams(u.hash.split('?')[1]).get('old') === q.get('old')
    && new URLSearchParams(u.hash.split('?')[1]).get('new') === q.get('new'));
  await expect(page.getByRole('heading', { level: 1, name: /Change analysis/ })).toBeVisible();
});

test('a failed promotion count opens exactly its records, each linked to its Stage history', async ({ page }) => {
  await openMetrics(page);
  const cell = page.getByTestId('metrics-env-row').first().locator('td[data-label="Failed"] a');
  await expect(cell).toHaveAttribute('data-lists', '2');
  await cell.click();
  const rows = page.getByTestId('metrics-records').getByTestId('metrics-record');
  await expect(rows).toHaveCount(2);
  const stage = rows.first().locator('td[data-label="Stage"] a').first();
  const href = (await stage.getAttribute('href')) ?? '';
  const name = new URLSearchParams(href.split('?')[1]).get('stage');
  expect(name).toBeTruthy();
  await stage.click();
  await expect(page).toHaveURL((u) => u.hash.startsWith('#/delivery/history?') && new URLSearchParams(u.hash.split('?')[1]).get('stage') === name);
  await expect(page.getByRole('heading', { level: 1 })).toContainText(`History of `);
});

test('a service link lands on a service page that renders', async ({ page }) => {
  await openMetrics(page);
  const link = page.getByTestId('metrics-service-row').first().locator('th a');
  await expect(link).toHaveText('payments-service');
  await link.click();
  await expect(page).toHaveURL(/#\/fleet\/services\/payments-service$/);
  await expect(page.getByRole('heading', { level: 1, name: 'payments-service' })).toBeVisible({ timeout: 20_000 });
  await expect(page.getByText(/not found|no service named/i)).toHaveCount(0);
});

test('a restricted reader is told records are left out and sees no coverage history', async ({ page }) => {
  await openMetrics(page, 'restricted');
  await expect(page.getByTestId('metrics-hidden')).toHaveText('Some run records name services outside your view and are left out of every number.');
  await expect(page.getByTestId('metrics-coverage-trend')).toHaveCount(0);
  await expect(page.getByTestId('metrics-coverage-restricted')).toBeVisible();
  await expect(page.getByTestId('metrics-coverage-now')).toBeVisible();
});

type Ratio = { count: number; of: number; unknown?: boolean };
type Env = { environment: string; upstream?: string[]; parents?: string[]; depth?: number; chainEnd?: boolean } & Record<string, unknown>;
const RATES = ['held', 'failed', 'rolledBack', 'verificationFailed'];

/** Every problem of one fixture answer: its topology against the API's rules and its numbers against its buckets. */
function fixtureProblems(st: Record<string, unknown>): string[] {
  const out: string[] = [];
  const envs = st.environments as Env[];
  const up = new Map(envs.map((e) => [e.environment, e.upstream ?? []]));
  for (const e of envs) {
    const closure = up.get(e.environment)!;
    for (const u of closure) {
      for (const v of up.get(u) ?? []) if (!closure.includes(v)) out.push(`${e.environment}: upstream holds ${u} but not its upstream ${v}`);
    }
    if (e.chainEnd && (e.depth === undefined || !closure.length)) out.push(`${e.environment}: a chain end with no depth or no upstream`);
    if (e.depth === undefined) continue;
    const want = closure.filter((u) => !closure.some((v) => (up.get(v) ?? []).includes(u)));
    if (JSON.stringify([...(e.parents ?? [])].sort()) !== JSON.stringify([...want].sort())) out.push(`${e.environment}: parents ${e.parents} are not the reduction ${want}`);
  }
  // No rate with nothing to take it over may read as known, anywhere in the answer.
  JSON.stringify(st, (k, v: unknown) => {
    const r = v as Ratio;
    if (RATES.includes(k) && r && r.of === 0 && !r.unknown) out.push(`${k} of 0 is not unknown`);
    return v;
  });
  // Each environment's counts add up over the buckets.
  const buckets = st.buckets as { metrics?: { environments: Env[] } }[];
  for (const e of envs) {
    for (const k of RATES) {
      const sum = buckets.reduce((n, b) => n + ((b.metrics?.environments.find((x) => x.environment === e.environment)?.[k] as Ratio | undefined)?.count ?? 0), 0);
      if (sum !== (e[k] as Ratio).count) out.push(`${e.environment} ${k}: buckets count ${sum}, the window ${(e[k] as Ratio).count}`);
    }
  }
  return out;
}

test('the fixture serves 5, 60 and 200 environments with the API\'s topology', async ({ page }) => {
  await openMetrics(page);
  for (const [size, want] of [['5', 5], ['', 60], ['60', 60], ['200', 200]] as const) {
    const st = await page.evaluate(async (n) => {
      if (n) sessionStorage.setItem('fixture.stats.envs', n); else sessionStorage.removeItem('fixture.stats.envs');
      const w = window as unknown as { __pactoServe: (method: string, path: string, body: string | null) => Promise<{ body: string }> };
      const get = async (path: string) => JSON.parse((await w.__pactoServe('GET', path, null)).body);
      return { answer: await get('/api/stats?window=90d'), blocked: (await get('/api/stats/records?window=90d&metric=blocked')).total };
    }, size);
    const envs = st.answer.environments as Env[];
    expect(envs, `size ${size || 'unset'}`).toHaveLength(want);
    expect(fixtureProblems(st.answer), `size ${size || 'unset'}`).toEqual([]);
    expect(envs.some((e) => e.chainEnd)).toBe(true);
    expect(st.answer.buckets).toHaveLength(13);
    expect(st.answer.buckets.filter((b: { metrics?: unknown }) => !b.metrics)).toHaveLength(1);
    expect(st.blocked).toBe(st.answer.totals.gates.blocked);
    const groups = st.answer.leadTimeGroups as { key: string; buckets: { unknown?: boolean }[] }[];
    expect(groups[0].key).toBe('chain-end');
    for (const g of groups) expect(g.buckets.filter((b) => b.unknown).length).toBeGreaterThan(0);
    expect(st.answer.environmentsBy.owner.length).toBeGreaterThan(1);
  }
  // The default keeps its names, the cycle and the unordered row.
  await page.evaluate(() => sessionStorage.removeItem('fixture.stats.envs'));
  const sixty = (await answer(page)).environments as Env[];
  expect(sixty[58]).toMatchObject({ cycle: true });
  expect(sixty[59]).toMatchObject({ unordered: true });
  expect(sixty.filter((e) => e.depth !== undefined).map((e) => e.depth)).toEqual([...Array(8).fill(0), ...Array(12).fill(1), ...Array(16).fill(2), ...Array(22).fill(3)]);
  // The other sizes render.
  for (const size of ['5', '200'] as const) await openMetricsAt(size)(page);
});

test('Metrics holds its layout in a wide font', async ({ page }) => {
  await openMetrics(page);
  await page.addStyleTag({ content: WIDE_FONT });
  await loaded(page);
  await settle(page);
  expect(await layoutProblems(page)).toEqual([]);
});
