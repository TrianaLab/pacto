import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync } from 'node:fs';
import { join, relative } from 'node:path';
import { parse } from 'svelte/compiler';

/**
 * Architecture guard (ADR-6, requirement, item 9): the generated OpenAPI SDK is
 * the ONLY way the dashboard talks to the backend. This test fails if new code
 * bypasses it - a raw fetch of a backend route, or a hand-built `/api/...` URL -
 * outside the single transport seam and the facade. It keeps the wire contract
 * from drifting back into scattered handwritten clients.
 *
 * Allowed exceptions, by design:
 *  - lib/transport.ts   the single fetch seam behind the generated client;
 *  - lib/api.ts         the facade, which passes typed operation paths to the
 *                       generated client (type-checked against the contract, not
 *                       URL construction);
 *  - lib/generated/**   the generated SDK itself.
 *
 * The remaining item-9 invariants are enforced at COMPILE time in api.typetest.ts
 * (svelte-check, threshold error), which is stronger than a regex and is what
 * TypeScript can verify directly: facade request types derive from the generated
 * operations, no facade method returns `Promise<unknown>`, and product entity detail
 * leaves the facade as NarrowedEntityDetail. This file covers the structural
 * invariants TypeScript cannot: where raw fetch and backend paths may appear, that
 * the static fixtures are request-semantic, and that no hand-written wire DTO mirror
 * has returned.
 */

const SRC = join(process.cwd(), 'src'); // vitest runs from the frontend package root

const FETCH_ALLOW = ['lib/transport.ts'];
const API_PATH_ALLOW = ['lib/transport.ts', 'lib/api.ts'];

function sourceFiles(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = join(dir, entry.name);
    if (entry.isDirectory()) {
      if (entry.name === 'generated' || entry.name === 'node_modules') continue;
      out.push(...sourceFiles(full));
      continue;
    }
    if (!/\.(ts|svelte)$/.test(entry.name)) continue;
    // Test and compile-time-test files legitimately reference paths/fetch mocks.
    if (/\.(test|typetest)\.ts$/.test(entry.name)) continue;
    out.push(full);
  }
  return out;
}

const files = sourceFiles(SRC).map((f) => ({ rel: relative(SRC, f), body: readFileSync(f, 'utf8') }));

describe('frontend backend-access architecture', () => {
  it('scans a non-trivial set of source files', () => {
    expect(files.length).toBeGreaterThan(20);
  });

  // RAW_NETWORK flags common raw network-capability usage: bare `fetch(`, the global
  // fetch qualified or optional-chained (`window.fetch(`, `globalThis.fetch(`,
  // `self.fetch(`, `window?.fetch(`), and the other raw-HTTP capabilities
  // (`new XMLHttpRequest`/`EventSource`/`WebSocket`, `.sendBeacon(`). It is a
  // best-effort defense-in-depth text scan: a dynamic alias (`const f = fetch`) or a
  // bracket-access spelling (`globalThis['fetch']`) can still evade a text scan, so a
  // lint-level no-restricted rule is the durable enforcement (a noted follow-up). It
  // deliberately does NOT match the word "fetch" in prose or a `fetch:` property.
  const RAW_NETWORK =
    /(?<![.\w])fetch\s*\(|\b(?:globalThis|window|self)\s*\??\.\s*fetch\s*\(|new\s+(?:XMLHttpRequest|EventSource|WebSocket)\b|\.sendBeacon\s*\(/;
  // BACKEND_PATH flags a hand-built backend route literal. `/health` and `/metrics`
  // are anchored to a full path segment (a trailing char that is neither a word char
  // nor a hyphen) so a client route or asset like `/metrics-overview`, `/health-report`
  // or `/metricsIcon` is NOT a false positive in a fleet health/metrics dashboard.
  const BACKEND_PATH = /['"`]\/(?:api\/|(?:health|metrics)(?![\w-]))/;

  it('performs raw backend network access only in the transport seam', () => {
    const offenders = files
      .filter((f) => !FETCH_ALLOW.includes(f.rel))
      .filter((f) => RAW_NETWORK.test(f.body))
      .map((f) => f.rel);
    expect(offenders, `raw network access outside the transport seam: ${offenders.join(', ')}`).toEqual([]);
  });

  it('references a backend path only in the facade and transport', () => {
    const offenders = files
      .filter((f) => !API_PATH_ALLOW.includes(f.rel))
      .filter((f) => BACKEND_PATH.test(f.body))
      .map((f) => f.rel);
    expect(offenders, `hand-built backend URL outside the generated-SDK facade: ${offenders.join(', ')}`).toEqual([]);
  });

  it('the guard patterns catch common raw-access spellings without lookalike false positives', () => {
    // Self-test the regexes so a future weakening (or an over-broad tweak) is caught.
    for (const bad of [
      'fetch("/x")', 'window.fetch(u)', 'globalThis.fetch(u)', 'self.fetch(u)',
      'window?.fetch(u)', 'new XMLHttpRequest()', 'new WebSocket(u)', 'new EventSource(u)', 'navigator.sendBeacon(u)',
    ]) {
      expect(RAW_NETWORK.test(bad), `RAW_NETWORK should flag: ${bad}`).toBe(true);
    }
    for (const ok of ['// a failed fetch never', 'fetch: fetchAll', 'const refetch = () => {}', 'obj.prefetch(x)']) {
      expect(RAW_NETWORK.test(ok), `RAW_NETWORK must not flag: ${ok}`).toBe(false);
    }
    for (const bad of ["'/api/services'", "'/health'", "`/metrics`", "'/health/x'"]) {
      expect(BACKEND_PATH.test(bad), `BACKEND_PATH should flag: ${bad}`).toBe(true);
    }
    for (const ok of ["'/metrics-overview'", "'/health-report'", "'/metricsIcon.svg'", "'/apiary'"]) {
      expect(BACKEND_PATH.test(ok), `BACKEND_PATH must not flag lookalike: ${ok}`).toBe(false);
    }
  });

  it('matches static fixtures by request semantics, not by raw pathname', () => {
    const transport = readFileSync(join(SRC, 'lib/transport.ts'), 'utf8');
    // A request-semantic model: fixtures carry method + path + normalized query (+
    // body), and the matcher compares those - never a raw-URL/pathname table lookup.
    expect(transport, 'StaticRoute must carry an HTTP method').toMatch(/interface StaticRoute[\s\S]*method:/);
    expect(transport, 'StaticRoute must carry a path').toMatch(/interface StaticRoute[\s\S]*path:/);
    expect(transport, 'the matcher must compare the request method').toMatch(/r\.method/);
    // The old pathname-only lookup ("pathname in data.routes") must not return.
    expect(transport, 'static matching must not be pathname-only').not.toMatch(/\bin\s+data\.routes\b/);
    expect(transport).not.toMatch(/data\.routes\[/);
  });

  it('keeps no hand-written wire DTO mirror (the generated schema is the only wire truth)', () => {
    // ADR-6 reversed the hand-maintained productTypes.ts mirror + in-Go structural
    // drift parser. Neither may return: the generated SDK is the single wire truth.
    const files = readdirSync(join(SRC, 'lib'));
    expect(files, 'a hand-written wire DTO mirror (productTypes.ts) must not exist').not.toContain('productTypes.ts');
    expect(files).not.toContain('productTypes.typetest.ts');
  });
});

/**
 * Product visual-coherence guard. The complaint that started this work was that the
 * app "feels like several generations of Pacto UI stitched together", and the most
 * literal instance of that was one control -- a `<details>` disclosure -- carrying five
 * unrelated designs across five product screens: an accent-coloured link, quiet grey,
 * an inherited default, a hand-rolled caret, and one whose `display: flex` summary had
 * silently lost its native marker and so read as a dead label.
 *
 * Legacy `pacto doc` surfaces (src/sections/**) are deliberately out of scope: that host
 * is not Fleet-capable and keeps its own presentation.
 */
describe('product visual coherence', () => {
  const PRODUCT_DISCLOSURE = files.filter(
    (f) => /\.svelte$/.test(f.rel) && !f.rel.startsWith('sections/') && f.body.includes('<details'),
  );

  it('scans the product disclosures', () => {
    expect(PRODUCT_DISCLOSURE.length).toBeGreaterThanOrEqual(4);
  });

  it('opens every product disclosure with the one shared control', () => {
    const offenders = PRODUCT_DISCLOSURE.filter(
      (f) => !/<details[^>]*class="[^"]*\bdisclosure\b/.test(f.body)
        || !f.body.includes('class="disclosure-caret"'),
    ).map((f) => f.rel);
    expect(
      offenders,
      `product <details> must use the shared .disclosure class and .disclosure-caret: ${offenders.join(', ')}`,
    ).toEqual([]);
  });

  it('defines the shared disclosure exactly once, in the shared stylesheet', () => {
    const css = readFileSync(join(SRC, 'styles/components.css'), 'utf8');
    expect(css).toMatch(/\.disclosure\s*>\s*summary\s*\{/);
    // A summary laid out with flex loses ::marker, which is how the caret earns its keep.
    expect(css).toMatch(/\.disclosure-caret\b/);
  });
});

/**
 * Visualization-system guard.
 *
 * The redesign audit RETIRED four chart types from the product because each answered a
 * question the data cannot support: a readiness donut (a part-of-whole reading of scores
 * that are not parts of a whole), a heatmap and a treemap (dense colour with no textual
 * equivalent), and a priority quadrant (a synthetic two-axis score nobody can trace back
 * to a finding). They still exist, and legitimately so, on the legacy non-Fleet `pacto
 * doc` surfaces, which keep their own presentation and are out of scope.
 *
 * What must not happen is their quiet return to the product. This guard names the product
 * surfaces explicitly, so adding a Fleet view puts it under the rule automatically, and a
 * decision to bring one of these back has to be made out loud by editing this list.
 */
describe('product visualization system', () => {
  const isProduct = (rel: string) =>
    /^views\/Fleet[^/]*\.svelte$/.test(rel) || rel.startsWith('views/entity/') || rel.startsWith('components/viz/');
  const PRODUCT = files.filter((f) => isProduct(f.rel));

  // The primitives the product is allowed to draw with. Each is bounded, textual and
  // accessible by construction; see components/viz/viz.test.ts for their contracts.
  const ALLOWED_VIZ = ['DistributionBar.svelte', 'HorizontalBars.svelte', 'PostureBars.svelte'];
  // Retired: a shape whose meaning the product data cannot honestly support.
  const RETIRED = /\b(ReadinessDonut|ReadinessHeatmap|TreemapChart|PriorityQuadrant)\b|conic-gradient|stroke-dasharray/;

  it('scans the product surfaces', () => {
    expect(PRODUCT.length).toBeGreaterThanOrEqual(10);
  });

  it('draws with the shared primitives only, and never with a retired chart type', () => {
    const offenders = PRODUCT.filter((f) => RETIRED.test(f.body)).map((f) => f.rel);
    expect(
      offenders,
      `retired chart type (donut/heatmap/treemap/quadrant) back on a product surface: ${offenders.join(', ')}`,
    ).toEqual([]);
  });

  it('keeps the primitive set closed, so a new chart type is a decision and not an accident', () => {
    const present = readdirSync(join(SRC, 'components/viz')).filter((f) => f.endsWith('.svelte'));
    expect(present.sort()).toEqual(ALLOWED_VIZ.slice().sort());
  });

  it('still allows the legacy non-Fleet host its own charts', () => {
    // Proof the guard is scoped, not vacuous: the retired components DO still exist and
    // are still used somewhere, just never on a product surface.
    const legacy = files.filter((f) => !isProduct(f.rel) && RETIRED.test(f.body)).map((f) => f.rel);
    expect(legacy.length, 'the guard would be vacuous if nothing used these anywhere').toBeGreaterThan(0);
  });
});

/**
 * Design-system guards (requirements 7, 8, 13, 14, 22).
 *
 * Three defects motivated these, and each was invisible to a green test suite because
 * every one of them is a CSS declaration that silently does nothing:
 *
 *  1. A component reached for `--text-md`, `--radius-md` or `--c-accent-border` before
 *     any of them was declared. An undefined custom property makes the whole
 *     declaration invalid at computed-value time, so `font-size: var(--text-md)`
 *     did not fall back to something close -- it INHERITED, and a section title that
 *     was trying to be one size ended up whatever its parent happened to be.
 *  2. Components picked a size by HTML tag, so the same visual role rendered at two
 *     sizes depending on whether it was nested, and the fix-ups were hard-coded
 *     `font-size` overrides on `h2`/`h3` selectors -- a second, private type scale.
 *  3. Definitions lived in a `data-tip`, a CSS `::after` fed by `attr()`. It is
 *     mouse-only: a `<th>` or a `<span>` takes no focus, and the shared rule removes
 *     the tooltip entirely under `@media (hover: none)`.
 *
 * SCOPE, stated explicitly because the token rule would otherwise produce false
 * positives: only GLOBAL design tokens are checked -- the families declared in
 * styles/tokens.css and listed in GLOBAL_TOKEN_FAMILIES below. A component-local
 * custom property (`--tone-c`, set by a tone class and read by the element it colours)
 * belongs to its component, is not part of the shared vocabulary, and is not checked.
 * A file that declares a token itself is likewise never flagged for using it.
 *
 * This is a source scan over files on disk, not a runtime sanitizer: it parses
 * declarations and `var()` references out of stylesheets and component `<style>`
 * blocks. Requirement 20's e2e/typography.spec.ts is the other half -- it measures
 * COMPUTED styles in a real browser, which is the only place "these two look the same"
 * can actually be proven.
 */
describe('product design system', () => {
  const STYLES = join(SRC, 'styles');
  const globalCss = readdirSync(STYLES)
    .filter((f) => f.endsWith('.css'))
    .map((f) => readFileSync(join(STYLES, f), 'utf8'))
    .join('\n');

  const declarations = (css: string) => new Set(css.match(/--[a-zA-Z0-9-]+(?=\s*:)/g) ?? []);
  const references = (css: string) =>
    new Set([...css.matchAll(/var\(\s*(--[a-zA-Z0-9-]+)/g)].map((m) => m[1]));

  const DECLARED = declarations(globalCss);
  // The shared vocabulary, by family. Anything outside these prefixes is a
  // component-local variable and out of scope by design.
  const GLOBAL_TOKEN_FAMILIES = [
    '--text-', '--font-', '--line-', '--sp-', '--radius-', '--c-', '--shadow-',
    '--role-', '--touch-', '--transition-', '--chart-', '--container-', '--navbar-',
  ];
  const isGlobalToken = (t: string) => GLOBAL_TOKEN_FAMILIES.some((p) => t.startsWith(p));

  // Every source file that can carry CSS: components, views and the stylesheets
  // themselves (a token family that references a token from another family is the same
  // bug one level up).
  const STYLED = [
    ...files.filter((f) => f.rel.endsWith('.svelte')),
    ...readdirSync(STYLES).filter((f) => f.endsWith('.css'))
      .map((f) => ({ rel: `styles/${f}`, body: readFileSync(join(STYLES, f), 'utf8') })),
  ];

  it('declares a real token vocabulary', () => {
    expect(DECLARED.size).toBeGreaterThan(50);
    // The three that were missing, named so a regression is unambiguous.
    for (const t of ['--text-md', '--radius-md', '--c-accent-border']) {
      expect(DECLARED.has(t), `${t} must be declared`).toBe(true);
    }
  });

  it('references no undeclared global design token', () => {
    const offenders: string[] = [];
    for (const f of STYLED) {
      const local = declarations(f.body);
      for (const t of references(f.body)) {
        if (isGlobalToken(t) && !DECLARED.has(t) && !local.has(t)) offenders.push(`${f.rel}: ${t}`);
      }
    }
    expect(offenders, `undeclared global design token:\n  ${offenders.join('\n  ')}`).toEqual([]);
  });

  it('scopes the token rule to the shared vocabulary, not to component-local variables', () => {
    // Proof the rule is scoped rather than vacuous or over-broad: `--tone-c` is a real
    // component-local variable in the tree, is NOT globally declared, and must not be
    // flagged; a made-up token in a global family must be.
    expect(DECLARED.has('--tone-c'), 'the fixture assumes --tone-c is component-local').toBe(false);
    expect(isGlobalToken('--tone-c')).toBe(false);
    expect(isGlobalToken('--text-enormous')).toBe(true);
    expect(DECLARED.has('--text-enormous')).toBe(false);
    const usesLocal = files.filter((f) => references(f.body).has('--tone-c'));
    expect(usesLocal.length, 'the scope carve-out would be vacuous if nothing used a local var').toBeGreaterThan(0);
  });

  /**
   * Requirement 8: a component picks a ROLE, not a size. The typography roles are the
   * whole vocabulary, and a heading's LEVEL (its place in the outline) must not decide
   * how big it looks -- that coupling is what put a section title above the page title
   * it sat under.
   */
  const ROLE_CLASSES = [...globalCss.matchAll(/^\.(t-[a-z0-9-]+)\s*[,{]/gm)].map((m) => m[1]);
  const PRODUCT_SURFACES = files.filter((f) => f.rel.endsWith('.svelte')).filter((f) =>
    /^views\/(Fleet[^/]*|ChangeAnalysisView|GraphView)\.svelte$/.test(f.rel)
    || f.rel.startsWith('views/entity/')
    || /^components\/(PageHeader|PreviewSection|OperationalSummary|HelpTip|EntityIdentity|Breadcrumbs)\.svelte$/.test(f.rel)
    || f.rel.startsWith('components/viz/'));

  it('scans the product surfaces', () => {
    expect(PRODUCT_SURFACES.length).toBeGreaterThanOrEqual(15);
    expect(STYLED.length).toBeGreaterThan(30);
  });

  it('declares the nine typography roles once, in the shared stylesheet', () => {
    expect([...new Set(ROLE_CLASSES)].sort()).toEqual([
      't-body', 't-body-2', 't-code', 't-label', 't-meta', 't-metric',
      't-page-title', 't-section-title', 't-subsection-title',
    ]);
  });

  it('uses only declared role classes on product surfaces', () => {
    const known = new Set(ROLE_CLASSES);
    const offenders: string[] = [];
    for (const f of PRODUCT_SURFACES) {
      for (const m of f.body.matchAll(/\bt-[a-z0-9-]+\b/g)) {
        if (!known.has(m[0])) offenders.push(`${f.rel}: ${m[0]}`);
      }
    }
    expect(offenders, `unknown typography role class:\n  ${offenders.join('\n  ')}`).toEqual([]);
  });

  it('sets no font-size on a heading selector, so visual role stays independent of level', () => {
    // `h2 { font-size: ... }` inside a component is the private type scale returning.
    // base.css maps each level to its default role once; a component that wants a
    // different look asks for a different ROLE class.
    const HEADING_SIZE = /(^|[\s,{}])h[1-6]\b[^{}]*\{[^{}]*font-size\s*:/m;
    const offenders = PRODUCT_SURFACES
      .filter((f) => HEADING_SIZE.test(f.body))
      .map((f) => f.rel);
    expect(offenders, `hard-coded heading font-size on a product surface: ${offenders.join(', ')}`).toEqual([]);
    // Non-vacuous: the pattern really does match the shape it is looking for.
    expect(HEADING_SIZE.test('.gv-drawer-head h2 { margin: 0; font-size: var(--text-md); }')).toBe(true);
    expect(HEADING_SIZE.test('.rr-head h2 { margin: 0; }')).toBe(false);
  });

  /**
   * Requirement 22: ONE shared visible page-title grammar, in ONE place.
   *
   * Found by the browser acceptance, not by reading source: the Operational graph and
   * Change analysis workspaces named themselves with a bare `<h1>`. base.css paints an
   * h1 at the page-title role, so both LOOKED right -- and both sat outside every check
   * that reasons about roles, which is how they were the only two canonical routes with
   * no measurable page title at all.
   *
   * A role class on each private h1 fixed the size and left the grammar duplicated, so
   * the rule is now the stronger one: a product page does not write a page title, it
   * asks PageHeader for one. That is also what makes the eyebrow, the status badge, the
   * count and the `document.title` mirror arrive with it instead of per page.
   */
  it('takes every product page title from the one shared header', () => {
    const offenders = PRODUCT_SURFACES
      .filter((f) => f.rel !== 'components/PageHeader.svelte' && /<h1\b/.test(f.body))
      .map((f) => f.rel);
    expect(offenders, `a product page declaring its own page title:\n  ${offenders.join('\n  ')}`).toEqual([]);

    const header = readFileSync(join(SRC, 'components/PageHeader.svelte'), 'utf8');
    expect(header, 'the shared header must carry the page-title role').toMatch(/<h1\b[^>]*t-page-title/);

    // Non-vacuous: the product surfaces really are pages with titles -- they just get
    // them from the header now. And the legacy host still writes its own h1s, so this
    // is a scope rather than a claim that the tag has left the repository.
    const usesHeader = PRODUCT_SURFACES.filter((f) => /<PageHeader\b/.test(f.body)).length;
    expect(usesHeader, 'no product surface renders the shared header').toBeGreaterThan(4);
    expect(files.filter((f) => /<h1\b/.test(f.body)).length).toBeGreaterThan(1);
  });

  /**
   * Requirement 17: ONE page scaffold, in ONE place.
   *
   * The Operational graph and Change analysis sat at different distances from the app
   * bar. The cause was not a missing margin on one page: every product view carried a
   * PRIVATE copy of the same shell rule, the copies had drifted to two different gap
   * values, and one view had no shell at all and inherited whatever its first child
   * brought. Eight near-identical rules cannot stay identical.
   *
   * `workspace-geometry.spec.ts` measures the result in the browser, which is the real
   * acceptance. This is the cheap source-level companion: it names the file that opened
   * a ninth copy, in the unit run, before anything is built.
   */
  it('roots every product page on the one shared page scaffold', () => {
    // Pages, not page CONTENTS: `views/entity/*` are the per-kind bodies rendered
    // inside the entity page, and a body that opened its own shell would be a page
    // inside a page.
    const PAGES = PRODUCT_SURFACES.filter((f) => /^views\/[^/]+\.svelte$/.test(f.rel));
    // Comments and `<svelte:*>` bindings render nothing, so they may precede the shell.
    const ROOT = /<\/script>\s*(?:(?:<!--[\s\S]*?-->|<svelte:[^>]*>)\s*)*<div class="product-page">/;
    const offenders = PAGES.filter((f) => !ROOT.test(f.body)).map((f) => f.rel);
    expect(offenders, `a product page with a shell of its own:\n  ${offenders.join('\n  ')}`).toEqual([]);
    expect(PAGES.length, 'no product pages were scanned').toBeGreaterThan(6);

    // The scaffold itself lives in the shared stylesheet, once, and lays the page out.
    // Without this the rule above would be satisfiable by a class nobody styles.
    expect(globalCss, 'the shared page scaffold must be declared once, globally')
      .toMatch(/\.product-page\s*\{[^}]*flex-direction:\s*column[^}]*gap:/);
    expect(PAGES.filter((f) => /\.product-page\s*\{/.test(f.body)).map((f) => f.rel),
      'a product page redeclaring the shared scaffold').toEqual([]);
  });

  /**
   * A role class only wins if nothing outranks it. `.section-title` is the legacy V1
   * uppercase micro-label at --text-sm; put it on the same element as a role class and
   * the cascade picks the legacy rule, so a subsection title rendered a step SMALLER
   * than the body text beneath it while its siblings rendered correctly. The stylesheet
   * was valid, the class list was the bug.
   */
  it('never mixes a legacy V1 class with a typography role on the same element', () => {
    const LEGACY = ['section-title', 'tab-count', 'text-2', 'text-3'];
    const offenders: string[] = [];
    for (const f of PRODUCT_SURFACES) {
      for (const m of f.body.matchAll(/class="([^"]*\bt-[a-z0-9-]+\b[^"]*)"/g)) {
        const classes = m[1].split(/\s+/);
        const clash = classes.filter((c) => LEGACY.includes(c));
        if (clash.length) offenders.push(`${f.rel}: "${m[1]}" (legacy: ${clash.join(', ')})`);
      }
    }
    expect(offenders, `a legacy class competing with a typography role:\n  ${offenders.join('\n  ')}`).toEqual([]);
  });

  /**
   * Requirement 13: the default-open policy is INFORMATIONAL. An active failure is
   * never something a reader has to go looking for, so a section toned as an error
   * cannot also be collapsed shut.
   */
  it('never collapses an error-toned section shut by default', () => {
    const offenders: string[] = [];
    for (const f of PRODUCT_SURFACES) {
      for (const m of f.body.matchAll(/<PreviewSection\b[^>]*>/gs)) {
        const tag = m[0];
        if (!/tone="err"/.test(tag)) continue;
        if (/\bcollapsible\b/.test(tag) && !/open=\{true\}|\bopen\b(?!=)/.test(tag)) {
          offenders.push(`${f.rel}: ${tag.slice(0, 80)}`);
        }
      }
    }
    expect(offenders, `an error-toned section must not default to collapsed:\n  ${offenders.join('\n  ')}`).toEqual([]);
  });

  /**
   * Requirement 14 / 19: hover is supplementary, never the sole access path.
   *
   * `data-tip` renders its text through a CSS `::after { content: attr(data-tip) }`,
   * which means the words exist ONLY while a pointer hovers: they are not in the
   * accessibility tree, the host is usually not focusable, and the shared rule hides
   * them outright under `@media (hover: none)`. HelpTip is the product's answer -- a
   * real button, reachable by Tab and by touch, dismissed by Escape.
   *
   * Legacy `pacto doc` surfaces keep `data-tip`; they are a different host and out of
   * scope, which is also what keeps this guard honest rather than vacuous.
   */
  it('carries no hover-only definition on a product surface', () => {
    const offenders = PRODUCT_SURFACES.filter((f) => f.body.includes('data-tip')).map((f) => f.rel);
    expect(offenders, `hover-only data-tip on a product surface: ${offenders.join(', ')}`).toEqual([]);
    // Non-vacuous: the legacy host still uses data-tip, so the rule is a scope, not a
    // statement that the attribute has disappeared from the repository.
    const legacy = files.filter((f) => f.body.includes('data-tip')).map((f) => f.rel);
    expect(legacy.length, 'the guard would be vacuous if data-tip existed nowhere').toBeGreaterThan(0);
  });

  it('keeps the help affordance keyboard-, screen-reader- and touch-operable', () => {
    const tip = readFileSync(join(SRC, 'components/HelpTip.svelte'), 'utf8');
    expect(tip, 'the affordance must be a real button, not a hover target').toMatch(/<button\b/);
    expect(tip, 'it must have an accessible name').toMatch(/aria-label=/);
    expect(tip, 'its open state must be exposed').toMatch(/aria-expanded=/);
    expect(tip, 'the text must be associated with the control, not just painted').toMatch(/aria-describedby=/);
    expect(tip, 'Escape must close it').toMatch(/'Escape'/);
    expect(tip, 'it must open on focus, not only on hover').toMatch(/onfocus=/);
  });
});

/**
 * Product vocabulary. "Fleet" is an internal word -- the snapshot package, the /fleet
 * routes, the host capability flag -- and it kept leaking into the words on screen
 * ("Fleet posture" above a page about services, "how this fleet knows"). The words a
 * product surface renders are services, revisions, operational targets and data
 * sources; the internal name is allowed anywhere it is a route, an identifier or a
 * comment, and nowhere a reader can see it.
 */
describe('product vocabulary', () => {
  const PRODUCT_UI = files.filter((f) => f.rel.endsWith('.svelte')).filter((f) =>
    /^views\/(Fleet[^/]*|ChangeAnalysisView|GraphView)\.svelte$/.test(f.rel)
    || f.rel.startsWith('views/entity/')
    || /^components\/(viz\/.*|OperationalSummary|Navbar|Breadcrumbs|EntitySearch)\.svelte$/.test(f.rel));

  /**
   * The words a reader can actually see, taken from the Svelte compiler's own parse of
   * the component rather than by deleting things from its text.
   *
   * That distinction is the point. This used to strip the script block, the stylesheet,
   * the comments and the class attributes with a chain of regex replacements -- which
   * is exactly the shape of a hand-rolled HTML sanitizer, and was read as one. It never
   * was one: it is source analysis, in a test, over files on disk. So it now says so
   * structurally. Nothing is removed. The parser already hands back the script, the
   * stylesheet and the template as separate things, so this walks the template alone,
   * and within it only the nodes that reach the screen: text, and the human-readable
   * literals in attributes and template expressions beside it.
   */
  const readerVisibleText = (source: string): string => {
    const out: string[] = [];
    const visit = (node: unknown): void => {
      if (Array.isArray(node)) { node.forEach(visit); return; }
      if (!node || typeof node !== 'object') return;
      const n = node as { type?: string; data?: string; name?: string; value?: unknown };
      switch (n.type) {
        // A note to the next developer, and a hook for the stylesheet. Neither is read
        // by anyone using the product.
        case 'Comment':
        case 'ClassDirective':
          return;
        case 'Text':
          out.push(String(n.data ?? ''));
          return;
        case 'Attribute':
          if (n.name === 'class') return;
          break;
        case 'Literal': // a string built inside a template expression
          if (typeof n.value === 'string') out.push(n.value);
          return;
        case 'TemplateElement':
          out.push(String((n.value as { cooked?: string } | undefined)?.cooked ?? ''));
          return;
      }
      for (const v of Object.values(n)) if (v && typeof v === 'object') visit(v);
    };
    visit(parse(source, { modern: true }).fragment);
    return out.join('\n');
  };
  // A route, an identifier or a view id -- never a word a reader sees.
  const INTERNAL = /fleet[A-Z][A-Za-z]*|[/'"#]fleet|fleet-[a-z]/g;

  it('scans the product surfaces', () => {
    expect(PRODUCT_UI.length).toBeGreaterThanOrEqual(12);
  });

  it('reads the words a reader sees, and not the ones only a developer does', () => {
    // The guard is only worth its green tick if the extraction underneath it is right,
    // so it is checked against a component that puts the internal name in all six
    // places at once -- three a reader meets, three they never do.
    const sample = [
      "<script>const fleetLoader = 'fleet inside the script';</script>",
      '<!-- fleet inside a comment -->',
      '<div class="fleet-card" class:fleet-active={true} title="fleet inside a title">',
      "  fleet inside the text {'fleet inside an expression'}",
      '</div>',
      '<style>.fleet-card { color: red; }</style>',
    ].join('\n');
    const visible = readerVisibleText(sample);
    expect(visible).toContain('fleet inside the text');
    expect(visible).toContain('fleet inside a title');
    expect(visible).toContain('fleet inside an expression');
    expect(visible).not.toContain('inside the script');
    expect(visible).not.toContain('inside a comment');
    expect(visible).not.toContain('fleet-card');
    expect(visible).not.toContain('fleet-active');
  });

  it('renders the internal name nowhere a reader can see it', () => {
    const offenders = PRODUCT_UI
      .filter((f) => /\bfleets?\b/i.test(readerVisibleText(f.body).replace(INTERNAL, '')))
      .map((f) => f.rel);
    expect(offenders, `internal "fleet" wording rendered on a product surface: ${offenders.join(', ')}`).toEqual([]);
  });

  it('proves the guard is not vacuous: the internal name is still used as a route', () => {
    const routed = PRODUCT_UI.filter((f) => INTERNAL.test(f.body)).map((f) => f.rel);
    expect(routed.length).toBeGreaterThan(0);
  });
});

/**
 * In-page navigation guard (requirements 15 and 16).
 *
 * A long page needs a way to reach its own sections, and there are two ways to build
 * one. Only one of them is available here.
 *
 * The obvious way -- `<a href="#operational-summary">` -- is what the platform is for,
 * and it is exactly wrong in this application: the URL fragment ALREADY carries the
 * route. A jump link would overwrite `#/fleet/services/x` with `#operational-summary`,
 * so following it would navigate away from the page it was trying to scroll, and Back
 * would then walk through jump targets instead of pages. So in-page navigation is a
 * button that scrolls, and the fragment keeps its single meaning.
 *
 * The second rule is what keeps the navigator honest. Its entries are DISCOVERED from
 * the rendered DOM, never hand-listed beside a page: a page that hand-lists its own
 * contents offers sections the data did not produce as soon as the two drift, which is
 * a worse failure than having no contents list at all.
 */
describe('product in-page navigation', () => {
  // The product host, plus the shared components it is built from. The legacy `pacto
  // doc` surfaces (src/sections/**, ServiceDetailView) are a different host with their
  // own contents list and their own navigation, and stay out of scope.
  const PRODUCT_UI = files.filter((f) => f.rel.endsWith('.svelte')).filter((f) =>
    /^views\/(Fleet[^/]*|ChangeAnalysisView|GraphView)\.svelte$/.test(f.rel)
    || f.rel.startsWith('views/entity/')
    || f.rel.startsWith('components/'));

  // A source scan reads comments too, and both rules below are about what the TEMPLATE
  // does -- a comment explaining why an `href="#anchor"` is forbidden must not read as
  // one. Comments are removed before matching, never from the file.
  const code = (s: string) => s
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/^\s*\/\/.*$/gm, '');

  it('scans the product surfaces', () => {
    expect(PRODUCT_UI.length).toBeGreaterThanOrEqual(15);
  });

  it('spends the URL fragment on routes only, never on a jump target', () => {
    // Any literal fragment href must be a ROUTE (`#/...`). `#` alone and `#anchor` are
    // both the second-meaning bug; the router owns everything after the hash.
    const JUMP = /href\s*=\s*(?:"#(?!\/)|'#(?!\/)|\{`#(?!\/))/;
    const offenders = PRODUCT_UI.filter((f) => JUMP.test(code(f.body))).map((f) => f.rel);
    expect(offenders, `an in-page fragment link would collide with the hash route: ${offenders.join(', ')}`).toEqual([]);
    // Non-vacuous, in both directions: the pattern catches the shape it names, it does
    // NOT catch the route form, and the product really is full of links -- they are just
    // built by the router's URL helpers rather than written as literals, which is the
    // other half of why a raw fragment href stands out as a mistake.
    expect(JUMP.test('<a href="#operational-summary">')).toBe(true);
    expect(JUMP.test('<a href="#">')).toBe(true);
    expect(JUMP.test('<a href="#/fleet/services">')).toBe(false);
    expect(PRODUCT_UI.filter((f) => /href=\{/.test(f.body)).length).toBeGreaterThan(3);
    expect(readFileSync(join(SRC, 'lib/router.ts'), 'utf8')).toMatch(/'#\//);
  });

  it('has exactly one "On this page" navigator, and it scrolls rather than links', () => {
    const toc = readFileSync(join(SRC, 'components/PageToc.svelte'), 'utf8');
    expect(toc, 'entries must be buttons').toMatch(/<button\b[^>]*class="toc-link"/);
    expect(toc, 'it must name itself for assistive tech').toMatch(/aria-label=\{label\}/);
    const others = PRODUCT_UI.filter((f) => f.rel !== 'components/PageToc.svelte' && /On this page/.test(code(f.body)));
    expect(others.map((f) => f.rel), 'a second contents list has appeared').toEqual([]);
  });

  it('discovers its entries from the rendered page, never from a hand-written list', () => {
    const toc = readFileSync(join(SRC, 'components/PageToc.svelte'), 'utf8');
    expect(toc, 'entries come from the DOM').toMatch(/querySelectorAll\('\[data-toc\]\[id\]'\)/);
    expect(toc, 'and are kept in step with it').toMatch(/new MutationObserver\(/);
  });

  it('tags sections through the shared section grammar, so a page cannot forget to', () => {
    // PreviewSection is every titled block on a product page, so tagging it there is
    // what makes the navigator complete without each page opting in section by section.
    const ps = readFileSync(join(SRC, 'components/PreviewSection.svelte'), 'utf8');
    expect(ps, 'only top-level sections are page-outline entries').toMatch(/level === 2 && title/);
    expect(ps).toMatch(/'data-toc': title/);
    // The hand-written sections that are not PreviewSections must be tagged too, or the
    // contents would silently skip them.
    const tagged = PRODUCT_UI.filter((f) => /data-toc="/.test(f.body)).map((f) => f.rel);
    expect(tagged.length, 'no page contributes a section to the navigator').toBeGreaterThan(3);
  });
});
