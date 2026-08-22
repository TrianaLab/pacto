import { expect, test, type APIRequestContext, type Locator, type Page } from '@playwright/test';

// Level 6 of the taxonomy in docs/maintainers/testing.md, applied to the
// documentation site instead of the dashboard: the real `mkdocs build --strict`
// output, served over HTTP, driven in Chromium.
//
// What the surrounding gates already prove, and therefore what this one must not
// re-prove: `make mermaid-check` parses every fence outside the site, and
// e2e/mermaid.spec.ts renders bundle documentation inside the dashboard. Neither
// one loads the MkDocs site, so neither can see the Material theme, the
// integration-doc injection hook, or instant navigation. Those three are the
// whole subject here.

/**
 * Every diagram this gate covers, with labels each one must actually paint.
 *
 * The number of entries per page is asserted against the built HTML, so adding a
 * diagram to one of these pages fails the gate until it is declared here.
 * Coverage cannot rot quietly.
 */
const CORE = {
  path: '/operational-graph/',
  diagrams: [
    ['Operational Graph', 'Fleet Query', 'Evidence Server', 'Live Kubernetes'],
    ['EvidenceSet', 'Findings + Coverage', 'Query answers'],
  ],
};

const INSTANT_TARGET = {
  path: '/impact/',
  diagrams: [['Semantic diff', 'Fleet Snapshot', 'Impact result', 'Owners to notify']],
};

// Owned by integrations/kubernetes/docs/overview.md and assembled into the site
// by release/scripts/mkdocs_integration_hook.py. It is here because injected
// pages are the ones no other check looks at.
const INTEGRATION = {
  path: '/integrations/kubernetes/overview/',
  diagrams: [['Pacto CR', 'K8s API', 'Status + Conditions', 'Prometheus metrics']],
};

type Covered = typeof CORE;

/**
 * Material renders each diagram into a CLOSED shadow root, which no test can
 * read. Forcing open mode flips the encapsulation flag and nothing else — same
 * Mermaid, same DOM, same pixels — and it is the only way to assert on what the
 * reader actually sees rather than on the source that preceded it.
 */
function pierceClosedShadowRoots() {
  const attachShadow = Element.prototype.attachShadow;
  Element.prototype.attachShadow = function (this: Element, init: ShadowRootInit) {
    return attachShadow.call(this, { ...init, mode: 'open' });
  };
}

/**
 * A diagram that renders because a CDN answered is a diagram that stops
 * rendering when the CDN does. Every cross-origin request is aborted, so the
 * site has to be self-sufficient, and the recorded attempts make the dependency
 * itself assertable rather than merely absent.
 */
async function serveOnlyFromTheSite(page: Page, origin: string) {
  const offSite: string[] = [];
  await page.route('**/*', route => {
    const url = route.request().url();
    // Parsed origins compared exactly, never a string prefix: `http://127.0.0.1:43220`
    // starts with the text of `http://127.0.0.1:4322`, so a prefix test would let a
    // whole family of foreign origins through the one barrier this suite rests on.
    if (sameOrigin(url, origin)) return route.continue();
    offSite.push(`${route.request().resourceType()} ${url}`);
    return route.abort();
  });
  return offSite;
}

function sameOrigin(url: string, origin: string) {
  try {
    return new URL(url).origin === origin;
  } catch {
    // Unparseable, and opaque schemes parse to the origin "null" — either way it is
    // not the site, so it does not get to load.
    return false;
  }
}

function watchForErrors(page: Page) {
  const errors: string[] = [];
  page.on('pageerror', error => errors.push(`pageerror: ${error.message}`));
  page.on('console', message => {
    // Aborting off-site requests logs its own network failures; those are the
    // point of the abort, not a defect. Anything the diagram runtime says is.
    if (message.type() === 'error' && /mermaid/i.test(message.text())) {
      errors.push(`console: ${message.text()}`);
    }
  });
  return errors;
}

/** How many diagrams the built HTML declares, before any script has run. */
async function declaredDiagrams(request: APIRequestContext, path: string) {
  const response = await request.get(path);
  expect(response.ok(), `${path} is served`).toBe(true);
  return (await response.text()).match(/<pre class="mermaid">/g)?.length ?? 0;
}

/** The label text of a rendered diagram, with the injected stylesheet removed. */
async function renderedText(svg: Locator) {
  return svg.evaluate(element => {
    const copy = element.cloneNode(true) as Element;
    copy.querySelectorAll('style').forEach(style => style.remove());
    return (copy.textContent ?? '').replace(/\s+/g, ' ').trim();
  });
}

async function expectEveryDiagramRendered(page: Page, request: APIRequestContext, covered: Covered) {
  expect(
    await declaredDiagrams(request, covered.path),
    `${covered.path} declares exactly the diagrams this gate covers`,
  ).toBe(covered.diagrams.length);

  // Mermaid replaces each <pre> host with a <div class="mermaid"> holding the
  // SVG, so this count reaching the declared one is the transformation itself —
  // and it retries, which is the readiness signal. No sleeps.
  const svgs = page.locator('div.mermaid svg');
  await expect(svgs).toHaveCount(covered.diagrams.length);

  // Nothing may be left in its pre-render form, and nothing may have rendered
  // into Mermaid's error placeholder.
  await expect(page.locator('pre.mermaid, code.language-mermaid')).toHaveCount(0);
  await expect(page.locator('div.mermaid .error-icon')).toHaveCount(0);

  for (const [index, labels] of covered.diagrams.entries()) {
    const svg = svgs.nth(index);
    await expect(svg).toBeVisible();

    const box = await svg.boundingBox();
    expect(box, `diagram ${index} has a layout box`).not.toBeNull();
    expect(box!.width, `diagram ${index} has width`).toBeGreaterThan(0);
    expect(box!.height, `diagram ${index} has height`).toBeGreaterThan(0);

    const text = await renderedText(svg);
    expect(text, `diagram ${index} is not empty`).not.toBe('');
    expect(text, `diagram ${index} parsed`).not.toContain('Syntax error');
    for (const label of labels) {
      expect(text, `diagram ${index} labels`).toContain(label);
    }
  }
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript(pierceClosedShadowRoots);
});

test('a core page renders its diagrams from the site itself', async ({ page, request, baseURL }) => {
  const errors = watchForErrors(page);
  const offSite = await serveOnlyFromTheSite(page, new URL(baseURL!).origin);

  await page.goto(CORE.path);
  await expectEveryDiagramRendered(page, request, CORE);

  expect(
    offSite.filter(attempt => attempt.startsWith('script')),
    'the diagram runtime is served by the site, not fetched from a third party',
  ).toEqual([]);
  expect(errors).toEqual([]);
});

test('a look-alike origin is still off-site', async ({ page, baseURL }) => {
  const offSite = await serveOnlyFromTheSite(page, new URL(baseURL!).origin);
  await page.goto(CORE.path);

  // The whole suite rests on one barrier: nothing loads unless the site served it.
  // Port 43220 starts with the text of port 4322, so a prefix comparison would have
  // called this the site and let it through. Drive the real handler and prove it did
  // not, or every other test here can pass on someone else's runtime.
  const lookAlike = new URL(baseURL!);
  lookAlike.port = `${lookAlike.port}0`;
  const probe = `${lookAlike.origin}/probe.js`;
  await page.evaluate(url => fetch(url).catch(() => {}), probe);

  await expect
    .poll(() => offSite.some(attempt => attempt.endsWith(probe)))
    .toBe(true);
});

test('an injected integration page renders its diagram', async ({ page, request, baseURL }) => {
  const errors = watchForErrors(page);
  await serveOnlyFromTheSite(page, new URL(baseURL!).origin);

  await page.goto(INTEGRATION.path);
  await expectEveryDiagramRendered(page, request, INTEGRATION);
  expect(errors).toEqual([]);
});

test('diagrams render after instant navigation, not only after a full load', async ({
  page,
  request,
  baseURL,
}) => {
  const errors = watchForErrors(page);
  await serveOnlyFromTheSite(page, new URL(baseURL!).origin);

  await page.goto(CORE.path);
  await expectEveryDiagramRendered(page, request, CORE);

  // A full page load destroys the window; instant navigation swaps the container
  // and leaves it standing. This witness is what tells the two apart, so the test
  // cannot quietly degrade into a second direct page load.
  await page.evaluate(() => {
    (window as unknown as { __sameDocument?: true }).__sameDocument = true;
  });

  await page.locator(`.md-nav--primary a[href$="${INSTANT_TARGET.path}"]`).first().click();
  await page.waitForURL(`**${INSTANT_TARGET.path}`);

  expect(
    await page.evaluate(() => (window as unknown as { __sameDocument?: true }).__sameDocument),
    'the click was intercepted by instant navigation rather than reloading the document',
  ).toBe(true);

  await expectEveryDiagramRendered(page, request, INSTANT_TARGET);
  expect(errors).toEqual([]);
});
