import { test, expect, type Locator, type Page } from '@playwright/test';
import { appReady, settle } from './ready';
import { ROUTES, figureLinks, installScaleFixture, layoutProblems, loaded, openMetricsAt, toggleSidebar } from './layoutGate';

// Browser acceptance for the Metrics page (#493 S4). The fixture replaces the demo's own run
// records (#493 S5), so every test installs the scale fixture and picks its state through
// `sessionStorage['fixture.stats']` before the first load: `off` serves no repository,
// `restricted` a narrow signed-in view and unset a recorded window. Playwright shards by
// test, so each test sets its own state and depends on no other.

const WIDE_FONT = '*{font-family: Verdana, "DejaVu Sans", sans-serif !important}';

async function withStats(page: Page, mode: string, envs = '') {
  await page.addInitScript(installScaleFixture);
  await page.addInitScript(([m, n]) => {
    sessionStorage.setItem('fixture.stats', m);
    if (n) sessionStorage.setItem('fixture.stats.envs', n);
  }, [mode, envs]);
}

async function openMetrics(page: Page, mode = '', hash = '#/metrics', envs = '') {
  await withStats(page, mode, envs);
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
  // The coverage line breaks at that week (two M segments) instead of dropping to 0.
  const line = page.getByTestId('metrics-coverage').locator('.lc-line').first();
  expect(((await line.getAttribute('d')) ?? '').match(/M/g)).toHaveLength(2);
  await expect(page.getByTestId('metrics-coverage').locator('.lc-unknown')).toHaveCount(1);
  // A rate with nothing to take it over is unknown, never 0%: the entry group holds one.
  const health = page.getByTestId('metrics-health');
  await health.locator('tr.rung button').first().click();
  const unknown = health.locator('tbody tr:not(.rung) .cell.unk').first();
  await expect(unknown.locator('.plate')).toHaveText('unknown');
  await expect(unknown.locator('.hatch')).toHaveCount(1);
  await expect(unknown.locator('.visually-hidden')).toContainText('unknown, 0 of 0 promotions');
});

test('environments follow the Stage order, not their names', async ({ page }) => {
  await openMetrics(page);
  const health = page.getByTestId('metrics-health');
  // The groups are promotion depths, never names; the cycle and the unordered one have none.
  await expect(health.locator('tr.rung button bdi')).toHaveText(['entry', 'hop 2', 'hop 3', 'hop 4', 'Depth unknown']);
  await health.getByRole('button', { name: 'Show as table' }).click();
  await health.getByRole('button', { name: /^Show all 60 rows$/ }).click();
  const want = (await answer(page)).environments.map((e) => e.environment);
  const shown = await health.locator('table tbody th').evaluateAll((ths) => ths.map((th) => th.getAttribute('title')));
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
  const health = page.getByTestId('metrics-health');
  await health.getByRole('button', { name: 'Show as table' }).click();
  const cell = health.locator('table tbody tr').first().locator('td[data-label="Failed"] a');
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

/** What the one chart tooltip says right now: its title, each row's value and label, and its foot. */
type Tip = { title: string; values: string[]; labels: string[]; foot: string };
const tipNow = (page: Page): Promise<Tip> => page.locator('#chart-tip.on').evaluate((el) => ({
  title: el.querySelector('.tt')?.textContent ?? '',
  values: [...el.querySelectorAll('.row b')].map((b) => b.textContent ?? ''),
  labels: [...el.querySelectorAll('.row span:not(.lk)')].map((s) => s.textContent ?? ''),
  foot: el.querySelector('.foot')?.textContent ?? '',
}));
const tipHidden = (page: Page) => expect(page.locator('#chart-tip')).not.toHaveClass(/\bon\b/);

/** The tooltip of every hit target, read by keyboard focus, in bucket order. */
async function tipsOf(page: Page, hits: Locator): Promise<Tip[]> {
  const out: Tip[] = [];
  for (let i = 0; i < await hits.count(); i++) {
    await hits.nth(i).focus();
    out.push(await tipNow(page));
  }
  return out;
}

/** The tooltip of each bucket of a line chart: focus reads the last, Left steps back. */
async function lineTips(page: Page, hit: Locator, n: number): Promise<Tip[]> {
  await hit.focus();
  const out = [await tipNow(page)];
  for (let i = 1; i < n; i++) {
    await page.keyboard.press('ArrowLeft');
    await expect(page.locator('#chart-tip .tt')).not.toHaveText(out[0].title);
    out.unshift(await tipNow(page));
  }
  return out;
}

/** The rows of one of a card's table views, each cell's text, then back to the chart. */
async function tableOf(card: Locator, which = 0): Promise<string[][]> {
  await card.getByRole('button', { name: 'Show as table' }).click();
  const rows = await card.locator('.cc-table').nth(which).locator('tbody tr')
    .evaluateAll((trs) => trs.map((tr) => [...tr.children].map((c) => (c.textContent ?? '').trim())));
  await card.getByRole('button', { name: 'Show as chart' }).click();
  return rows;
}

test('the three tiles draw a sparkline each and the gates a bar by verdict', async ({ page }) => {
  await openMetrics(page);
  const tiles = page.getByTestId('metrics-tiles').locator('.mx-tile');
  await expect(tiles).toHaveCount(3);
  await expect(tiles.nth(0).locator('.mx-tile-label')).toHaveText('Breaking changes caught');
  await expect(tiles.nth(0).locator('.cc.compact svg')).toBeVisible();
  await expect(tiles.nth(0).locator('.cc.compact .cc-hit')).toHaveCount(5);
  await expect(tiles.nth(1).locator('.mx-tile-label')).toHaveText('Release gates');
  await expect(tiles.nth(1).locator('.mx-tile-value small')).toContainText('blocked of');
  await expect(tiles.nth(1).locator('.dist')).toBeVisible();
  await expect(tiles.nth(2).locator('.mx-tile-label')).toHaveText(/^Lead time to /);
  await expect(tiles.nth(2).locator('.lc.compact svg')).toBeVisible();
});

test('hover and keyboard focus on a column show the same tooltip, and Escape hides it', async ({ page }) => {
  await openMetrics(page);
  const hit = page.getByTestId('metrics-gates').locator('.cc-hit').last();
  await hit.hover();
  const hovered = await tipNow(page);
  expect(hovered.labels).toEqual(['Clear', 'Blocked', 'Unverified']);
  for (const v of hovered.values) expect(v).toMatch(/^\d[\d,]*$/);
  await page.mouse.move(1, 1);
  await tipHidden(page);
  await hit.focus();
  expect(await tipNow(page)).toEqual(hovered);
  await page.keyboard.press('Escape');
  await tipHidden(page);
});

test('each card\'s table shows the numbers its tooltips show', async ({ page }) => {
  await openMetrics(page);
  // Gate outcomes: the three verdicts, or the reason where the week is hatched.
  const gates = page.getByTestId('metrics-gates');
  const gateRows = await tableOf(gates);
  const gateTips = await tipsOf(page, gates.locator('.cc-hit'));
  expect(gateTips.map((t) => t.title)).toEqual(gateRows.map((r) => r[0]));
  gateTips.forEach((t, i) => {
    if (t.values[0] === '') expect(gateRows[i].slice(1)).toEqual([t.labels[0], t.labels[0], t.labels[0]]);
    else expect(t.values).toEqual(gateRows[i].slice(1));
  });
  // What Pacto stopped: breaking changes and their runs, then consumers protected.
  const stopped = page.getByTestId('metrics-stopped');
  const stopRows = await tableOf(stopped);
  const breaking = await tipsOf(page, stopped.locator('.mx-multiples > div').nth(0).locator('.cc-hit'));
  const prot = await tipsOf(page, stopped.locator('.mx-multiples > div').nth(1).locator('.cc-hit'));
  stopRows.forEach((r, i) => {
    if (breaking[i].values[0] === '') {
      expect(r.slice(1)).toEqual([breaking[i].labels[0], breaking[i].labels[0], breaking[i].labels[0]]);
      return;
    }
    expect(breaking[i].values[0]).toBe(r[1]);
    expect(breaking[i].foot).toBe(`${r[2]} ${r[2] === '1' ? 'run' : 'runs'}`);
    expect(prot[i].values[0]).toBe(r[3]);
  });
  // Coverage: one line stop read week by week with the arrow keys.
  const coverage = page.getByTestId('metrics-coverage');
  const covRows = await tableOf(coverage);
  const covTips = await lineTips(page, coverage.locator('.lc-hit'), covRows.length);
  covTips.forEach((t, i) => {
    expect(t.title).toBe(covRows[i][0]);
    if (t.values[0] === '') expect([t.labels[0], covRows[i][1]]).toEqual(['no records', 'no records']);
    else expect(t.values).toEqual(covRows[i].slice(1, 4));
  });
});

test('the lead-time table\'s weekly rows match the lead tile\'s sparkline', async ({ page }) => {
  await openMetrics(page);
  const rows = await tableOf(page.getByTestId('metrics-lead'), 1);
  expect(rows.length).toBe(5);
  const tips = await lineTips(page, page.getByTestId('metrics-tiles').locator('.mx-tile').nth(2).locator('.lc-hit'), rows.length);
  let known = 0;
  tips.forEach((t, i) => {
    expect(t.title).toBe(rows[i][0]);
    if (rows[i][1] === 'unknown') expect(t.values[0]).not.toMatch(/\d/);
    else { expect(t.values[0]).toBe(rows[i][1]); known++; }
  });
  expect(known).toBeGreaterThan(0);
});

test('unverified wears the dots and no records the hatch', async ({ page }) => {
  await openMetrics(page);
  const gates = page.getByTestId('metrics-gates');
  const fill = (l: Locator) => l.evaluate((e) => getComputedStyle(e).fill);
  expect(await fill(gates.locator('.cc-col[data-series="unverified"]').first())).toContain('viz-dots');
  expect(await fill(gates.locator('.cc-unknown').first())).toContain('viz-hatch');
  const image = (l: Locator) => l.evaluate((e) => getComputedStyle(e).backgroundImage);
  const dots = await image(gates.locator('.mx-key i.dots'));
  const hatch = await image(gates.locator('.mx-hatch').first());
  expect(dots).not.toBe('none');
  expect(hatch).not.toBe('none');
  expect(dots).not.toBe(hatch);
});

test('5 environments draw one small panel each and no heatmap', async ({ page }) => {
  await openMetrics(page, '', '#/metrics', '5');
  const health = page.getByTestId('metrics-health');
  await expect(health.getByTestId('metrics-env-panel')).toHaveCount(5);
  await expect(health.locator('.hm-scroll')).toHaveCount(0);
  await expect(page.getByRole('group', { name: 'Filter by promotion depth' })).toHaveCount(0);
  // The panels share one scale, so the 67% bar draws longer than the 33% one.
  const eu = health.getByTestId('metrics-env-panel').filter({ has: page.locator('.hb-title', { hasText: /^prod-eu$/ }) });
  const width = async (label: string) => eu.locator('.hb-row').filter({ has: page.locator('.hb-label', { hasText: new RegExp(`^${label}$`) }) }).locator('.hb-fill').evaluate((e) => e.getBoundingClientRect().width);
  expect(await width('Failed')).toBeGreaterThan(await width('Held'));
});

test('60 environments draw the heatmap and say how many they show', async ({ page }) => {
  await openMetrics(page);
  const health = page.getByTestId('metrics-health');
  await expect(health.locator('.hm-scroll')).toBeVisible();
  await expect(health.getByTestId('metrics-env-count')).toHaveText('60 of 60 environments');
  await expect(health.getByTestId('metrics-env-panel')).toHaveCount(0);
});

test('200 environments collapse into pooled groups that page open', async ({ page }) => {
  await openMetrics(page, '', '#/metrics', '200');
  const health = page.getByTestId('metrics-health');
  await expect(health.getByTestId('metrics-env-count')).toHaveText('200 of 200 environments');
  const rungs = health.locator('tr.rung button');
  await expect(rungs.first()).toBeVisible();
  for (const b of await rungs.all()) await expect(b).toHaveAttribute('aria-expanded', 'false');
  await expect(health.locator('.hm tbody th[scope="row"]')).toHaveCount(0);
  // A pooled row's tooltip says what it pooled and names the group's worst environment.
  const entry = (await answer(page, '?window=30d')).environments.filter((e) => (e as Env).depth === 0) as Env[];
  await health.locator('tr.rung').first().locator('.cell').first().hover();
  const tip = await tipNow(page);
  expect(tip.title).toBe('Held in entry');
  expect(tip.foot).toMatch(/^pooled over \d+ of \d+ environments/);
  const [, name, count, of] = /Worst: (.+), (\d+)\/(\d+)/.exec(tip.foot) ?? [];
  const held = (e: Env) => e.held as Ratio;
  const max = Math.max(...entry.filter((e) => held(e).of > 0).map((e) => held(e).count / held(e).of));
  expect(entry.map((e) => e.environment)).toContain(name);
  expect(Number(count) / Number(of)).toBe(max);
  // Opening a group shows its 10 worst, then pages by 20.
  await rungs.first().click();
  const rows = health.locator('.hm tbody th[scope="row"]');
  await expect(rows).toHaveCount(10);
  await health.getByRole('button', { name: `Show all ${entry.length}` }).click();
  await expect(rows).toHaveCount(Math.min(20, entry.length));
  await expect(health.locator('tr.more')).toContainText(`1 to ${Math.min(20, entry.length)} of ${entry.length}`);
  await health.locator('tr.more').getByRole('button', { name: 'Next' }).click();
  await expect(health.locator('tr.more')).toContainText(`21 to ${Math.min(40, entry.length)} of ${entry.length}`);
  await health.locator('tr.more').getByRole('button', { name: 'Show the 10 worst' }).click();
  await expect(rows).toHaveCount(10);
});

test('the depth chip scopes delivery health and lead time and keeps the depth in the URL', async ({ page }) => {
  await openMetrics(page, '', '#/metrics', '200');
  const filter = page.getByRole('group', { name: 'Filter by promotion depth' });
  const chip = filter.getByRole('button', { name: /^hop 2, / });
  const n = Number(/(\d+) environments?$/.exec((await chip.getAttribute('aria-label')) ?? '')?.[1]);
  await chip.click();
  await expect(page).toHaveURL(/#\/metrics\?(.*&)?depth=1(&|$)/);
  await expect(chip).toHaveAttribute('aria-pressed', 'true');
  await expect(page.getByTestId('metrics-env-count')).toHaveText(`${n} of 200 environments`);
  await expect(page.getByTestId('metrics-tiles').locator('.mx-tile').nth(2).locator('.mx-tile-label')).toHaveText('Lead time to hop 2');
  await expect(page.getByTestId('metrics-lead-pager')).toContainText(`of ${n}`);
  await filter.getByRole('button', { name: /^All / }).click();
  await expect(page).not.toHaveURL(/depth=/);
});

test('delivery health groups by owner and by domain', async ({ page }) => {
  await openMetrics(page, '', '#/metrics', '200');
  const health = page.getByTestId('metrics-health');
  const labels = health.locator('tr.rung button bdi');
  await page.getByLabel('Group by').selectOption('owner');
  await expect(labels.first()).toBeVisible();
  expect(await labels.allTextContents()).toContain('payments');
  await expect(health.getByText(/^owner inferred from the fleet snapshot of .+, partial$/)).toBeVisible();
  await page.getByLabel('Group by').selectOption('domain');
  await expect(labels.filter({ hasText: /^eu-central-1$/ })).toHaveCount(1);
  await expect(health.getByText(/^domain inferred from the fleet snapshot of /)).toBeVisible();
  // An environment is still counted once however it is grouped.
  await expect(health.getByTestId('metrics-env-count')).toHaveText('200 of 200 environments');
});

test('lead time lists the 10 slowest, then pages by 20', async ({ page }) => {
  await openMetrics(page);
  const lead = page.getByTestId('metrics-lead');
  const names = lead.locator('.rd-name');
  const pager = page.getByTestId('metrics-lead-pager');
  await expect(names).toHaveCount(10);
  await expect(pager).toContainText('The 10 slowest p50 of 60');
  await pager.getByRole('button', { name: 'Show all 60' }).click();
  await expect(names).toHaveCount(20);
  await expect(pager).toContainText('1 to 20 of 60');
  await pager.getByRole('button', { name: 'Next' }).click();
  await expect(pager).toContainText('21 to 40 of 60');
  await pager.getByRole('button', { name: 'Show the 10 slowest' }).click();
  await expect(names).toHaveCount(10);
});

test('a long environment name is cut in the middle and keeps the full name in its title', async ({ page }) => {
  await openMetrics(page);
  const health = page.getByTestId('metrics-health');
  await health.locator('tr.rung button').first().click();
  const nm = health.locator('.hm tbody th[scope="row"] .nm').first();
  const title = (await nm.getAttribute('title')) ?? '';
  const shown = (await nm.locator('.fit').textContent()) ?? '';
  expect(shown).toContain('…');
  expect(title.length).toBeGreaterThan(shown.length);
  const [head, tail] = shown.split('…');
  expect(title.startsWith(head)).toBe(true);
  expect(title.endsWith(tail)).toBe(true);
});

test('on a phone the page never scrolls sideways and only the heatmap does', async ({ page }) => {
  await openMetrics(page);
  await page.setViewportSize({ width: 390, height: 844 });
  const health = page.getByTestId('metrics-health');
  await health.locator('tr.rung button').first().click();
  await settle(page);
  const found = await page.evaluate(() => ({
    page: document.documentElement.scrollWidth <= window.innerWidth,
    scrollers: [...document.querySelectorAll<HTMLElement>('body *')]
      .filter((e) => e.scrollWidth > e.clientWidth + 1 && ['auto', 'scroll'].includes(getComputedStyle(e).overflowX))
      .map((e) => e.className),
  }));
  expect(found.page).toBe(true);
  for (const c of found.scrollers) expect(c).toContain('hm-scroll');
});

test('the link gate follows at least one count on the charts and on the table views', async ({ page }) => {
  await page.addInitScript(installScaleFixture);
  await appReady(page);
  for (const name of ['metrics', 'metrics tables']) {
    await ROUTES.find((r) => r.name === name)!.open!(page);
    expect((await figureLinks(page)).length, name).toBeGreaterThan(0);
  }
});

test('the heatmap is one tab stop and the arrow keys move the focused cell', async ({ page }) => {
  await openMetrics(page);
  const health = page.getByTestId('metrics-health');
  await health.locator('tr.rung button').first().click();
  const stops = health.locator('.hm .cell[tabindex="0"]');
  await expect(stops).toHaveCount(1);
  await stops.first().focus();
  const focused = () => page.evaluate(() => {
    const c = document.activeElement as HTMLElement;
    const tr = c.closest('tr')!;
    return { row: [...tr.parentElement!.parentElement!.querySelectorAll('tr')].indexOf(tr), col: [...tr.querySelectorAll('.cell')].indexOf(c), cell: c.classList.contains('cell') };
  });
  const start = await focused();
  await page.keyboard.press('ArrowRight');
  expect(await focused()).toEqual({ ...start, col: start.col + 1 });
  await page.keyboard.press('ArrowDown');
  expect(await focused()).toEqual({ ...start, row: start.row + 1, col: start.col + 1 });
  await page.keyboard.press('Home');
  expect((await focused()).col).toBe(0);
  await expect(stops).toHaveCount(1);
});
