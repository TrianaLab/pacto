import { expect, test, type Page } from '@playwright/test';

// The same Level 6 gate as mermaid.spec.ts, pointed at the other thing only a real
// browser can see: whether the site can be *operated* from the keyboard. Neither
// `mkdocs build --strict` nor any Markdown check can observe focus, and both of
// the behaviours below failed silently — the page looked right in a screenshot
// while a keyboard reader was stranded.
//
// Two pages, because they differ in the way that matters: the home page uses the
// hero template from overrides/ and its first heading is far down the document,
// so its skip link has real distance to cover; every other page uses the stock
// Material template and skips barely a screen.
const PAGES = ['/', '/quickstart/'];

/** Where the keyboard is, described well enough to name in a failure message. */
async function focused(page: Page) {
  return page.evaluate(() => {
    const el = document.activeElement;
    if (!el || el === document.body) return null;
    const style = getComputedStyle(el);
    const box = el.getBoundingClientRect();
    return {
      tag: el.tagName.toLowerCase(),
      id: el.id,
      className: typeof el.className === 'string' ? el.className : '',
      text: (el.textContent ?? '').replace(/\s+/g, ' ').trim().slice(0, 40),
      // Answered here, in the same read as everything else. Asking a second
      // page.evaluate() where the focus is lets it move between the two, which
      // produced a failure whose message named an element that satisfied the
      // assertion it had just failed.
      inContent: !!el.closest('.md-content'),
      // A ring drawn on a zero-sized element is not a ring anyone can see, so the
      // element's own box is part of the question, not separate from it.
      ring: style.outlineStyle !== 'none' && parseFloat(style.outlineWidth) > 0,
      width: Math.round(box.width),
      height: Math.round(box.height),
    };
  });
}

for (const path of PAGES) {
  test(`the skip link moves focus, not just the viewport (${path})`, async ({ page }) => {
    await page.goto(path);

    // Tab once: Material's skip link is deliberately the first stop on the page.
    await page.keyboard.press('Tab');
    const skip = await focused(page);
    expect(skip?.className, 'the first tab stop is the skip link').toContain('md-skip');

    const href = await page.locator('.md-skip').getAttribute('href');
    const targetId = decodeURIComponent(new URL(href!, page.url()).hash.slice(1));
    expect(targetId, 'the skip link names a target').not.toBe('');

    await page.keyboard.press('Enter');

    // The regression this exists for: the browser scrolls to a heading, headings
    // are not focusable, focus stays on <body>, and the next Tab restarts at the
    // top — so "skip to content" returns the reader to the header they skipped.
    // Asserting on scrollY would have passed throughout. docs/javascripts/skip-link.js.
    const landed = await focused(page);
    expect(landed, 'activating the skip link leaves focus somewhere').not.toBeNull();
    expect(landed!.id, 'focus is on the skip link target itself').toBe(targetId);

    // And it is still there once the page settles. The skip link's href is a
    // full URL, so instant navigation refetches the page and replaces the body
    // behind the reader's back; the heading focused above is detached and focus
    // silently returns to <body> ~200ms later. Asserting only the line above
    // passed throughout that. docs/javascripts/skip-link.js.
    await page.waitForLoadState('networkidle');
    await page.waitForTimeout(300);
    const settled = await focused(page);
    expect(
      settled?.id,
      `focus survives the re-render, got ${JSON.stringify(settled)}`,
    ).toBe(targetId);

    // And onward from there: the next stop must be inside the content, not back
    // in the header — otherwise focus moved but the sequence did not.
    await page.keyboard.press('Tab');
    const next = await focused(page);
    expect(
      next?.inContent,
      `the stop after the target is inside the content, got ${JSON.stringify(next)}`,
    ).toBe(true);
  });

  test(`the skip link target survives being replaced (${path})`, async ({ page }) => {
    // The test above asserts the outcome the reader sees, and it passed for
    // months while the restoration behind it was dead: the observer in
    // docs/javascripts/skip-link.js disarmed on the first mutation batch of
    // every load, because our own target still holding the focus looked to it
    // like a reader who had tabbed on. Focus survived anyway whenever the
    // re-render happened not to detach the heading -- which is most of the
    // time, and not all of it. This asserts the mechanism instead of the luck:
    // replace the heading node, which is what a re-render does to it, and it
    // has to come back.
    await page.goto(path);
    await page.keyboard.press('Tab');
    const href = await page.locator('.md-skip').getAttribute('href');
    const targetId = decodeURIComponent(new URL(href!, page.url()).hash.slice(1));
    await page.keyboard.press('Enter');

    // Deliberately short: the restoration is bounded at 2s from the click, and
    // a test that races that bound would trade one flake for another.
    await page.waitForTimeout(400);
    await page.evaluate((id) => {
      const h = document.getElementById(id)!;
      h.parentNode!.replaceChild(h.cloneNode(true), h);
    }, targetId);
    await page.waitForTimeout(200);
    const restored = await focused(page);
    expect(
      restored?.id,
      `focus follows the target through a re-render, got ${JSON.stringify(restored)}`,
    ).toBe(targetId);

    // The other half of the same guard: staying armed for the rest of the
    // window must not let it steal the focus back from a reader who moved on.
    await page.keyboard.press('Tab');
    const moved = await focused(page);
    expect(moved?.id, 'the reader has tabbed off the target').not.toBe(targetId);
    await page.evaluate(() => document.body.appendChild(document.createElement('span')));
    await page.waitForTimeout(200);
    const kept = await focused(page);
    expect(
      kept?.id,
      `a later re-render does not take the focus back, got ${JSON.stringify(kept)}`,
    ).not.toBe(targetId);
  });

  test(`every early tab stop shows where the focus is (${path})`, async ({ page }) => {
    await page.goto(path);

    // Far enough to cross the header, the palette switch, the search field, the
    // repository link and the whole nav tab row — the stretch where Material
    // ships no focus ring of its own and a reader has nothing to follow.
    const invisible: unknown[] = [];
    for (let i = 0; i < 18; i++) {
      await page.keyboard.press('Tab');
      const stop = await focused(page);
      if (!stop) break;
      // A zero-box control (the palette radio) is legitimate as long as the label
      // standing in for it draws the ring; ask the DOM which element is painted.
      const painted =
        stop.ring && stop.width > 0 && stop.height > 0
          ? true
          : await page.evaluate(() => {
              const proxy = document.activeElement?.nextElementSibling;
              if (!proxy) return false;
              const style = getComputedStyle(proxy);
              return style.outlineStyle !== 'none' && parseFloat(style.outlineWidth) > 0;
            });
      if (!painted) invisible.push({ stop: i + 1, ...stop });
    }
    expect(invisible, 'tab stops with no visible focus indicator').toEqual([]);
  });
}
