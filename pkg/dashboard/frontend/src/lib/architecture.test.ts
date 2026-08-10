import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync } from 'node:fs';
import { join, relative } from 'node:path';

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
