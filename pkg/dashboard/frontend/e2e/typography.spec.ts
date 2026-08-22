import { test, expect } from '@playwright/test';
import {
  boot, canonicalKeys, runAnalysis, sampleRoles, normalBody,
  assertPageHierarchy, assertRoleCoherence, type RoleSample,
} from './typographyChecks';

/**
 * Desktop typography acceptance in real Chromium, from COMPUTED styles.
 *
 * The unit guards in `architecture.test.ts` read source: they can prove no component
 * references an undeclared token and no heading selector sets a size. They cannot prove
 * what PAINTED. Two failure modes get through them, and this spec caught both:
 *
 *   - an undeclared `var()` is not a parse error, it is invalid-at-computed-value-time,
 *     so the property silently falls back to `inherit`;
 *   - a role class can simply LOSE the cascade to a legacy class on the same element,
 *     which is how one subsection title rendered as a 13px uppercase micro-label while
 *     its siblings rendered at 14px sentence case.
 *
 * See typographyChecks.ts for why this measures relationships and never absolute pixels.
 */

// One WASM boot per route; this is a budget for a multi-route walk, not a latency
// assertion.
const SWEEP_TIMEOUT = 300_000;

test.describe('typography hierarchy on desktop', () => {
  test('every canonical route, measured in the browser', async ({ page }) => {
    test.setTimeout(SWEEP_TIMEOUT);
    const k = await canonicalKeys(page);
    const e = encodeURIComponent;

    const routes: Array<[string, string]> = [
      ['Overview', '#/fleet'],
      ['Services list', '#/fleet/services'],
      ['Service detail', `#/fleet/services/${e(k.service)}`],
      ['Revision detail', `#/fleet/revisions/${e(k.revision)}`],
      ['Target detail', `#/fleet/targets/${e(k.target)}`],
      ['Owners list', '#/fleet/owners'],
      ['Data sources list', '#/fleet/sources'],
      ['Needs attention', '#/fleet/attention'],
      ['Graph discovery', '#/fleet/graph'],
      ['Graph focused', `#/fleet/graph/service/${e(k.service)}`],
      ['Change analysis', `#/fleet/changes/${e(k.service)}`],
    ];

    const all: RoleSample[] = [];
    for (const [label, hash] of routes) {
      await boot(page, hash);
      if (label === 'Change analysis') await runAnalysis(page);
      const s = await sampleRoles(page, label);
      assertPageHierarchy(s, label, await normalBody(page));
      all.push(...s);
    }
    assertRoleCoherence(all, 'desktop');
  });

  test('a section title is the same size whether it is an h2 or an h3', async ({ page }) => {
    test.setTimeout(SWEEP_TIMEOUT);
    // The narrow case, isolated so its failure message says what
    // broke rather than "one role, two sizes" from the sweep.
    //
    // The two genuinely coexist across the product: a Service page's sections are h2s
    // directly under the page title, and the Change analysis stages are h3s -- nested
    // one level deeper in the outline, because the workspace has a heading between them
    // and the page, but peers of a Service section in the reading hierarchy. Under a
    // tag-driven scale those two facts contradicted each other and the tag won.
    const k = await canonicalKeys(page);
    const e = encodeURIComponent;

    const read = () => page.evaluate(() => {
      const out: Record<string, { size: string; weight: string; text: string }> = {};
      for (const el of Array.from(document.querySelectorAll('main .t-section-title'))) {
        if (el.getClientRects().length === 0) continue;
        const cs = getComputedStyle(el);
        out[el.tagName] ??= {
          size: cs.fontSize, weight: cs.fontWeight,
          text: (el.textContent || '').replace(/\s+/g, ' ').trim().slice(0, 40),
        };
      }
      return out;
    });

    await boot(page, `#/fleet/services/${e(k.service)}`);
    const onService = await read();
    await boot(page, `#/fleet/changes/${e(k.service)}`);
    await runAnalysis(page);
    const onChanges = await read();

    const seen = { ...onService, ...onChanges };
    expect(Object.keys(seen).length, `expected the section role on at least two element types, saw ${JSON.stringify(seen)}`)
      .toBeGreaterThan(1);
    expect(onService.H2, `the Service page rendered no h2 in the section role: ${JSON.stringify(onService)}`).toBeDefined();
    expect(onChanges.H3, `Change analysis rendered no h3 in the section role: ${JSON.stringify(onChanges)}`).toBeDefined();

    expect(onChanges.H3.size, `h3 "${onChanges.H3.text}" vs h2 "${onService.H2.text}"`).toBe(onService.H2.size);
    expect(onChanges.H3.weight, `h3 "${onChanges.H3.text}" vs h2 "${onService.H2.text}"`).toBe(onService.H2.weight);
  });

  test('the page title dominates on the densest entity page', async ({ page }) => {
    test.setTimeout(SWEEP_TIMEOUT);
    // Requirement 9's actual complaint: on a Revision page the contract inspector's
    // section headings visually competed with the name of the page itself, so nothing
    // said "you are here". Measured against the LARGEST section title on the page, not
    // the first one found.
    const k = await canonicalKeys(page);
    await boot(page, `#/fleet/revisions/${encodeURIComponent(k.revision)}`);
    const s = await sampleRoles(page, 'Revision detail');

    const title = s.filter((x) => x.role === 't-page-title');
    expect(title).toHaveLength(1);
    const sections = s.filter((x) => x.role === 't-section-title');
    expect(sections.length, 'the revision page rendered no section titles to compare against')
      .toBeGreaterThan(2);

    const biggest = Math.max(...sections.map((x) => x.size));
    expect(title[0].size, `page title ${title[0].size}px vs largest section ${biggest}px`).toBeGreaterThan(biggest);
    // Dominance is not one pixel. A 5% edge is invisible; the ramp is a real step.
    expect(title[0].size / biggest).toBeGreaterThan(1.2);
  });
});
