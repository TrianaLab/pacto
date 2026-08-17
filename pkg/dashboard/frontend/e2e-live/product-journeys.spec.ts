import { test, expect } from '@playwright/test';
import type { Locator, Page } from '@playwright/test';
import {
  fixture, entityUrl, surfaceProvides, CAPABILITY_OPERATIONAL_TARGET,
} from './fixture';

// LIVE Kind PRODUCT ACCEPTANCE in a real browser.
//
// These journeys run against the operator-managed dashboard container in a kind
// cluster (port-forwarded), seeded by tests/acceptance/kind/operational-graph.sh: real
// bundles published to an in-cluster registry, real Pacto CRs reconciled by the
// operator, a real managed observation source and the real Evidence Server. The
// keys they address were DISCOVERED by tests/acceptance/kind/productready through the
// same Product API and handed over in PW_FIXTURE — nothing here constructs an
// identity, and nothing here re-derives a product judgement (reconciliation,
// compliance, retrievability) that the backend already made.
//
// Boundary with the offline suite: e2e/ drives the same frontend against the
// in-browser WASM backend, so it owns exhaustive interaction, a11y and viewport
// coverage against seeded data. This suite owns what only a live cluster can
// prove — that the real bundle, the real HTTP API, the real operator status path
// and real observed evidence render as one coherent product. Neither duplicates
// the other.

// section addresses a PreviewSection by its heading. The component renders a bare
// <section>/<details> with no accessible name of its own, so its canonical test id
// plus its heading is the closest stable handle to what a reader sees.
function section(page: Page, title: string): Locator {
  return page
    .getByTestId('preview-section')
    .filter({ has: page.getByRole('heading', { name: title, exact: true }) });
}

// factValue reads the value beside a labelled fact. The product's fact strips and
// definition lists are label/value pairs (<span>Kind</span><span>observation</span>,
// <dt>Ref</dt><dd>oci://…</dd>), so the value is addressed through the label a
// reader reads, never through a class name.
function factValue(scope: Page | Locator, label: string): Locator {
  return scope.getByText(label, { exact: true }).locator('xpath=following-sibling::*[1]');
}

// copyValue reads an EXACT identifier out of a copyable fact. The copy control renders
// the value and a copy button side by side, so the fact's own text is the identifier
// with the button glyph glued onto it; the <code> is the identifier itself, which is
// what "this digest, exactly" has to be asserted against.
function copyValue(scope: Page | Locator, label: string): Locator {
  return factValue(scope, label).getByRole('code');
}

// openDisclosure expands a <details> by its canonical test id and returns it. It is a
// test id and not an accessible name because a <details> maps to role=group, whose name
// comes from the author only — a summary a reader can see is not a name a role query
// can match. Assertions run against expanded content only: text hidden inside a closed
// disclosure is not something the user was shown. Closed, the element IS its summary,
// so clicking it is clicking the control the reader clicks.
async function openDisclosure(scope: Page | Locator, testid: string): Promise<Locator> {
  const d = scope.getByTestId(testid);
  await expect(d).toBeVisible();
  if (!(await d.evaluate((el) => (el as HTMLDetailsElement).open))) {
    await d.click();
  }
  await expect(d).toHaveJSProperty('open', true);
  return d;
}

// expandSection opens a collapsible PreviewSection. A non-collapsible one is already
// open, so this is a no-op there.
async function expandSection(sec: Locator, title: string) {
  if (await sec.evaluate((el) => el.tagName === 'DETAILS' && !(el as HTMLDetailsElement).open)) {
    await sec.getByRole('heading', { name: title, exact: true }).click();
  }
}

function escapeRe(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

// expectEntityPage asserts WHICH entity is on screen: the heading names the kind and
// the entity (the h1 carries a visually-hidden kind prefix), and the page's own
// Identifier disclosure carries — exactly — the canonical key the fixture discovered.
// Comparing URLs instead would compare escaping schemes, not identity.
async function expectEntityPage(page: Page, kind: string, label: RegExp | string, key: string) {
  const name = typeof label === 'string' ? escapeRe(label) : label.source;
  await expect(page.getByTestId('page-title')).toHaveAccessibleName(
    new RegExp(`^${escapeRe(kind)}:\\s+${name}$`),
  );
  const ident = await openDisclosure(page, 'entity-identifier');
  await expect(copyValue(ident, 'Canonical key')).toHaveText(key);
}

// revisionLabel is how the product names a revision: its service and its version. The
// page header prints exactly this, so a journey that expected a bare version number
// would be asserting about a label the product never writes.
function revisionLabel(service: string, version: string): string {
  return `${service} ${version}`;
}

// A — the overview states where its knowledge came from, and routes to the sources.
test('A: the overview reports every live data source and its health', async ({ page }) => {
  await page.goto('/#/fleet');
  await expect(page.getByRole('heading', { level: 1, name: 'Operational overview' })).toBeVisible();

  // The fleet is seeded, so the overview must never claim there is nothing to track:
  // that is the one knowledge posture the fixture rules out.
  await expect(page.getByText('No services tracked yet.')).toHaveCount(0);
  await expect(page.getByText('Nothing is being tracked yet')).toHaveCount(0);

  const band = page.getByRole('region', { name: 'Data sources' });
  await expect(band).toBeVisible();
  const chips = band.getByRole('navigation', { name: 'Data sources by health' }).getByRole('link');
  // The two sources productready proved usable are named AND healthy here; the
  // Evidence Server is named (its health is its own to report).
  await expect(chips.filter({ hasText: fixture.ociSource })).toContainText('Available');
  await expect(chips.filter({ hasText: fixture.observationSource })).toContainText('Available');
  await expect(chips.filter({ hasText: fixture.evidenceSource })).toHaveCount(1);

  await band.getByRole('link', { name: 'View all data sources' }).click();
  await expect(page).toHaveURL(/#\/fleet\/sources/);
  await expect(page.getByTestId('source-tally')).toBeVisible();
  const list = page.getByTestId('source-list');
  for (const id of [fixture.ociSource, fixture.observationSource, fixture.evidenceSource]) {
    await expect(list.getByRole('listitem').filter({ hasText: id })).toHaveCount(1);
  }
});

// B — inventory to a service, and the service states its reconciled dependency.
test('B: services search reaches orders, which reconciles its checkout dependency', async ({ page }) => {
  await page.goto('/#/fleet/services');
  await expect(page.getByRole('heading', { level: 1, name: 'Services' })).toBeVisible();

  await page.getByTestId('svc-search').fill(fixture.ordersName);
  await page.getByTestId('svc-search').press('Enter');
  await page
    .getByTestId('service-list')
    .getByRole('link')
    .filter({ hasText: fixture.ordersName })
    .first()
    .click();

  await expectEntityPage(page, 'Service', fixture.ordersName, fixture.ordersService);
  await expect(page.getByRole('region', { name: 'Operational summary' })).toBeVisible();

  // The declared dependency and the observed traffic are the same edge here, and the
  // backend already said so: the page must show that verdict, not recompute it.
  const observed = section(page, 'Observed traffic and differences');
  const row = observed.getByRole('listitem').filter({ hasText: fixture.checkoutName });
  await expect(row).toHaveCount(1);
  await expect(row).toContainText('Depends on');
  await expect(row).toContainText('Matched');
});

// C — the service's running revision is a real, immutable, retrievable OCI revision
// that declares the checkout dependency. A K8s-synthetic revision could not.
test('C: the orders revision carries real OCI content identity and its declaration', async ({ page }) => {
  await page.goto(entityUrl('service', fixture.ordersService));
  await section(page, 'Revisions in use')
    .getByRole('listitem')
    .filter({ hasText: fixture.ordersVersion })
    .first()
    .getByRole('link')
    .click();

  await expectEntityPage(
    page, 'Revision', revisionLabel(fixture.ordersName, fixture.ordersVersion), fixture.ordersRevision,
  );
  await expect(page.getByText('Retrievable content')).toBeVisible();

  const identity = await openDisclosure(page, 'revision-identity');
  await expect(copyValue(identity, 'Digest')).toHaveText(/^sha256:[0-9a-f]{64}$/);
  // The resolved ref is pinned to the digest under the registry the fixture published
  // to: this content is addressable again, not a name that may move.
  await expect(copyValue(identity, 'Resolved ref')).toHaveText(
    new RegExp(`^oci://${escapeRe(fixture.domain)}/${escapeRe(fixture.ordersName)}@sha256:[0-9a-f]{64}$`),
  );

  const declared = section(page, 'Declared dependencies');
  const dep = declared.getByRole('listitem').filter({ hasText: fixture.checkoutName });
  await expect(dep).toHaveCount(1);
  await expect(factValue(dep, 'Ref')).toHaveText(`oci://${fixture.domain}/${fixture.checkoutName}`);
  await expect(factValue(dep, 'Required')).toHaveText('No');
  await expect(factValue(dep, 'Compatibility')).toHaveText(/^\^\d+\.\d+\.\d+$/);
});

// D — the operational target is its own entity: where the service RUNS, matched to a
// revision. Target and revision are never flattened into one page.
test('D: the checkout target is a distinct entity matched to the running revision', async ({ page }) => {
  // Only where something reconciles a declared contract against a running workload.
  // Skipping is not the same as omitting: the surface DECLARED it has no controller,
  // the gate subtracted these facts from the count it proved, and the report says so.
  test.skip(
    !surfaceProvides(CAPABILITY_OPERATIONAL_TARGET),
    `the ${fixture.surface} surface provides no ${CAPABILITY_OPERATIONAL_TARGET}: nothing there reconciles a Pacto CR`,
  );
  await page.goto(entityUrl('service', fixture.checkoutService));
  await section(page, 'Operational targets').getByRole('listitem').first().getByRole('link').click();

  await expectEntityPage(page, 'Operational target', /.+/, fixture.checkoutTarget);
  // Compliance is stated once, by the header badge, in the product's own vocabulary.
  await expect(page.getByTestId('page-title').locator('xpath=following-sibling::*[1]')).toHaveText(
    /^(Compliant|Not compliant|Warning|Invalid|Unknown|Not evaluated)$/,
  );

  // The target states BOTH identity dimensions, and states them independently. The
  // operator pins this workload to a digest, so the revision match is exact. It writes
  // that pin into status.contract.resolvedRef WITHOUT the oci:// scheme, and a
  // scheme-less ref is not something Pacto's resolver can retrieve content through --
  // so the content dimension is honestly "local", not retrievable. Exact match over
  // non-retrievable content is the documented, deliberate pair (pkg/fleet/ref.go, and
  // TestTargetIdentity_ExactMatch_NonRetrievable pins this exact operator shape); the
  // whole region is asserted so a third claim could not appear between them unnoticed.
  const identity = page.getByRole('region', { name: 'Operational target identity' });
  await expect(identity).toHaveText(
    'Revision match Exact revision match Content Local reference (not retrievable)',
  );
  await expect(factValue(page, 'Service')).toContainText(fixture.checkoutName);
  await expect(factValue(page, 'Data source')).toHaveText(/\S/);

  // The revision it runs is a DIFFERENT entity with its own canonical key — the
  // published revision A, not a copy of the target.
  await factValue(page, 'Running revision').click();
  await expectEntityPage(
    page, 'Revision', revisionLabel(fixture.checkoutName, fixture.checkoutVersionA), fixture.checkoutRevisionA,
  );
  // And THAT revision is the immutable OCI content the operator's digest pin resolved
  // to. This is the K8s -> OCI path closing on itself: the exact match above is a match
  // to content that is addressable again, under the registry the fixture published to.
  const revIdentity = await openDisclosure(page, 'revision-identity');
  await expect(copyValue(revIdentity, 'Resolved ref')).toHaveText(
    new RegExp(`^oci://${escapeRe(fixture.domain)}/${escapeRe(fixture.checkoutName)}@sha256:[0-9a-f]{64}$`),
  );
});

// E — the graph draws real topology for orders. An honest empty state would be a
// truthful answer to a different question; this fixture has edges.
test('E: the operational graph draws the orders neighborhood with its matched edge', async ({ page }) => {
  await page.goto('/#/fleet/graph');
  await expect(page.getByTestId('graph-discovery')).toBeVisible();
  await expect(page.getByTestId('neighborhood-canvas')).toHaveCount(0); // search-first: nothing drawn yet

  await page
    .getByRole('searchbox', { name: 'Search for a service, revision or target to focus the graph' })
    .fill(fixture.ordersName);
  await page
    .getByTestId('graph-focus-link')
    .filter({ hasText: 'Service' })
    .filter({ hasText: fixture.ordersName })
    .first()
    .click();

  await expect(page).toHaveURL(/#\/fleet\/graph\/.+/);
  await expect(page.getByRole('group', { name: 'Graph controls' })).toBeVisible();
  await expect(page.getByRole('img', { name: /^Operational graph topology/ })).toBeVisible();
  await expect(page.getByTestId('graph-empty')).toHaveCount(0);
  await expect(page.getByTestId('graph-render-error')).toHaveCount(0);

  await openDisclosure(page, 'graph-textalt');
  const dep = page
    .getByTestId('graph-edge')
    .filter({ hasText: fixture.ordersName })
    .filter({ hasText: fixture.checkoutName })
    .filter({ hasText: 'Depends on' });
  await expect(dep).toHaveCount(1);
  await expect(dep.getByTestId('edge-difference')).toHaveText('Matched');
});

// F — the managed observation source is a first-class data source with its own
// identity and its own health, distinct from the snapshot's completeness.
test('F: the managed observation source has its own page, kind and health', async ({ page }) => {
  await page.goto('/#/fleet/sources');
  const search = page.getByRole('searchbox', { name: 'Search data sources' });
  await search.fill(fixture.observationSource);
  await search.press('Enter');
  await page
    .getByTestId('source-list')
    .getByRole('link')
    .filter({ hasText: fixture.observationSource })
    .first()
    .click();

  await expectEntityPage(page, 'Data source', fixture.observationSource, fixture.observationSource);

  const about = page.getByRole('region', { name: 'This data source' });
  // THIS source's health, in a sentence about this source.
  await expect(about).toContainText(
    'This data source answered in full, so everything it holds is in the snapshot.',
  );
  // …which is a different fact from how complete the snapshot is. The snapshot caveat
  // is never inside this section, whichever way it resolves.
  await expect(about.getByText('The fleet snapshot is missing data.')).toHaveCount(0);
  // Offline traces, not K8s, OCI or the Evidence Server.
  await expect(factValue(about, 'Kind')).toHaveText('observation');
  await expect(section(page, 'Product entities contributed')).toBeVisible();
});

// G — a real semantic change between two published revisions, and who it reaches.
test('G: change analysis compares the two published checkout revisions', async ({ page }) => {
  await page.goto(entityUrl('service', fixture.checkoutService));
  const all = section(page, 'All revisions');
  await expandSection(all, 'All revisions'); // collapsed while B is published but not running
  await all.getByRole('listitem').filter({ hasText: fixture.checkoutVersionB }).first().getByRole('link').click();
  await expectEntityPage(
    page, 'Revision', revisionLabel(fixture.checkoutName, fixture.checkoutVersionB), fixture.checkoutRevisionB,
  );

  // The action carries this revision over as the LATER side; the earlier side is chosen
  // by canonical key, so the comparison is of content, not of a version string.
  await page.getByRole('link', { name: 'Compare revisions' }).click();
  await expect(page.getByLabel('Later revision')).toHaveValue(fixture.checkoutRevisionB);
  await page.getByLabel('Earlier revision').selectOption(fixture.checkoutRevisionA);
  await page.getByRole('button', { name: 'Compare revisions' }).click();

  const changed = page.getByTestId('changes-what-changed');
  await expect(changed).toBeVisible();
  await expect(page.getByTestId('changes-counts')).toContainText(/[1-9]\d* breaking/);

  const affects = page.getByTestId('changes-what-it-affects');
  await expect(affects.getByRole('heading', { name: /^Affected consumers/ })).toBeVisible();
  await expect(affects.getByRole('row').filter({ hasText: fixture.ordersName })).toHaveCount(1);
});

// H — the remote, signed evidence target survived alongside K8s, OCI and observation.
test('H: the external payments target is reachable and attributed to the Evidence Server', async ({ page }) => {
  await page.goto(entityUrl('service', fixture.evidenceService));
  const targets = section(page, 'Operational targets');
  await expect(targets.getByRole('listitem')).toHaveCount(1); // external: evidence is the only witness
  await targets.getByRole('listitem').first().getByRole('link').click();

  await expectEntityPage(page, 'Operational target', /.+/, fixture.evidenceTarget);
  await expect(
    page
      .getByText(/^(Data source|Contributing data sources)$/)
      .locator('xpath=following-sibling::*[1]')
      .filter({ hasText: fixture.evidenceSource }),
  ).not.toHaveCount(0);
});
