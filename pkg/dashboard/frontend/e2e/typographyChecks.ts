import { expect, type Page } from '@playwright/test';

/*
 * NOTE ON THE FILENAME: this is `typographyChecks.ts`, not `*.spec.ts`, so Playwright's
 * default testMatch does not collect it. It is a helper imported by the desktop spec and
 * by the mobile spec, which run under two different projects and therefore cannot share
 * a file.
 */

/**
 * Shared typography acceptance, measured from COMPUTED styles in a real browser
 * (requirement 20).
 *
 * A source scan cannot prove this. The bug that started the pass was
 * `font-size: var(--text-md)` against a token nobody had declared: perfectly readable
 * source, and an invalid declaration at computed-value time, so the element INHERITED
 * a size instead of taking one. Only the browser knows what actually painted. The
 * mirror-image bug was the opposite -- a valid declaration in the wrong place, a chart
 * title hard-coding the section size onto a level-3 heading, so a subsection rendered
 * larger than the section containing it.
 *
 * So this measures relationships and role coherence, never absolute pixels. Asserting
 * "the page title is 24.38px" would break on the next deliberate scale change and prove
 * nothing about hierarchy; asserting "the page title is larger than every section title
 * on the page" is the actual product claim.
 */

/** The nine visual roles. A tenth would be a design decision, not a stylesheet edit. */
export const ROLES = [
  't-page-title', 't-section-title', 't-subsection-title',
  't-body', 't-body-2', 't-label', 't-meta', 't-metric', 't-code',
] as const;

export interface RoleSample {
  route: string;
  role: string;
  tag: string;
  size: number;
  weight: number;
  text: string;
}

/**
 * NORMAL BODY is the text that carries no role class at all -- most of the page. It is
 * measured from `main`'s own computed size rather than from a `.t-body` element, because
 * `.t-body` is an OVERRIDE for the rare place that needs to restate the default, and
 * pinning the hierarchy to it would only prove that the override exists. The requirement
 * asks that the page title beat normal body text; this is normal body text.
 */
export async function normalBody(page: Page): Promise<number> {
  return page.evaluate(() => {
    const m = document.querySelector('main');
    return m ? parseFloat(getComputedStyle(m).fontSize) : NaN;
  });
}

/**
 * sampleRoles reads every PAINTED role-classed element in `main`.
 *
 * "Painted" is `getClientRects().length > 0`, which is what excludes content inside a
 * closed `<details>`. That is deliberate: a collapsed section's title is not something
 * the reader is comparing sizes against, and including it would make the measurement
 * depend on which disclosures happen to default open.
 */
export async function sampleRoles(page: Page, route: string): Promise<RoleSample[]> {
  const raw = await page.evaluate((roles) => {
    const main = document.querySelector('main');
    if (!main) return [];
    const out: Array<Omit<RoleSample, 'route'>> = [];
    for (const role of roles) {
      for (const el of Array.from(main.querySelectorAll(`.${role}`))) {
        if (el.getClientRects().length === 0) continue;
        const cs = getComputedStyle(el);
        out.push({
          role,
          tag: el.tagName,
          size: parseFloat(cs.fontSize),
          weight: Number(cs.fontWeight),
          text: (el.textContent || '').replace(/\s+/g, ' ').trim().slice(0, 48),
        });
      }
    }
    return out;
  }, ROLES as unknown as string[]);
  return raw.map((r) => ({ ...r, route }));
}

let bootSeq = 0;

/**
 * boot enters `hash` as a real DEEP LINK -- fresh document, engine boot, first paint of
 * that route.
 *
 * The cache-busting query is load-bearing for the same reason it is in headings.spec.ts:
 * a goto that differs only in its fragment is a same-document navigation, so the previous
 * route's DOM survives and every measurement below it would be taken against a page we
 * already measured.
 */
export async function boot(page: Page, hash: string): Promise<void> {
  await page.goto(`/index.html?boot=${++bootSeq}${hash}`);
  await page.waitForFunction(() => !document.body.textContent?.includes('Loading Pacto'), null, { timeout: 60_000 });
  await expect(page.locator('main h1')).toBeVisible({ timeout: 30_000 });
  await settle(page);
}

/**
 * settle waits for the page to stop growing.
 *
 * An entity route paints its h1 from the URL before its data lands, so "the h1 is
 * visible" is true a beat before the sections exist. Measuring there does not fail --
 * it passes, having compared one heading against nothing. So this waits for the count
 * of role-classed elements to hold steady rather than for a fixed sleep, which is both
 * faster on the quick routes and honest on the slow ones.
 *
 * Zero is never settled. Under a parallel run every worker boots its own wasm engine,
 * and a route whose data has not landed within one sample interval reads 0 twice in a
 * row -- "stable", and empty. That measured nothing and passed, which is the failure
 * mode this whole file exists to prevent, so an empty page keeps waiting for the full
 * budget instead.
 *
 * Nor is a page that is still LOADING settled, even though it is not empty. A product
 * page paints its header before its data lands and the loading shell underneath carries
 * role classes of its own, so the count reaches a small stable number with not one
 * section title on screen. Under a parallel run that is exactly what a slow worker sees,
 * and a sweep measuring it compares the page title against nothing. `.state-box` is
 * ProductEmptyState -- the one element every not-ready view state renders -- so its
 * presence means the page has not finished becoming itself yet.
 */
async function settle(page: Page): Promise<void> {
  const sample = () => page.evaluate((roles: string[]) => ({
    n: document.querySelectorAll(roles.map((r) => `main .${r}`).join(',')).length,
    pending: !!document.querySelector('main .state-box'),
  }), ROLES as unknown as string[]);
  let prev = -1;
  for (let i = 0; i < 120; i++) {
    const { n, pending } = await sample();
    if (n > 0 && n === prev && !pending) return;
    prev = n;
    await page.waitForTimeout(250);
  }
}

/** Entity keys DISCOVERED from the Product API, so the sweep follows the demo fixture. */
export async function canonicalKeys(page: Page): Promise<Record<string, string>> {
  await boot(page, '#/fleet');
  return page.evaluate(async () => {
    const first = async (kind: string) => {
      const r = await (await fetch(`/api/fleet/entities?kinds=${kind}&limit=1`)).json();
      return (r.entities || [])[0]?.key || '';
    };
    return {
      service: 'payments-service',
      revision: await first('revision'),
      target: await first('target'),
    };
  });
}

/**
 * runAnalysis drives the Change analysis workspace to its RESULT state.
 *
 * Scoped to a service, the route opens on a revision picker: an honest idle state, and
 * one with almost no typography in it. Measuring there would have audited a form and
 * called the analysis covered -- the stage headings, the charts, the consumers table and
 * the incomplete-evidence section only exist after the comparison runs.
 */
export async function runAnalysis(page: Page): Promise<void> {
  const compare = page.getByRole('button', { name: /Compare revisions/i });
  await expect(compare).toBeEnabled({ timeout: 30_000 });
  await compare.click();
  await expect(page.getByRole('heading', { name: /Affected consumers/i })).toBeVisible({ timeout: 60_000 });
  await settle(page);
}

const of = (s: RoleSample[], role: string) => s.filter((x) => x.role === role);
const sizes = (s: RoleSample[]) => [...new Set(s.map((x) => x.size))].sort((a, b) => a - b);
const weights = (s: RoleSample[]) => [...new Set(s.map((x) => x.weight))].sort((a, b) => a - b);
const show = (s: RoleSample[]) =>
  s.map((x) => `${x.route} ${x.tag}.${x.role} ${x.size}px/${x.weight} "${x.text}"`).join('\n  ');

/**
 * assertPageHierarchy is the per-route claim: the page names itself once, and that name
 * dominates everything under it.
 */
export function assertPageHierarchy(samples: RoleSample[], label: string, body: number): void {
  const title = of(samples, 't-page-title');
  expect(title.length, `${label}: expected exactly one visible page title, got:\n  ${show(title)}`).toBe(1);
  const pt = title[0].size;

  // Requirement 20's first claim, on every route: page title > section title > normal
  // body / meta.
  expect(pt, `${label}: page title (${pt}px) must be larger than normal body text (${body}px)`).toBeGreaterThan(body);
  for (const role of ['t-section-title', 't-subsection-title', 't-body', 't-body-2', 't-meta', 't-label']) {
    for (const s of of(samples, role)) {
      expect(pt, `${label}: page title (${pt}px) must be larger than ${s.tag}.${role} (${s.size}px) "${s.text}"`)
        .toBeGreaterThan(s.size);
    }
  }

  // A section title outranks ordinary reading text and the small print.
  for (const sec of of(samples, 't-section-title')) {
    expect(sec.size, `${label}: section title "${sec.text}" (${sec.size}px) must be larger than normal body text (${body}px)`)
      .toBeGreaterThan(body);
    for (const s of [...of(samples, 't-meta'), ...of(samples, 't-label')]) {
      expect(sec.size, `${label}: section title "${sec.text}" (${sec.size}px) must be larger than ${s.tag}.${s.role} (${s.size}px)`)
        .toBeGreaterThan(s.size);
    }
  }

  // A subsection sits UNDER its parent section. Equal size would be a flat page.
  for (const sub of of(samples, 't-subsection-title')) {
    for (const sec of of(samples, 't-section-title')) {
      expect(sub.size, `${label}: subsection "${sub.text}" (${sub.size}px) must be smaller than section "${sec.text}" (${sec.size}px)`)
        .toBeLessThan(sec.size);
    }
    // ...but still reads as a title, not as body copy. This is the half of the hierarchy
    // that survives when a subsection lands at the body SIZE: weight has to carry it.
    expect(sub.weight, `${label}: subsection "${sub.text}" must be heavier than body text`).toBeGreaterThan(400);
    expect(sub.size, `${label}: subsection "${sub.text}" (${sub.size}px) dropped below the body text (${body}px)`)
      .toBeGreaterThanOrEqual(body);
  }
}

/**
 * assertRoleCoherence is the cross-route claim, and the one the requirement is really
 * about: the same visual role renders identically wherever it appears, WHATEVER element
 * carries it. An `h2` and an `h3` and a `<p>` in the section role are the same size and
 * the same weight, because the role decides and the tag does not.
 */
export function assertRoleCoherence(all: RoleSample[], label: string): void {
  expect(all.length, `${label}: nothing was measured`).toBeGreaterThan(50);

  for (const role of ROLES) {
    const s = of(all, role);
    if (s.length === 0) continue;
    expect(sizes(s), `${label}: ${role} rendered at more than one size:\n  ${show(s)}`).toHaveLength(1);
    expect(weights(s), `${label}: ${role} rendered at more than one weight:\n  ${show(s)}`).toHaveLength(1);
  }

  // The rule is only worth its green tick if a role is genuinely carried by more than
  // one kind of element somewhere. Otherwise "role independent of tag" is untested.
  const spread = ROLES.map((r) => ({ role: r, tags: new Set(of(all, r).map((x) => x.tag)) }))
    .filter((x) => x.tags.size > 1);
  expect(
    spread.length,
    `no role is carried by more than one element type, so tag-independence is unproven:\n  ${
      ROLES.map((r) => `${r}: ${[...new Set(of(all, r).map((x) => x.tag))].join(',')}`).join('\n  ')}`,
  ).toBeGreaterThan(0);

  // The section role specifically: it is the one that used to be decided by tag, and
  // the one where the regression is invisible (an h3 forced up to the h2 size looks
  // right on the page it was tuned on and wrong on every other).
  const sec = of(all, 't-section-title');
  expect([...new Set(sec.map((x) => x.tag))].length,
    `the section role is only ever on ${[...new Set(sec.map((x) => x.tag))].join(',')}, so it proves nothing about tag-independence`)
    .toBeGreaterThan(1);

  // Global ordering across the whole sweep. Roles absent from the product are skipped
  // rather than asserted against NaN -- `t-body` and `t-code` are overrides for text
  // that already paints correctly by default, so a route sweep legitimately never sees
  // them, and a silent NaN comparison would have made this whole block vacuous.
  const ramp = ['t-page-title', 't-section-title', 't-subsection-title', 't-meta']
    .map((r) => ({ role: r, size: of(all, r)[0]?.size }))
    .filter((x): x is { role: string; size: number } => x.size !== undefined);
  expect(ramp.length, `${label}: too few roles present to check the ramp`).toBeGreaterThan(2);
  for (let i = 1; i < ramp.length; i++) {
    expect(ramp[i - 1].size, `${label}: ${ramp[i - 1].role} (${ramp[i - 1].size}px) must be larger than ${ramp[i].role} (${ramp[i].size}px)`)
      .toBeGreaterThan(ramp[i].size);
  }
}
