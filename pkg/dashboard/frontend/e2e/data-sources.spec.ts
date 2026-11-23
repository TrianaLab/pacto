import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { boot } from './typographyChecks';
import { appReady, settle } from './ready';
import { installScaleFixture } from './layoutGate';

/** detect.FluxInstallHint, the compiled-in proposal every Applier row shows. */
const FLUX_INSTALL_HINT = 'Set idp.flux.install=true on this Pacto release, at the chart version it runs: helm upgrade <release> oci://ghcr.io/trianalab/pacto/charts/pacto-operator --version <version> -n <namespace> --reset-then-reuse-values --set idp.flux.install=true';

/**
 * Data sources as a product surface: findable from the overview, walkable to a source,
 * and honest about the two different things a source page counts.
 *
 * The demo fleet is the fixture and it is adversarial on purpose (examples/demo/
 * source_fleet.go): fifteen sources in three health states, of which `local` is Available
 * and `edge-cluster` is Unavailable. That combination is the whole point -- it means
 * the snapshot is partial WHILE the source you are looking at is fine, which is the
 * state that used to print "Source unavailable" above a page badged Available.
 *
 * None of these can be proven off the components. The unit tests prove each piece draws
 * what it is handed; only the built browser proves the overview, the inventory and the
 * source page are describing the same fifteen sources, that the links between them land,
 * and that a number computed over the complete population did not quietly become a
 * number computed over the page.
 */

const flat = (s: string | null) => (s ?? '').replace(/\s+/g, ' ').trim();

/**
 * The demo fleet: 12 available (the service-graph trace backend, the linked
 * workloads, the hub's two members and the Backstage catalog among them), 1 partial
 * (registry-mirror), 1 unavailable (edge-cluster), all tallied, plus three advisory
 * partial sources: the mesh (k8s-production-mesh), Jaeger (detected-jaeger) and the
 * OTLP receiver (otlp-receiver). The inventory lists them, and the tallies leave them
 * out: an advisory source that is not available never counts against the fleet.
 */
const DEMO_SOURCES_TALLIED = 14;
const DEMO_UNTALLIED = ['detected-jaeger', 'k8s-production-mesh', 'otlp-receiver'];
const DEMO_SOURCES_LISTED = DEMO_SOURCES_TALLIED + DEMO_UNTALLIED.length;

/**
 * The pills the legacy navbar's sources popover lists: the /api/sources rows that are
 * data sources, one per source kind. The demo's four egress readers come back as rows
 * of their own with `egress` set, so they never add a pill. The hub's two members
 * share one target-snapshot pill.
 */
const NAV_PILLS = 11;
const EGRESS_ROWS = { 'kernel-egress': 'partial', 'istio-egress': 'complete', 'cilium-fqdn': 'unavailable', 'egress-gateway': 'partial' };

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

  test('the overview counts the whole source population once, and names the sources', async ({ page }) => {
    await boot(page, '#/fleet');
    const band = await overviewSources(page);
    // The trust figure counts the sources by state (one carrier per fact, L3), so the
    // section states no second tally; its chips name the sources and agree with the
    // inventory. Each source that needs a look keeps its own chip; more than three
    // healthy ones fold into one "12 available" chip. The advisory mesh, Jaeger and OTLP
    // receiver sources are partial and not tallied, so they get no warning chip beside
    // the figure that leaves them out; the inventory lists them.
    await expect(band.locator('.ov-tally')).toHaveCount(0);
    await expect(page.locator('#fig-trust .src-n').last()).toHaveText(String(DEMO_SOURCES_TALLIED - 2));

    const chips = band.locator('a.sh-chip');
    await expect(chips).toHaveCount(3);
    for (const id of DEMO_UNTALLIED) await expect(band.locator('a.sh-chip', { hasText: id })).toHaveCount(0);
    await expect(band.getByTestId('sources-healthy')).toHaveText(/12 available/);
    await expect(band.getByTestId('sources-healthy')).toHaveAttribute('href', /#\/fleet\/sources\?.*sourceHealth=available/);
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
    await expect(page.locator('.page-hd')).toContainText(`${DEMO_SOURCES_LISTED} data sources`);

    // 2. Inventory -> a source.
    await page.locator('.lv-item a', { hasText: 'local' }).first().click();
    await expect(page.locator('main h1')).toContainText('local');

    // 3. The trail says how it got here, and the last crumb is not a link. The h1 names
    //    the source from the route while its record loads, and until then the trail is
    //    the loading one ("Overview › Data source"), so the trail is awaited, not read.
    const crumbs = page.getByRole('navigation', { name: 'Breadcrumb' });
    await expect(crumbs).toContainText(/Overview\s*›\s*Data sources\s*›\s*local/);
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
    await expect(page.locator('main h1')).toContainText('Is anything wrong?');
    await expect(page.locator('#sec-sources')).toBeVisible();
    await page.goForward();
    await expect(page.locator('main h1')).toContainText('Data sources');
    await page.goForward();
    await expect(page.locator('main h1')).toContainText('local');
    await expect(page.locator('#sec-this-data-source')).toBeVisible();
  });

  test('the inventory summary counts every source and filters to the bucket you pick', async ({ page }) => {
    await boot(page, '#/fleet/sources');
    // The figure's state bar: each legend item is "<state> <count>" and links to its filter.
    const tally = page.getByTestId('source-tally');
    const links = tally.locator('.dist-item a');
    const legend = () => links.evaluateAll((as) => as.map((a) => `${a.querySelector('.dist-label')?.textContent} ${a.querySelector('.dist-value')?.textContent}`));
    await expect.poll(legend, { timeout: 20_000 }).toEqual(['Unavailable 1', 'Partial 1', `Current ${DEMO_SOURCES_TALLIED - 2}`]);

    // The bucket IS the filter. Following it must leave exactly that many rows.
    await links.first().click();
    await expect(page).toHaveURL(/sourceHealth=unavailable/);
    await expect(page.locator('.lv-item')).toHaveCount(1);
    await expect(page.locator('.lv-item').first()).toContainText('edge-cluster');

    // The summary is over the COMPLETE population, so a filter does not shrink it --
    // it only marks where you are.
    await expect.poll(legend).toEqual(['Unavailable 1', 'Partial 1', `Current ${DEMO_SOURCES_TALLIED - 2}`]);
    await expect(tally.locator('a[aria-current="true"] .dist-label')).toHaveText('Unavailable');

    // Partial counts registry-mirror only, so its filter lists that one row, never the
    // advisory sources the bucket leaves out.
    await links.nth(1).click();
    await expect(page).toHaveURL(/sourceHealth=partial/);
    await expect(page.locator('.lv-item')).toHaveCount(1);
    await expect(page.locator('.lv-item').first()).toContainText('registry-mirror');

    // The header counts every listed source and the bar three fewer, so the bar names
    // the advisory sources it leaves out.
    const untallied = tally.getByTestId('source-untallied');
    await expect(untallied).toContainText(`${DEMO_UNTALLIED.length} advisory sources not counted:`);
    for (const id of DEMO_UNTALLIED) await expect(untallied.getByRole('link', { name: id })).toHaveCount(1);
  });
});

test.describe('the Backstage catalog is one more source', () => {
  test('the inventory lists the catalog as an available source and its page names it', async ({ page }) => {
    await boot(page, '#/fleet/sources');
    const row = page.locator('.lv-item', { hasText: 'backstage-catalog' });
    await expect(row).toHaveCount(1, { timeout: 20_000 });
    await row.locator('a').first().click();
    await expect(page.locator('main h1')).toContainText('backstage-catalog');
    await expect(page.locator('#sec-this-data-source')).toBeVisible();
    const sources = await page.evaluate(async () => ((await (await fetch('/api/sources')).json()).sources as Array<{ type: string }>));
    expect(sources.filter((s) => s.type === 'backstage-catalog')).toHaveLength(1);
  });
});

test.describe('the inventory says what Pacto detected', () => {
  test('one Detected providers block lists the demo cluster rows, and no destination was added', async ({ page }) => {
    await boot(page, '#/fleet/sources');
    const block = page.getByTestId('detected-providers');
    await expect(block.getByRole('heading', { level: 2, name: 'Detected providers' })).toBeVisible();
    const rows = block.getByTestId('provider-row');
    // Fifteen reported rows (the Pacto CRD, the kernel-egress row,
    // Kubernetes workloads, the Argo CD, Flux and Helm rows that group the
    // inferred services, the Kargo promotion row, the Istio row of the mesh
    // source, the demo and partner registries and five reporting adapter
    // rows, one producer's signed and unsigned reports apart), the operator's
    // declared Installation row, the thirteen the cluster's operator detected
    // (Datadog, three egress readers, Crossplane and the Backstage catalog among them) and the three trace
    // backends their edge sources report: five show, the
    // reported and declared ones first and least healthy first, the rest
    // behind Show all, and no cluster is unread in the demo.
    await expect(rows).toHaveCount(5);
    for (const i of [0, 1, 2, 3, 4]) await expect(rows.nth(i)).not.toHaveClass(/dp-inferred/);
    // Istio is partial, so it is the first row, and it says why.
    const istio = rows.filter({ has: page.locator('.dp-name bdi[title="Istio"]') });
    await expect(istio).toHaveCount(1);
    await expect(istio).toContainText('observed edges');
    await istio.getByText('Why partial').click();
    await expect(istio.locator('.dp-why li')).toHaveText(['1 namespace without sidecars']);
    await expect(block).toContainText('Pacto CRD');
    await expect(block).toContainText('reports provisioning');
    await block.getByRole('button', { name: 'Show all 32 providers' }).click();
    await expect(rows).toHaveCount(32);
    await expect(block).toContainText('Kubernetes workloads');
    await expect(block).toContainText('service grouping, repository provenance');
    // One registry serves Referrers and _catalog; the other serves neither,
    // so Pacto writes its records there as write-once tags.
    await expect(block).toContainText('Registry demo.pacto.local');
    await expect(block).toContainText('Registry partners.demo.pacto.local');
    await expect(block).toContainText('no Referrers, no _catalog');
    await expect(rows.nth(15)).not.toHaveClass(/dp-inferred/);
    await expect(rows.nth(16)).toHaveClass(/dp-inferred/);
    // The running Crossplane is one more inferred row in the same block: its
    // version, what it serves and one provenance mark, never a block of its own.
    const crossplane = rows.filter({ has: page.locator('.dp-name bdi[title="Crossplane"]') });
    await expect(crossplane).toHaveCount(1);
    await expect(crossplane).toHaveClass(/dp-inferred/);
    await expect(crossplane.locator('.dp-evidence')).toContainText('Crossplane v2.1.0 runs and serves apiextensions.crossplane.io/v2');
    await expect(crossplane.getByRole('button', { name: 'Inferred: from cluster-detection' })).toHaveCount(1);
    await expect(page.getByTestId('detected-providers')).toHaveCount(1);
    await expect(block.getByTestId('provider-not-read')).toHaveCount(0);
    // The block lives inside Data sources: the primary navigation gained no entry.
    const primary = page.getByRole('navigation', { name: 'Primary' }).first();
    await expect(primary.getByRole('link', { name: /provider/i })).toHaveCount(0);
  });

  test('the egress readers are provider rows and never a data source pill', async ({ page }) => {
    await boot(page, '#/fleet/sources');
    const block = page.getByTestId('detected-providers');
    await expect(block.getByTestId('provider-row').first()).toBeVisible();
    await block.getByRole('button', { name: /^Show all/ }).click();
    const row = (name: string) => block.getByTestId('provider-row').filter({ has: page.locator('.dp-name').getByTitle(name, { exact: true }) });
    // The cluster source reports what the node agent read, so its row is not inferred.
    const kernel = row('Kernel egress');
    await expect(kernel).toHaveCount(1);
    await expect(kernel).not.toHaveClass(/dp-inferred/);
    await expect(kernel.locator('.dp-evidence')).toContainText('node agent on 3/4 nodes');
    await expect(kernel.locator('.dp-status')).toHaveText('Partial');
    for (const name of ['Istio egress', 'Cilium FQDN', 'Egress gateway']) await expect(row(name)).toHaveClass(/dp-inferred/);
    await expect(row('Egress gateway').locator('.dp-evidence')).toContainText('1 Istio egress gateway found; logs: auto');
    // The external-egress token selects the reader; the cell shows the sentence alone.
    await expect(row('Istio egress').locator('.dp-used')).toHaveText('reads calls leaving the cluster; nothing is turned on');
    // /api/sources lists the readers apart from the data sources the nav counts.
    const sources = await page.evaluate(async () => ((await (await fetch('/api/sources')).json()).sources as Array<{ type: string; egress?: boolean; coverage?: string }>));
    expect(sources.filter((s) => !s.egress)).toHaveLength(NAV_PILLS);
    expect(Object.fromEntries(sources.filter((s) => s.egress).map((s) => [s.type, s.coverage]))).toEqual(EGRESS_ROWS);
  });

  test('Connect an agent says the demo serves no MCP endpoint and opens with the keyboard', async ({ page }) => {
    await boot(page, '#/fleet/sources');
    const line = page.getByTestId('connect-agent');
    await expect(line).toHaveCount(1);
    await expect(line).not.toHaveAttribute('open', '');
    const summary = line.locator('summary');
    await expect(summary).toHaveText('Connect an agent');
    await summary.focus();
    await page.keyboard.press('Enter');
    await expect(line).toHaveAttribute('open', '');
    await expect(line.getByTestId('connect-agent-source')).toHaveText('This demo runs in your browser and serves no MCP endpoint. An installed Pacto serves it at /mcp.');
    await expect(line.getByRole('link', { name: 'Connect an agent over MCP' })).toHaveAttribute('href', 'https://pacto.run/latest/mcp-integration/');
    // It sits right after Detected providers, and the page gained no other disclosure block.
    const after = await page.evaluate(() => {
      const dp = document.querySelector('[data-testid="detected-providers"]'), ca = document.querySelector('[data-testid="connect-agent"]');
      return !!(dp && ca && dp.compareDocumentPosition(ca) & Node.DOCUMENT_POSITION_FOLLOWING);
    });
    expect(after).toBe(true);
  });

  test('the Argo CD row offers the sync gate in one muted line', async ({ page }) => {
    await boot(page, '#/fleet/sources');
    const block = page.getByTestId('detected-providers');
    await block.getByRole('button', { name: /^Show all \d+ providers$/ }).click();
    const argo = block.getByTestId('provider-row').filter({ has: page.locator('.dp-name bdi[title="Argo CD"]') });
    await expect(argo).toHaveCount(1);
    const offer = argo.getByTestId('provider-offer');
    await expect(offer).toHaveCount(1);
    await expect(offer).toContainText('Gate every Argo CD sync with one repo-server plugin');
    const link = offer.getByRole('link', { name: 'Set up: Gate every Argo CD sync with one repo-server plugin' });
    await expect(link).toHaveAttribute('href', 'https://pacto.run/latest/integrations/kubernetes/gitops/#gate-every-argo-cd-sync');
    await expect(link).toHaveAttribute('target', '_blank');
    await expect(link).toHaveAttribute('rel', 'noopener noreferrer');
    // The Argo CD and Kargo rows are the only ones that offer a gate.
    await expect(block.getByTestId('provider-offer')).toHaveCount(2);
  });

  test('the Applier row offers the Flux install as a proposal to review', async ({ page }) => {
    // The demo cluster runs Flux, so it shows no Applier row: the scale fixture carries one.
    await page.addInitScript(installScaleFixture);
    await boot(page, '#/fleet/sources');
    const block = page.getByTestId('detected-providers');
    await block.getByRole('button', { name: /^Show all \d+ providers$/ }).click();
    const applier = block.getByTestId('provider-row').filter({ has: page.locator('.dp-name bdi[title="Applier"]') });
    await expect(applier).toHaveCount(1);
    await expect(applier.locator('.dp-used')).toHaveText('proposal for delivery in profile idp; set it on the Pacto release yourself');
    await applier.getByText('More details').click();
    const hint = applier.locator('.dp-hint');
    await expect(hint).toContainText('Proposal to review');
    await expect(hint).toContainText(FLUX_INSTALL_HINT);
  });

  test('Kargo row offers the shared AnalysisTemplate', async ({ page }) => {
    await boot(page, '#/fleet/sources');
    const block = page.getByTestId('detected-providers');
    await block.getByRole('button', { name: /^Show all \d+ providers$/ }).click();
    const kargo = block.getByTestId('provider-row').filter({ has: page.locator('.dp-name bdi[title="Kargo"]') });
    await expect(kargo).toHaveCount(1);
    await expect(kargo).toContainText('promotion owned by Kargo');
    const offer = kargo.getByTestId('provider-offer');
    await expect(offer).toHaveCount(1);
    await expect(offer).toContainText('Gate every Kargo promotion with one shared AnalysisTemplate');
    const link = offer.getByRole('link', { name: 'Set up: Gate every Kargo promotion with one shared AnalysisTemplate' });
    await expect(link).toHaveAttribute('href', 'https://pacto.run/latest/integrations/kubernetes/gitops-kargo/#kargo');
    await settle(page);
    const r = await new AxeBuilder({ page }).include('[data-testid="detected-providers"]')
      .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
    expect(r.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) }))).toEqual([]);
  });

  // axe.spec.ts audits Data sources with the rows collapsed, so the offer line (behind
  // "Show all") is audited here, contrast included, in both palettes.
  for (const colorScheme of ['dark', 'light'] as const) {
    test(`the expanded providers block, offer line included, passes axe in ${colorScheme}`, async ({ page }) => {
      await page.emulateMedia({ colorScheme });
      await boot(page, '#/fleet/sources');
      const block = page.getByTestId('detected-providers');
      await block.getByRole('button', { name: /^Show all \d+ providers$/ }).click();
      await expect(block.getByTestId('provider-offer')).toHaveCount(2);
      await settle(page);
      const r = await new AxeBuilder({ page }).include('[data-testid="detected-providers"]')
        .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
      expect(r.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) }))).toEqual([]);
    });
  }
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

// The organization index (516-S3) on the scale fixture: the kind tally names it and the
// inventory lists its row, so the layout matrix lays both out.
test('the sources page names one organization index in the tally and lists its row', async ({ page }) => {
  await page.addInitScript(installScaleFixture);
  await appReady(page);
  await page.goto('/#/fleet/sources');
  await expect(page.locator('main')).toContainText('organization index', { timeout: 30_000 });
  await expect(page.getByTestId('source-list')).toContainText('registry-index');
});
