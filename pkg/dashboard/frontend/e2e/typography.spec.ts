import { test, expect } from '@playwright/test';
import {
  boot, canonicalKeys, runAnalysis, sampleRoles, normalBody,
  assertPageHierarchy, assertRoleCoherence, type RoleSample,
} from './typographyChecks';
import { DOC_ORIGIN } from './ports';

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

  test('a section title is the same size whether it is an h2 or a p', async ({ page }) => {
    test.setTimeout(SWEEP_TIMEOUT);
    // The narrow case, isolated so its failure message says what
    // broke rather than "one role, two sizes" from the sweep.
    //
    // The two genuinely coexist across the product: a Service page's sections are h2s
    // directly under the page title, and the graph's empty discovery state titles its
    // placeholder with a p, which is no heading in the outline but reads as a section
    // title. Under a tag-driven scale those two facts contradicted each other and the
    // tag won.
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
    await boot(page, '#/fleet/graph');
    const onDiscovery = await read();

    expect(onService.H2, `the Service page rendered no h2 in the section role: ${JSON.stringify(onService)}`).toBeDefined();
    expect(onDiscovery.P, `graph discovery rendered no p in the section role: ${JSON.stringify(onDiscovery)}`).toBeDefined();

    expect(onDiscovery.P.size, `p "${onDiscovery.P.text}" vs h2 "${onService.H2.text}"`).toBe(onService.H2.size);
    expect(onDiscovery.P.weight, `p "${onDiscovery.P.text}" vs h2 "${onService.H2.text}"`).toBe(onService.H2.weight);
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

  // The identity (signed document): serif titles over a double rule, italic subsections,
  // serif small-caps labels. It lives in the type tokens, so this reads what PAINTED: a
  // private `text-transform: uppercase` is exactly how the first try at small-caps labels
  // lost to forty component rules and still rendered capitals.
  test('the signed-document identity paints on every kind of page', async ({ page }) => {
    test.setTimeout(SWEEP_TIMEOUT);
    const k = await canonicalKeys(page);
    const e = encodeURIComponent;
    for (const hash of ['#/fleet', `#/fleet/services/${e(k.service)}`, '#/fleet/attention', '#/fleet/owners']) {
      await boot(page, hash);
      const got = await page.evaluate(async () => {
        await document.fonts.ready;
        const cs = (sel: string) => {
          const el = Array.from(document.querySelectorAll(sel)).find((x) => x.getClientRects().length > 0);
          return el ? getComputedStyle(el) : null;
        };
        const title = cs('main h1');
        const sub = cs('main .t-subsection-title');
        const label = cs('main .t-label, main th');
        const upper = Array.from(document.querySelectorAll('body *'))
          .filter((x) => x.getClientRects().length > 0 && getComputedStyle(x).textTransform === 'uppercase' && !x.closest('.rt-method'))
          .map((x) => `${x.tagName}.${x.className}`).slice(0, 5);
        return {
          loaded: document.fonts.check('700 16px "Source Serif 4"') && Array.from(document.fonts).some((f) => f.family.includes('Source Serif 4') && f.status === 'loaded'),
          titleFamily: title?.fontFamily ?? '', titleRule: title?.borderBottomStyle ?? '',
          subStyle: sub ? sub.fontStyle : 'italic', labelCaps: label ? label.fontVariantCaps : 'all-small-caps',
          upper,
        };
      });
      expect(got.loaded, `${hash}: the bundled Source Serif 4 never loaded`).toBe(true);
      expect(got.titleFamily, `${hash}: page title family`).toMatch(/^"?Source Serif 4/);
      expect(got.titleRule, `${hash}: page title rule`).toBe('double');
      expect(got.subStyle, `${hash}: subsection style`).toBe('italic');
      expect(got.labelCaps, `${hash}: label caps`).toBe('all-small-caps');
      expect(got.upper, `${hash}: uppercase text outside the label role`).toEqual([]);
    }
  });

  // The legacy views have one host, the offline `pacto doc` export (fleet:false), and
  // they write bare <h1>s and `.section-title` headings. Option A applies there too: the
  // double rule under the h1 and a serif section title, not a small-caps label.
  test('the signed-document identity paints on the legacy non-Fleet host', async ({ page }) => {
    test.setTimeout(SWEEP_TIMEOUT);
    const go = async (hash: string) => {
      await page.goto(`${DOC_ORIGIN}/index.html${hash}`);
      await expect(page.locator('main h1')).toBeVisible({ timeout: 60_000 });
    };
    await go('#/services');
    const name = await page.evaluate(
      () => (globalThis as { __PACTO_STATIC__?: { service?: string } }).__PACTO_STATIC__?.service || '',
    );
    expect(name, 'the offline export did not name its service').not.toBe('');
    await go(`#/services/${encodeURIComponent(name)}`);
    await expect(page.locator('main .section-title').first()).toBeVisible({ timeout: 30_000 });
    const got = await page.evaluate(async () => {
      await document.fonts.ready;
      const h1 = getComputedStyle(document.querySelector('main h1')!);
      const st = Array.from(document.querySelectorAll('main .section-title')).find((x) => x.getClientRects().length > 0)!;
      const probe = document.createElement('h2');
      probe.className = 't-section-title';
      document.querySelector('main')!.appendChild(probe);
      const want = getComputedStyle(probe).fontSize;
      probe.remove();
      const cs = getComputedStyle(st);
      return {
        rule: h1.borderBottomStyle, family: cs.fontFamily, size: cs.fontSize, want,
        caps: cs.fontVariantCaps, weight: cs.fontWeight,
      };
    });
    expect(got.rule, 'legacy page title rule').toBe('double');
    expect(got.family, 'legacy section title family').toMatch(/^"?Source Serif 4/);
    expect(got.size, 'legacy section title size').toBe(got.want);
    expect(got.caps, 'legacy section title is not a small-caps label').toBe('normal');
    expect(got.weight).toBe('700');
    // The rule marks the page title only. A doc that opens with `# ...` renders its h1
    // inside <main> too, and must not draw a second page-title rule in the doc card.
    await page.getByRole('heading', { level: 2, name: /^Documentation/ }).getByRole('button').click();
    await page.locator('main .detail-card-header-btn', { hasText: 'overview.md' }).first().click();
    const docH1 = page.locator('main .markdown-body h1').first();
    await expect(docH1).toBeVisible({ timeout: 30_000 });
    const doc = await docH1.evaluate((el) => {
      const cs = getComputedStyle(el);
      return { rule: cs.borderBottomStyle, pad: cs.paddingBottom };
    });
    expect(doc.rule, 'a markdown h1 draws the page-title rule').toBe('none');
    expect(doc.pad).toBe('0px');
  });
});
